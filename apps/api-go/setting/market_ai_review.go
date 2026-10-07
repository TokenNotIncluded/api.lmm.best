package setting

import "errors"

const (
	ToolMarketAIReviewModeOptionKey = "ToolMarketAIReviewMode"
	StoreAIReviewModeOptionKey      = "StoreAIReviewMode"
	MarketAIReviewOff               = "off"
	MarketAIReviewAssist            = "assist"
	MarketAIReviewAuto              = "auto"
)

func IsMarketAIReviewOption(key string) bool {
	return key == ToolMarketAIReviewModeOptionKey || key == StoreAIReviewModeOptionKey
}

func ValidateMarketAIReviewMode(mode string) error {
	if mode != MarketAIReviewOff && mode != MarketAIReviewAssist && mode != MarketAIReviewAuto {
		return errors.New("market AI review mode must be off, assist or auto")
	}
	return nil
}
