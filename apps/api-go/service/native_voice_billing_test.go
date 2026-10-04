package service

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/pkg/billingexpr"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	hosttypes "github.com/LIghtJUNction/api.lmm.best/types"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const nativeVoiceMinuteExpr = `tier("minute", audio_s * (50000.0 / 60))`

func freezeNativeVoiceTestPrice(info *relaycommon.RelayInfo, expression string, group float64) {
	info.OriginModelName = "gpt-live-1"
	info.StartTime = time.Now()
	info.PriceData = hosttypes.PriceData{GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: group}}
	info.TieredBillingSnapshot = &billingexpr.BillingSnapshot{
		BillingMode: "tiered_expr", ModelName: info.OriginModelName,
		ExprString: expression, ExprHash: billingexpr.ExprHashString(expression),
		GroupRatio: group, QuotaPerUnit: 500000, ExprVersion: 1,
	}
}

func enableNativeVoiceTestLogs(t *testing.T, db *gorm.DB, info *relaycommon.RelayInfo) {
	t.Helper()
	require.NoError(t, db.AutoMigrate(&model.Log{}, &model.Channel{}, &model.PublicRelayContribution{}))
	previousLogDB, previousLogEnabled := model.LOG_DB, common.LogConsumeEnabled
	model.LOG_DB, common.LogConsumeEnabled = db, true
	t.Cleanup(func() { model.LOG_DB, common.LogConsumeEnabled = previousLogDB, previousLogEnabled })
	provider := model.Channel{Name: t.Name()}
	require.NoError(t, db.Create(&provider).Error)
	info.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: provider.Id}
}

func readNativeVoiceTestLog(t *testing.T, db *gorm.DB) (model.Log, map[string]any) {
	t.Helper()
	var logs []model.Log
	require.NoError(t, db.Where("type = ?", model.LogTypeConsume).Find(&logs).Error)
	require.Len(t, logs, 1, "finalization must record exactly one consumption")
	var other map[string]any
	require.NoError(t, json.Unmarshal([]byte(logs[0].Other), &other))
	return logs[0], other
}

func assertNativeVoiceBalances(t *testing.T, db *gorm.DB, info *relaycommon.RelayInfo, subscription int64, wallet, token int) {
	t.Helper()
	var sub model.UserSubscription
	require.NoError(t, db.Where("user_id = ?", info.UserId).First(&sub).Error)
	require.Equal(t, subscription, sub.AmountUsed)
	assertWalletBudgetBalances(t, db, info, 1000000-wallet, 1000000-token, token)
}

func TestNativeVoiceBillingDurationUsesOneOwnerAndNoSyntheticTokens(t *testing.T) {
	for _, tc := range []struct {
		name, preference string
		grant            int64
		group            float64
		quota, wallet    int
		sub              int64
	}{
		{"wallet", "wallet_only", 100000, 1, 12500, 12500, 0},
		{"partial subscription", "subscription_first", 100, 1, 12500, 12400, 100},
		{"group discount", "wallet_only", 100000, 0.5, 6250, 6250, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, info, c := subscriptionBillingFixture(t, tc.grant, true, tc.preference)
			freezeNativeVoiceTestPrice(info, nativeVoiceMinuteExpr, tc.group)
			enableNativeVoiceTestLogs(t, db, info)
			owner, apiErr := NewNativeVoiceBilling(c, info, 10)
			require.Nil(t, apiErr)
			require.NotNil(t, owner)
			require.Same(t, owner.session, info.Billing)
			require.NoError(t, owner.EnsureBudget(30))
			require.NoError(t, owner.EnsureBudget(30))
			require.NoError(t, owner.EnsureBudget(20), "a lower snapshot must not release an active budget")
			require.Equal(t, tc.quota, owner.session.GetReservedBudget())
			require.NoError(t, owner.Finalize(30, false, true, "session.closed"))
			require.NoError(t, owner.Finalize(300, true, false, "duplicate disconnect"))
			require.Error(t, owner.EnsureBudget(40))
			assertNativeVoiceBalances(t, db, info, tc.sub, tc.wallet, tc.quota)
			log, other := readNativeVoiceTestLog(t, db)
			require.Equal(t, tc.quota, log.Quota)
			require.Zero(t, log.PromptTokens)
			require.Zero(t, log.CompletionTokens)
			require.EqualValues(t, 30, other["audio_seconds"])
			require.Equal(t, "reported", other["audio_usage_status"])
			require.Equal(t, true, other["usage_finalized"])
			require.Equal(t, false, other["usage_estimated"])
			require.NotContains(t, other, "usage_finalization_pending")
			require.Equal(t, "tiered_expr", other["billing_mode"])
			require.Equal(t, "expression", other["price_unit"])
			require.Equal(t, billingexpr.ExprHashString(nativeVoiceMinuteExpr), other["expr_hash"])
		})
	}
}

