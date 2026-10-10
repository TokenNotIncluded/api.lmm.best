package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

func marketAIWorkerFixture(t *testing.T, mode string) (*gorm.DB, int, *model.MerchantStoreProduct) {
	t.Helper()
	db, userID := setupAssistantFundingTestDB(t, 1000000)
	db.Logger = logger.Discard
	require.NoError(t, db.AutoMigrate(&model.Option{}))
	require.NoError(t, model.BootstrapMerchantStoreWriterGate(db))
	require.NoError(t, db.AutoMigrate(append(model.MerchantStoreModels(), &model.Option{}, &model.ModerationJob{}, &model.ViolationFeeRecord{}, &model.AssistantRequestReview{}, &model.ModerationNotice{}, &model.WalletTransfer{})...))
	for key, value := range map[string]string{setting.StoreAIReviewModeOptionKey: mode, setting.ModerationEnabledOptionKey: "true", setting.ModerationGroupPoliciesOptionKey: `{"default":{"mode":"strict","amount_currency":"USD","category_fines_usd":{"hate":100}}}`} {
		require.NoError(t, db.Create(&model.Option{Key: key, Value: value}).Error)
	}
	require.NoError(t, model.SetMerchantStorePaymentCategories(userID, model.MerchantStorePaymentCategories{PlatformEnabled: true}))
	_, gatewayErr := model.SaveMerchantStoreGateway(userID, "balance", true, "")
	require.NoError(t, gatewayErr)
	p, err := model.SaveMerchantStoreProduct(userID, "", model.MerchantStoreProductInput{Title: "Public title", Description: "Public description", PriceQuota: 500000, PaymentMethods: []string{"balance"}, Links: []model.MerchantStoreLink{{Title: "Public link", Description: "Public link text", URL: "https://example.test/PRIVATE-URL-TOKEN"}}, Contact: "PRIVATE-CONTACT", ImageURLs: []string{"https://example.test/PRIVATE-IMAGE-TOKEN"}})
	require.NoError(t, err)
	require.NoError(t, db.Create(&model.MerchantStoreStock{ID: "private-stock", ProductID: p.ID, Ciphertext: "PRIVATE-CARD-CIPHERTEXT", State: "available"}).Error)
	require.NoError(t, model.SubmitMerchantStoreProduct(userID, p.ID))
	return db, userID, p
}

func marketAIWorkerNoEffects(t *testing.T, db *gorm.DB, userID int) {
	t.Helper()
	var user model.User
	require.NoError(t, db.First(&user, userID).Error)
	require.Equal(t, 1000000, user.Quota)
	require.Equal(t, common.UserStatusEnabled, user.Status)
	for _, table := range []any{&model.ViolationFeeRecord{}, &model.AssistantRequestReview{}, &model.ModerationNotice{}, &model.WalletTransfer{}, &model.MerchantStoreTransfer{}} {
		var n int64
		require.NoError(t, db.Model(table).Count(&n).Error)
		require.Zero(t, n)
	}
}

func TestMarketAIWorkerNormalDispatcherNeverRunsChatPenalties(t *testing.T) {
	for _, test := range []struct {
		name, mode, want string
		flagged          bool
	}{{"assist", setting.MarketAIReviewAssist, "pending", true}, {"reject", setting.MarketAIReviewAuto, "rejected", true}, {"approve", setting.MarketAIReviewAuto, "published", false}} {
		t.Run(test.name, func(t *testing.T) {
			db, userID, p := marketAIWorkerFixture(t, test.mode)
			old := marketAIReviewProvider
			t.Cleanup(func() { marketAIReviewProvider = old })
			calls := 0
			marketAIReviewProvider = func(ctx context.Context, group, reviewModel, text string, preflight func(context.Context) error, onBatch func(context.Context, int, moderationDecision) error) (moderationDecision, error) {
				calls++
				require.Equal(t, "default", group)
				require.Equal(t, setting.DefaultModerationModel, reviewModel)
				require.NoError(t, preflight(ctx))
				require.Contains(t, text, "Public link text")
				for _, secret := range []string{"PRIVATE-URL", "PRIVATE-IMAGE", "PRIVATE-CONTACT", "PRIVATE-CARD", "price_quota", "payment_methods", "pickup"} {
					require.NotContains(t, text, secret)
				}
				d := moderationDecision{Flagged: test.flagged, Scores: map[string]float64{"hate": 0.99}, ResponseModel: setting.DefaultModerationModel, ResponseID: "modr-offline-market", RequestID: "req-offline-market"}
				if test.flagged {
					d.Categories = []string{"hate"}
				}
				require.NoError(t, onBatch(ctx, 1, d))
				return d, nil
			}
			job, err := model.ClaimModerationJob(context.Background(), "market-service-test", time.Now().Unix(), 60)
			require.NoError(t, err)
			require.NotNil(t, job)
			processModerationJob(context.Background(), job.LeaseOwner, job)
			require.Equal(t, 1, calls)
			require.NoError(t, db.First(p, "id = ?", p.ID).Error)
			require.Equal(t, test.want, p.Status)
			var row model.ModerationJob
			require.NoError(t, db.First(&row, job.ID).Error)
			require.Equal(t, model.ModerationJobCompleted, row.Status)
			require.Empty(t, row.Payload)
			require.Len(t, row.ProviderCalls(), 1)
			require.Zero(t, row.ChargedQuota)
			require.Equal(t, "none", row.FeeStatus)
			marketAIWorkerNoEffects(t, db, userID)
		})
	}
}

