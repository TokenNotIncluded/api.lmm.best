package helper

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/pkg/billingexpr"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type trackingBodyStorage struct {
	data       []byte
	reader     *bytes.Reader
	bytesCalls int
}

func newTrackingBodyStorage(data []byte) *trackingBodyStorage {
	return &trackingBodyStorage{data: data, reader: bytes.NewReader(data)}
}

func (s *trackingBodyStorage) Read(p []byte) (int, error) {
	return s.reader.Read(p)
}

func (s *trackingBodyStorage) Seek(offset int64, whence int) (int64, error) {
	return s.reader.Seek(offset, whence)
}

func (s *trackingBodyStorage) Close() error {
	return nil
}

func (s *trackingBodyStorage) Bytes() ([]byte, error) {
	s.bytesCalls++
	// Mirror diskStorage.Bytes: materialize a distinct full-size slice so the
	// test observes the retained-copy cost that matters for large spilled bodies.
	return append([]byte(nil), s.data...), nil
}

func (s *trackingBodyStorage) Size() int64 {
	return int64(len(s.data))
}

func (s *trackingBodyStorage) IsDisk() bool {
	return true
}

func (s *trackingBodyStorage) NewReader() (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.data)), nil
}

func billingExprTestContext(data []byte, headers map[string]string) (*gin.Context, *trackingBodyStorage) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		ctx.Request.Header.Set(key, value)
	}
	storage := newTrackingBodyStorage(data)
	ctx.Set(common.KeyBodyStorage, storage)
	return ctx, storage
}

func TestResolveIncomingBillingExprRequestInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("Content-Type", "application/json")

	body := []byte(`{"service_tier":"fast"}`)
	ctx.Request.Body = io.NopCloser(bytes.NewReader(body))
	ctx.Set(common.KeyRequestBody, body)

	info := &relaycommon.RelayInfo{
		RequestHeaders: map[string]string{"Content-Type": "application/json"},
	}

	input, err := ResolveIncomingBillingExprRequestInput(ctx, info)
	require.NoError(t, err)
	require.Equal(t, body, input.Body)
	require.Equal(t, "application/json", input.Headers["Content-Type"])
}

func TestResolveIncomingBillingExprRequestInputForExprSkipsUnusedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := bytes.Repeat([]byte{'x'}, 1<<20)
	info := &relaycommon.RelayInfo{RequestHeaders: map[string]string{"Content-Type": "application/json"}}

	legacyCtx, legacyStorage := billingExprTestContext(body, nil)
	legacyInput, err := ResolveIncomingBillingExprRequestInput(legacyCtx, info)
	require.NoError(t, err)
	require.Equal(t, 1, legacyStorage.bytesCalls)
	require.Len(t, legacyInput.Body, len(body))

	optimizedCtx, optimizedStorage := billingExprTestContext(body, nil)
	optimizedInput, err := ResolveIncomingBillingExprRequestInputForExpr(
		optimizedCtx,
		info,
		`tier("base", p * 0.5 + c * 3)`,
	)
	require.NoError(t, err)
	require.Zero(t, optimizedStorage.bytesCalls)
	require.Empty(t, optimizedInput.Body)
	require.Equal(t, "application/json", optimizedInput.Headers["Content-Type"])

	t.Logf("retained billing body bytes: legacy=%d optimized=%d", len(legacyInput.Body), len(optimizedInput.Body))
}

func TestResolveIncomingBillingExprRequestInputForExprPreservesParamBody(t *testing.T) {
	body := []byte(`{"service_tier":"fast"}`)
	ctx, storage := billingExprTestContext(body, nil)
	info := &relaycommon.RelayInfo{RequestHeaders: map[string]string{"Content-Type": "application/json"}}
	expr := `has(param("service_tier"), "fast") ? tier("fast", p * 2) : tier("base", p)`

	input, err := ResolveIncomingBillingExprRequestInputForExpr(ctx, info, expr)
	require.NoError(t, err)
	require.Equal(t, 1, storage.bytesCalls)
	require.Equal(t, body, input.Body)

	cost, trace, err := billingexpr.RunExprWithRequest(expr, billingexpr.TokenParams{P: 10}, input)
	require.NoError(t, err)
	require.Equal(t, float64(20), cost)
	require.Equal(t, "fast", trace.MatchedTier)
}

