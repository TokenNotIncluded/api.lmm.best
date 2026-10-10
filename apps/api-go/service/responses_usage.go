package service

import "github.com/LIghtJUNction/api.lmm.best/relaykit/dto"

// ApplyResponsesUsage normalizes Responses API token names into the common
// usage fields consumed by billing while preserving provider detail fields.
func ApplyResponsesUsage(dst *dto.Usage, src *dto.Usage) {
	if dst == nil || src == nil {
		return
	}
	if src.AudioSeconds != nil {
		seconds := *src.AudioSeconds
		dst.AudioSeconds = &seconds
	}
	if src.ImageOutputUsageSource != "" {
		dst.ImageOutputUsageSource = src.ImageOutputUsageSource
	}
	if src.InputTokens != 0 {
		dst.PromptTokens = src.InputTokens
		dst.InputTokens = src.InputTokens
	}
	if src.OutputTokens != 0 {
		dst.CompletionTokens = src.OutputTokens
		dst.OutputTokens = src.OutputTokens
	}
	if src.TotalTokens != 0 {
		dst.TotalTokens = src.TotalTokens
	}
	if src.InputTokensDetails != nil {
		inputDetails := dto.CloneInputTokenDetails(*src.InputTokensDetails)
		dst.InputTokensDetails = &inputDetails
		dst.PromptTokensDetails = dto.CloneInputTokenDetails(inputDetails)
	}
	outputDetails := src.CompletionTokenDetails
	if src.OutputTokensDetails != nil {
		outputDetails = *src.OutputTokensDetails
	}
	if !isZeroOutputTokenDetails(outputDetails) {
		dst.CompletionTokenDetails = outputDetails
		dst.OutputTokensDetails = &outputDetails
	}
	dst.PromptCacheHitTokens = src.PromptCacheHitTokens
}

func isZeroOutputTokenDetails(details dto.OutputTokenDetails) bool {
	return details.TextTokens == 0 &&
		details.AudioTokens == 0 &&
		details.ImageTokens == 0 &&
		details.ReasoningTokens == 0
}
