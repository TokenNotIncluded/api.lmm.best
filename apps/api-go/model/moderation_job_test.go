package model

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

func setupModerationEffectsTestDB(t *testing.T) (*gorm.DB, *User) {
	t.Helper()
	ensureModerationTestCreditAnchor(t)
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&Option{}, &ModerationJob{}, &ModerationNotice{}, &ViolationFeeRecord{}, &ViolationFeeAppeal{}, &AssistantRequestReview{}, &AssistantReviewReset{}, &UnifiedTodoRead{}))
	user := &User{Username: "moderation-owner", AffCode: "moderation-owner", Group: "default", Quota: int(10 * common.QuotaPerUnit), UsedQuota: 123, RequestCount: 7, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(user).Error)
	writeModerationTestPolicy(t, db, true, "strict", map[string]float64{"violence": 2, "hate": 1})
	return db, user
}

func ensureModerationTestCreditAnchor(t *testing.T) {
	t.Helper()
	if _, err := common.CreditsPerUSD(); err != nil {
		require.NoError(t, common.SetCreditsPerUSD(decimal.NewFromInt(500000)))
		t.Cleanup(common.ClearCreditsPerUSD)
	}
}

func moderationTestCurrencyBasis(t *testing.T, anchor, legacyUnit int64) {
	t.Helper()
	previousAnchor, previousError := common.CreditsPerUSD()
	previousUnit := common.QuotaPerUnit
	t.Cleanup(func() {
		common.QuotaPerUnit = previousUnit
		if previousError != nil {
			common.ClearCreditsPerUSD()
		} else {
			require.NoError(t, common.SetCreditsPerUSD(previousAnchor))
		}
	})
	require.NoError(t, common.SetCreditsPerUSD(decimal.NewFromInt(anchor)))
	common.QuotaPerUnit = float64(legacyUnit)
}

