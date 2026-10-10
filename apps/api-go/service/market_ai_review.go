package service

import (
	"context"
	"errors"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
)

var marketAIReviewProvider = callOfficialModeration
var marketAIReviewTechnicalValidation = ValidateToolMarketRemote

func processMarketAIReview(parent context.Context, owner string, job *model.ModerationJob) {
	ctx, cancel := context.WithTimeout(parent, moderationRequestTimeout)
	defer cancel()
	if job.InputTruncated || len(job.Payload) > model.ModerationMaxPayloadBytes {
		cancelModerationJob(parent, owner, job, "market_review_input_too_large")
		return
	}
	if job.CapturedMode != setting.MarketAIReviewAssist && job.CapturedMode != setting.MarketAIReviewAuto {
		cancelModerationJob(parent, owner, job, "market_review_disabled")
		return
	}
	preflight := func(ctx context.Context) error { return model.MarketAIReviewPreflight(ctx, job) }
	if err := preflight(ctx); err != nil {
		marketAIReviewFailure(parent, owner, job, err)
		return
	}
	decision, err := marketAIReviewProvider(ctx, job.ReviewGroup, job.ReviewModel, job.Payload, preflight, func(ctx context.Context, index int, d moderationDecision) error {
		if d.ResponseID == "" && d.RequestID == "" {
			return nil
		}
		if err := model.AppendModerationProviderCall(ctx, job.ID, owner, model.ModerationProviderCall{Attempt: job.Attempts, BatchIndex: index, ResponseID: d.ResponseID, RequestID: d.RequestID}, time.Now().Unix()); err != nil {
			if errors.Is(err, model.ErrModerationLeaseLost) {
				return err
			}
			return errors.New("moderation_trace_unavailable")
		}
		return nil
	})
	if err != nil {
		marketAIReviewFailure(parent, owner, job, err)
		return
	}
	if parent.Err() != nil {
		return
	}
	technical := false
	if job.Source == model.ModerationSourceMarketTool && !decision.Flagged && job.CapturedMode == setting.MarketAIReviewAuto {
		s, readErr := model.ReadMarketAIReviewSettings(ctx)
		if readErr == nil && s.Mode(job.Source) == setting.MarketAIReviewAuto {
			// This is the existing technical executor check, separate from what
			// the moderation classifier knows about the public listing text.
			technical = marketAIReviewTechnicalValidation(ctx, job.UserID, job.TargetID, false) == nil
		}
	}
	if parent.Err() != nil {
		return
	}
	completion, cancelCompletion := context.WithTimeout(parent, 5*time.Second)
	defer cancelCompletion()
	if err = model.CompleteMarketAIReview(completion, job.ID, owner, model.MarketAIReviewCompletion{Flagged: decision.Flagged, Categories: decision.Categories, Scores: decision.Scores, ResponseModel: decision.ResponseModel, TechnicalValidationPassed: technical, Now: time.Now().Unix()}); err != nil && !errors.Is(err, model.ErrModerationLeaseLost) {
		common.SysError("market_ai_review_completion_failed")
	}
}

func marketAIReviewFailure(ctx context.Context, owner string, job *model.ModerationJob, err error) {
	if ctx.Err() != nil || errors.Is(err, model.ErrModerationLeaseLost) {
		return
	}
	code := err.Error()
	if code == "market_review_disabled" || code == "market_review_stale" {
		cancelModerationJob(ctx, owner, job, code)
		return
	}
	switch code {
	case "moderation_settings_unavailable", "moderation_route_unavailable", "moderation_provider_unavailable", "moderation_provider_rejected", "moderation_response_invalid", "moderation_trace_unavailable":
	default:
		code = "market_review_unavailable"
	}
	retryModerationJob(ctx, owner, job, code)
}
