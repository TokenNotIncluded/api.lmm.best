package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/relay/helper"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAssistantStreamHeaderCommitKeepsInternalErrorStatusMutable(t *testing.T) {
	for name, writer := range map[string]func(gin.ResponseWriter) gin.ResponseWriter{
		"recorder":  func(w gin.ResponseWriter) gin.ResponseWriter { return newAssistantRelayRecorder(w) },
		"streaming": func(w gin.ResponseWriter) gin.ResponseWriter { return newAssistantStreamingRelayWriter(w, nil) },
	} {
		t.Run(name, func(t *testing.T) {
			outer := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(outer)
			c.Request = httptest.NewRequest(http.MethodPost, "/internal-assistant-relay", nil)
			c.Writer = writer(c.Writer)
			require.NoError(t, helper.CommitEventStreamHeaders(c))
			require.False(t, c.Writer.Written())
			c.Writer.WriteHeader(http.StatusBadGateway)
			require.Equal(t, http.StatusBadGateway, c.Writer.Status())
			require.False(t, common.GetContextKeyBool(c, constant.ContextKeyHTTPStreamCommitted))
			require.False(t, helper.HTTPStreamDownstreamFailed(c))
			require.False(t, outer.Flushed)
			require.Empty(t, outer.Body.String())
		})
	}
}
