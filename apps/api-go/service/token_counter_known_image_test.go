package service

import (
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestEstimateRequestTokenSkipsFetchForKnownImageURLOnStream(t *testing.T) {
	gin.SetMode(gin.TestMode)

	prevCountToken := constant.CountToken
	prevGetMedia := constant.GetMediaToken
	prevNotStream := constant.GetMediaTokenNotStream
	t.Cleanup(func() {
		constant.CountToken = prevCountToken
		constant.GetMediaToken = prevGetMedia
		constant.GetMediaTokenNotStream = prevNotStream
	})
	constant.CountToken = true
	constant.GetMediaToken = true
	constant.GetMediaTokenNotStream = false

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "qwen3.5-122b-a10b-fp8")

	meta := &types.TokenCountMeta{
		TokenType:     types.TokenTypeTokenizer,
		CombineText:   "这是什么？",
		MessagesCount: 1,
		Files: []*types.FileMeta{
			types.NewImageFileMeta(
				types.NewURLFileSource("http://10.0.0.1/internal-test.png"),
				"",
			),
		},
	}
	info := &relaycommon.RelayInfo{
		IsStream:    true,
		RelayFormat: types.RelayFormatOpenAI,
	}

	tokens, err := EstimateRequestToken(c, meta, info)
	require.NoError(t, err, "known image URL must not be fetched during stream token count")
	require.GreaterOrEqual(t, tokens, 520)
}
