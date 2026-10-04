package openai

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	relayconstant "github.com/LIghtJUNction/api.lmm.best/relay/constant"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const moderationResponseFixture = `{
  "id": "modr-test", "model": "omni-moderation-2024-09-26",
  "results": [
    {"flagged": false, "categories": {"hate": false, "future-category": false},
     "category_scores": {"hate": 0.000012, "future-category": 0},
     "category_applied_input_types": {"hate": ["text", "image"]},
     "future_result": {"retained": true}},
    {"flagged": true, "categories": {"violence": true},
     "category_scores": {"violence": 1}}
  ],
  "provider_extension": {"retained": "exactly"}
}`

type moderationResponseBody struct {
	io.Reader
	closed bool
}

func (b *moderationResponseBody) Close() error {
	b.closed = true
	return nil
}

func TestModerationDoResponsePreservesNativeResult(t *testing.T) {
	for _, forceFormat := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "force format enabled"}[forceFormat], func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/moderations", nil)
			body := &moderationResponseBody{Reader: strings.NewReader(moderationResponseFixture)}
			response := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"vendor-request"}},
				Body:       body,
			}
			info := &relaycommon.RelayInfo{
				ChannelMeta:     &relaycommon.ChannelMeta{},
				RelayMode:       relayconstant.RelayModeModerations,
				RelayFormat:     types.RelayFormatOpenAI,
				OriginModelName: "omni-moderation-latest",
			}
			info.ChannelSetting.ForceFormat = forceFormat
			info.SetEstimatePromptTokens(12345)

			usage, apiErr := (&Adaptor{}).DoResponse(c, response, info)

			require.Nil(t, apiErr)
			require.Equal(t, moderationResponseFixture, recorder.Body.String())
			require.Equal(t, http.StatusOK, recorder.Code)
			require.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
			require.Equal(t, "vendor-request", recorder.Header().Get("X-Request-Id"))
			require.NotContains(t, recorder.Body.String(), `"usage"`)
			require.Equal(t, &dto.Usage{UsageSource: "moderation_unmetered"}, usage)
			require.Equal(t, "omni-moderation-2024-09-26", info.ResponseModel.ReturnedModel)
			require.True(t, body.closed)
		})
	}
}

func TestModerationRejectsMalformedResultsBeforeDelivery(t *testing.T) {
	valid := `{"id":"modr-test","model":"omni-moderation-latest","results":[{"flagged":false,"categories":{"hate":false},"category_scores":{"hate":0.01}}]}`
	cases := map[string]string{
		"invalid JSON":       `{"results":`,
		"multiple JSON":      valid + `{}`,
		"trailing delimiter": valid + `]`,
		"missing id":         strings.Replace(valid, `"id":"modr-test",`, "", 1),
		"empty model":        strings.Replace(valid, "omni-moderation-latest", "", 1),
		"missing results":    `{"id":"modr-test","model":"omni-moderation-latest","choices":[]}`,
		"empty results":      `{"id":"modr-test","model":"omni-moderation-latest","results":[]}`,
		"wrong flagged":      strings.Replace(valid, `"flagged":false`, `"flagged":"false"`, 1),
		"missing flagged":    strings.Replace(valid, `"flagged":false,`, "", 1),
		"null category":      strings.Replace(valid, `"categories":{"hate":false}`, `"categories":{"hate":null}`, 1),
		"wrong category":     strings.Replace(valid, `"categories":{"hate":false}`, `"categories":{"hate":0}`, 1),
		"missing categories": strings.Replace(valid, `"categories":{"hate":false},`, "", 1),
		"null score":         strings.Replace(valid, `"category_scores":{"hate":0.01}`, `"category_scores":{"hate":null}`, 1),
		"string score":       strings.Replace(valid, `"category_scores":{"hate":0.01}`, `"category_scores":{"hate":"0.01"}`, 1),
		"out of range score": strings.Replace(valid, `"category_scores":{"hate":0.01}`, `"category_scores":{"hate":2}`, 1),
		"provider error":     `{"error":{"type":"invalid_request_error","message":"invalid model"}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/moderations", nil)
			response := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"X-Invalid-Upstream": {"must not be copied"}},
				Body:       io.NopCloser(strings.NewReader(body)),
			}

			usage, apiErr := OpenaiModerationHandler(c, nil, response)

			require.Nil(t, usage)
			require.NotNil(t, apiErr)
			require.Equal(t, http.StatusBadGateway, apiErr.StatusCode)
			require.True(t, types.IsSkipRetryError(apiErr))
			require.Empty(t, recorder.Body.String())
			require.Empty(t, recorder.Header().Get("X-Invalid-Upstream"))
		})
	}
}

type moderationErrorReader struct{}

func (moderationErrorReader) Read([]byte) (int, error) {
	return 0, errors.New("upstream body interrupted")
}

func TestModerationResponseReadFailureIsNotDelivered(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/moderations", nil)
	body := &moderationResponseBody{Reader: moderationErrorReader{}}

	usage, apiErr := OpenaiModerationHandler(c, nil, &http.Response{StatusCode: http.StatusOK, Body: body})

	require.Nil(t, usage)
	require.NotNil(t, apiErr)
	require.Equal(t, http.StatusBadGateway, apiErr.StatusCode)
	require.Empty(t, recorder.Body.String())
	require.True(t, body.closed)
}
