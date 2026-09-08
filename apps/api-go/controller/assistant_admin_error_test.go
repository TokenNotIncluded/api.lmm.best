package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssistantAdminMutationErrorsDisableTransportRetry(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, upstream := range []bool{false, true} {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Set("assistant_admin_mutation_attempted", true)
			if streaming {
				session := newAssistantStreamSession(c.Writer)
				c.Set(assistantStreamSessionKey, session)
				require.NoError(t, session.start())
			}
			if upstream {
				writeAssistantUpstreamError(c, "ASSISTANT_EMPTY_UPSTREAM_RESPONSE", "answer failed after a management operation")
			} else {
				writeAssistantError(c, http.StatusBadGateway, "ASSISTANT_AGENT_MAX_STEPS", errors.New("answer did not finish"))
			}
			assert.Contains(t, recorder.Body.String(), `"retryable":false`)
			assert.NotContains(t, recorder.Body.String(), `"retryable":true`)
		}
	}
}