func TestNativeVoiceBillingFreeAndFixedContracts(t *testing.T) {
	previousRatios := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios)) })
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"native-voice-free":0}`))
	for _, tc := range []struct {
		name     string
		group    float64
		fixed    bool
		price    float64
		final    bool
		seconds  float64
		zeroKey  bool
		wantFee  int
		freeExpr bool
	}{
		{"free group", 0, false, 0, true, 30, false, 0, true},
		{"free fixed", 1, true, 0, true, 30, false, 0, false},
		{"explicit zero ratio", 1, false, 0, true, 30, true, 0, false},
		{"fixed session", 1, true, 0.01, true, 30, false, 5000, false},
		{"failed fixed initialization refunds", 1, true, 0.01, false, 0, false, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, info, c := subscriptionBillingFixture(t, 100000, true, "subscription_first")
			info.OriginModelName = "native-voice-fixed"
			info.PriceData = hosttypes.PriceData{UsePrice: tc.fixed, ModelPrice: tc.price, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: tc.group}}
			if tc.freeExpr {
				freezeNativeVoiceTestPrice(info, nativeVoiceMinuteExpr, 0)
			}
			if tc.zeroKey {
				info.OriginModelName = "native-voice-free"
			}
			enableNativeVoiceTestLogs(t, db, info)
			if tc.wantFee == 0 && tc.final {
				require.NoError(t, db.Model(&model.User{}).Where("id = ?", info.UserId).Update("quota", 0).Error)
				require.NoError(t, db.Model(&model.Token{}).Where("id = ?", info.TokenId).Update("remain_quota", 0).Error)
			}
			owner, apiErr := NewNativeVoiceBilling(c, info, 10)
			require.Nil(t, apiErr)
			require.NoError(t, owner.EnsureBudget(100))
			require.NoError(t, owner.Finalize(tc.seconds, false, tc.final, "closed"))
			log, other := readNativeVoiceTestLog(t, db)
			require.Equal(t, tc.wantFee, log.Quota)
			if tc.fixed {
				require.Equal(t, "session", other["price_unit"])
			}
			if tc.wantFee == 0 && tc.final {
				var records int64
				require.NoError(t, db.Model(&model.SubscriptionPreConsumeRecord{}).Count(&records).Error)
				require.Zero(t, records, "free sessions must not hold one subscription quota")
			}
		})
	}
}

func TestNativeVoiceBillingFreezesExpressionAndOriginalRequest(t *testing.T) {
	db, info, c := subscriptionBillingFixture(t, 100000, true, "wallet_only")
	expression := `param("mode") == "paid" && header("x-tier") == "paid" ? tier("paid", audio_s * (50000.0 / 60)) : tier("free", 0)`
	freezeNativeVoiceTestPrice(info, expression, 1)
	info.BillingRequestInput = &billingexpr.RequestInput{Body: []byte(`{"mode":"paid"}`), Headers: map[string]string{"x-tier": "paid"}}
	enableNativeVoiceTestLogs(t, db, info)
	owner, apiErr := NewNativeVoiceBilling(c, info, 10)
	require.Nil(t, apiErr)
	info.TieredBillingSnapshot.ExprString = "0"
	info.TieredBillingSnapshot.ExprHash = billingexpr.ExprHashString("0")
	info.TieredBillingSnapshot.GroupRatio = 0
	info.BillingRequestInput.Body[9] = 'f'
	info.BillingRequestInput.Headers["x-tier"] = "free"
	info.PriceData.GroupRatioInfo.GroupRatio = 0
	require.NoError(t, owner.EnsureBudget(30))
	require.NoError(t, owner.Finalize(30, false, true, "session.closed"))
	assertNativeVoiceBalances(t, db, info, 0, 12500, 12500)
	log, other := readNativeVoiceTestLog(t, db)
	require.Equal(t, 12500, log.Quota)
	decoded, err := base64.StdEncoding.DecodeString(other["expr_b64"].(string))
	require.NoError(t, err)
	require.Equal(t, expression, string(decoded))
	require.Equal(t, "paid", other["matched_tier"])
}

func TestNativeVoiceBillingBudgetRejectsWithoutDebit(t *testing.T) {
	db, info, c := subscriptionBillingFixture(t, 100000, true, "wallet_only")
	freezeNativeVoiceTestPrice(info, nativeVoiceMinuteExpr, 1)
	enableNativeVoiceTestLogs(t, db, info)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", info.UserId).Update("quota", 5000).Error)
	owner, apiErr := NewNativeVoiceBilling(c, info, 10)
	require.Nil(t, apiErr)
	reserved := owner.session.GetReservedBudget()
	require.Equal(t, 4167, reserved)
	require.Error(t, owner.EnsureBudget(30))
	require.Equal(t, reserved, owner.session.GetReservedBudget())
	var user model.User
	require.NoError(t, db.First(&user, info.UserId).Error)
	require.Equal(t, 5000-reserved, user.Quota)
	require.NoError(t, owner.Finalize(5, false, false, "budget_exhausted"))
	require.NoError(t, db.First(&user, info.UserId).Error)
	require.Equal(t, 5000-2083, user.Quota)
	_, other := readNativeVoiceTestLog(t, db)
	require.Equal(t, true, other["usage_finalization_pending"])
	for _, seconds := range []float64{-1, math.NaN(), math.Inf(1)} {
		require.Error(t, validateNativeVoiceSeconds(seconds))
	}
}

func TestNativeVoiceBillingEstimatedAndServedTailRemainAuditable(t *testing.T) {
	db, info, c := subscriptionBillingFixture(t, 100000, true, "wallet_only")
	freezeNativeVoiceTestPrice(info, nativeVoiceMinuteExpr, 1)
	enableNativeVoiceTestLogs(t, db, info)
	owner, apiErr := NewNativeVoiceBilling(c, info, 10)
	require.Nil(t, apiErr)
	require.NoError(t, owner.Finalize(12, true, false, "client_disconnect"))
	log, other := readNativeVoiceTestLog(t, db)
	require.Equal(t, 5000, log.Quota)
	require.Equal(t, "estimated", other["audio_usage_status"])
	require.Equal(t, "active_session_clock", other["usage_estimate_basis"])
	require.Equal(t, true, other["usage_finalization_pending"])
	require.EqualValues(t, 833, other["over_budget_quantity"])
	assertNativeVoiceBalances(t, db, info, 0, 5000, 5000)
}

func TestNativeVoiceBillingRejectsTokenPriceAndMalformedSnapshot(t *testing.T) {
	_, info, c := subscriptionBillingFixture(t, 100000, true, "wallet_only")
	info.OriginModelName = "gpt-live-1"
	info.PriceData = hosttypes.PriceData{ModelRatio: 1, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}}
	owner, apiErr := NewNativeVoiceBilling(c, info, 10)
	require.Nil(t, owner)
	require.NotNil(t, apiErr)
	require.Equal(t, types.ErrorCodeModelPriceError, apiErr.GetErrorCode())
	require.Nil(t, info.Billing, "an incompatible price must fail before reserving or contacting upstream")
	freezeNativeVoiceTestPrice(info, `tier("tokens", p * 3)`, 1)
	owner, apiErr = NewNativeVoiceBilling(c, info, 10)
	require.Nil(t, owner)
	require.NotNil(t, apiErr)
	require.Nil(t, info.Billing)
	freezeNativeVoiceTestPrice(info, nativeVoiceMinuteExpr, 1)
	info.TieredBillingSnapshot.ExprHash = "wrong"
	owner, apiErr = NewNativeVoiceBilling(c, info, 10)
	require.Nil(t, owner)
	require.NotNil(t, apiErr)
	require.Nil(t, info.Billing)
}

func TestNativeVoiceBillingConfirmedZeroRefundsEntireReservation(t *testing.T) {
	db, info, c := subscriptionBillingFixture(t, 100000, true, "wallet_only")
	freezeNativeVoiceTestPrice(info, nativeVoiceMinuteExpr, 1)
	enableNativeVoiceTestLogs(t, db, info)
	owner, apiErr := NewNativeVoiceBilling(c, info, 15)
	require.Nil(t, apiErr)
	require.NoError(t, owner.Finalize(0, false, true, "session.closed"))
	assertNativeVoiceBalances(t, db, info, 0, 0, 0)
	log, other := readNativeVoiceTestLog(t, db)
	require.Zero(t, log.Quota)
	require.EqualValues(t, 0, other["audio_seconds"])
	require.Equal(t, "reported", other["audio_usage_status"])
	require.Equal(t, true, other["usage_finalized"])
}

func TestNativeVoiceBillingSettlementFailureIsLoggedAndNotReplayed(t *testing.T) {
	db, info, c := subscriptionBillingFixture(t, 100000, true, "wallet_only")
	freezeNativeVoiceTestPrice(info, nativeVoiceMinuteExpr, 1)
	enableNativeVoiceTestLogs(t, db, info)
	owner, apiErr := NewNativeVoiceBilling(c, info, 10)
	require.Nil(t, apiErr)
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("native_voice_token_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "tokens" {
			tx.AddError(errors.New("injected final token write failure"))
		}
	}))
	err := owner.Finalize(30, false, true, "session.closed")
	require.ErrorContains(t, err, "injected final token write failure")
	require.NoError(t, db.Callback().Update().Remove("native_voice_token_failure"))
	require.Equal(t, err, owner.Finalize(30, false, true, "duplicate"))
	log, other := readNativeVoiceTestLog(t, db)
	require.Equal(t, 12500, log.Quota)
	admin := other["admin_info"].(map[string]any)
	require.Contains(t, admin["billing_settlement_error"], "injected final token write failure")
	var user model.User
	require.NoError(t, db.First(&user, info.UserId).Error)
	require.Equal(t, 1000000-12500, user.Quota, "the committed wallet debit must not be refunded or replayed")
}

func TestNativeVoiceBillingConcurrentFinalizationDoesNotDoubleCharge(t *testing.T) {
	db, info, c := subscriptionBillingFixture(t, 100000, true, "wallet_only")
	freezeNativeVoiceTestPrice(info, nativeVoiceMinuteExpr, 1)
	enableNativeVoiceTestLogs(t, db, info)
	owner, apiErr := NewNativeVoiceBilling(c, info, 15)
	require.Nil(t, apiErr)
	var workers sync.WaitGroup
	errors := make(chan error, 16)
	for i := 0; i < 16; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			errors <- owner.Finalize(30, false, true, "session.closed")
		}()
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	assertNativeVoiceBalances(t, db, info, 0, 12500, 12500)
	log, _ := readNativeVoiceTestLog(t, db)
	require.Equal(t, 12500, log.Quota)
}

func TestNativeVoiceBillingFinalPriceFailureSettlesOnlyAuthorizedBudget(t *testing.T) {
	for _, tc := range []struct {
		name, expression string
		seconds          float64
	}{
		{"expression division by zero at final usage", `tier("duration", audio_s * (50000.0 / 60) / (30 - audio_s))`, 30},
		{"finite duration exceeds quota domain", nativeVoiceMinuteExpr, 1e12},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, info, c := subscriptionBillingFixture(t, 100000, true, "wallet_only")
			freezeNativeVoiceTestPrice(info, tc.expression, 1)
			enableNativeVoiceTestLogs(t, db, info)
			owner, apiErr := NewNativeVoiceBilling(c, info, 10)
			require.Nil(t, apiErr, "the initial estimate must be valid")
			reserved := owner.session.GetReservedBudget()
			err := owner.Finalize(tc.seconds, false, true, "session.closed")
			require.Error(t, err)
			require.Equal(t, err, owner.Finalize(tc.seconds, false, true, "duplicate"))
			require.False(t, owner.session.NeedsRefund(), "no unsettled financial hold may remain after the audited fallback")
			require.Error(t, owner.EnsureBudget(tc.seconds))
			assertNativeVoiceBalances(t, db, info, 0, reserved, reserved)
			log, other := readNativeVoiceTestLog(t, db)
			require.Equal(t, reserved, log.Quota)
			require.EqualValues(t, tc.seconds, other["audio_seconds"])
			require.Equal(t, true, other["usage_finalized"], "reported duration and a pricing failure are separate facts")
			require.Equal(t, true, other["billing_price_error"])
			require.Equal(t, true, other["billed_at_reserved_budget"])
			require.Equal(t, true, other["billing_estimated"])
			require.Equal(t, "authorized_session_budget", other["billing_estimate_basis"])
			require.Equal(t, true, other["billing_finalization_pending"])
			require.NotContains(t, other, "matched_tier")
			require.NotContains(t, other, "request_rules")
		})
	}
}