func writeModerationTestPolicy(t *testing.T, db *gorm.DB, enabled bool, mode string, fines map[string]float64) {
	t.Helper()
	settings := setting.DefaultModerationSettings()
	settings.Enabled, settings.AssistantEnabled = enabled, enabled
	settings.GroupPolicies = map[string]setting.ModerationGroupPolicy{"default": {Mode: mode, CategoryFinesUSD: fines}}
	for key, value := range settings.OptionValues() {
		require.NoError(t, db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value"})}).Create(&Option{Key: key, Value: value}).Error)
	}
}

func moderationTestJob(userID int, request, source, mode string) *ModerationJob {
	return &ModerationJob{UserID: userID, RequestID: request, Source: source, Group: "default", ReviewGroup: "default", ReviewModel: setting.DefaultModerationModel, Payload: "current user text", CapturedMode: mode, CapturedCategoryFinesJSON: `{"violence":2,"hate":1}`}
}

func claimModerationTestJob(t *testing.T, job *ModerationJob, owner string) *ModerationJob {
	t.Helper()
	created, err := EnqueueModerationJob(context.Background(), job)
	require.NoError(t, err)
	require.True(t, created)
	claimed, err := ClaimModerationJob(context.Background(), owner, common.GetTimestamp(), 60)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.Equal(t, job.ID, claimed.ID)
	return claimed
}

func flaggedModerationCompletion() ModerationCompletion {
	return ModerationCompletion{Flagged: true, Categories: []string{"hate", "violence"}, Scores: map[string]float64{"hate": .8, "violence": .9}, ResponseModel: setting.DefaultModerationModel, CurrentMode: setting.ModerationModeStrict, CategoryFinesUSD: map[string]float64{"violence": 2, "hate": 1}}
}

func TestModerationWritesDoNotLogPrivatePayloadOnFailure(t *testing.T) {
	db, user := setupModerationEffectsTestDB(t)
	require.NoError(t, db.Exec(`CREATE TRIGGER moderation_test_reject BEFORE INSERT ON moderation_jobs BEGIN SELECT RAISE(ABORT, 'fixture write failed'); END;`).Error)
	var captured bytes.Buffer
	DB = db.Session(&gorm.Session{Logger: logger.New(log.New(&captured, "", 0), logger.Config{LogLevel: logger.Info})})
	job := moderationTestJob(user.Id, "private-sql-failure", ModerationSourceRelayInput, "strict")
	job.Payload = "PRIVATE_MODERATION_TEXT_MUST_NEVER_APPEAR_IN_SQL_LOGS"
	created, err := EnqueueModerationJob(context.Background(), job)
	require.Error(t, err)
	require.False(t, created)
	require.Empty(t, captured.String(), "queue SQL logging must remain silent even when a payload INSERT fails")
}

func TestModerationEffectsOneRequestOneMaximumFineAndReceipt(t *testing.T) {
	db, user := setupModerationEffectsTestDB(t)
	job := claimModerationTestJob(t, moderationTestJob(user.Id, "same-request", ModerationSourceRelayInput, "strict"), "worker-a")
	completion := flaggedModerationCompletion()
	require.NoError(t, CompleteModerationJob(context.Background(), job.ID, "worker-a", completion))
	require.NoError(t, CompleteModerationJob(context.Background(), job.ID, "worker-a", completion), "lost response replay is harmless")
	second := claimModerationTestJob(t, moderationTestJob(user.Id, "same-request", ModerationSourceAssistantInput, "strict"), "worker-b")
	require.NoError(t, CompleteModerationJob(context.Background(), second.ID, "worker-b", completion))
	var stored User
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Equal(t, int(8*common.QuotaPerUnit), stored.Quota)
	require.Equal(t, 123, stored.UsedQuota)
	require.Equal(t, 7, stored.RequestCount)
	for _, table := range []any{&ViolationFeeRecord{}, &AssistantRequestReview{}} {
		var count int64
		require.NoError(t, db.Model(table).Count(&count).Error)
		require.EqualValues(t, 1, count)
	}
	var noticeCount int64
	require.NoError(t, db.Model(&ModerationNotice{}).Count(&noticeCount).Error)
	require.EqualValues(t, 2, noticeCount)
	var result ModerationJob
	require.NoError(t, db.First(&result, job.ID).Error)
	require.Empty(t, result.Payload)
	require.Equal(t, "violence", result.FeeCategory)
	require.Equal(t, int(2*common.QuotaPerUnit), result.ChargedQuota)
	var duplicate ModerationJob
	require.NoError(t, db.First(&duplicate, second.ID).Error)
	require.Zero(t, duplicate.ChargedQuota)
	require.Equal(t, "already_processed", duplicate.FeeStatus)
	stats, err := ModerationStats(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 1, stats.Fined)
	require.EqualValues(t, 2, stats.Completed)
	views, total, err := ListModerationJobs(context.Background(), ModerationJobFilter{UserID: user.Id})
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	for _, view := range views {
		require.Empty(t, view.Payload)
		require.Empty(t, view.CapturedCategoryFinesJSON)
		require.Empty(t, view.LeaseOwner)
	}
	var receipt ViolationFeeRecord
	require.NoError(t, db.First(&receipt).Error)
	appeal, err := SubmitViolationFeeAppeal(user.Id, receipt.ID, "请复核本次误判")
	require.NoError(t, err)
	_, err = ReviewViolationFeeAppeal(99, appeal.ID, true, "已确认误判")
	require.NoError(t, err)
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Equal(t, int(10*common.QuotaPerUnit), stored.Quota)
	require.Equal(t, 123, stored.UsedQuota)
}

func TestModerationEffectsBalancesNeverCreateDebtOrEraseOldDebt(t *testing.T) {
	for _, balance := range []int{int(.5 * common.QuotaPerUnit), 0, -123} {
		t.Run(strconv.Itoa(balance), func(t *testing.T) {
			db, user := setupModerationEffectsTestDB(t)
			require.NoError(t, db.Model(user).Update("quota", balance).Error)
			job := claimModerationTestJob(t, moderationTestJob(user.Id, "balance", ModerationSourceRelayInput, "strict"), "worker")
			require.NoError(t, CompleteModerationJob(context.Background(), job.ID, "worker", flaggedModerationCompletion()))
			var stored User
			require.NoError(t, db.First(&stored, user.Id).Error)
			want := 0
			if balance < 0 {
				want = balance
			}
			require.Equal(t, want, stored.Quota)
			var receipt ViolationFeeRecord
			require.NoError(t, db.First(&receipt).Error)
			charged := balance
			if charged < 0 {
				charged = 0
			}
			require.Equal(t, charged, receipt.ChargedQuota)
		})
	}
}

func TestModerationEffectsPolicyDowngradeDisableAndGroupChange(t *testing.T) {
	for _, test := range []struct {
		name, mode, group string
		enabled           bool
		captured          string
		wantCancelled     bool
		wantFine          float64
	}{
		{"current tolerant", "tolerant", "default", true, "strict", false, 0},
		{"captured tolerant", "strict", "default", true, "tolerant", false, 0},
		{"disabled", "strict", "default", false, "strict", true, 0},
		{"group changed", "strict", "another", true, "strict", true, 0},
		{"fine reduced", "strict", "default", true, "strict", false, .25},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, user := setupModerationEffectsTestDB(t)
			job := claimModerationTestJob(t, moderationTestJob(user.Id, test.name, ModerationSourceRelayInput, test.captured), "worker")
			writeModerationTestPolicy(t, db, test.enabled, test.mode, map[string]float64{"violence": .25})
			require.NoError(t, db.Model(user).Update("group", test.group).Error)
			require.NoError(t, CompleteModerationJob(context.Background(), job.ID, "worker", flaggedModerationCompletion()))
			var stored User
			require.NoError(t, db.First(&stored, user.Id).Error)
			require.Equal(t, int((10-test.wantFine)*common.QuotaPerUnit), stored.Quota)
			var result ModerationJob
			require.NoError(t, db.First(&result, job.ID).Error)
			require.Empty(t, result.Payload)
			if test.wantCancelled {
				require.Equal(t, ModerationJobCancelled, result.Status)
			} else {
				require.Equal(t, ModerationJobCompleted, result.Status)
			}
		})
	}
}

