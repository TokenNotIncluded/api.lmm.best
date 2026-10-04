package service

import (
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/logger"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/pkg/billingexpr"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

// NativeVoiceBilling owns one duration-priced session. Prices, the group
// discount and expression request inputs are frozen before contacting upstream.
// seconds passed to EnsureBudget and Finalize are cumulative, never deltas.
type NativeVoiceBilling struct {
	mu             sync.Mutex
	ctx            *gin.Context
	info           *relaycommon.RelayInfo
	session        *BillingSession
	modelName      string
	groupRatio     float64
	quotaPerUnit   float64
	fixedPrice     float64
	fixed          bool
	free           bool
	initialSeconds float64
	request        billingexpr.RequestInput
	snapshot       *billingexpr.BillingSnapshot
	finished       bool
	finalError     error
}

// NewNativeVoiceBilling uses the price helper's frozen snapshot for native Live,
// transcription and translation sessions. The controller must set the original
// BillingRequestInput and NativeVoiceReserveSeconds before calling that helper.
func NewNativeVoiceBilling(c *gin.Context, info *relaycommon.RelayInfo, initialSeconds float64) (*NativeVoiceBilling, *types.NewAPIError) {
	if c == nil || info == nil || strings.TrimSpace(info.OriginModelName) == "" {
		return nil, nativeVoicePriceError(errors.New("native voice billing context and model are required"))
	}
	if err := validateNativeVoiceSeconds(initialSeconds); err != nil {
		return nil, nativeVoicePriceError(err)
	}
	groupRatio := info.PriceData.GroupRatioInfo.GroupRatio
	if !finiteNonnegative(groupRatio) || !finiteNonnegative(common.QuotaPerUnit) || common.QuotaPerUnit == 0 {
		return nil, nativeVoicePriceError(errors.New("invalid native voice group ratio or quota unit"))
	}
	owner := &NativeVoiceBilling{
		ctx: c, info: info, modelName: info.OriginModelName,
		groupRatio: groupRatio, quotaPerUnit: common.QuotaPerUnit, initialSeconds: initialSeconds,
		request: cloneNativeVoiceRequest(info),
	}
	if err := owner.freezePrice(info); err != nil {
		return nil, nativeVoicePriceError(err)
	}
	quota, tier, err := owner.quotaFor(initialSeconds)
	if err != nil {
		return nil, nativeVoicePriceError(err)
	}
	if !owner.free && quota == 0 {
		// An expression may be zero for a short session and paid later. Only an
		// explicit free price/group contract may bypass the authorization gate.
		quota = 1
	}
	if owner.snapshot != nil {
		owner.snapshot.EstimatedQuotaAfterGroup = quota
		if tier != nil {
			owner.snapshot.EstimatedQuotaBeforeGroup = tier.ActualQuotaBeforeGroup
			owner.snapshot.EstimatedTier = tier.MatchedTier
		}
		auditSnapshot := *owner.snapshot
		info.TieredBillingSnapshot = &auditSnapshot
		request := cloneNativeVoiceInput(owner.request)
		info.BillingRequestInput = &request
	}
	info.PriceData.FreeModel = owner.free
	info.PriceData.UsePrice = owner.fixed
	info.PriceData.ModelPrice = owner.fixedPrice
	info.PriceData.QuotaToPreConsume = quota
	info.ForcePreConsume = true
	session, apiErr := NewBudgetBillingSession(c, info, quota)
	if apiErr != nil {
		return nil, apiErr
	}
	owner.session = session
	info.Billing = session
	return owner, nil
}

func nativeVoicePriceError(err error) *types.NewAPIError {
	return types.NewErrorWithStatusCode(err, types.ErrorCodeModelPriceError, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
}

func finiteNonnegative(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func validateNativeVoiceSeconds(seconds float64) error {
	if !finiteNonnegative(seconds) {
		return errors.New("native voice duration must be finite and nonnegative")
	}
	return nil
}

func cloneNativeVoiceInput(input billingexpr.RequestInput) billingexpr.RequestInput {
	cloned := billingexpr.RequestInput{Body: append([]byte(nil), input.Body...), Headers: make(map[string]string, len(input.Headers))}
	for name, value := range input.Headers {
		cloned.Headers[name] = value
	}
	return cloned
}

func cloneNativeVoiceRequest(info *relaycommon.RelayInfo) billingexpr.RequestInput {
	input := billingexpr.RequestInput{Headers: info.RequestHeaders}
	if info.BillingRequestInput != nil {
		input = *info.BillingRequestInput
	}
	return cloneNativeVoiceInput(input)
}

func (b *NativeVoiceBilling) freezePrice(info *relaycommon.RelayInfo) error {
	if snapshot := info.TieredBillingSnapshot; snapshot != nil && snapshot.BillingMode == "tiered_expr" {
		if strings.TrimSpace(snapshot.ExprString) == "" || snapshot.ExprHash != billingexpr.ExprHashString(snapshot.ExprString) {
			return fmt.Errorf("native voice model %s has an invalid frozen expression", b.modelName)
		}
		usedVars := billingexpr.UsedVars(snapshot.ExprString)
		if !usedVars["audio_s"] {
			for _, variable := range []string{"p", "c", "len", "cr", "cc", "cc1h", "img", "img_o", "ai", "ao", "cr_text", "cr_img", "cr_audio"} {
				if usedVars[variable] {
					return fmt.Errorf("native voice model %s has a token expression without audio_s", b.modelName)
				}
			}
		}
		cloned := *snapshot
		b.snapshot = &cloned
		b.snapshot.GroupRatio = b.groupRatio
		b.quotaPerUnit = snapshot.QuotaPerUnit
		if !finiteNonnegative(b.quotaPerUnit) || b.quotaPerUnit == 0 {
			return errors.New("invalid frozen native voice quota unit")
		}
		b.free = b.groupRatio == 0
		return nil
	}
	if info.PriceData.UsePrice {
		price := info.PriceData.ModelPrice
		if !finiteNonnegative(price) {
			return fmt.Errorf("native voice model %s has an invalid fixed price", b.modelName)
		}
		b.fixed, b.fixedPrice = true, price
		b.free = price == 0 || b.groupRatio == 0
		return nil
	}
	if info.PriceData.FreeModel {
		b.free = true
		return nil
	}
	// A configured zero ratio remains an explicit administrator free override.
	// Nonzero text/audio token ratios are never treated as dollars per minute.
	if ratio, exists, _ := ratio_setting.GetModelRatio(b.modelName); exists && ratio == 0 {
		b.free = true
		return nil
	}
	if b.groupRatio == 0 {
		b.free = true
		return nil
	}
	return fmt.Errorf("native voice model %s requires a duration expression using audio_s or a fixed session price", b.modelName)
}

func (b *NativeVoiceBilling) quotaFor(seconds float64) (int, *billingexpr.TieredResult, error) {
	if err := validateNativeVoiceSeconds(seconds); err != nil {
		return 0, nil, err
	}
	if b.free {
		return 0, nil, nil
	}
	if b.snapshot != nil {
		result, err := billingexpr.ComputeTieredQuotaWithRequest(b.snapshot, billingexpr.TokenParams{AudioSeconds: &seconds}, b.request)
		if err != nil {
			return 0, nil, err
		}
		if result.Clamp != nil {
			return 0, nil, result.Clamp
		}
		if result.ActualQuotaBeforeGroup < 0 || result.ActualQuotaAfterGroup < 0 {
			return 0, nil, errors.New("native voice billing expression produced a negative price")
		}
		return enforceTieredMinimumQuota(result.ActualQuotaAfterGroup, &result, b.groupRatio), &result, nil
	}
	dollars := decimal.NewFromFloat(b.fixedPrice)
	amount := dollars.Mul(decimal.NewFromFloat(b.quotaPerUnit)).Mul(decimal.NewFromFloat(b.groupRatio))
	quota, err := common.QuotaFromDecimalStrict(amount)
	if err == nil && quota == 0 && amount.IsPositive() {
		quota = 1
	}
	return quota, nil, err
}

// EnsureBudget authorizes the cumulative charge before more work is forwarded.
// Reserving only a subscription's grant is insufficient: the session's budget
// also includes authorized wallet overflow and the full finite-key reservation.
func (b *NativeVoiceBilling) EnsureBudget(seconds float64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.finished {
		return errors.New("native voice billing is already finalized")
	}
	quota, _, err := b.quotaFor(seconds)
	if err != nil {
		return err
	}
	if quota <= b.session.GetReservedBudget() {
		return nil
	}
	return b.session.Reserve(quota)
}

// Finalize settles and logs once, including sessions with duration but no token
// usage. finalized reports whether the protocol supplied confirmed final usage;
// estimated reports whether seconds were derived from an explicitly labeled
// fallback, such as forwarded PCM bytes. A disconnect alone confirms neither.
func (b *NativeVoiceBilling) Finalize(seconds float64, estimated, finalized bool, reason string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.finished {
		return b.finalError
	}
	quota, tier, priceErr := b.quotaFor(seconds)
	if priceErr != nil {
		// The upstream work has already happened. A malformed custom expression
		// or an unrepresentable report cannot leave the funding hold dangling.
		// Retain only the already authorized budget and preserve the measured
		// duration separately from this explicitly audited pricing fallback.
		quota = b.session.GetReservedBudget()
		tier = nil
		var clamp *common.QuotaClamp
		if errors.As(priceErr, &clamp) {
			noteQuotaClamp(b.info, clamp)
		}
		logger.LogError(b.ctx, "error pricing native voice usage: "+priceErr.Error())
	}
	if priceErr == nil && seconds == 0 && !finalized {
		// Failed initialization never incurs an administrator per-session fee.
		quota = 0
	}
	b.finished = true
	settlementErr := SettleBilling(b.ctx, b.info, quota)
	if settlementErr != nil {
		logger.LogError(b.ctx, "error settling native voice billing: "+settlementErr.Error())
	}
	model.UpdateUserUsedQuotaAndRequestCount(b.info.UserId, quota)
	if b.info.ChannelMeta != nil {
		model.UpdateChannelUsedQuota(b.info.ChannelId, quota)
		if err := model.RecordPublicRelayUsage(b.info.ChannelId, quota); err != nil {
			common.SysError("failed to record native voice public relay usage: " + err.Error())
		}
	}
	other := b.logMetadata(seconds, estimated, finalized, reason, quota, tier, settlementErr)
	if priceErr != nil {
		other["billing_price_error"] = true
		other["billed_at_reserved_budget"] = true
		other["billing_estimated"] = true
		other["billing_estimate_basis"] = "authorized_session_budget"
		other["billing_finalization_pending"] = true
		admin, _ := other["admin_info"].(map[string]interface{})
		if admin == nil {
			admin = make(map[string]interface{})
			other["admin_info"] = admin
		}
		admin["billing_price_error"] = priceErr.Error()
		if !finiteNonnegative(seconds) {
			delete(other, "audio_seconds")
			other["audio_usage_status"] = "invalid"
			admin["invalid_audio_seconds"] = fmt.Sprintf("%g", seconds)
		}
	}
	attachQuotaSaturation(b.ctx, b.info, other)
	// An expression can fail with an infinite result. JSON cannot encode an
	// infinite audit number; retain its spelling so the entire fee log remains
	// usable instead of turning Other into an empty serialization result.
	if clamp := b.info.QuotaClamp; clamp != nil && (math.IsNaN(clamp.Original) || math.IsInf(clamp.Original, 0)) {
		if admin, ok := other["admin_info"].(map[string]interface{}); ok {
			if marker, ok := admin["quota_saturation"].(map[string]interface{}); ok {
				marker["original"] = fmt.Sprintf("%g", clamp.Original)
			}
		}
	}
	useTime := 0
	if !b.info.StartTime.IsZero() {
		useTime = int(time.Since(b.info.StartTime).Seconds())
	}
	channelID := 0
	if b.info.ChannelMeta != nil {
		channelID = b.info.ChannelId
	}
	model.RecordConsumeLog(b.ctx, b.info.UserId, model.RecordConsumeLogParams{
		ChannelId: channelID, ModelName: b.modelName, TokenName: b.ctx.GetString("token_name"),
		Quota: quota, TokenId: b.info.TokenId, UseTimeSeconds: useTime,
		Content:  fmt.Sprintf("音频时长 %.3f 秒，分组倍率 %g", seconds, b.groupRatio),
		IsStream: true, Group: b.info.UsingGroup, Other: other,
	})
	b.finalError = errors.Join(priceErr, settlementErr)
	return b.finalError
}

func (b *NativeVoiceBilling) logMetadata(seconds float64, estimated, finalized bool, reason string, quota int, tier *billingexpr.TieredResult, settlementErr error) map[string]interface{} {
	other := map[string]interface{}{
		"billing_mode": "audio_duration", "audio_seconds": seconds, "group_ratio": b.groupRatio,
		"price_unit": "expression", "pricing_currency": "USD", "audio_usage_status": "reported",
		"usage_estimated": estimated, "usage_finalized": finalized, "session_end_reason": reason,
		"initial_reserved_seconds": b.initialSeconds, "reserved_quota": b.session.GetReservedBudget(),
	}
	if !finalized {
		other["usage_finalization_pending"] = true
	}
	if estimated {
		other["audio_usage_status"] = "estimated"
		other["usage_estimate_basis"] = "forwarded_pcm_bytes"
		upstreamModel := b.modelName
		if b.info.ChannelMeta != nil && b.info.UpstreamModelName != "" {
			upstreamModel = b.info.UpstreamModelName
		}
		if upstreamModel == "gpt-live-1" {
			other["usage_estimate_basis"] = "active_session_clock"
		}
	}
	if b.fixed {
		other["billing_mode"], other["price_unit"], other["model_price"] = "fixed", "session", b.fixedPrice
	}
	if b.snapshot != nil {
		other["billing_mode"] = "tiered_expr"
		other["expr_b64"] = base64.StdEncoding.EncodeToString([]byte(b.snapshot.ExprString))
		other["expr_hash"] = b.snapshot.ExprHash
		other["price_unit"] = "expression"
		if tier != nil {
			other["matched_tier"] = tier.MatchedTier
			if len(tier.RequestRules) > 0 {
				other["request_rules"] = tier.RequestRules
			}
		}
	}
	if over := quota - b.session.GetReservedBudget(); over > 0 {
		other["over_budget_quantity"] = over
	}
	if b.info.IsModelMapped {
		other["is_model_mapped"] = true
		other["upstream_model_name"] = b.info.UpstreamModelName
	}
	appendRequestPath(b.ctx, b.info, other)
	appendBillingInfo(b.info, other)
	AppendResponseModelLogInfo(b.info, other)
	appendSubscriptionSettlementLog(other, b.info, settlementErr)
	if settlementErr != nil {
		admin, _ := other["admin_info"].(map[string]interface{})
		if admin == nil {
			admin = make(map[string]interface{})
			other["admin_info"] = admin
		}
		admin["billing_settlement_error"] = settlementErr.Error()
	}
	return other
}
