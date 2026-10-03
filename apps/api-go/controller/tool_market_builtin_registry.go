package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"sync"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// BuiltinToolMarketServiceDefinition is the trusted, code-owned catalog. Tool
// schemas come from the same SDK registration as the compatible MCP endpoints.
type BuiltinToolMarketServiceDefinition struct {
	Key         string
	Name        string
	Description string
	Tools       []*mcp.Tool
}

type toolMarketBuiltinCallCaptureKey struct{}

type toolMarketBuiltinCallCapture struct {
	sync.Mutex
	output           json.RawMessage
	jsonTextFallback bool
}

// Keep SDK schema inference, defaults and validation, while capturing the
// original typed values for the trusted internal dispatcher. Compatible HTTP
// endpoints have no capture context and retain their existing SDK behavior.
func addToolMarketBuiltinMCPTool[In, Out any](server *mcp.Server, tool *mcp.Tool, handler mcp.ToolHandlerFor[In, Out]) {
	mcp.AddTool(server, tool, func(ctx context.Context, request *mcp.CallToolRequest, input In) (*mcp.CallToolResult, Out, error) {
		capture, _ := ctx.Value(toolMarketBuiltinCallCaptureKey{}).(*toolMarketBuiltinCallCapture)
		if capture != nil && len(request.Params.Arguments) != 0 {
			// SDK validation has run, but its generic decoder rounds numbers
			// before decoding In. Override only supplied fields, retaining the
			// defaults already populated into this typed input.
			decoder := json.NewDecoder(bytes.NewReader(request.Params.Arguments))
			decoder.UseNumber()
			if err := decoder.Decode(&input); err != nil {
				var zero Out
				return nil, zero, err
			}
		}
		result, output, err := handler(ctx, request, input)
		if capture != nil && err == nil && (result == nil || result.InputRequests == nil) {
			if data, marshalErr := json.Marshal(output); marshalErr == nil && !bytes.Equal(data, []byte("null")) {
				capture.Lock()
				capture.output = data
				capture.jsonTextFallback = result == nil || result.Content == nil
				capture.Unlock()
			}
		}
		return result, output, err
	})
}

// Restore supplied numeric leaves after SDK validation and defaults. Keep the
// SDK's resulting shape and added default fields instead of replacing the
// normalized output with the original object wholesale.
func restoreToolMarketBuiltinOutputNumbers(normalized, original json.RawMessage) json.RawMessage {
	normalized = bytes.TrimSpace(normalized)
	original = bytes.TrimSpace(original)
	if len(normalized) == 0 || len(original) == 0 {
		return normalized
	}
	if original[0] == '-' || original[0] >= '0' && original[0] <= '9' {
		if normalized[0] == '-' || normalized[0] >= '0' && normalized[0] <= '9' {
			return slices.Clone(original)
		}
		return normalized
	}
	if original[0] != normalized[0] {
		return normalized
	}
	switch original[0] {
	case '{':
		var normalizedFields, originalFields map[string]json.RawMessage
		if json.Unmarshal(normalized, &normalizedFields) != nil || json.Unmarshal(original, &originalFields) != nil {
			return normalized
		}
		for key, value := range originalFields {
			if current, ok := normalizedFields[key]; ok {
				normalizedFields[key] = restoreToolMarketBuiltinOutputNumbers(current, value)
			}
		}
		if data, err := json.Marshal(normalizedFields); err == nil {
			return data
		}
	case '[':
		var normalizedItems, originalItems []json.RawMessage
		if json.Unmarshal(normalized, &normalizedItems) != nil || json.Unmarshal(original, &originalItems) != nil {
			return normalized
		}
		for index := range min(len(normalizedItems), len(originalItems)) {
			normalizedItems[index] = restoreToolMarketBuiltinOutputNumbers(normalizedItems[index], originalItems[index])
		}
		if data, err := json.Marshal(normalizedItems); err == nil {
			return data
		}
	}
	return normalized
}

var toolMarketBuiltinDrawingRelay struct {
	sync.Mutex
	handler http.Handler
}

var toolMarketBuiltinCatalog struct {
	sync.Mutex
	data []byte
}

// SetToolMarketBuiltinDrawingRelay shares the drawing endpoint's relay and
// admission state with the built-in market executor. It must be set at startup.
func SetToolMarketBuiltinDrawingRelay(relay http.Handler) {
	if relay == nil {
		return
	}
	toolMarketBuiltinDrawingRelay.Lock()
	defer toolMarketBuiltinDrawingRelay.Unlock()
	toolMarketBuiltinDrawingRelay.handler = relay
}

