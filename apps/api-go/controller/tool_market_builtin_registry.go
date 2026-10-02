package controller

import (
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
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
			if call, ok := request.(*mcp.CallToolRequest); ok {
				call.Extra = &mcp.RequestExtra{TokenInfo: &identity}
			}
			return next(ctx, method, request)
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
	return client.CallTool(ctx, &callParams)
}