func TestMarketAIWorkerProviderFailureIsDurableAndManual(t *testing.T) {
	db, userID, p := marketAIWorkerFixture(t, setting.MarketAIReviewAuto)
	old := marketAIReviewProvider
	t.Cleanup(func() { marketAIReviewProvider = old })
	marketAIReviewProvider = func(context.Context, string, string, string, func(context.Context) error, func(context.Context, int, moderationDecision) error) (moderationDecision, error) {
		return moderationDecision{}, errors.New("moderation_provider_unavailable")
	}
	var last int64
	for attempt := 0; attempt < model.ModerationJobMaxAttempts; attempt++ {
		job, err := model.ClaimModerationJob(context.Background(), "market-service-test", time.Now().Unix(), 60)
		require.NoError(t, err)
		require.NotNil(t, job)
		last = job.ID
		processModerationJob(context.Background(), job.LeaseOwner, job)
		require.NoError(t, db.Model(&model.ModerationJob{}).Where("id = ?", job.ID).Update("next_attempt_at", time.Now().Unix()).Error)
	}
	var row model.ModerationJob
	require.NoError(t, db.First(&row, last).Error)
	require.Equal(t, model.ModerationJobFailed, row.Status)
	require.Equal(t, "manual_required", row.MarketOutcome)
	require.Empty(t, row.Payload)
	require.NoError(t, db.First(p, "id = ?", p.ID).Error)
	require.Equal(t, "pending", p.Status)
	marketAIWorkerNoEffects(t, db, userID)
}

func TestMarketAIWorkerManualReviewDuringProviderCallWins(t *testing.T) {
	db, userID, p := marketAIWorkerFixture(t, setting.MarketAIReviewAuto)
	old := marketAIReviewProvider
	t.Cleanup(func() { marketAIReviewProvider = old })
	marketAIReviewProvider = func(ctx context.Context, _, _, _ string, preflight func(context.Context) error, _ func(context.Context, int, moderationDecision) error) (moderationDecision, error) {
		require.NoError(t, preflight(ctx))
		require.NoError(t, model.ReviewMerchantStoreProduct(userID, p.ID, false, "Human review while provider runs"))
		return moderationDecision{ResponseModel: setting.DefaultModerationModel, Scores: map[string]float64{"hate": 0.01}}, nil
	}
	job, err := model.ClaimModerationJob(context.Background(), "market-service-test", time.Now().Unix(), 60)
	require.NoError(t, err)
	processModerationJob(context.Background(), job.LeaseOwner, job)
	require.NoError(t, db.First(p, "id = ?", p.ID).Error)
	require.Equal(t, "rejected", p.Status)
	require.Equal(t, userID, p.ReviewedBy)
	require.Empty(t, p.AIReviewToken)
	var row model.ModerationJob
	require.NoError(t, db.First(&row, job.ID).Error)
	require.Equal(t, model.ModerationJobCancelled, row.Status)
	marketAIWorkerNoEffects(t, db, userID)
}

func TestMarketAIWorkerDoesNotDependOnChatEnabled(t *testing.T) {
	db, _, p := marketAIWorkerFixture(t, setting.MarketAIReviewAuto)
	require.NoError(t, db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&model.Option{Key: setting.ModerationEnabledOptionKey, Value: "false"}).Error)
	old := marketAIReviewProvider
	t.Cleanup(func() { marketAIReviewProvider = old })
	marketAIReviewProvider = func(context.Context, string, string, string, func(context.Context) error, func(context.Context, int, moderationDecision) error) (moderationDecision, error) {
		return moderationDecision{ResponseModel: setting.DefaultModerationModel, Scores: map[string]float64{"hate": 0.01}}, nil
	}
	job, err := model.ClaimModerationJob(context.Background(), "market-service-test", time.Now().Unix(), 60)
	require.NoError(t, err)
	require.NotNil(t, job)
	processModerationJob(context.Background(), job.LeaseOwner, job)
	require.NoError(t, db.First(p, "id = ?", p.ID).Error)
	require.Equal(t, "published", p.Status)
}