func builtinToolMarketDrawingRelay() http.Handler {
	toolMarketBuiltinDrawingRelay.Lock()
	defer toolMarketBuiltinDrawingRelay.Unlock()
	if toolMarketBuiltinDrawingRelay.handler == nil {
		// Standalone consumers still share one admission instance across calls.
		toolMarketBuiltinDrawingRelay.handler = newDrawingMCPRelayEngine(middleware.RelayRequestAdmission())
	}
	return toolMarketBuiltinDrawingRelay.handler
}

func builtinToolMarketServer(serviceKey string, relay http.Handler) (*mcp.Server, error) {
	switch serviceKey {
	case "drawing":
		return newDrawingMCPServer(relay), nil
	case "open_source_bounties":
		return newOpenSourceBountyMCPServer(), nil
	case "wallet":
		server := mcp.NewServer(&mcp.Implementation{Name: "api.lmm.best-wallet", Version: common.Version}, nil)
		registerWalletMCPTools(server)
		return server, nil
	default:
		return nil, errors.New("unknown built-in tool service")
	}
}

// BuiltinToolMarketDefinitions enumerates the actual SDK-inferred schemas,
// without maintaining a second handwritten schema or contacting a URL.
func BuiltinToolMarketDefinitions(ctx context.Context) ([]BuiltinToolMarketServiceDefinition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	toolMarketBuiltinCatalog.Lock()
	defer toolMarketBuiltinCatalog.Unlock()
	if toolMarketBuiltinCatalog.data == nil {
		definitions, err := loadBuiltinToolMarketDefinitions(ctx)
		if err != nil {
			return nil, err
		}
		data, err := json.Marshal(definitions)
		if err != nil {
			return nil, err
		}
		toolMarketBuiltinCatalog.data = data
	}
	// Definitions are static for this binary, while model availability and
	// access are checked by each handler at execution time. Return independent
	// trees so schema rewriting in a client cannot mutate the trusted catalog.
	var definitions []BuiltinToolMarketServiceDefinition
	if err := json.Unmarshal(toolMarketBuiltinCatalog.data, &definitions); err != nil {
		return nil, err
	}
	return definitions, nil
}

func loadBuiltinToolMarketDefinitions(ctx context.Context) ([]BuiltinToolMarketServiceDefinition, error) {
	definitions := []BuiltinToolMarketServiceDefinition{
		{Key: "drawing", Name: "Drawing", Description: "Discover available image models and generate images after confirmation. Tool calls are free; image generation uses normal model billing."},
		{Key: "open_source_bounties", Name: "Open-source bounties", Description: "Publish, accept, review, and settle open-source bounties. Tool calls are free; funded rewards and tips use your actual wallet balance."},
		{Key: "wallet", Name: "Wallet", Description: "Read your balance, open the recharge page, and create a transfer link after confirming the exact amount. Tool calls are free; transfers use your actual wallet balance."},
	}
	for index := range definitions {
		server, err := builtinToolMarketServer(definitions[index].Key, nil)
		if err != nil {
			return nil, err
		}
		tools, err := listBuiltinToolMarketServerTools(ctx, server)
		if err != nil {
			return nil, fmt.Errorf("list built-in %s tools: %w", definitions[index].Key, err)
		}
		definitions[index].Tools = tools
	}
	return definitions, nil
}

func connectBuiltinToolMarketServer(ctx context.Context, server *mcp.Server) (*mcp.ClientSession, func(), error) {
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		return nil, nil, err
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "api.lmm.best-builtin-market", Version: common.Version}, &mcp.ClientOptions{
		// Confirmation belongs to the market caller. Never satisfy input
		// requests automatically, or turn them into a client callback failure.
		MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true},
	})
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		_ = serverSession.Close()
		return nil, nil, err
	}
	return clientSession, func() {
		_ = clientSession.Close()
		_ = serverSession.Close()
	}, nil
}

func listBuiltinToolMarketServerTools(ctx context.Context, server *mcp.Server) ([]*mcp.Tool, error) {
	client, closeSession, err := connectBuiltinToolMarketServer(ctx, server)
	if err != nil {
		return nil, err
	}
	defer closeSession()
	var tools []*mcp.Tool
	params := &mcp.ListToolsParams{}
	for {
		result, err := client.ListTools(ctx, params)
		if err != nil {
			return nil, err
		}
		tools = append(tools, result.Tools...)
		if result.NextCursor == "" {
			return tools, nil
		}
		params = &mcp.ListToolsParams{Cursor: result.NextCursor}
	}
}

