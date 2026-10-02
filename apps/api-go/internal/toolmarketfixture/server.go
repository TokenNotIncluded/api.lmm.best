// Package toolmarketfixture implements a deterministic MCP service for testing
// marketplace discovery, consent and settlement. It never contacts a model,
// payment provider or other external service.
package toolmarketfixture

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Options applies only to this standalone test service, not LMM's server.
type Options struct {
	Bearer string
	OnCall func(name string)
}

func object(properties map[string]any, required ...string) map[string]any {
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}

func textProperty() map[string]any {
	return map[string]any{"type": "string", "minLength": 1, "maxLength": 1000}
}

// NewServer exposes real Streamable HTTP MCP tools with bounded inputs. The
// failures are deliberate business/protocol fixtures and have no side effects.
func NewServer(options Options) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "lmm-market-test-mcp", Version: "1.0.0"}, &mcp.ServerOptions{
		Instructions: "This service is a deterministic marketplace TEST fixture. It never generates real images or payment links. Prices are set separately in LMM's reviewed marketplace version, not by this MCP server.",
	})
	add := func(tool *mcp.Tool, handler mcp.ToolHandler) {
		server.AddTool(tool, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if options.OnCall != nil {
				options.OnCall(tool.Name)
			}
			return handler(ctx, request)
		})
	}
	add(&mcp.Tool{Name: "fixture_echo", Description: "TEST: return the supplied text in native structured MCP content.", InputSchema: object(map[string]any{"text": textProperty()}, "text"), OutputSchema: object(map[string]any{"text": textProperty()}, "text")}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var input struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(request.Params.Arguments, &input) != nil || input.Text == "" || len(input.Text) > 1000 {
			return invalidInput(), nil
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: input.Text}}, StructuredContent: map[string]any{"text": input.Text}}, nil
	})
	integer := map[string]any{"type": "integer", "minimum": -1_000_000, "maximum": 1_000_000}
	add(&mcp.Tool{Name: "fixture_add", Description: "TEST: add two bounded integers. No financial operation.", InputSchema: object(map[string]any{"a": integer, "b": integer}, "a", "b"), OutputSchema: object(map[string]any{"sum": map[string]any{"type": "integer", "minimum": -2_000_000, "maximum": 2_000_000}}, "sum")}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var input struct {
			A *int `json:"a"`
			B *int `json:"b"`
		}
		if json.Unmarshal(request.Params.Arguments, &input) != nil || input.A == nil || input.B == nil || *input.A < -1_000_000 || *input.A > 1_000_000 || *input.B < -1_000_000 || *input.B > 1_000_000 {
			return invalidInput(), nil
		}
		return &mcp.CallToolResult{StructuredContent: map[string]any{"sum": *input.A + *input.B}}, nil
	})
	add(&mcp.Tool{Name: "fixture_image", Description: "TEST: return a fixed one-pixel PNG as native MCP image content; no image model is called.", InputSchema: object(map[string]any{})}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// A fixed transparent pixel, not generated model output.
		pixel, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{Data: pixel, MIMEType: "image/png"}}}, nil
	})
	add(&mcp.Tool{Name: "fixture_fail", Description: "TEST: explicit MCP business failure. LMM must release the hold and charge nothing.", InputSchema: object(map[string]any{})}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "Intentional test failure; no work was performed."}}}, nil
	})
	add(&mcp.Tool{Name: "fixture_pending", Description: "TEST: intermediate business result. LMM must not settle it as a successful call.", InputSchema: object(map[string]any{}), OutputSchema: object(map[string]any{"status": map[string]any{"type": "string"}}, "status")}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{StructuredContent: map[string]any{"status": "pending"}}, nil
	})
	add(&mcp.Tool{Name: "fixture_unknown", Description: "TEST: deliberate protocol error. LMM must not replay an uncertain call and must recover the hold after its deadline.", InputSchema: object(map[string]any{})}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return nil, errors.New("intentional test-only protocol failure")
	})
	add(&mcp.Tool{Name: "fixture_slow_echo", Description: "TEST: wait at most two seconds and echo text, for concurrent replay/cancellation checks.", InputSchema: object(map[string]any{"text": textProperty(), "delay_ms": map[string]any{"type": "integer", "minimum": 0, "maximum": 2000}}, "text", "delay_ms"), OutputSchema: object(map[string]any{"text": textProperty()}, "text")}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var input struct {
			Text    string `json:"text"`
			DelayMS *int   `json:"delay_ms"`
		}
		if json.Unmarshal(request.Params.Arguments, &input) != nil || input.Text == "" || len(input.Text) > 1000 || input.DelayMS == nil || *input.DelayMS < 0 || *input.DelayMS > 2000 {
			return invalidInput(), nil
		}
		timer := time.NewTimer(time.Duration(*input.DelayMS) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			return &mcp.CallToolResult{StructuredContent: map[string]any{"text": input.Text}}, nil
		}
	})
	return server
}

func invalidInput() *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "Invalid test fixture arguments."}}}
}

// Handler serves /mcp and /health. If configured, only this test service's
// explicit bearer is accepted; browser cookies cannot substitute for it.
func Handler(server *mcp.Server, options Options) http.Handler {
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, PropagateRequestCancellation: true})
	secretHash := sha256.Sum256([]byte(options.Bearer))
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"ok","test_fixture":true}`))
	})
	mux.Handle("/mcp", http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		if options.Bearer != "" {
			headers := request.Header.Values("Authorization")
			valid := len(headers) == 1 && strings.HasPrefix(headers[0], "Bearer ")
			if valid {
				candidate := sha256.Sum256([]byte(strings.TrimPrefix(headers[0], "Bearer ")))
				valid = subtle.ConstantTimeCompare(candidate[:], secretHash[:]) == 1
			}
			if !valid {
				writer.Header().Set("WWW-Authenticate", `Bearer realm="lmm-market-test-mcp"`)
				http.Error(writer, "test MCP authorization required", http.StatusUnauthorized)
				return
			}
		}
		request.Body = http.MaxBytesReader(writer, request.Body, 256<<10)
		mcpHandler.ServeHTTP(writer, request)
	}))
	return mux
}
