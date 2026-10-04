package openai

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

// ValidateModerationResponse checks the public moderation response contract
// without restricting category names or discarding provider extensions.
func ValidateModerationResponse(body []byte) (string, error) {
	var response struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Results []struct {
			Flagged        *bool               `json:"flagged"`
			Categories     map[string]*bool    `json:"categories"`
			CategoryScores map[string]*float64 `json:"category_scores"`
		} `json:"results"`
	}
	if err := common.Unmarshal(body, &response); err != nil {
		return "", errors.New("invalid moderation response JSON")
	}
	if strings.TrimSpace(response.ID) == "" || strings.TrimSpace(response.Model) == "" {
		return "", errors.New("moderation response requires id and model")
	}
	if len(response.Results) == 0 {
		return "", errors.New("moderation response requires non-empty results")
	}
	for _, result := range response.Results {
		if result.Flagged == nil || len(result.Categories) == 0 || len(result.CategoryScores) == 0 {
			return "", errors.New("moderation result requires flagged, categories and category_scores")
		}
		for _, category := range result.Categories {
			if category == nil {
				return "", errors.New("moderation categories must be booleans")
			}
		}
		for _, score := range result.CategoryScores {
			if score == nil || *score < 0 || *score > 1 {
				return "", errors.New("moderation category scores must be numbers between 0 and 1")
			}
		}
	}
	return response.Model, nil
}

// OpenaiModerationHandler delivers the vendor's moderation JSON unchanged.
// Moderation has no token usage report; the billing owner applies the configured
// price policy instead of treating it as a chat response with missing usage.
func OpenaiModerationHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	badResponse := func(err error) (*dto.Usage, *types.NewAPIError) {
		return nil, types.NewErrorWithStatusCode(err, types.ErrorCodeBadResponseBody, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
	}
	if resp == nil || resp.Body == nil {
		return badResponse(errors.New("invalid moderation HTTP response"))
	}
	defer service.CloseResponseBodyGracefully(resp)
	body, err := common.ReadResponseBody(resp)
	if err != nil {
		return badResponse(fmt.Errorf("failed to read moderation response: %w", err))
	}
	responseModel, err := ValidateModerationResponse(body)
	if err != nil {
		return badResponse(err)
	}
	if info != nil {
		info.ObserveResponseModel(responseModel)
	}
	service.IOCopyBytesGracefully(c, resp, body)
	return &dto.Usage{UsageSource: "moderation_unmetered"}, nil
}