func TestModerationEffectsOutputNeverPenalizesAndFailedJobsNeverFlag(t *testing.T) {
	db, user := setupModerationEffectsTestDB(t)
	job := claimModerationTestJob(t, moderationTestJob(user.Id, "assistant-output", ModerationSourceAssistantOutput, "strict"), "worker")
	require.NoError(t, CompleteModerationJob(context.Background(), job.ID, "worker", flaggedModerationCompletion()))
	var count int64
	require.NoError(t, db.Model(&ViolationFeeRecord{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Model(&ModerationNotice{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	failed := claimModerationTestJob(t, moderationTestJob(user.Id, "failed-input", ModerationSourceRelayInput, "strict"), "worker")
	for attempt := 0; attempt < 3; attempt++ {
		now := common.GetTimestamp()
		require.NoError(t, RetryModerationJob(context.Background(), failed.ID, "worker", now, now, "provider request failed"))
		if attempt < 2 {
			var err error
			failed, err = ClaimModerationJob(context.Background(), "worker", now, 60)
			require.NoError(t, err)
			require.NotNil(t, failed)
		}
	}
	var result ModerationJob
	require.NoError(t, db.First(&result, failed.ID).Error)
	require.Equal(t, ModerationJobFailed, result.Status)
	require.False(t, result.Flagged)
	require.Empty(t, result.Payload)
	require.NoError(t, PopulateUserWalletRiskContext(context.Background(), []*User{user}))
	require.Zero(t, user.WalletRisk.ModerationFlaggedCount)
}

func TestModerationEffectsLeaseLossAndAtomicRollback(t *testing.T) {
	db, user := setupModerationEffectsTestDB(t)
	job := claimModerationTestJob(t, moderationTestJob(user.Id, "lease", ModerationSourceRelayInput, "strict"), "worker")
	require.ErrorIs(t, CompleteModerationJob(context.Background(), job.ID, "other", flaggedModerationCompletion()), ErrModerationLeaseLost)
	require.NoError(t, db.Migrator().DropTable(&ModerationNotice{}))
	require.Error(t, CompleteModerationJob(context.Background(), job.ID, "worker", flaggedModerationCompletion()))
	var stored User
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Equal(t, int(10*common.QuotaPerUnit), stored.Quota)
	var count int64
	require.NoError(t, db.Model(&ViolationFeeRecord{}).Count(&count).Error)
	require.Zero(t, count)
	var result ModerationJob
	require.NoError(t, db.First(&result, job.ID).Error)
	require.Equal(t, ModerationJobRunning, result.Status)
	require.NotEmpty(t, result.Payload)
	require.NoError(t, db.Model(&result).Update("lease_until", common.GetTimestamp()-1).Error)
	require.ErrorIs(t, CompleteModerationJob(context.Background(), job.ID, "worker", flaggedModerationCompletion()), ErrModerationLeaseLost)
	staleCompletion := flaggedModerationCompletion()
	staleCompletion.Now = common.GetTimestamp() - 60
	require.ErrorIs(t, CompleteModerationJob(context.Background(), job.ID, "worker", staleCompletion), ErrModerationLeaseLost, "a timestamp captured before waiting for locks cannot revive an expired lease")
}

func TestModerationPayloadIdempotencyAndMetadataPrivacy(t *testing.T) {
	db, user := setupModerationEffectsTestDB(t)
	job := moderationTestJob(user.Id, "dedup", ModerationSourceRelayInput, "tolerant")
	job.Payload = "password=secret-value " + strings.Repeat("中", ModerationMaxPayloadBytes/3+3)
	created, err := EnqueueModerationJob(context.Background(), job)
	require.NoError(t, err)
	require.True(t, created)
	require.True(t, job.InputTruncated)
	require.LessOrEqual(t, len(job.Payload), ModerationMaxPayloadBytes)
	require.NotContains(t, job.Payload, "secret-value")
	duplicate := moderationTestJob(user.Id, "dedup", ModerationSourceRelayInput, "tolerant")
	duplicate.Payload = "password=secret-value " + strings.Repeat("中", ModerationMaxPayloadBytes/3+3)
	created, err = EnqueueModerationJob(context.Background(), duplicate)
	require.NoError(t, err)
	require.False(t, created)
	encoded, err := json.Marshal(job)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "current user text")
	require.NotContains(t, string(encoded), "secret-value")
	var count int64
	require.NoError(t, db.Model(&ModerationJob{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestModerationRiskOnlyCommittedUserInputsBeforePagination(t *testing.T) {
	db, user := setupModerationEffectsTestDB(t)
	require.NoError(t, db.AutoMigrate(&WalletTransfer{}, &Checkin{}, &TopUp{}))
	recipient := &User{Username: "moderation-recipient", AffCode: "moderation-recipient", Group: "default", Quota: 100, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(recipient).Error)
	writeModerationTestPolicy(t, db, true, "tolerant", nil)
	for _, request := range []string{"risk-1", "risk-2", "risk-3", "risk-4", "risk-5"} {
		job := claimModerationTestJob(t, moderationTestJob(user.Id, request, ModerationSourceRelayInput, "tolerant"), "worker")
		require.NoError(t, CompleteModerationJob(t.Context(), job.ID, "worker", flaggedModerationCompletion()))
	}
	duplicate := claimModerationTestJob(t, moderationTestJob(user.Id, "risk-1", ModerationSourceAssistantInput, "tolerant"), "worker")
	require.NoError(t, CompleteModerationJob(t.Context(), duplicate.ID, "worker", flaggedModerationCompletion()))
	output := claimModerationTestJob(t, moderationTestJob(user.Id, "output-only", ModerationSourceAssistantOutput, "tolerant"), "worker")
	require.NoError(t, CompleteModerationJob(t.Context(), output.ID, "worker", flaggedModerationCompletion()))
	require.NoError(t, PopulateUserWalletRiskContext(t.Context(), []*User{user, recipient}))
	require.EqualValues(t, 5, user.WalletRisk.ModerationFlaggedCount)
	require.EqualValues(t, 5, user.WalletRisk.ModerationReviewedCount)
	require.Equal(t, .8, user.WalletRisk.Score)
	page, total, err := GetAllUsers(&common.PageInfo{Page: 1, PageSize: 1}, false, NewUserSortOptions("risk_score", "desc"))
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.Equal(t, user.Id, page[0].Id)
	high := .8
	options := NewUserSortOptions("id", "desc")
	options.Filters.RiskMin = &high
	page, total, err = GetAllUsers(&common.PageInfo{Page: 1, PageSize: 1}, false, options)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, user.Id, page[0].Id)
	transfer, err := CreateWalletTransfer(user.Id, 10, "moderation-risk-transfer")
	require.NoError(t, err)
	_, err = ClaimWalletTransfer(transfer.Token, recipient.Id)
	require.NoError(t, err)
	require.NoError(t, PopulateUserWalletRiskContext(t.Context(), []*User{recipient}))
	require.Zero(t, recipient.WalletRisk.Score, "content risk never propagates through wallet transfers")
	require.NoError(t, ResetAssistantReviewViolations(user.Id, common.GetTimestamp()))
	require.NoError(t, PopulateUserWalletRiskContext(t.Context(), []*User{user}))
	require.Zero(t, user.WalletRisk.ModerationFlaggedCount)
}

func TestModerationEffectsPostgres(t *testing.T) {
	ensureModerationTestCreditAnchor(t)
	db := openIsolatedPostgresCacheTestDB(t, &User{}, &Option{}, &ModerationJob{}, &ModerationNotice{}, &ViolationFeeRecord{}, &AssistantRequestReview{}, &AssistantReviewReset{})
	previousDB, previousLog, previousRedis := DB, LOG_DB, common.RedisEnabled
	DB, LOG_DB, common.RedisEnabled = db, db, false
	t.Cleanup(func() { DB, LOG_DB, common.RedisEnabled = previousDB, previousLog, previousRedis })
	usePostgresDatabaseType(t)
	user := &User{Username: "moderation-pg", AffCode: "moderation-pg", Group: "default", Quota: int(10 * common.QuotaPerUnit), Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(user).Error)
	writeModerationTestPolicy(t, db, true, "strict", map[string]float64{"violence": 2})
	first := claimModerationTestJob(t, moderationTestJob(user.Id, "concurrent-real-request", ModerationSourceRelayInput, "strict"), "node-a")
	second := claimModerationTestJob(t, moderationTestJob(user.Id, "concurrent-real-request", ModerationSourceAssistantInput, "strict"), "node-b")
	var wait sync.WaitGroup
	errs := make(chan error, 2)
	for _, job := range []*ModerationJob{first, second} {
		wait.Add(1)
		go func(job *ModerationJob) {
			defer wait.Done()
			errs <- CompleteModerationJob(t.Context(), job.ID, job.LeaseOwner, flaggedModerationCompletion())
		}(job)
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var stored User
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Equal(t, int(8*common.QuotaPerUnit), stored.Quota)
	var count int64
	require.NoError(t, db.Model(&ViolationFeeRecord{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	// Hold the same shared policy row as an administrator disabling reviews.
	// The other node must wait, then observe false rather than its stale cache.
	job := claimModerationTestJob(t, moderationTestJob(user.Id, "disable-fence", ModerationSourceRelayInput, "strict"), "node-c")
	tx := db.Begin()
	require.NoError(t, tx.Error)
	_, err := LockModerationSettings(tx)
	require.NoError(t, err)
	require.NoError(t, tx.Model(&Option{}).Where("key = ?", setting.ModerationEnabledOptionKey).Update("value", "false").Error)
	finished := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		finished <- CompleteModerationJob(ctx, job.ID, "node-c", flaggedModerationCompletion())
	}()
	select {
	case err := <-finished:
		tx.Rollback()
		t.Fatalf("completion escaped configuration fence: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	require.NoError(t, tx.Commit().Error)
	require.NoError(t, <-finished)
	var final ModerationJob
	require.NoError(t, db.First(&final, job.ID).Error)
	require.Equal(t, ModerationJobCancelled, final.Status)
	require.Empty(t, final.Payload)
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Equal(t, int(8*common.QuotaPerUnit), stored.Quota)
}

func TestModerationFineCurrencyBasesPreserveDebitsAndWriteTrueUSD(t *testing.T) {
	for _, test := range []struct {
		name                                                  string
		capturedCurrency, currentCurrency, completionCurrency string
		captured, current, completion                         float64
		anchor, legacyUnit                                    int64
		balance, requested, charged                           int
		requestedUSD                                          float64
	}{
		{"missing currency stays legacy", "", "", "", 2, 1.5, 1.25, 500000, 3000000, 10000000, 3750000, 3750000, 7.5},
		{"explicit legacy stays legacy", "legacy_pricing_unit", "legacy_pricing_unit", "legacy_pricing_unit", 2, 1.5, 1.25, 500000, 3000000, 10000000, 3750000, 3750000, 7.5},
		{"USD ignores changed legacy scale", "USD", "USD", "USD", 2, 1.5, 1.25, 500000, 3000000, 10000000, 625000, 625000, 1.25},
		{"USD requires no legacy scale", "USD", "USD", "USD", 2, 1.5, 1.25, 500000, 0, 10000000, 625000, 625000, 1.25},
		{"legacy capture USD current", "", "USD", "USD", .2, .7, .8, 500000, 3000000, 1000000, 350000, 350000, .7},
		{"USD capture legacy current", "USD", "", "USD", .7, .2, .8, 500000, 3000000, 1000000, 350000, 350000, .7},
		{"legacy completion lowers USD ceiling", "USD", "USD", "", .7, .8, .1, 500000, 3000000, 1000000, 300000, 300000, .6},
		{"legacy floor avoids USD roundtrip", "", "", "", .000014, .000014, .000014, 3000000, 500000, 10, 7, 7, 7.0 / 3000000},
		{"mixed floor avoids USD roundtrip", "", "USD", "USD", .000014, .000003, .000003, 3000000, 500000, 10, 7, 7, 7.0 / 3000000},
		{"USD below credit remains warning", "USD", "USD", "USD", .000001, .000001, .000001, 500000, 3000000, 10, 0, 0, .000001},
		{"partial wallet keeps USD receipt", "", "USD", "USD", .000014, .000003, .000003, 3000000, 500000, 3, 7, 3, 7.0 / 3000000},
		{"empty wallet creates no debt", "USD", "USD", "USD", .000002, .000002, .000002, 500000, 3000000, 0, 1, 0, .000002},
		{"old debt is preserved", "USD", "USD", "USD", .000002, .000002, .000002, 500000, 3000000, -123, 1, 0, .000002},
		{"legacy ceiling is not reinterpreted", "", "", "", 1000, 1000, 1000, 500000, 3000000, 3000000000, 3000000000, 3000000000, 6000},
	} {
		t.Run(test.name, func(t *testing.T) {
			moderationTestCurrencyBasis(t, test.anchor, test.legacyUnit)
			db, user := setupModerationEffectsTestDB(t)
			require.NoError(t, db.Model(user).Update("quota", test.balance).Error)
			policy := setting.ModerationGroupPolicy{Mode: "strict", AmountCurrency: test.currentCurrency, CategoryFinesUSD: map[string]float64{"violence": test.current}}
			require.NoError(t, db.Model(&Option{}).Where("key = ?", setting.ModerationGroupPoliciesOptionKey).Update("value", setting.ModerationGroupPoliciesJSON(map[string]setting.ModerationGroupPolicy{"default": policy})).Error)
			job := moderationTestJob(user.Id, test.name, ModerationSourceRelayInput, "strict")
			job.CapturedAmountCurrency = test.capturedCurrency
			capturedJSON, err := json.Marshal(map[string]float64{"violence": test.captured})
			require.NoError(t, err)
			job.CapturedCategoryFinesJSON = string(capturedJSON)
			claimed := claimModerationTestJob(t, job, "worker")
			require.Equal(t, test.capturedCurrency, claimed.CapturedAmountCurrency)
			require.JSONEq(t, string(capturedJSON), claimed.CapturedCategoryFinesJSON)
			completion := flaggedModerationCompletion()
			completion.Categories = []string{"violence"}
			completion.AmountCurrency = test.completionCurrency
			completion.CategoryFinesUSD = map[string]float64{"violence": test.completion}
			require.NoError(t, CompleteModerationJob(t.Context(), claimed.ID, "worker", completion))
			require.NoError(t, CompleteModerationJob(t.Context(), claimed.ID, "worker", completion), "replay must not debit twice")
			var stored User
			require.NoError(t, db.First(&stored, user.Id).Error)
			require.Equal(t, test.balance-test.charged, stored.Quota)
			require.Equal(t, 123, stored.UsedQuota)
			require.Equal(t, 7, stored.RequestCount)
			var records []ViolationFeeRecord
			require.NoError(t, db.Find(&records).Error)
			if test.requested == 0 {
				require.Empty(t, records)
			} else {
				require.Len(t, records, 1)
				require.Equal(t, "USD", records[0].AmountCurrency)
				require.Equal(t, test.requested, records[0].RequestedQuota)
				require.Equal(t, test.charged, records[0].ChargedQuota)
				require.InDelta(t, float64(test.charged)/float64(test.anchor), records[0].ChargedAmountUSD, 1e-12)
				require.InDelta(t, test.requestedUSD, records[0].RequestedAmountUSD, 1e-12)
			}
			var notice ModerationNotice
			require.NoError(t, db.First(&notice).Error)
			require.Equal(t, test.charged, notice.ChargedQuota)
		})
	}
}

func TestModerationCurrencyFailureKeepsWalletAndJobUnchanged(t *testing.T) {
	for _, test := range []string{"uninitialized anchor", "invalid completion currency", "invalid captured currency", "invalid legacy unit"} {
		t.Run(test, func(t *testing.T) {
			moderationTestCurrencyBasis(t, 500000, 3000000)
			db, user := setupModerationEffectsTestDB(t)
			job := claimModerationTestJob(t, moderationTestJob(user.Id, test, ModerationSourceRelayInput, "strict"), "worker")
			completion := flaggedModerationCompletion()
			switch test {
			case "uninitialized anchor":
				common.ClearCreditsPerUSD()
			case "invalid completion currency":
				completion.AmountCurrency = "CNY"
			case "invalid captured currency":
				require.NoError(t, db.Model(job).Update("captured_amount_currency", "CNY").Error)
			case "invalid legacy unit":
				common.QuotaPerUnit = 0
			}
			require.Error(t, CompleteModerationJob(t.Context(), job.ID, "worker", completion))
			var stored User
			require.NoError(t, db.First(&stored, user.Id).Error)
			require.Equal(t, user.Quota, stored.Quota)
			var pending ModerationJob
			require.NoError(t, db.First(&pending, job.ID).Error)
			require.Equal(t, ModerationJobRunning, pending.Status)
			require.NotEmpty(t, pending.Payload)
			for _, table := range []any{&ViolationFeeRecord{}, &ModerationNotice{}} {
				var count int64
				require.NoError(t, db.Model(table).Count(&count).Error)
				require.Zero(t, count)
			}
		})
	}
}

func TestModerationUSDRecordDoesNotRewriteHistoricalAmounts(t *testing.T) {
	moderationTestCurrencyBasis(t, 500000, 3000000)
	db, user := setupModerationEffectsTestDB(t)
	old := ViolationFeeRecord{UserID: user.Id, RequestID: "old-fee", RequestedAmountUSD: 2, ChargedAmountUSD: .5, RequestedQuota: 1000000, ChargedQuota: 250000, Status: ViolationFeeRecordStatusCharged}
	require.NoError(t, db.Create(&old).Error)
	job := claimModerationTestJob(t, moderationTestJob(user.Id, "new-fee", ModerationSourceRelayInput, "strict"), "worker")
	require.NoError(t, CompleteModerationJob(t.Context(), job.ID, "worker", flaggedModerationCompletion()))
	var persisted ViolationFeeRecord
	require.NoError(t, db.First(&persisted, old.ID).Error)
	require.Equal(t, old, persisted)
	var fresh ViolationFeeRecord
	require.NoError(t, db.Where("request_id = ?", "new-fee").First(&fresh).Error)
	require.Equal(t, "USD", fresh.AmountCurrency)
	require.Equal(t, 12.0, fresh.RequestedAmountUSD)
	require.Equal(t, 12.0, fresh.ChargedAmountUSD)
}

func TestModerationWarningOnlyNeedsNoMonetaryBasis(t *testing.T) {
	moderationTestCurrencyBasis(t, 500000, 3000000)
	db, user := setupModerationEffectsTestDB(t)
	writeModerationTestPolicy(t, db, true, "strict", map[string]float64{})
	job := claimModerationTestJob(t, moderationTestJob(user.Id, "zero-fine", ModerationSourceRelayInput, "strict"), "worker")
	common.ClearCreditsPerUSD()
	common.QuotaPerUnit = 0
	require.NoError(t, CompleteModerationJob(t.Context(), job.ID, "worker", flaggedModerationCompletion()))
	var stored User
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Equal(t, user.Quota, stored.Quota)
	var notice ModerationNotice
	require.NoError(t, db.First(&notice).Error)
	require.Zero(t, notice.ChargedQuota)
	var records int64
	require.NoError(t, db.Model(&ViolationFeeRecord{}).Count(&records).Error)
	require.Zero(t, records)
}

func TestModerationOwnerDeletionErasesPendingPrivateData(t *testing.T) {
	db, user := setupModerationEffectsTestDB(t)
	migrateUserAssistantData(t)
	job := moderationTestJob(user.Id, "pending-at-delete", ModerationSourceRelayInput, "strict")
	created, err := EnqueueModerationJob(t.Context(), job)
	require.NoError(t, err)
	require.True(t, created)
	require.NoError(t, db.Create(&ModerationNotice{UserID: user.Id, JobID: job.ID, RequestID: job.RequestID, Source: job.Source, Mode: "tolerant", CategoriesJSON: `["violence"]`}).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if err := lockAssistantOwner(tx, user.Id); err != nil {
			return err
		}
		if err := deleteUserAssistantData(tx, user.Id); err != nil {
			return err
		}
		return tx.Delete(user).Error
	}))
	for _, record := range []any{&ModerationJob{}, &ModerationNotice{}} {
		var count int64
		require.NoError(t, db.Model(record).Count(&count).Error)
		require.Zero(t, count)
	}
	created, err = EnqueueModerationJob(t.Context(), moderationTestJob(user.Id, "after-delete", ModerationSourceRelayInput, "strict"))
	require.Error(t, err)
	require.False(t, created)
}

func TestModerationDisabledOwnerAndUnreviewedCapsuleCannotBeFined(t *testing.T) {
	db, user := setupModerationEffectsTestDB(t)
	job := moderationTestJob(user.Id, "over-limit", ModerationSourceRelayInput, "strict")
	job.Payload = "[UNREVIEWED_OVERSIZED_TEXT]"
	job.InputTruncated = true
	claimed := claimModerationTestJob(t, job, "worker")
	require.NoError(t, CompleteModerationJob(t.Context(), claimed.ID, "worker", flaggedModerationCompletion()))
	var result ModerationJob
	require.NoError(t, db.First(&result, claimed.ID).Error)
	require.Equal(t, ModerationJobCancelled, result.Status)
	require.False(t, result.Flagged)
	require.Empty(t, result.Payload)
	require.NoError(t, db.Model(user).Update("status", common.UserStatusDisabled).Error)
	created, err := EnqueueModerationJob(t.Context(), moderationTestJob(user.Id, "disabled-enqueue", ModerationSourceRelayInput, "strict"))
	require.ErrorIs(t, err, ErrModerationJobInvalid)
	require.False(t, created)
	var count int64
	require.NoError(t, db.Model(&ViolationFeeRecord{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestModerationFineRoundsDownToActualWalletUnits(t *testing.T) {
	for _, test := range []struct {
		name             string
		amount           float64
		balance, charged int
	}{
		{"below one unit", .000001, 10, 0},
		{"exact one unit", .000002, 10, 1},
		{"fractional unit", .000003, 10, 1},
		{"exact decimal boundary", .000014, 10, 7},
		{"partial balance", .000014, 3, 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, user := setupModerationEffectsTestDB(t)
			require.NoError(t, db.Model(user).Update("quota", test.balance).Error)
			writeModerationTestPolicy(t, db, true, "strict", map[string]float64{"violence": test.amount})
			job := moderationTestJob(user.Id, test.name, ModerationSourceRelayInput, "strict")
			encoded, err := json.Marshal(map[string]float64{"violence": test.amount})
			require.NoError(t, err)
			job.CapturedCategoryFinesJSON = string(encoded)
			claimed := claimModerationTestJob(t, job, "worker")
			completion := flaggedModerationCompletion()
			completion.Categories = []string{"violence"}
			completion.CategoryFinesUSD = map[string]float64{"violence": test.amount}
			require.NoError(t, CompleteModerationJob(t.Context(), claimed.ID, "worker", completion))
			var stored User
			require.NoError(t, db.First(&stored, user.Id).Error)
			require.Equal(t, test.balance-test.charged, stored.Quota)
			var records []ViolationFeeRecord
			require.NoError(t, db.Find(&records).Error)
			if test.charged == 0 {
				require.Empty(t, records)
			} else {
				require.Len(t, records, 1)
				require.Equal(t, test.charged, records[0].ChargedQuota)
				require.Equal(t, float64(test.charged)/common.QuotaPerUnit, records[0].ChargedAmountUSD)
				require.LessOrEqual(t, records[0].ChargedAmountUSD, test.amount)
			}
			var notice ModerationNotice
			require.NoError(t, db.First(&notice).Error)
			require.Equal(t, test.charged, notice.ChargedQuota)
		})
	}
}
