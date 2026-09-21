package middleware

import (
	"errors"
	"math"
	"net/http"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/pkg/admission"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/gin-gonic/gin"
)

const largeRequestConcurrencyError types.ErrorCode = "large_request_concurrency_exceeded"

// RelayRequestAdmission must be constructed once per router and shared across
// authenticated relay groups, before rate-limit/model/body parsing. The opt-in
// environment settings are read at router creation, not per request. A zero
// concurrency limit preserves existing admission behavior.
func RelayRequestAdmission() gin.HandlerFunc {
	limit := common.GetEnvOrDefault("RELAY_LARGE_REQUEST_MAX_CONCURRENCY", 0)
	if limit < 0 {
		common.SysError("RELAY_LARGE_REQUEST_MAX_CONCURRENCY must be nonnegative; admission disabled")
		limit = 0
	}
	if limit == 0 {
		return func(c *gin.Context) { c.Next() }
	}
	thresholdMB := common.GetEnvOrDefault("RELAY_LARGE_REQUEST_THRESHOLD_MB", 4)
	if thresholdMB <= 0 || int64(thresholdMB) > math.MaxInt64>>20 {
		common.SysError("RELAY_LARGE_REQUEST_THRESHOLD_MB is out of range; using 4 MiB")
		thresholdMB = 4
	}
	limiter := admission.NewLargeRequestLimiter(int64(limit), int64(thresholdMB)<<20)
	return relayRequestAdmission(limiter)
}

func relayRequestAdmission(limiter *admission.LargeRequestLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Context().Err() != nil {
			c.Abort()
			return
		}
		release, allowed := limiter.TryAcquire(c.Request)
		if !allowed {
			writeLargeRequestAdmissionError(c)
			return
		}
		defer release() // also releases on cancellation and downstream panic
		c.Next()
	}
}

func writeLargeRequestAdmissionError(c *gin.Context) {
	apiErr := types.NewErrorWithStatusCode(
		errors.New("too many large requests are in progress; retry later"),
		largeRequestConcurrencyError, http.StatusTooManyRequests,
		types.ErrOptionWithSkipRetry(),
	)
	c.Header("Retry-After", "1")
	switch {
	case strings.HasPrefix(c.Request.URL.Path, "/v1/messages"):
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
			"type": "error", "error": apiErr.ToClaudeError(),
		})
	case strings.HasPrefix(c.Request.URL.Path, "/v1beta/models/") ||
		strings.HasPrefix(c.Request.URL.Path, "/v1/models/"):
		c.AbortWithStatusJSON(http.StatusTooManyRequests, apiErr.ToGeminiError())
	default:
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": apiErr.ToOpenAIError()})
	}
}
