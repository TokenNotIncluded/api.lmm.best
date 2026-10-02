package controller

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gorm.io/gorm"
)

// EnsureToolMarketBuiltinCatalog registers only code-owned definitions. User
// drafts cannot select this executor or change these zero-price snapshots.
func EnsureToolMarketBuiltinCatalog(ctx context.Context) error {
	inputs, err := toolMarketBuiltinCatalogInputs(ctx)
	if err != nil {
		return err
	}
	if err := model.EnsureToolMarketBuiltinServices(inputs); err != nil {
		return err
	}
	return model.VerifyToolMarketBuiltinServices(ctx, inputs)
}

// VerifyToolMarketBuiltinCatalog is the read-only startup path for workers.
// Only the migration writer may register or advance compiled-in definitions.
func VerifyToolMarketBuiltinCatalog(ctx context.Context) error {
	inputs, err := toolMarketBuiltinCatalogInputs(ctx)
	if err != nil {
		return err
	}
	return model.VerifyToolMarketBuiltinServices(ctx, inputs)
}

func toolMarketBuiltinCatalogInputs(ctx context.Context) ([]model.ToolMarketBuiltinServiceInput, error) {
	definitions, err := BuiltinToolMarketDefinitions(ctx)
	if err != nil {
		return nil, err
	}
	inputs := make([]model.ToolMarketBuiltinServiceInput, 0, len(definitions))
	for _, definition := range definitions {
		input := model.ToolMarketBuiltinServiceInput{Key: definition.Key, Name: definition.Name, Description: definition.Description}
		for _, tool := range definition.Tools {
			inputSchema, err := json.Marshal(tool.InputSchema)
			if err != nil {
				return nil, err
			}
			var outputSchema json.RawMessage
			if tool.OutputSchema != nil {
				outputSchema, err = json.Marshal(tool.OutputSchema)
				if err != nil {
					return nil, err
				}
			}
			permissions := []string{"write"}
			if tool.Annotations != nil && tool.Annotations.ReadOnlyHint {
				permissions = []string{"read"}
			}
			input.Tools = append(input.Tools, model.ToolMarketToolInput{Name: tool.Name, Description: tool.Description, InputSchema: inputSchema, OutputSchema: outputSchema, Permissions: permissions, PriceQuota: 0})
		}
		inputs = append(inputs, input)
	}
	return inputs, nil
}

func marketBuiltinVersionCurrent(ctx context.Context, key, versionID string) error {
	inputs, err := toolMarketBuiltinCatalogInputs(ctx)
	if err != nil {
		return err
	}
	for _, input := range inputs {
		if input.Key != key {
			continue
		}
		expected, err := model.ToolMarketBuiltinVersionID(input)
		if err != nil {
			return err
		}
		if expected == versionID {
			return nil
		}
	}
	// A rolling deployment may see a database version registered by newer
	// code. An older process must not run its own handler under that grant.
	return model.ErrToolMarketConflict
}

func marketBuiltinDefinition(ctx context.Context, serviceID, toolName string) (string, *mcp.Tool, error) {
	definitions, err := BuiltinToolMarketDefinitions(ctx)
	if err != nil {
		return "", nil, err
	}
	for _, definition := range definitions {
		if model.ToolMarketBuiltinServiceID(definition.Key) != serviceID {
			continue
		}
		for _, tool := range definition.Tools {
			if tool.Name == toolName {
				return definition.Key, tool, nil
			}
		}
	}
	return "", nil, model.ErrToolMarketDenied
}

// GetToolMarketExecutionResponseWithBuiltins exposes a pending confirmation as
// delivery data. It is neither a paid success nor an uncertain remote result.
func GetToolMarketExecutionResponseWithBuiltins(userID int, clientID, id string) (*service.ToolMarketExecutionResponse, error) {
	response, err := service.GetToolMarketExecutionResponse(userID, clientID, id)
	if err != nil {
		return nil, err
	}
	if response.Call.ExecutionStatus == "awaiting_confirmation" {
		response.Result, err = model.GetToolMarketBuiltinConfirmation(userID, clientID, id)
		if err != nil {
			return nil, err
		}
		response.ResultExpiresAt = response.Call.ResolveBy
	} else if response.Call.OwnerID == 0 {
		if response.Call.ExecutionStatus == "unknown" {
			response.ErrorCode = "TOOL_MARKET_RESULT_UNKNOWN"
		} else if response.Call.SettlementStatus == "held" && len(response.Result) != 0 {
			response.ErrorCode = "TOOL_MARKET_SETTLEMENT_PENDING"
		}
	}
	return response, nil
}

