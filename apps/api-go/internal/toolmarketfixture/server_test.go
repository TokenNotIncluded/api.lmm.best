package toolmarketfixture

import (
	"bytes"
	"context"
	"image/png"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

type bearerTransport struct {
	base  http.RoundTripper
	token string
}

func (transport bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+transport.token)
	return transport.base.RoundTrip(clone)
}

func TestFixtureUsesRealHTTPSMCPAndDoesNotExecuteDuringDiscovery(t *testing.T) {
	var calls atomic.Int32
	options := Options{Bearer: "fixture-only-bearer-not-a-real-secret", OnCall: func(string) { calls.Add(1) }}
	server := httptest.NewTLSServer(Handler(NewServer(options), options))
	t.Cleanup(server.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "fixture-client", Version: "1"}, &mcp.ClientOptions{MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true}})
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{base: server.Client().Transport, token: options.Bearer}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	tools, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, tools.Tools, 9)
	require.Zero(t, calls.Load(), "discovery must never execute a paid business tool")

	echo, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "fixture_echo", Arguments: map[string]any{"text": "paid fixture"}})
	require.NoError(t, err)
	require.False(t, echo.IsError)
	require.Equal(t, "paid fixture", echo.StructuredContent.(map[string]any)["text"])
	add, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "fixture_add", Arguments: map[string]any{"a": 20, "b": 22}})
	require.NoError(t, err)
	require.Equal(t, float64(42), add.StructuredContent.(map[string]any)["sum"])
	image, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "fixture_image", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.Len(t, image.Content, 1)
	pixel, ok := image.Content[0].(*mcp.ImageContent)
	require.True(t, ok)
	require.Equal(t, "image/png", pixel.MIMEType)
	require.NotEmpty(t, pixel.Data)
	decoded, err := png.Decode(bytes.NewReader(pixel.Data))
	require.NoError(t, err, "the image fixture must contain a decodable PNG")
	require.Equal(t, 1, decoded.Bounds().Dx())
	require.Equal(t, 1, decoded.Bounds().Dy())
	failure, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "fixture_fail", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.True(t, failure.IsError)
	invalid, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "fixture_invalid_output", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.Equal(t, "not an integer", invalid.StructuredContent.(map[string]any)["value"])
	empty, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "fixture_empty", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.Empty(t, empty.Content)
	require.Nil(t, empty.StructuredContent)
	pending, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "fixture_pending", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.Equal(t, "pending", pending.StructuredContent.(map[string]any)["status"])
	_, err = session.CallTool(context.Background(), &mcp.CallToolParams{Name: "fixture_unknown", Arguments: map[string]any{}})
	require.Error(t, err)
	slow, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "fixture_slow_echo", Arguments: map[string]any{"text": "bounded", "delay_ms": 0}})
	require.NoError(t, err)
	require.Equal(t, "bounded", slow.StructuredContent.(map[string]any)["text"])
	require.Equal(t, int32(9), calls.Load())
}

func TestFixtureAuthenticationRejectsMissingWrongAndDuplicateBearers(t *testing.T) {
	options := Options{Bearer: "fixture-only-bearer-not-a-real-secret"}
	handler := Handler(NewServer(options), options)
	for _, headers := range [][]string{nil, {"Bearer wrong"}, {"Basic password"}, {"Bearer " + options.Bearer, "Bearer " + options.Bearer}} {
		request := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		for _, value := range headers {
			request.Header.Add("Authorization", value)
		}
		request.AddCookie(&http.Cookie{Name: "session", Value: options.Bearer})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		require.Equal(t, http.StatusUnauthorized, response.Code)
		require.NotContains(t, response.Body.String(), options.Bearer)
	}
}
