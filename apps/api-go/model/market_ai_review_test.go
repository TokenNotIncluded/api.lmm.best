package model

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func marketAIOptions(t *testing.T, db *gorm.DB, tool, store string) {
	t.Helper()
	for key, value := range map[string]string{setting.ToolMarketAIReviewModeOptionKey: tool, setting.StoreAIReviewModeOptionKey: store, setting.ModerationEnabledOptionKey: "true", setting.ModerationGroupPoliciesOptionKey: `{"default":{"mode":"strict","amount_currency":"USD","category_fines_usd":{"hate":100}}}`} {
		require.NoError(t, db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&Option{Key: key, Value: value}).Error)
	}
}

func marketAIProduct(t *testing.T, mode string) (*gorm.DB, User, User, *MerchantStoreProduct) {
	t.Helper()
	db := marketTestDB(t)
	require.NoError(t, db.AutoMigrate(append(MerchantStoreModels(), &ViolationFeeRecord{}, &AssistantRequestReview{}, &ModerationNotice{}, &WalletTransfer{})...))
	seller := marketTestUser(t, db, "ai-seller", 1000000, common.RoleCommonUser)
	root := marketTestUser(t, db, "ai-root", 500, common.RoleRootUser)
	marketAIOptions(t, db, setting.MarketAIReviewOff, mode)
	require.NoError(t, SetMerchantStorePaymentCategories(seller.Id, MerchantStorePaymentCategories{PlatformEnabled: true}))
	_, gatewayErr := SaveMerchantStoreGateway(seller.Id, "balance", true, "")
	require.NoError(t, gatewayErr)
	p, err := SaveMerchantStoreProduct(seller.Id, "", MerchantStoreProductInput{Title: "Useful public listing", Description: "A public description", PriceQuota: 500000, PaymentMethods: []string{"balance"}, Links: []MerchantStoreLink{{Title: "Public link title", Description: "Public link description", URL: "https://example.test/private?pickup=NEVER-SEND-URL"}}, Contact: "NEVER-SEND-CONTACT", ImageURLs: []string{"https://example.test/NEVER-SEND-IMAGE"}})
	require.NoError(t, err)
	require.NoError(t, SubmitMerchantStoreProduct(seller.Id, p.ID))
	require.NoError(t, db.First(p, "id = ?", p.ID).Error)
	return db, seller, root, p
}

func marketAIClaim(t *testing.T) *ModerationJob {
	t.Helper()
	job, err := ClaimModerationJob(context.Background(), "market-test-worker", common.GetTimestamp(), 60)
	require.NoError(t, err)
	require.NotNil(t, job)
	return job
}
func marketAIComplete(t *testing.T, j *ModerationJob, flagged, technical bool) error {
	t.Helper()
	cats := []string{}
	if flagged {
		cats = []string{"hate"}
	}
	return CompleteMarketAIReview(context.Background(), j.ID, j.LeaseOwner, MarketAIReviewCompletion{Flagged: flagged, Categories: cats, Scores: map[string]float64{"hate": 0.9}, ResponseModel: setting.DefaultModerationModel, TechnicalValidationPassed: technical})
}
func marketAINoFees(t *testing.T, seller, root User) {
	t.Helper()
	var u User
	require.NoError(t, DB.First(&u, seller.Id).Error)
	require.Equal(t, seller.Quota, u.Quota)
	require.Equal(t, common.UserStatusEnabled, u.Status)
	u = User{}
	require.NoError(t, DB.First(&u, root.Id).Error)
	require.Equal(t, root.Quota, u.Quota)
	for _, table := range []any{&ViolationFeeRecord{}, &AssistantRequestReview{}, &ModerationNotice{}, &WalletTransfer{}, &MerchantStoreTransfer{}} {
		var count int64
		require.NoError(t, DB.Model(table).Count(&count).Error)
		require.Zero(t, count)
	}
}

