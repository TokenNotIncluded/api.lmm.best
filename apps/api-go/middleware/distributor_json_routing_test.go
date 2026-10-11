package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/i18n"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func routingJSONContext(t *testing.T, path, raw string) *gin.Context {
	t.Helper()
	require.NoError(t, i18n.Init())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json; charset=utf-8")
	t.Cleanup(func() { common.CleanupBodyStorage(c) })
	return c
}

func TestJSONRoutingRejectsAmbiguousEnvelope(t *testing.T) {
	tests := []struct{ name, raw string }{
		{"duplicate-model", `{"model":"a","model":"b"}`},
		{"identical-model", `{"model":"a","model":"a"}`},
		{"null-first-model", `{"model":null,"model":"a"}`},
		{"null-last-model", `{"model":"a","model":null}`},
		{"escaped-model", `{"model":"a","mo\u0064el":"b"}`},
		{"model-case-alias", `{"model":"a","Model":"b"}`},
		{"single-model-case-alias", `{"MODEL":"a"}`},
		{"escaped-model-case-alias", `{"\u004dodel":"a"}`},
		{"duplicate-group", `{"model":"a","group":"one","group":"two"}`},
		{"identical-group", `{"model":"a","group":"one","group":"one"}`},
		{"null-group", `{"model":"a","group":null,"group":"one"}`},
		{"escaped-group", `{"model":"a","group":"one","gro\u0075p":"two"}`},
		{"single-group-case-alias", `{"model":"a","GROUP":"one"}`},
		{"non-string-model", `{"model":123}`},
		{"non-string-group", `{"model":"a","group":[]}`},
		{"root-array", `[{"model":"a"}]`},
		{"root-null", `null`},
		{"trailing-object", `{"model":"a"}{}`},
	}
	for _, path := range []string{"/v1/chat/completions", "/pg/chat/completions", "/pg/images/generations", "/v1/audio/transcriptions"} {
		t.Run(path, func(t *testing.T) {
			for _, tc := range tests {
				t.Run(tc.name, func(t *testing.T) {
					_, _, err := getModelRequest(routingJSONContext(t, path, tc.raw))
					require.Error(t, err)
				})
			}
		})
	}
}

func TestJSONRoutingPreservesBodyAndDecoderContinuity(t *testing.T) {
	tests := []struct{ name, raw string }{
		{"ordinary", ` {"model":"a","group":"one"} `},
		{"escaped-fields", `{"mo\u0064el":"a","gro\u0075p":"one"}`},
		{"escaped-values", `{"model":"\u0061","group":"o\u006ee"}`},
		{"null-fields", `{"model":null,"group":null}`},
		{"missing-fields", `{}`},
		{"unknown-duplicates", `{"model":"a","group":"one","future":1,"future":2}`},
		{"nested-fields", `{"model":"a","group":"one","metadata":{"model":"b","Model":"c","group":"two"}}`},
		{"provider-numbers", `{"model":"a","group":"one","future":{"large":1e400,"exact":9007199254740993}}`},
		{"unicode", `{"model":"模型-\ud83d\ude00","group":"分组"}`},
		{"unpaired-surrogate", `{"model":"a\ud800","group":"one\udfff"}`},
		{"invalid-utf8", "{\"model\":\"a\xff\",\"group\":\"one\xfe\"}"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := routingJSONContext(t, "/pg/chat/completions", tc.raw)
			ingress, err := getModelFromRequest(c)
			require.NoError(t, err)
			body, err := io.ReadAll(c.Request.Body)
			require.NoError(t, err)
			require.Equal(t, tc.raw, string(body))
			var decoded dto.PlayGroundRequest
			require.NoError(t, common.UnmarshalBodyReusable(c, &decoded))
			require.Equal(t, decoded.Model, ingress.Model)
			require.Equal(t, decoded.Group, ingress.Group)
			body, err = io.ReadAll(c.Request.Body)
			require.NoError(t, err)
			require.Equal(t, tc.raw, string(body))
		})
	}
}

