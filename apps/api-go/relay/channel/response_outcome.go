package channel

import (
	"net/http"

	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/gin-gonic/gin"
)

// DoResponse records completion for legacy stream adaptors that do not publish
// StreamStatus. A protocol-specific status takes precedence over this fallback.
// Keep usage and errors unchanged so outcome classification does not affect billing.
func DoResponse(adaptor Adaptor, c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (any, *types.NewAPIError) {
	info.ResetResponseOutcome()
	usage, apiErr := adaptor.DoResponse(c, resp, info)
	info.CompleteResponseOutcome(apiErr)
	return usage, apiErr
}
