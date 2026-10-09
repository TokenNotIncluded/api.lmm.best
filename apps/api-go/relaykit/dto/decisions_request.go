package dto

import (
	"net/http"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/decisions"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
)

// DecisionsRequest satisfies the relay request contract without chat conversion.
type DecisionsRequest struct {
	decisions.Request
}

var _ Request = (*DecisionsRequest)(nil)

func (r *DecisionsRequest) IsStream(_ *http.Request) bool { return false }

func (r *DecisionsRequest) SetModelName(model string) {
	if model != "" {
		r.Model = model
	}
}

func (r *DecisionsRequest) GetTokenCountMeta() *types.TokenCountMeta {
	// Reuse the existing text/image estimate for the shared input schema.
	// This object is local metadata only; it is never sent upstream.
	input := OpenAIResponsesRequest{Model: r.Model, Input: r.Input}
	meta := input.GetTokenCountMeta()
	meta.CombineText += "\n" + r.QuestionText()
	meta.MaxTokens = 0
	return meta
}

func (r *DecisionsRequest) GetSecurityText() string {
	if r == nil {
		return ""
	}
	return r.GetTokenCountMeta().CombineText
}
