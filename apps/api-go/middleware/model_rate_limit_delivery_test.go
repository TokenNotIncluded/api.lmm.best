package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/relay/channel"
	"github.com/LIghtJUNction/api.lmm.best/relay/channel/ali"
	"github.com/LIghtJUNction/api.lmm.best/relay/channel/baidu"
	"github.com/LIghtJUNction/api.lmm.best/relay/channel/cloudflare"
	"github.com/LIghtJUNction/api.lmm.best/relay/channel/cohere"
	"github.com/LIghtJUNction/api.lmm.best/relay/channel/coze"
	"github.com/LIghtJUNction/api.lmm.best/relay/channel/dify"
	"github.com/LIghtJUNction/api.lmm.best/relay/channel/gemini"
	"github.com/LIghtJUNction/api.lmm.best/relay/channel/replicate"
	"github.com/LIghtJUNction/api.lmm.best/relay/channel/zhipu"
	relayconstant "github.com/LIghtJUNction/api.lmm.best/relay/constant"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelRequestRateLimitReleasesLegacyNonStreamDeliveryFailures(t *testing.T) {
	fixtures := []struct {
		name, model, body string
		mode              int
		adaptor           channel.Adaptor
	}{
		{"zhipu chat", "gpt-4o", `{"success":true,"data":{"choices":[{"role":"assistant","content":"hello"}],"usage":{"prompt_tokens":10,"completion_tokens":1,"total_tokens":11}}}`, relayconstant.RelayModeChatCompletions, &zhipu.Adaptor{}},
		{"coze chat", "gpt-4o", `{"code":0,"data":[{"type":"answer","content":"hello"}]}`, relayconstant.RelayModeChatCompletions, &coze.Adaptor{}},
		{"baidu chat", "gpt-4o", `{"result":"hello","usage":{"prompt_tokens":10,"completion_tokens":1,"total_tokens":11}}`, relayconstant.RelayModeChatCompletions, &baidu.Adaptor{}},
		{"baidu embedding", "gpt-4o", `{"data":[{"object":"embedding","index":0,"embedding":[0.1]}],"usage":{"prompt_tokens":10,"total_tokens":10}}`, relayconstant.RelayModeEmbeddings, &baidu.Adaptor{}},
		{"cloudflare chat", "gpt-4o", `{"choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]}`, relayconstant.RelayModeChatCompletions, &cloudflare.Adaptor{}},
		{"cloudflare STT", "gpt-4o", `{"result":{"text":"hello"}}`, relayconstant.RelayModeAudioTranscription, &cloudflare.Adaptor{}},
		{"cohere chat", "gpt-4o", `{"text":"hello","meta":{"billed_units":{"input_tokens":10,"output_tokens":1}}}`, relayconstant.RelayModeChatCompletions, &cohere.Adaptor{}},
		{"cohere rerank", "gpt-4o", `{"results":[{"index":0,"relevance_score":0.8}],"meta":{"billed_units":{"input_tokens":10}}}`, relayconstant.RelayModeRerank, &cohere.Adaptor{}},
		{"ali rerank", "gpt-4o", `{"output":{"results":[{"index":0,"relevance_score":0.8}]},"usage":{"total_tokens":10}}`, relayconstant.RelayModeRerank, &ali.Adaptor{}},
		{"dify chat", "gpt-4o", `{"answer":"hello","metadata":{"usage":{"prompt_tokens":10,"completion_tokens":1,"total_tokens":11}}}`, relayconstant.RelayModeChatCompletions, &dify.Adaptor{}},
		{"gemini image", "imagen-3.0-generate-002", `{"predictions":[{"bytesBase64Encoded":"aGVsbG8="}]}`, relayconstant.RelayModeImagesGenerations, &gemini.Adaptor{}},
		{"replicate image", "gpt-4o", `{"status":"succeeded","output":["https://example.invalid/image.png"]}`, relayconstant.RelayModeImagesGenerations, &replicate.Adaptor{}},
	}

	for _, backend := range []string{"memory", "redis"} {
		t.Run(backend, func(t *testing.T) {
			configureModelOutcomeTest(t, backend, 0, 1)
			for _, fixture := range fixtures {
				t.Run(fixture.name, func(t *testing.T) {
					for _, failure := range []string{"closed pipe", "short write"} {
						t.Run(failure, func(t *testing.T) {
							userID := nextModelOutcomeUserID()
							var measuredUsage []*dto.Usage
							router := gin.New()
							router.POST("/probe", func(c *gin.Context) { c.Set("id", userID) }, ModelRequestRateLimit(), func(c *gin.Context) {
								info := genModelOutcomeRelayInfo(c, false)
								info.RelayMode = fixture.mode
								info.RelayFormat = types.RelayFormatOpenAI
								info.UpstreamModelName = fixture.model
								info.SetEstimatePromptTokens(10)
								c.Header("X-Delivery-Probe", "preserved")
								c.Header("Cache-Control", "no-store")
								usage, apiErr := channel.DoResponse(fixture.adaptor, c, &http.Response{
									StatusCode: http.StatusOK,
									Header:     make(http.Header),
									Body:       io.NopCloser(strings.NewReader(fixture.body)),
								}, info)
								require.Nil(t, apiErr, "delivery failure must retain the adaptor's retry and billing contract")
								usageValue, ok := usage.(*dto.Usage)
								require.True(t, ok)
								require.NotNil(t, usageValue)
								assert.True(t, info.ResponseCompleted)
								assert.Empty(t, c.Errors, "the explicit outcome must cover errors swallowed by legacy adaptors")
								assert.NoError(t, c.Request.Context().Err())
								failed := len(measuredUsage) == 0
								assert.Equal(t, failed, info.ResponseFailed)
								assert.Equal(t, !failed, modelRequestSucceeded(c))
								measuredUsage = append(measuredUsage, usageValue)
							})

							failed := httptest.NewRecorder()
							var downstream http.ResponseWriter = &modelOutcomeFailedWriter{ResponseRecorder: failed, closed: make(chan bool)}
							if failure == "short write" {
								downstream = &modelOutcomeShortWriter{ResponseRecorder: failed}
							}
							router.ServeHTTP(downstream, httptest.NewRequest(http.MethodPost, "/probe", nil))
							require.Equal(t, http.StatusOK, failed.Code)
							require.Len(t, measuredUsage, 1)

							succeeded := httptest.NewRecorder()
							router.ServeHTTP(succeeded, httptest.NewRequest(http.MethodPost, "/probe", nil))
							require.Equal(t, http.StatusOK, succeeded.Code, "a failed delivery must release the success slot")
							require.Len(t, measuredUsage, 2)
							assert.Equal(t, measuredUsage[0], measuredUsage[1], "delivery outcome must not change measured usage")
							assert.NotEmpty(t, succeeded.Body.String(), "the successful request uses the same actual adaptor")
							assert.Equal(t, succeeded.Header(), failed.Header(), "delivery outcome must preserve response headers")
							assert.NotEmpty(t, succeeded.Header().Get("Content-Type"))
							assert.Equal(t, "preserved", failed.Header().Get("X-Delivery-Probe"))
							assert.Equal(t, "no-store", failed.Header().Get("Cache-Control"))

							limited := httptest.NewRecorder()
							router.ServeHTTP(limited, httptest.NewRequest(http.MethodPost, "/probe", nil))
							require.Equal(t, http.StatusTooManyRequests, limited.Code, "the successful HTTP response commits exactly one slot")
							assert.Len(t, measuredUsage, 2, "a limited request must not reach the adaptor")
						})
					}
				})
			}
		})
	}
}