// ExecuteToolMarketWithBuiltins shares the marketplace's exact-version grants
// and durable request identity, while keeping internal calls off the external
// HTTPS executor and third-party payment switch.
func ExecuteToolMarketWithBuiltins(ctx context.Context, in model.ToolMarketReserveInput, requestState string, inputResponses mcp.InputResponseMap) (*service.ToolMarketExecutionResponse, error) {
	if len(requestState) > 512 || len(inputResponses) > 16 || (requestState == "" && len(inputResponses) != 0) || (requestState != "" && len(inputResponses) == 0) {
		return nil, model.ErrToolMarketInput
	}
	prior, replayErr := model.LookupToolMarketReplay(in)
	if replayErr == nil {
		if prior.ExecutionStatus != "awaiting_confirmation" || requestState == "" {
			return GetToolMarketExecutionResponseWithBuiltins(in.UserID, in.ClientID, prior.ID)
		}
	} else if !errors.Is(replayErr, gorm.ErrRecordNotFound) {
		return nil, replayErr
	}
	grantID := in.GrantID
	if replayErr == nil {
		// Lookup has already rejected an explicitly different grant. A pending
		// call stays bound to its original authorization even if a newer grant
		// has since become the default for this client/tool/version.
		grantID = prior.GrantID
	}
	execution, err := model.GetToolMarketExecution(in.UserID, in.ClientID, in.ToolID, in.VersionID, grantID)
	if err != nil {
		return nil, err
	}
	if execution.Version.ExecutionType != "builtin" {
		if requestState != "" || len(inputResponses) != 0 {
			return nil, model.ErrToolMarketInput
		}
		return service.ExecuteToolMarketRemote(ctx, in)
	}
	if execution.Service.OwnerID != 0 || execution.Tool.PriceQuota != 0 || execution.Version.Endpoint != "" {
		return nil, model.ErrToolMarketDenied
	}
	key, definition, err := marketBuiltinDefinition(ctx, execution.Service.ID, execution.Tool.Name)
	if err != nil {
		return nil, err
	}
	if err := marketBuiltinVersionCurrent(ctx, key, execution.Version.ID); err != nil {
		return nil, err
	}
	// Reject malformed arguments before reserving a call or creating a
	// confirmation. SDK dispatch still validates and decodes the same schema.
	schemaData, err := json.Marshal(definition.InputSchema)
	if err != nil {
		return nil, model.ErrToolMarketInput
	}
	var schema jsonschema.Schema
	var arguments map[string]any
	if len(in.Arguments) > 128<<10 || json.Unmarshal(schemaData, &schema) != nil || json.Unmarshal(in.Arguments, &arguments) != nil || arguments == nil {
		return nil, service.ErrMarketRemoteInput
	}
	resolved, err := schema.Resolve(nil)
	if err != nil || resolved.Validate(arguments) != nil {
		return nil, service.ErrMarketRemoteInput
	}
	in.GrantID = execution.Grant.ID
	var call *model.ToolMarketCall
	if replayErr == nil {
		call = prior
		started, err := model.ResumeToolMarketBuiltinCall(call.ID, requestState)
		if err != nil {
			return nil, err
		}
		if !started {
			return GetToolMarketExecutionResponseWithBuiltins(in.UserID, in.ClientID, call.ID)
		}
	} else {
		if requestState != "" {
			return nil, model.ErrToolMarketConflict
		}
		in.ResolveBy = common.GetTimestamp() + 120
		var created bool
		call, created, err = model.ReserveToolMarketCall(in)
		if err != nil {
			return nil, err
		}
		if !created {
			return GetToolMarketExecutionResponseWithBuiltins(in.UserID, in.ClientID, call.ID)
		}
		started, err := model.StartToolMarketCall(call.ID)
		if err != nil {
			_ = model.FinishToolMarketCall(call.ID, false)
			return nil, err
		}
		if !started {
			return GetToolMarketExecutionResponseWithBuiltins(in.UserID, in.ClientID, call.ID)
		}
	}
	// These capabilities come from the exact tool grant after Start/Resume has
	// rechecked it, never from client arguments or an old token's broad scope.
	info := &auth.TokenInfo{UserID: strconv.Itoa(in.UserID), Extra: map[string]any{
		"market_builtin": true, "market_tool_grant": true, "market_client_id": in.ClientID, "market_request_id": call.ID,
		"market_tool_id": in.ToolID, "market_version_id": in.VersionID, "market_grant_id": execution.Grant.ID,
	}}
	if key == "wallet" && definition.Annotations != nil && !definition.Annotations.ReadOnlyHint {
		info.Extra["wallet_write"] = true
	}
	result, callErr := CallBuiltinToolMarketTool(ctx, key, info, &mcp.CallToolParams{Name: execution.Tool.Name, Arguments: in.Arguments, RequestState: requestState, InputResponses: inputResponses})
	if callErr != nil || result == nil {
		// Cancellation or a lost SDK response does not prove that the underlying
		// wallet/model transaction was rolled back. Preserve the original call
		// as unknown; never encourage a new request to replay its side effects.
		_ = model.MarkToolMarketCallUnknown(call.ID)
		response, err := GetToolMarketExecutionResponseWithBuiltins(in.UserID, in.ClientID, call.ID)
		if response != nil {
			response.ErrorCode = "TOOL_MARKET_RESULT_UNKNOWN"
		}
		return response, err
	}
	data, err := json.Marshal(result)
	if err != nil || len(data) > 2<<20 {
		_ = model.MarkToolMarketCallUnknown(call.ID)
		return GetToolMarketExecutionResponseWithBuiltins(in.UserID, in.ClientID, call.ID)
	}
	if len(result.InputRequests) != 0 || result.RequestState != "" {
		if result.RequestState == "" || len(result.InputRequests) == 0 {
			_ = model.MarkToolMarketCallUnknown(call.ID)
			return GetToolMarketExecutionResponseWithBuiltins(in.UserID, in.ClientID, call.ID)
		}
		if err := model.RecordToolMarketBuiltinConfirmation(call.ID, result.RequestState, data); err != nil {
			_ = model.MarkToolMarketCallUnknown(call.ID)
			response, readErr := GetToolMarketExecutionResponseWithBuiltins(in.UserID, in.ClientID, call.ID)
			if response != nil {
				response.ErrorCode = "TOOL_MARKET_RESULT_UNKNOWN"
			}
			return response, readErr
		}
		return GetToolMarketExecutionResponseWithBuiltins(in.UserID, in.ClientID, call.ID)
	}
	if err := model.RecordToolMarketResult(call.ID, !result.IsError, data); err != nil {
		_ = model.MarkToolMarketCallUnknown(call.ID)
		response, readErr := GetToolMarketExecutionResponseWithBuiltins(in.UserID, in.ClientID, call.ID)
		if response != nil {
			response.ErrorCode = "TOOL_MARKET_RESULT_UNKNOWN"
		}
		return response, readErr
	}
	if err := model.FinishToolMarketCall(call.ID, !result.IsError); err != nil {
		response, readErr := GetToolMarketExecutionResponseWithBuiltins(in.UserID, in.ClientID, call.ID)
		if response != nil && response.Call.SettlementStatus == "held" {
			response.ErrorCode = "TOOL_MARKET_SETTLEMENT_PENDING"
		}
		return response, readErr
	}
	return GetToolMarketExecutionResponseWithBuiltins(in.UserID, in.ClientID, call.ID)
}

