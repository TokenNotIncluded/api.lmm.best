package helper

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGeminiActionValidation(t *testing.T) {
	for _, tc := range []struct {
		action string
		want   any
	}{
		{"generateContent", &dto.GeminiChatRequest{}},
		{"streamGenerateContent", &dto.GeminiChatRequest{}},
		{"embedContent", &dto.GeminiEmbeddingRequest{}},
		{"batchEmbedContents", &dto.GeminiBatchEmbeddingRequest{}},
		{"countTokens", nil},
		{"countTokens:generateContent", nil},
		{"embedContentSuffix", nil},
		{"", nil},
	} {
		t.Run(tc.action, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/test:"+tc.action, strings.NewReader(`{"contents":[{"parts":[{"text":"hello"}]}]}`))
			c.Request.Header.Set("Content-Type", "application/json")
			t.Cleanup(func() { common.CleanupBodyStorage(c) })
			request, err := GetAndValidateRequest(c, types.RelayFormatGemini)
			if tc.want == nil {
				require.Error(t, err)
				require.Nil(t, request)
			} else {
				require.NoError(t, err)
				require.IsType(t, tc.want, request)
			}
		})
	}
}