// CallBuiltinToolMarketTool only accepts identity supplied by the already
// authenticated market controller. Arguments and request metadata never supply
// an account identity. All SDK input validation and input-required round trips
// remain identical to the original MCP endpoints.
func CallBuiltinToolMarketTool(ctx context.Context, serviceKey string, tokenInfo *auth.TokenInfo, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	var relay http.Handler
	if serviceKey == "drawing" {
		relay = builtinToolMarketDrawingRelay()
	}
	server, err := builtinToolMarketServer(serviceKey, relay)
	if err != nil {
		return nil, err
	}
	return callBuiltinToolMarketServer(ctx, server, tokenInfo, params)
}

func callBuiltinToolMarketServer(ctx context.Context, server *mcp.Server, tokenInfo *auth.TokenInfo, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	if tokenInfo == nil {
		return nil, errors.New("built-in MCP authentication context is missing")
	}
	userID, err := strconv.Atoi(tokenInfo.UserID)
	if err != nil || userID <= 0 {
		return nil, errors.New("built-in MCP authentication context is invalid")
	}
	if params == nil || params.Name == "" {
		return nil, errors.New("built-in MCP tool name is missing")
	}
	identity := *tokenInfo
	identity.Scopes = slices.Clone(tokenInfo.Scopes)
	identity.Extra = maps.Clone(tokenInfo.Extra)
	if identity.Extra == nil {
		identity.Extra = make(map[string]any)
	}
	identity.Extra["market_builtin"] = true
	capture := &toolMarketBuiltinCallCapture{}
	var captureMu sync.Mutex
	var nativeStructuredContent, nativeMeta, nativeContent json.RawMessage
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
			call, isToolCall := request.(*mcp.CallToolRequest)
			if isToolCall {
				call.Extra = &mcp.RequestExtra{TokenInfo: &identity}
				ctx = context.WithValue(ctx, toolMarketBuiltinCallCaptureKey{}, capture)
			}
			result, err := next(ctx, method, request)
			if native, ok := result.(*mcp.CallToolResult); isToolCall && err == nil && ok && native != nil {
				capture.Lock()
				output, fallback := slices.Clone(capture.output), capture.jsonTextFallback
				capture.Unlock()
				if output != nil && native.StructuredContent != nil && native.InputRequests == nil {
					if normalized, marshalErr := json.Marshal(native.StructuredContent); marshalErr == nil {
						output = restoreToolMarketBuiltinOutputNumbers(normalized, output)
						native.StructuredContent = output
						if fallback {
							native.Content = []mcp.Content{&mcp.TextContent{Text: string(output)}}
						}
					}
				}
				var structuredContent, meta, content json.RawMessage
				if native.StructuredContent != nil {
					structuredContent, _ = json.Marshal(native.StructuredContent)
				}
				if native.Meta != nil {
					meta, _ = json.Marshal(native.Meta)
				}
				if native.Content != nil {
					content, _ = json.Marshal(native.Content)
				}
				captureMu.Lock()
				nativeStructuredContent, nativeMeta, nativeContent = structuredContent, meta, content
				captureMu.Unlock()
			}
			return result, err
		}
	})
	client, closeSession, err := connectBuiltinToolMarketServer(ctx, server)
	if err != nil {
		return nil, err
	}
	defer closeSession()
	// SDK CallTool fills default arguments and protocol metadata. Keep those
	// internal additions out of the caller's request object.
	callParams := *params
	callParams.Meta = maps.Clone(params.Meta)
	result, err := client.CallTool(ctx, &callParams)
	if err != nil || result == nil {
		return result, err
	}
	// SDK's generic client JSON decoder represents arbitrary numbers as
	// float64. Keep the native handler's precise output at this internal
	// boundary without replacing its protocol result type or input requests.
	captureMu.Lock()
	structuredContent, meta, content := slices.Clone(nativeStructuredContent), slices.Clone(nativeMeta), slices.Clone(nativeContent)
	captureMu.Unlock()
	if structuredContent != nil {
		result.StructuredContent = structuredContent
	}
	if meta != nil {
		var restored mcp.Meta
		decoder := json.NewDecoder(bytes.NewReader(meta))
		decoder.UseNumber()
		if decoder.Decode(&restored) == nil {
			result.Meta = restored
		}
	}
	if content != nil {
		var restored []toolMarketContentMetadata
		decoder := json.NewDecoder(bytes.NewReader(content))
		decoder.UseNumber()
		if decoder.Decode(&restored) == nil {
			restoreToolMarketContentMetadata(result.Content, restored)
		}
	}
	return result, nil
}