// The web client cannot impersonate an external client or provide billing
// outcomes. MCP continuation fields are separate from the business arguments.
func InvokeToolMarketWithBuiltins(c *gin.Context) {
	var input struct {
		ToolID         string               `json:"tool_id"`
		VersionID      string               `json:"version_id"`
		GrantID        string               `json:"grant_id"`
		RequestKey     string               `json:"request_id"`
		Arguments      json.RawMessage      `json:"arguments"`
		RequestState   string               `json:"request_state"`
		InputResponses mcp.InputResponseMap `json:"input_responses"`
	}
	if c.ShouldBindJSON(&input) != nil {
		toolMarketRespond(c, nil, model.ErrToolMarketInput)
		return
	}
	response, err := ExecuteToolMarketWithBuiltins(c.Request.Context(), model.ToolMarketReserveInput{UserID: c.GetInt("id"), ClientID: model.ToolMarketWebClient, ToolID: input.ToolID, VersionID: input.VersionID, GrantID: input.GrantID, RequestKey: input.RequestKey, Arguments: input.Arguments}, input.RequestState, input.InputResponses)
	toolMarketRespond(c, response, err)
}

func GetToolMarketCallResultWithBuiltins(c *gin.Context) {
	response, err := GetToolMarketExecutionResponseWithBuiltins(c.GetInt("id"), "", c.Param("id"))
	toolMarketRespond(c, response, err)
}