func TestMarketAIReviewDefaultOffAndPrivateProjection(t *testing.T) {
	db, seller, root, p := marketAIProduct(t, setting.MarketAIReviewOff)
	var count int64
	require.NoError(t, db.Model(&ModerationJob{}).Count(&count).Error)
	require.Zero(t, count)
	marketAIOptions(t, db, setting.MarketAIReviewOff, setting.MarketAIReviewAssist)
	require.NoError(t, ReviewMerchantStoreProduct(root.Id, p.ID, false, "Resubmit for a reference"))
	require.NoError(t, SubmitMerchantStoreProduct(seller.Id, p.ID))
	j := marketAIClaim(t)
	require.Contains(t, j.Payload, "Public link description")
	for _, private := range []string{"NEVER-SEND-URL", "NEVER-SEND-CONTACT", "NEVER-SEND-IMAGE", "payment_methods", "price_quota", "gateway", "pickup"} {
		require.NotContains(t, j.Payload, private)
	}
	rows, err := ListMarketAIReviews(context.Background(), seller.Id, j.Source, p.ID, "")
	require.NoError(t, err)
	require.Nil(t, rows[0].Flagged)
	require.Nil(t, rows[0].Recommendation)
	require.Nil(t, rows[0].CategoryScores)
	stranger := marketTestUser(t, db, "ai-stranger", 0, common.RoleCommonUser)
	_, err = ListMarketAIReviews(context.Background(), stranger.Id, j.Source, p.ID, "")
	require.ErrorIs(t, err, ErrToolMarketDenied)
	require.NoError(t, marketAIComplete(t, j, true, false))
	rows, err = ListMarketAIReviews(context.Background(), root.Id, j.Source, p.ID, "")
	require.NoError(t, err)
	require.Equal(t, "reference", *rows[0].Outcome)
	require.False(t, rows[0].Applied)
	require.True(t, *rows[0].Flagged)
	data, err := json.Marshal(rows)
	require.NoError(t, err)
	require.NotContains(t, string(data), "Payload")
	require.NotContains(t, string(data), "NEVER-SEND")
	require.NoError(t, db.First(p, "id = ?", p.ID).Error)
	require.Equal(t, "pending", p.Status)
	stats, err := ModerationStats(context.Background())
	require.NoError(t, err)
	require.Zero(t, stats.Completed)
	_, total, err := ListModerationJobs(context.Background(), ModerationJobFilter{})
	require.NoError(t, err)
	require.Zero(t, total)
	marketAINoFees(t, seller, root)
}

func TestMarketAIReviewProductAutomaticAndManualOverride(t *testing.T) {
	for _, flagged := range []bool{false, true} {
		t.Run(map[bool]string{false: "approve", true: "reject"}[flagged], func(t *testing.T) {
			db, seller, root, p := marketAIProduct(t, setting.MarketAIReviewAuto)
			j := marketAIClaim(t)
			require.NoError(t, marketAIComplete(t, j, flagged, false))
			require.NoError(t, marketAIComplete(t, j, flagged, false))
			require.NoError(t, db.First(p, "id = ?", p.ID).Error)
			require.Equal(t, map[bool]string{false: "published", true: "rejected"}[flagged], p.Status)
			require.Zero(t, p.ReviewedBy)
			queue, err := ListMerchantStoreProducts(root.Id, true, 0, 20)
			require.NoError(t, err)
			require.Len(t, queue, 1, "the applied decision must remain reachable for human review")
			require.Equal(t, p.ID, queue[0].ID)
			require.NoError(t, ReviewMerchantStoreProduct(root.Id, p.ID, flagged, "Human override"))
			require.NoError(t, db.First(p, "id = ?", p.ID).Error)
			require.Equal(t, map[bool]string{false: "rejected", true: "published"}[flagged], p.Status)
			require.Equal(t, root.Id, p.ReviewedBy)
			require.Empty(t, p.AIReviewToken)
			queue, err = ListMerchantStoreProducts(root.Id, true, 0, 20)
			require.NoError(t, err)
			require.Empty(t, queue, "an already overridden decision cannot re-enter the queue")
			var completed ModerationJob
			require.NoError(t, db.First(&completed, j.ID).Error)
			require.Empty(t, completed.Payload)
			require.Equal(t, "overridden", completed.MarketOutcome)
			require.Zero(t, completed.ChargedQuota)
			require.Zero(t, completed.FeeRecordID)
			marketAINoFees(t, seller, root)
		})
	}
}

func TestMarketAIReviewCapturedAssistNeverBecomesAutomatic(t *testing.T) {
	db, seller, root, p := marketAIProduct(t, setting.MarketAIReviewAssist)
	j := marketAIClaim(t)
	marketAIOptions(t, db, setting.MarketAIReviewOff, setting.MarketAIReviewAuto)
	require.NoError(t, marketAIComplete(t, j, true, false))
	require.NoError(t, db.First(p, "id = ?", p.ID).Error)
	require.Equal(t, "pending", p.Status)
	var row ModerationJob
	require.NoError(t, db.First(&row, j.ID).Error)
	require.Equal(t, "reference", row.MarketOutcome)
	marketAINoFees(t, seller, root)
}