func TestMarketAIWorkerDisableDrainsActiveJobsBeforeOldWorkerRollback(t *testing.T) {
	db, userID, p := marketAIWorkerFixture(t, setting.MarketAIReviewAuto)
	var active int64
	activeJobs := func() int64 {
		require.NoError(t, db.Model(&model.ModerationJob{}).Where("source IN ? AND status IN ?", []string{model.ModerationSourceMarketTool, model.ModerationSourceMarketProduct}, []string{model.ModerationJobPending, model.ModerationJobRunning}).Count(&active).Error)
		return active
	}
	require.EqualValues(t, 1, activeJobs(), "an older worker cannot be restored while market work remains")
	require.NoError(t, db.Model(&model.Option{}).Where("key = ?", setting.StoreAIReviewModeOptionKey).Update("value", setting.MarketAIReviewOff).Error)
	s, err := model.ReadMarketAIReviewSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, setting.MarketAIReviewOff, s.ToolMode)
	require.Equal(t, setting.MarketAIReviewOff, s.StoreMode)
	require.EqualValues(t, 1, activeJobs(), "off alone does not establish the zero-active-job rollback barrier")
	old := marketAIReviewProvider
	t.Cleanup(func() { marketAIReviewProvider = old })
	marketAIReviewProvider = func(context.Context, string, string, string, func(context.Context) error, func(context.Context, int, moderationDecision) error) (moderationDecision, error) {
		t.Fatal("disabled jobs must drain without sending public text")
		return moderationDecision{}, nil
	}
	job, err := model.ClaimModerationJob(context.Background(), "market-service-test", time.Now().Unix(), 60)
	require.NoError(t, err)
	require.NotNil(t, job)
	processModerationJob(context.Background(), job.LeaseOwner, job)
	require.Zero(t, activeJobs())
	require.NoError(t, db.First(p, "id = ?", p.ID).Error)
	require.Equal(t, "pending", p.Status, "draining AI work preserves the listing for human review")
	var row model.ModerationJob
	require.NoError(t, db.First(&row, job.ID).Error)
	require.Equal(t, model.ModerationJobCancelled, row.Status)
	require.Equal(t, "stale", row.MarketOutcome)
	require.Empty(t, row.Payload)
	marketAIWorkerNoEffects(t, db, userID)
}

func TestMarketAIWorkerToolCannotUseOldDigestAfterFreshValidationFails(t *testing.T) {
	db, userID, _ := marketAIWorkerFixture(t, setting.MarketAIReviewOff)
	require.NoError(t, db.AutoMigrate(&model.ToolMarketService{}, &model.ToolMarketVersion{}, &model.ToolMarketTool{}, &model.ToolMarketToolVersion{}, &model.ToolMarketEvent{}))
	require.NoError(t, db.Create(&model.Option{Key: setting.ToolMarketAIReviewModeOptionKey, Value: setting.MarketAIReviewAuto}).Error)
	s, err := model.SaveToolMarketDraft(userID, "", model.ToolMarketDraftInput{Name: "Public tool", Description: "Public tool description", ExecutionType: "remote", Visibility: "public", Endpoint: "https://example.test/PRIVATE-ENDPOINT-TOKEN/mcp", Tools: []model.ToolMarketToolInput{{Name: "read", Description: "Public operation", InputSchema: json.RawMessage(`{"type":"object","default":"PRIVATE-SCHEMA-DEFAULT"}`), PriceQuota: 10}}})
	require.NoError(t, err)
	require.NoError(t, model.SubmitToolMarketDraft(userID, s.ID, s.DraftVersionID))
	require.NoError(t, db.Model(&model.ToolMarketVersion{}).Where("id = ?", s.DraftVersionID).Update("validation_digest", gorm.Expr("digest")).Error)
	oldProvider, oldValidation := marketAIReviewProvider, marketAIReviewTechnicalValidation
	t.Cleanup(func() { marketAIReviewProvider, marketAIReviewTechnicalValidation = oldProvider, oldValidation })
	marketAIReviewProvider = func(_ context.Context, _, _, text string, _ func(context.Context) error, _ func(context.Context, int, moderationDecision) error) (moderationDecision, error) {
		require.Contains(t, text, "Public operation")
		require.NotContains(t, text, "PRIVATE-ENDPOINT")
		require.NotContains(t, text, "PRIVATE-SCHEMA")
		return moderationDecision{ResponseModel: setting.DefaultModerationModel, Scores: map[string]float64{"hate": 0.01}}, nil
	}
	checks := 0
	marketAIReviewTechnicalValidation = func(_ context.Context, actor int, serviceID string, review bool) error {
		checks++
		require.Equal(t, userID, actor)
		require.Equal(t, s.ID, serviceID)
		require.False(t, review)
		return ErrMarketRemoteChanged
	}
	job, err := model.ClaimModerationJob(context.Background(), "market-service-test", time.Now().Unix(), 60)
	require.NoError(t, err)
	require.NotNil(t, job)
	processModerationJob(context.Background(), job.LeaseOwner, job)
	require.Equal(t, 1, checks)
	var version model.ToolMarketVersion
	require.NoError(t, db.First(&version, "id = ?", s.DraftVersionID).Error)
	require.Equal(t, "pending", version.Status)
	var row model.ModerationJob
	require.NoError(t, db.First(&row, job.ID).Error)
	require.Equal(t, model.ModerationJobCompleted, row.Status)
	require.Equal(t, "manual_required", row.MarketOutcome)
	require.Equal(t, "market_review_validation_required", row.ErrorMessage)
	marketAIWorkerNoEffects(t, db, userID)
}
