package gemini

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGeminiChatHandlerPropagatesCorrectedNumericPromptEstimate(t *testing.T) {
	t.Parallel()

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	model := "gemini-3-flash-preview"
	estimated := service.EstimateTokenByModel(model, strings.Repeat("7", 30))
	require.Greater(t, estimated, 3, "legacy Gemini estimator charged every numeric run as a constant three tokens")

	info := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatGemini,
		OriginModelName: model,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: model,
		},
	}
	info.SetEstimatePromptTokens(estimated)

	payload := dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{{
			Content: dto.GeminiChatContent{
				Role:  "model",
				Parts: []dto.GeminiPart{{Text: "ok"}},
			},
		}},
		UsageMetadata: dto.GeminiUsageMetadata{
			PromptTokenCount:     0,
			CandidatesTokenCount: 2,
			TotalTokenCount:      estimated + 2,
		},
	}
	body, err := common.Marshal(payload)
	require.NoError(t, err)

	usage, apiErr := GeminiChatHandler(c, info, &http.Response{Body: io.NopCloser(bytes.NewReader(body))})
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	require.Equal(t, estimated, usage.PromptTokens)
}
