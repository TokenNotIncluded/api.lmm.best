package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRelayRejectsGeminiCountingBeforeBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, prefix := range []string{"/v1", "/v1beta"} {
		for _, action := range []string{"countTokens", "unknown", "embedContentExtra"} {
			for _, body := range []string{`{"contents":[{"parts":[{"text":"hello"}]}]}`, `{"generateContentRequest":{"model":"models/test-model","contents":[{"parts":[{"text":"hello"}]}]}}`} {
				t.Run(prefix+action+body, func(t *testing.T) {
					// No database, pricing, credentials or upstream exist. Reaching
					// billing/channel initialization instead of validation would fail.
					router := gin.New()
					router.POST(prefix+"/models/*path", func(c *gin.Context) { Relay(c, types.RelayFormatGemini) })
					response := httptest.NewRecorder()
					request := httptest.NewRequest(http.MethodPost, prefix+"/models/test-model:"+action, strings.NewReader(body))
					request.Header.Set("Content-Type", "application/json")
					router.ServeHTTP(response, request)
					require.Equal(t, http.StatusBadRequest, response.Code)
					require.Contains(t, response.Body.String(), action)
				})
			}
		}
	}
}
