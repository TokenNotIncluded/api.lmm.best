package service

import (
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/relayconvert"
)

func ExtractOutputTextFromResponses(resp *dto.OpenAIResponsesResponse) string {
	return relayconvert.ExtractOutputTextFromResponses(resp)
}