func TestJSONRoutingValidatesLiveSessionEnvelope(t *testing.T) {
	for _, raw := range []string{
		`{"session":{"model":"live"},"session":{"model":"other"}}`,
		`{"session":null,"session":{"model":"live"}}`,
		`{"session":{"model":"live"},"se\u0073sion":{"model":"other"}}`,
		`{"Session":{"model":"live"}}`,
		`{"\u017fession":{"model":"live"}}`,
		`{"session":[]}`,
		`{"session":{"model":"live","model":"other"}}`,
		`{"session":{"model":"live","Model":"other"}}`,
		`{"session":{"model":"live","mo\u0064el":"other"}}`,
		`{"session":{"MODEL":"live"}}`,
	} {
		_, _, err := getModelRequest(routingJSONContext(t, "/v1/live/sessions", raw))
		require.Error(t, err, raw)
	}
	for _, raw := range []string{
		`{"session":{"model":"live"}}`,
		`{"model":"ignored","session":{"model":"live"}}`,
		`{"se\u0073sion":{"mo\u0064el":"live"}}`,
	} {
		request, _, err := getModelRequest(routingJSONContext(t, "/v1/live/sessions", raw))
		require.NoError(t, err)
		require.Equal(t, "live", request.Model)
	}
}

func TestJSONRoutingChecksDiskBackedBodies(t *testing.T) {
	previous := common.GetDiskCacheConfig()
	common.SetDiskCacheConfig(common.DiskCacheConfig{Enabled: true, ThresholdMB: 1, MaxSizeMB: 16, Path: t.TempDir()})
	t.Cleanup(func() { common.SetDiskCacheConfig(previous) })
	for _, suffix := range []string{"", `,"Model":"other"`} {
		raw := `{"model":"a","group":"one","future":"` + strings.Repeat("x", (1<<20)+1) + `"` + suffix + `}`
		c := routingJSONContext(t, "/pg/chat/completions", raw)
		storage, err := common.GetBodyStorage(c)
		require.NoError(t, err)
		require.True(t, storage.IsDisk())
		ingress, err := getModelFromRequest(c)
		if suffix != "" {
			require.Error(t, err)
			continue
		}
		require.NoError(t, err)
		var decoded dto.PlayGroundRequest
		require.NoError(t, common.UnmarshalBodyReusable(c, &decoded))
		require.Equal(t, decoded.Model, ingress.Model)
		require.Equal(t, decoded.Group, ingress.Group)
		body, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		require.Equal(t, raw, string(body))
	}
}

func TestJSONRoutingReturnsOwnedStrings(t *testing.T) {
	c := routingJSONContext(t, "/pg/chat/completions", `{"model":"owned-model","group":"owned-group"}`)
	ingress, err := getModelFromRequest(c)
	require.NoError(t, err)
	storage, err := common.GetBodyStorage(c)
	require.NoError(t, err)
	require.False(t, storage.IsDisk())
	body, err := storage.Bytes()
	require.NoError(t, err)
	// Mutate only after parsing to ensure no read-only body view escaped into
	// the returned authorization fields.
	for i := range body {
		body[i] = 'x'
	}
	require.Equal(t, "owned-model", ingress.Model)
	require.Equal(t, "owned-group", ingress.Group)
}

func TestJSONRoutingRejectsBeforeModelAuthorization(t *testing.T) {
	for _, path := range []string{"/v1/chat/completions", "/v1/audio/transcriptions"} {
		c := routingJSONContext(t, path, `{"model":"a","model":"b"}`)
		// An empty model allowlist would return 403 if routing ambiguities were
		// allowed to reach authorization. No database or upstream is involved.
		common.SetContextKey(c, constant.ContextKeyTokenModelLimitEnabled, true)
		Distribute()(c)
		require.True(t, c.IsAborted())
		require.Equal(t, http.StatusBadRequest, c.Writer.Status())
	}
}
