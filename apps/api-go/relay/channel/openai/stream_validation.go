package openai

import (
	"net/http"

	"github.com/LIghtJUNction/api.lmm.best/relay/helper"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
)

func validateOpenAIStreamResponse(resp *http.Response) *types.NewAPIError {
	// A stream request does not prove that the provider accepted SSE. Reject
	// explicit JSON/error responses before committing downstream success.
	// Legacy headerless inputs still wait for a business frame, without the
	// header-only early-flush path. Image JSON conversion is a separate path.
	if err := helper.ValidateEventStreamResponse(resp); err != nil {
		return types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusBadGateway)
	}
	return nil
}