func TestMarketAIReviewCompletionFencesDisabledOrEditedContent(t *testing.T) {
	for _, kind := range []string{"disabled", "hash"} {
		t.Run(kind, func(t *testing.T) {
			db, seller, root, p := marketAIProduct(t, setting.MarketAIReviewAuto)
			j := marketAIClaim(t)
			if kind == "disabled" {
				marketAIOptions(t, db, setting.MarketAIReviewOff, setting.MarketAIReviewOff)
			} else {
				require.NoError(t, db.Model(p).Update("description", "Changed after capture").Error)
			}
			require.NoError(t, marketAIComplete(t, j, false, false))
			require.NoError(t, db.First(p, "id = ?", p.ID).Error)
			require.Equal(t, "pending", p.Status)
			var row ModerationJob
			require.NoError(t, db.First(&row, j.ID).Error)
			require.Equal(t, ModerationJobCancelled, row.Status)
			require.Equal(t, "stale", row.MarketOutcome)
			require.Empty(t, row.Payload)
			marketAINoFees(t, seller, root)
		})
	}
}

func TestMarketAIReviewManualRaceAndIdenticalResubmission(t *testing.T) {
	db, seller, root, p := marketAIProduct(t, setting.MarketAIReviewAuto)
	j := marketAIClaim(t)
	var wg sync.WaitGroup
	var completeErr, manualErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		completeErr = CompleteMarketAIReview(context.Background(), j.ID, j.LeaseOwner, MarketAIReviewCompletion{ResponseModel: setting.DefaultModerationModel})
	}()
	go func() { defer wg.Done(); manualErr = ReviewMerchantStoreProduct(root.Id, p.ID, false, "Human wins") }()
	wg.Wait()
	require.NoError(t, manualErr)
	if completeErr != nil {
		require.ErrorIs(t, completeErr, ErrModerationLeaseLost)
	}
	require.NoError(t, db.First(p, "id = ?", p.ID).Error)
	require.Equal(t, "rejected", p.Status)
	require.Equal(t, root.Id, p.ReviewedBy)
	require.NoError(t, SubmitMerchantStoreProduct(seller.Id, p.ID))
	require.NoError(t, db.First(p, "id = ?", p.ID).Error)
	require.NotEqual(t, j.RequestID, p.AIReviewToken)
	next := marketAIClaim(t)
	require.Greater(t, next.ID, j.ID)
	require.Equal(t, j.InputDigest, next.InputDigest)
	if err := marketAIComplete(t, j, false, false); err != nil {
		require.ErrorIs(t, err, ErrModerationLeaseLost)
	}
	require.NoError(t, db.First(p, "id = ?", p.ID).Error)
	require.Equal(t, "pending", p.Status)
	marketAINoFees(t, seller, root)
}

func TestMarketAIReviewCannotEnterChatFineCompletion(t *testing.T) {
	_, seller, root, p := marketAIProduct(t, setting.MarketAIReviewAuto)
	j := marketAIClaim(t)
	err := CompleteModerationJob(context.Background(), j.ID, j.LeaseOwner, ModerationCompletion{Flagged: true, Categories: []string{"hate"}, Scores: map[string]float64{"hate": 0.99}, CurrentMode: setting.ModerationModeStrict, CategoryFinesUSD: map[string]float64{"hate": 100}, AmountCurrency: setting.ModerationAmountCurrencyUSD})
	require.ErrorIs(t, err, ErrModerationJobInvalid)
	marketAINoFees(t, seller, root)
	var row MerchantStoreProduct
	require.NoError(t, DB.First(&row, "id = ?", p.ID).Error)
	require.Equal(t, "pending", row.Status)
}

func TestMarketAIReviewOversizedAndProviderFailureStayManual(t *testing.T) {
	db, seller, root, p := marketAIProduct(t, setting.MarketAIReviewAuto)
	j := marketAIClaim(t)
	require.ErrorIs(t, CompleteMarketAIReview(context.Background(), j.ID, j.LeaseOwner, MarketAIReviewCompletion{ResponseModel: setting.DefaultModerationModel, Scores: map[string]float64{"hate": math.NaN()}}), ErrModerationJobInvalid)
	require.NoError(t, DB.Model(&ModerationJob{}).Where("id = ?", j.ID).Update("attempts", ModerationJobMaxAttempts).Error)
	require.NoError(t, RetryModerationJob(context.Background(), j.ID, j.LeaseOwner, common.GetTimestamp(), common.GetTimestamp(), "moderation_provider_unavailable"))
	var row ModerationJob
	require.NoError(t, db.First(&row, j.ID).Error)
	require.Equal(t, ModerationJobFailed, row.Status)
	require.Equal(t, "manual_required", row.MarketOutcome)
	require.Empty(t, row.Payload)
	require.NoError(t, ReviewMerchantStoreProduct(root.Id, p.ID, false, "retry after edit"))
	require.NoError(t, db.Model(p).Update("description", strings.Repeat("oversized", ModerationMaxPayloadBytes)).Error)
	require.NoError(t, SubmitMerchantStoreProduct(seller.Id, p.ID))
	row = ModerationJob{}
	require.NoError(t, db.Order("id DESC").First(&row).Error)
	require.Equal(t, ModerationJobFailed, row.Status)
	require.Equal(t, "market_review_input_too_large", row.ErrorMessage)
	require.Empty(t, row.Payload)
	require.NoError(t, db.First(p, "id = ?", p.ID).Error)
	require.Equal(t, "pending", p.Status)
	marketAINoFees(t, seller, root)
}

