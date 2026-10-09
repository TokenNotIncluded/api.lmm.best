package service

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/internal/marketprovider"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Provider presets publish only the reviewed execution and read-only discovery gateways.
// Account, wallet, key-management and unrestricted run-history tools must not
// expose a merchant's shared identity to marketplace customers.
func ToolMarketPresetTools(id string, tools []model.ToolMarketToolInput) ([]model.ToolMarketToolInput, error) {
	preset, ok := marketprovider.Find(id)
	if !ok {
		return nil, model.ErrToolMarketInput
	}
	rows := []model.ToolMarketToolInput{}
	hasExecute, hasInspect := false, false
	for _, tool := range tools {
		if tool.Name == preset.ExecuteTool {
			tool.ProviderPricing = &marketprovider.Pricing{Provider: id, Multiplier: "1.2"}
			tool.PriceQuota = 0 // the editor supplies a reviewed per-call ceiling
			hasExecute = true
			rows = append(rows, tool)
			continue
		}
		for _, name := range preset.ReadTools {
			if tool.Name == name {
				tool.Permissions = []string{"read", "network", "external_account"}
				tool.PriceQuota = 0
				rows = append(rows, tool)
				if name == preset.InspectTool {
					hasInspect = true
				}
			}
		}
	}
	if !hasExecute || !hasInspect {
		return nil, ErrMarketRemoteSchema
	}
	return rows, nil
}

// marketProviderPayload reads exact numeric JSON from structuredContent, or a
// single JSON text result used by older MCP servers. Never parse prose as price.
func marketProviderPayload(exact map[string]any) (map[string]any, error) {
	if failed, _ := exact["isError"].(bool); failed {
		return nil, marketprovider.ErrPrice
	}
	if data, ok := exact["structuredContent"].(map[string]any); ok {
		return data, nil
	}
	content, ok := exact["content"].([]any)
	if !ok || len(content) != 1 {
		return nil, marketprovider.ErrPrice
	}
	item, ok := content[0].(map[string]any)
	if !ok || item["type"] != "text" {
		return nil, marketprovider.ErrPrice
	}
	text, ok := item["text"].(string)
	if !ok {
		return nil, marketprovider.ErrPrice
	}
	var data map[string]any
	if marketDecodeExactJSON([]byte(text), &data) != nil || data == nil {
		return nil, marketprovider.ErrPrice
	}
	return data, nil
}

func marketProviderQuote(ctx context.Context, session *mcp.ClientSession, capture *marketWireCapture, pricing marketprovider.Pricing, args map[string]any, credential *model.ToolMarketResolvedCredential) (*marketprovider.Quote, error) {
	preset, ok := marketprovider.Find(pricing.Provider)
	if !ok {
		return nil, marketprovider.ErrPrice
	}
	query, err := marketprovider.QuoteArguments(pricing.Provider, args)
	if err != nil {
		return nil, err
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: preset.InspectTool, Arguments: query})
	if err != nil {
		return nil, err
	}
	if result == nil || result.IsError || len(result.InputRequests) != 0 || result.RequestState != "" {
		return nil, marketprovider.ErrPrice
	}
	exact, err := capture.result("tools/call")
	if err != nil || !marketRemoteSecretSafe(exact, credential) {
		return nil, marketprovider.ErrPrice
	}
	data, err := marketProviderPayload(exact)
	if err != nil {
		return nil, err
	}
	var q marketprovider.Quote
	var schema any
	var input any
	if pricing.Provider == "agentkey" {
		name, _ := data["name"].(string)
		if !strings.EqualFold(name, query["name"].(string)) {
			return nil, marketprovider.ErrPrice
		}
		raw, _ := json.Marshal(data["cost"])
		q, err = marketprovider.AgentKeyQuote(raw)
		schema, input = data["params"], args["params"]
		if input == nil {
			input = map[string]any{}
		}
	} else {
		if data["provider"] != query["provider"] || data["endpoint"] != query["endpoint"] {
			return nil, marketprovider.ErrPrice
		}
		raw, _ := json.Marshal(data["price"])
		q, err = marketprovider.MonidQuote(raw)
		schemas, _ := data["schema"].(map[string]any)
		schema, input = schemas["input"], args["input"]
	}
	if err != nil {
		return nil, err
	}
	if q.Unit != "call" {
		return nil, marketprovider.ErrVariablePrice
	}
	// Generic gateway schemas do not validate the selected operation's payload.
	// Reject missing/invalid operation schemas before reserving or running it.
	rawSchema, _ := json.Marshal(schema)
	rawInput, _ := json.Marshal(input)
	if _, err := ValidateToolMarketArguments(rawSchema, rawInput); err != nil {
		return nil, ErrMarketRemoteInput
	}
	return &q, nil
}

// A completed Monid run can still carry a 404 or 500 provider response. A
// running job is not a success, and cannot be charged as a completed tool call.
func marketProviderResult(pricing *marketprovider.Pricing, exact map[string]any) (pending bool, valid bool) {
	if pricing == nil {
		return false, true
	}
	data, err := marketProviderPayload(exact)
	if err != nil {
		return false, false
	}
	if pricing.Provider != "monid" {
		if success, ok := data["success"].(bool); ok && !success {
			return false, false
		}
		if failure, ok := data["error"]; ok && failure != nil && failure != "" {
			return false, false
		}
		return false, true
	}
	state, _ := data["status"].(string)
	switch strings.ToUpper(state) {
	case "RUNNING", "PENDING", "QUEUED", "PROCESSING":
		return true, false
	case "COMPLETED":
		provider, _ := data["providerResponse"].(map[string]any)
		code, ok := provider["httpStatus"].(json.Number)
		if !ok {
			return false, false
		}
		status, err := code.Int64()
		return false, err == nil && status >= 200 && status < 300
	default:
		return false, false
	}
}

func marketProviderAccountBoundary(endpoint, name string, args map[string]any) error {
	for _, preset := range marketprovider.Presets() {
		if endpoint == preset.Endpoint && (name == preset.InspectTool || name == preset.ExecuteTool) {
			_, err := marketprovider.QuoteArguments(preset.ID, args)
			return err
		}
	}
	return nil
}