func TestResolveIncomingBillingExprRequestInputForExprHeaderOnlyKeepsHeaders(t *testing.T) {
	body := []byte(`{"ignored":"large request body"}`)
	ctx, storage := billingExprTestContext(body, nil)
	info := &relaycommon.RelayInfo{RequestHeaders: map[string]string{
		"Content-Type": "application/json",
		"X-Plan":       "fast",
	}}
	expr := `header("x-plan") == "fast" ? tier("fast", p * 2) : tier("base", p)`

	input, err := ResolveIncomingBillingExprRequestInputForExpr(ctx, info, expr)
	require.NoError(t, err)
	require.Zero(t, storage.bytesCalls)
	require.Empty(t, input.Body)
	require.Equal(t, "fast", input.Headers["X-Plan"])

	cost, trace, err := billingexpr.RunExprWithRequest(expr, billingexpr.TokenParams{P: 10}, input)
	require.NoError(t, err)
	require.Equal(t, float64(20), cost)
	require.Equal(t, "fast", trace.MatchedTier)
}

func TestResolveIncomingBillingExprRequestInputForExprFailsSafeOnInvalidExpr(t *testing.T) {
	body := []byte(`{"service_tier":"fast"}`)
	ctx, storage := billingExprTestContext(body, nil)
	info := &relaycommon.RelayInfo{RequestHeaders: map[string]string{"Content-Type": "application/json"}}

	input, err := ResolveIncomingBillingExprRequestInputForExpr(ctx, info, `tier(`)
	require.NoError(t, err)
	require.Equal(t, 1, storage.bytesCalls)
	require.Equal(t, body, input.Body)
}

func TestResolveIncomingBillingExprRequestInputForExprDropsFrozenUnusedBody(t *testing.T) {
	body := bytes.Repeat([]byte{'x'}, 1<<20)
	info := &relaycommon.RelayInfo{
		RequestHeaders: map[string]string{
			"Content-Type": "application/json",
			"X-New":        "new",
		},
		BillingRequestInput: &billingexpr.RequestInput{
			Body: body,
			Headers: map[string]string{
				"X-Frozen": "frozen",
			},
		},
	}

	input, err := ResolveIncomingBillingExprRequestInputForExpr(nil, info, `header("x-frozen") == "frozen" ? tier("frozen", p) : tier("base", p * 2)`)
	require.NoError(t, err)
	require.Empty(t, input.Body)
	require.Equal(t, "new", input.Headers["X-New"])
	require.Equal(t, "frozen", input.Headers["X-Frozen"])
	require.Len(t, info.BillingRequestInput.Body, len(body))
}

func TestResolveIncomingBillingExprRequestInputForExprPreservesBillingResultWithoutParam(t *testing.T) {
	body := []byte(`{"service_tier":"fast"}`)
	ctx, _ := billingExprTestContext(body, nil)
	info := &relaycommon.RelayInfo{RequestHeaders: map[string]string{
		"Content-Type": "application/json",
		"X-Plan":       "fast",
	}}
	expr := `header("x-plan") == "fast" ? tier("fast", p * 2 + c * 3) : tier("base", p + c)`
	params := billingexpr.TokenParams{P: 100, C: 20, Len: 120}

	legacyInput, err := ResolveIncomingBillingExprRequestInput(ctx, info)
	require.NoError(t, err)
	legacyCost, legacyTrace, err := billingexpr.RunExprWithRequest(expr, params, legacyInput)
	require.NoError(t, err)

	optimizedCtx, _ := billingExprTestContext(body, nil)
	optimizedInput, err := ResolveIncomingBillingExprRequestInputForExpr(optimizedCtx, info, expr)
	require.NoError(t, err)
	optimizedCost, optimizedTrace, err := billingexpr.RunExprWithRequest(expr, params, optimizedInput)
	require.NoError(t, err)

	require.Equal(t, legacyCost, optimizedCost)
	require.Equal(t, legacyTrace.MatchedTier, optimizedTrace.MatchedTier)
	require.Empty(t, optimizedInput.Body)
}

func TestBuildBillingExprRequestInputFromRequest(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{
		Model:  "gemini-3.1-pro-preview",
		Stream: lo.ToPtr(true),
		Messages: []dto.Message{
			{
				Role:    "user",
				Content: "hi",
			},
		},
		MaxTokens: lo.ToPtr(uint(3000)),
	}

	input, err := BuildBillingExprRequestInputFromRequest(request, map[string]string{
		"Content-Type": "application/json",
		"X-Test":       "1",
	})
	require.NoError(t, err)
	require.Equal(t, "application/json", input.Headers["Content-Type"])
	require.Equal(t, "1", input.Headers["X-Test"])
	require.True(t, gjson.GetBytes(input.Body, "stream").Bool())
	require.Equal(t, "user", gjson.GetBytes(input.Body, "messages.0.role").String())
	require.Equal(t, float64(3000), gjson.GetBytes(input.Body, "max_tokens").Float())
}
