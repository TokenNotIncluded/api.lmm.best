package service

import (
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/gin-gonic/gin"
)

// This attests a completed text API response, not a charge, key creation or
// authorization. Only this delivery path may write the success marker.
func acquisitionTextResponseSucceeded(c *gin.Context, info *relaycommon.RelayInfo, outputTokens int, hasUpstreamUsage bool, rejected bool) bool {
	if c == nil || c.Request == nil || info == nil || info.UserId <= 0 || info.TokenId <= 0 || info.IsPlayground || outputTokens <= 0 || !hasUpstreamUsage || rejected {
		return false
	}
	if c.Request.Context().Err() != nil || c.Writer.Status() < 200 || c.Writer.Status() >= 300 || c.Writer.Size() <= 0 || c.GetBool("acquisition_excluded") {
		return false
	}
	if info.IsStream {
		return info.StreamStatus != nil && info.StreamStatus.IsNormalEnd() && info.StreamStatus.EndError == nil && !info.StreamStatus.HasErrors()
	}
	return true
}