func TestMarketAIReviewToolValidationAndSelfOverride(t *testing.T) {
	for _, validated := range []bool{false, true} {
		t.Run(map[bool]string{false: "manual", true: "publish"}[validated], func(t *testing.T) {
			db := marketTestDB(t)
			author := marketTestUser(t, db, "tool-ai-root", 1000, common.RoleRootUser)
			marketAIOptions(t, db, setting.MarketAIReviewAuto, setting.MarketAIReviewOff)
			s, err := SaveToolMarketDraft(author.Id, "", marketTestDraft(10))
			require.NoError(t, err)
			version := s.DraftVersionID
			require.NoError(t, SubmitToolMarketDraft(author.Id, s.ID, version))
			j := marketAIClaim(t)
			if validated {
				require.NoError(t, db.Model(&ToolMarketVersion{}).Where("id = ?", version).Update("validation_digest", gorm.Expr("digest")).Error)
			}
			require.NoError(t, marketAIComplete(t, j, false, validated))
			var v ToolMarketVersion
			require.NoError(t, db.First(&v, "id = ?", version).Error)
			if validated {
				require.Equal(t, "published", v.Status)
				queue, err := ListToolMarketReviewQueue(author.Id)
				require.NoError(t, err)
				require.Len(t, queue, 1)
				require.Equal(t, s.ID, queue[0].ID)
				require.NoError(t, ReviewToolMarketVersion(author.Id, s.ID, version, false, "Self review overrides AI"))
				require.NoError(t, db.First(&v, "id = ?", version).Error)
				require.Equal(t, "rejected", v.Status)
				require.Equal(t, author.Id, v.ReviewedBy)
				queue, err = ListToolMarketReviewQueue(author.Id)
				require.NoError(t, err)
				require.Empty(t, queue)
			} else {
				require.Equal(t, "pending", v.Status)
				var row ModerationJob
				require.NoError(t, db.First(&row, j.ID).Error)
				require.Equal(t, "manual_required", row.MarketOutcome)
			}
			var u User
			require.NoError(t, db.First(&u, author.Id).Error)
			require.Equal(t, author.Quota, u.Quota)
		})
	}
}

func TestMarketAIReviewToolQueuePrioritizesPendingAndExcludesSupersededLive(t *testing.T) {
	db := marketTestDB(t)
	author := marketTestUser(t, db, "queue-root", 1000, common.RoleRootUser)
	marketAIOptions(t, db, setting.MarketAIReviewAuto, setting.MarketAIReviewOff)
	published, err := SaveToolMarketDraft(author.Id, "", marketTestDraft(10))
	require.NoError(t, err)
	require.NoError(t, SubmitToolMarketDraft(author.Id, published.ID, published.DraftVersionID))
	j := marketAIClaim(t)
	require.NoError(t, db.Model(&ToolMarketVersion{}).Where("id = ?", published.DraftVersionID).Update("validation_digest", gorm.Expr("digest")).Error)
	require.NoError(t, marketAIComplete(t, j, false, true))
	pending, err := SaveToolMarketDraft(author.Id, "", marketTestDraft(10))
	require.NoError(t, err)
	require.NoError(t, SubmitToolMarketDraft(author.Id, pending.ID, pending.DraftVersionID))
	queue, err := ListToolMarketReviewQueue(author.Id)
	require.NoError(t, err)
	require.Len(t, queue, 2)
	require.Equal(t, pending.ID, queue[0].ID, "pending review wins even when the old AI decision is older")
	require.Equal(t, published.ID, queue[1].ID)
	_, err = SaveToolMarketDraft(author.Id, published.ID, marketTestDraft(11))
	require.NoError(t, err)
	queue, err = ListToolMarketReviewQueue(author.Id)
	require.NoError(t, err)
	require.Len(t, queue, 1, "a new draft hides the superseded live AI decision from this queue")
	require.Equal(t, pending.ID, queue[0].ID)
	stranger := marketTestUser(t, db, "queue-user", 0, common.RoleCommonUser)
	_, err = ListToolMarketReviewQueue(stranger.Id)
	require.ErrorIs(t, err, ErrToolMarketDenied)
}
