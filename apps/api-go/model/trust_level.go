package model

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/pkg/cachex"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const (
	TrustLevelMinUser = 0
	TrustLevelMaxUser = 4
	TrustLevelAdmin   = 5
	TrustLevelRoot    = 6

	trustLevelDecayPeriod = 90 * 24 * time.Hour
	trustAggregateTTL     = time.Minute
	// Trust aggregates are a read-through optimization. Keep their process
	// footprint bounded even when an installation sees a large number of users;
	// eviction only causes a fresh aggregate query on the next access.
	paidTopUpAggregateCacheMaxEntries = 16_384
	paidTopUpAggregateCacheMaxBytes   = 2 << 20
	LegacyPaidPolicyCurrency          = "legacy_pricing_unit"
)

var trustLevelThresholds = [...]float64{0, 0, 100, 500, 2000}
var trustLevelDiscountRatios = [...]float64{1, 1, 0.97, 0.94, 0.90}
var localAcceptanceDeveloperAccess atomic.Bool

// SetLocalAcceptanceDeveloperAccess stores the startup-validated, immutable
// local acceptance capability. Production startup leaves it disabled.
func SetLocalAcceptanceDeveloperAccess(enabled bool) {
	localAcceptanceDeveloperAccess.Store(enabled)
}

func LocalAcceptanceDeveloperAccessEnabled() bool {
	return localAcceptanceDeveloperAccess.Load()
}

type TrustLevelInfo struct {
	Level                         int     `json:"level"`
	LevelSource                   string  `json:"level_source"`
	PaidCredits                   *string `json:"paid_credits"`
	PaidCreditProjectionAvailable bool    `json:"paid_credit_projection_available"`
	NextLevelPaidCredits          *string `json:"next_level_paid_credits"`
	CreditsToNextLevel            *string `json:"credits_to_next_level"`
	AutomaticLevel                int     `json:"automatic_level"`
	OverrideLevel                 *int    `json:"override_level"`
	// PaidAmount retains the historical policy units Q, not USD or gateway cash.
	PaidAmount             float64  `json:"paid_amount"`
	PaidAmountCurrency     string   `json:"paid_amount_currency"`
	PaidAmountUSD          *float64 `json:"paid_amount_usd"`
	DiscountRatio          float64  `json:"discount_ratio"`
	DiscountPercent        float64  `json:"discount_percent"`
	NextLevel              *int     `json:"next_level"`
	NextLevelPaidAmount    *float64 `json:"next_level_paid_amount"`
	NextLevelPaidAmountUSD *float64 `json:"next_level_paid_amount_usd"`
	AmountToNextLevel      *float64 `json:"amount_to_next_level"`
	AmountToNextLevelUSD   *float64 `json:"amount_to_next_level_usd"`
	NextDecayAt            *int64   `json:"next_decay_at"`
	InactivityDecaySteps   int      `json:"inactivity_decay_steps"`
	DecayPeriodDays        int      `json:"decay_period_days"`
	Overridden             bool     `json:"overridden"`
}

type TrustLevelTier struct {
	Level                   int      `json:"level"`
	MinPaidCredits          string   `json:"min_paid_credits"`
	MinPaidAmount           float64  `json:"min_paid_amount"`
	MinPaidAmountCurrency   string   `json:"min_paid_amount_currency"`
	MinPaidAmountUSD        *float64 `json:"min_paid_amount_usd"`
	RequiresSuccessfulTopUp bool     `json:"requires_successful_top_up"`
	DiscountPercent         float64  `json:"discount_percent"`
	Benefits                []string `json:"benefits"`
	BenefitCount            int      `json:"benefit_count"`
	BenefitsHidden          bool     `json:"benefits_hidden"`
	DiscountHidden          bool     `json:"discount_hidden"`
}

var trustLevelBenefits = [...][]string{
	{"standard_access"},
	{"developer_access"},
	{"usage_discount"},
	{"usage_discount"},
	{"usage_discount"},
}

func trustLevelTier(level int) TrustLevelTier {
	return trustLevelTierWithConfiguration(level, GetTrustLevelConfiguration())
}

func trustLevelTierWithConfiguration(level int, config TrustLevelConfiguration) TrustLevelTier {
	if level < TrustLevelMinUser || level > TrustLevelMaxUser {
		return TrustLevelTier{}
	}
	tier := config.Tiers[level]
	benefits := append([]string(nil), tier.Benefits...)
	return TrustLevelTier{
		Level:                 level,
		MinPaidAmount:         float64(tier.MinPaidCredits) / 500000,
		MinPaidCredits:        strconv.FormatInt(tier.MinPaidCredits, 10),
		MinPaidAmountCurrency: LegacyPaidPolicyCurrency,
		MinPaidAmountUSD:      LegacyPolicyAmountUSD(float64(tier.MinPaidCredits) / 500000),
		// L1 can be reached through a successful top-up or an approved
		// administrator unlock request, so payment is not a prerequisite.
		RequiresSuccessfulTopUp: level >= 2,
		DiscountPercent:         (1 - tier.DiscountRatio) * 100,
		Benefits:                benefits,
		BenefitCount:            len(benefits),
	}
}

func GetTrustLevelTiers() []TrustLevelTier {
	config := GetTrustLevelConfiguration()
	tiers := make([]TrustLevelTier, 0, TrustLevelMaxUser-TrustLevelMinUser+1)
	for level := TrustLevelMinUser; level <= TrustLevelMaxUser; level++ {
		tiers = append(tiers, trustLevelTierWithConfiguration(level, config))
	}
	return tiers
}

// GetTrustLevelTierViews returns a privacy-preserving view for the current
// viewer. A tier at or below the viewer's effective level exposes its benefit
// codes; higher tiers expose only the number of benefits. This keeps the
// progression discoverable without leaking unreleased higher-level details.
func GetTrustLevelTierViews(viewerLevel int) []TrustLevelTier {
	if viewerLevel < TrustLevelMinUser {
		viewerLevel = TrustLevelMinUser
	}
	tiers := GetTrustLevelTiers()
	for index := range tiers {
		if tiers[index].Level <= viewerLevel {
			continue
		}
		tiers[index].Benefits = nil
		tiers[index].BenefitsHidden = true
		tiers[index].DiscountPercent = 0
		tiers[index].DiscountHidden = true
	}
	return tiers
}

type paidTopUpAggregate struct {
	// PaidAmountMicros retains historical legacy-policy micros (credits / Q),
	// not USD micros or the amount charged by the payment provider.
	PaidAmountMicros      int64
	PaidAmount            float64
	CreditedQuota         float64
	PaidCredits           int64
	ProjectionUnavailable bool
	LastPaidCompleteAt    int64
	// PaidRows counts the qualifying real-money recharges. It is kept next to
	// the amount so a cached aggregate can be re-judged against the current
	// threshold instead of freezing the verdict that was in force when the
	// aggregate was built.
	PaidRows int64
}

// paidActivationComplete applies the current boundary policy to a cached
// aggregate. Deciding here rather than at query time means an administrator
// raising or lowering the threshold takes effect immediately.
func (aggregate paidTopUpAggregate) paidActivationComplete(policy DeveloperAccessPolicy) bool {
	return policy.paidActivationEnabled && aggregate.PaidRows > 0 && aggregate.PaidCredits >= policy.trustConfiguration.Tiers[1].MinPaidCredits
}

// UserAccessSnapshot is the canonical payment-derived state for one user
// response. Callers can reuse it for trust, access, and onboarding without
// issuing duplicate recharge-history queries.
type UserAccessSnapshot struct {
	TrustLevel             TrustLevelInfo
	DeveloperAccess        DeveloperAccessState
	PaidAmountMicros       int64
	LastPaidCompleteAt     int64
	PaidActivationComplete bool
}

type cachedPaidTopUpAggregate struct {
	value paidTopUpAggregate
}

var paidTopUpAggregateCache = cachex.NewByteCache[cachedPaidTopUpAggregate](
	paidTopUpAggregateCacheMaxEntries,
	paidTopUpAggregateCacheMaxBytes,
	func(key string, _ cachedPaidTopUpAggregate) int64 {
		return int64(len(key) + 64)
	},
)

func paidTopUpAggregateCacheKey(userID int) string {
	return strconv.Itoa(userID)
}

func automaticTrustLevelCredits(paidCredits int64, activationComplete bool, config TrustLevelConfiguration) int {
	if !activationComplete {
		return TrustLevelMinUser
	}
	for level := TrustLevelMaxUser; level >= TrustLevelMinUser+2; level-- {
		if paidCredits >= config.Tiers[level].MinPaidCredits {
			return level
		}
	}
	return TrustLevelMinUser + 1
}

func EvaluateTrustLevel(role int, overrideLevel *int, paidAmount float64, activityAnchor int64, now int64) TrustLevelInfo {
	return EvaluateTrustLevelWithActivation(role, overrideLevel, paidAmount, paidAmount > 0, activityAnchor, now)
}

// EvaluateTrustLevelWithActivation keeps the independent activation predicate
// separate from cumulative credited platform amount. A successful payment or
// an approved non-payment activation may establish the L1 boundary; only the
// paid amount contributes to later paid progression.
func EvaluateTrustLevelWithActivation(role int, overrideLevel *int, paidAmount float64, activationComplete bool, activityAnchor int64, now int64) TrustLevelInfo {
	paidCredits := int64(0)
	if paidAmount > 0 && !math.IsNaN(paidAmount) && !math.IsInf(paidAmount, 0) {
		credits := decimal.NewFromFloat(paidAmount).Mul(decimal.NewFromInt(500000)).Floor()
		if credits.GreaterThan(decimal.NewFromInt(common.MaxWalletQuota)) {
			paidCredits = common.MaxWalletQuota
		} else {
			paidCredits = credits.IntPart()
		}
	}
	return evaluateTrustLevelCredits(role, overrideLevel, paidCredits, paidAmount, activationComplete, activityAnchor, now, GetTrustLevelConfiguration())
}

func evaluateTrustLevelCredits(role int, overrideLevel *int, paidCredits int64, paidAmount float64, activationComplete bool, activityAnchor int64, now int64, config TrustLevelConfiguration) TrustLevelInfo {
	if now <= 0 {
		now = time.Now().Unix()
	}
	if role == common.RoleRootUser {
		return administratorTrustLevelInfo(TrustLevelRoot, config)
	}
	if role >= common.RoleAdminUser {
		return administratorTrustLevelInfo(TrustLevelAdmin, config)
	}

	automaticLevel := automaticTrustLevelCredits(paidCredits, activationComplete, config)
	effectiveLevel := automaticLevel
	decaySteps := 0
	var nextDecayAt *int64
	if automaticLevel > 0 && activityAnchor > 0 && now > activityAnchor && config.DecayPeriodDays > 0 {
		periodSeconds := int64(config.DecayPeriodDays) * 86400
		decaySteps = int((now - activityAnchor) / periodSeconds)
		maxDecaySteps := automaticLevel - (TrustLevelMinUser + 1)
		if decaySteps > maxDecaySteps {
			decaySteps = maxDecaySteps
		}
		effectiveLevel = automaticLevel - decaySteps
		if effectiveLevel > TrustLevelMinUser+1 {
			value := activityAnchor + int64(decaySteps+1)*periodSeconds
			nextDecayAt = &value
		}
	}

	overridden := overrideLevel != nil
	if overridden {
		if *overrideLevel >= TrustLevelMinUser && *overrideLevel <= TrustLevelMaxUser {
			effectiveLevel = *overrideLevel
		} else {
			// A corrupted ordinary-user override must never fall back to an
			// automatically granted paid level.
			effectiveLevel = TrustLevelMinUser
		}
		nextDecayAt = nil
	}

	paidCreditsString := strconv.FormatInt(paidCredits, 10)
	info := TrustLevelInfo{
		Level:                         effectiveLevel,
		LevelSource:                   "automatic",
		PaidCredits:                   &paidCreditsString,
		PaidCreditProjectionAvailable: true,
		AutomaticLevel:                automaticLevel,
		OverrideLevel:                 overrideLevel,
		PaidAmount:                    paidAmount,
		PaidAmountCurrency:            LegacyPaidPolicyCurrency,
		PaidAmountUSD:                 LegacyPolicyAmountUSD(paidAmount),
		DiscountRatio:                 config.Tiers[effectiveLevel].DiscountRatio,
		DiscountPercent:               (1 - config.Tiers[effectiveLevel].DiscountRatio) * 100,
		NextDecayAt:                   nextDecayAt,
		InactivityDecaySteps:          decaySteps,
		DecayPeriodDays:               config.DecayPeriodDays,
		Overridden:                    overridden,
	}
	if overridden {
		info.PaidCredits = nil
		info.PaidCreditProjectionAvailable = false
		info.LevelSource = "override"
	}
	if automaticLevel < TrustLevelMaxUser && !overridden {
		next := automaticLevel + 1
		thresholdCredits := config.Tiers[next].MinPaidCredits
		threshold := float64(thresholdCredits) / 500000
		remaining := threshold - paidAmount
		if remaining < 0 {
			remaining = 0
		}
		nextCredits := strconv.FormatInt(thresholdCredits, 10)
		remainingCredits := thresholdCredits - paidCredits
		if remainingCredits < 0 {
			remainingCredits = 0
		}
		remainingString := strconv.FormatInt(remainingCredits, 10)
		info.NextLevelPaidCredits = &nextCredits
		info.CreditsToNextLevel = &remainingString
		info.NextLevel = &next
		info.NextLevelPaidAmount = &threshold
		info.NextLevelPaidAmountUSD = LegacyPolicyAmountUSD(threshold)
		info.AmountToNextLevel = &remaining
		info.AmountToNextLevelUSD = LegacyPolicyAmountUSD(remaining)
	}
	return info
}

func administratorTrustLevelInfo(level int, config TrustLevelConfiguration) TrustLevelInfo {
	ratio := 1.0
	for _, tier := range config.RoleTiers {
		if tier.Level == level {
			ratio = tier.DiscountRatio
		}
	}
	return TrustLevelInfo{
		Level:              level,
		LevelSource:        "role",
		PaidCredits:        nil,
		AutomaticLevel:     TrustLevelMinUser,
		PaidAmountCurrency: LegacyPaidPolicyCurrency,
		PaidAmountUSD:      LegacyPolicyAmountUSD(0),
		DiscountRatio:      ratio,
		DiscountPercent:    (1 - ratio) * 100,
		DecayPeriodDays:    config.DecayPeriodDays,
	}
}

func getPaidTopUpAggregate(userID int) (paidTopUpAggregate, error) {
	if userID <= 0 {
		return paidTopUpAggregate{}, nil
	}
	aggregates, err := getPaidTopUpAggregates([]int{userID})
	if err != nil {
		return paidTopUpAggregate{}, err
	}
	if aggregates[userID].ProjectionUnavailable {
		return paidTopUpAggregate{}, ErrPaidCreditProjectionUnavailable
	}
	return aggregates[userID], nil
}

func getPaidTopUpAggregates(userIDs []int) (map[int]paidTopUpAggregate, error) {
	return getPaidTopUpAggregatesContext(context.Background(), userIDs)
}

func getPaidTopUpAggregatesContext(ctx context.Context, userIDs []int) (map[int]paidTopUpAggregate, error) {
	if len(userIDs) == 0 {
		return map[int]paidTopUpAggregate{}, nil
	}
	if _, err := common.LegacyPricingQuotaPerUnit(); err != nil {
		return nil, err
	}
	result := make(map[int]paidTopUpAggregate, len(userIDs))
	missing := make([]int, 0, len(userIDs))
	seen := make(map[int]struct{}, len(userIDs))
	for _, userID := range userIDs {
		if userID <= 0 {
			continue
		}
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		if cached, ok := paidTopUpAggregateCache.Load(paidTopUpAggregateCacheKey(userID)); ok {
			result[userID] = cached.value
			continue
		}
		missing = append(missing, userID)
	}

	if len(missing) == 0 {
		return result, nil
	}
	if DB == nil {
		return nil, gorm.ErrInvalidDB
	}

	fresh, err := getFreshPaidTopUpAggregatesContext(ctx, missing)
	if err != nil {
		return nil, err
	}
	for _, userID := range missing {
		aggregate := fresh[userID]
		result[userID] = aggregate
		paidTopUpAggregateCache.SetWithTTL(
			paidTopUpAggregateCacheKey(userID),
			cachedPaidTopUpAggregate{value: aggregate},
			trustAggregateTTL,
		)
	}
	return result, nil
}

func getFreshPaidTopUpAggregate(userID int) (paidTopUpAggregate, error) {
	if userID <= 0 {
		return paidTopUpAggregate{}, nil
	}
	aggregates, err := getFreshPaidTopUpAggregates([]int{userID})
	if err != nil {
		return paidTopUpAggregate{}, err
	}
	if aggregates[userID].ProjectionUnavailable {
		return paidTopUpAggregate{}, ErrPaidCreditProjectionUnavailable
	}
	return aggregates[userID], nil
}

func getFreshPaidTopUpAggregates(userIDs []int) (map[int]paidTopUpAggregate, error) {
	return getFreshPaidTopUpAggregatesContext(context.Background(), userIDs)
}

func getFreshPaidTopUpAggregatesContext(ctx context.Context, userIDs []int) (map[int]paidTopUpAggregate, error) {
	result := make(map[int]paidTopUpAggregate, len(userIDs))
	uniqueUserIDs := make([]int, 0, len(userIDs))
	seen := make(map[int]struct{}, len(userIDs))
	for _, userID := range userIDs {
		if userID <= 0 {
			continue
		}
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		uniqueUserIDs = append(uniqueUserIDs, userID)
	}
	if len(uniqueUserIDs) == 0 {
		return result, nil
	}
	if DB == nil {
		return nil, gorm.ErrInvalidDB
	}

	type paidTopUpSummary struct {
		UserId                 int
		CreditedQuota          decimal.Decimal
		LastPaidCompleteAt     int64
		ActivationCompleteRows int64
		ProjectionUnknown      int64
	}
	var summaries []paidTopUpSummary
	activityExpression := "CASE WHEN complete_time > 0 THEN complete_time ELSE create_time END"
	creditedQuotaExpression, creditedQuotaArgs, _, err := legacyPaidPolicyCreditedQuotaSQL()
	if err != nil {
		return nil, err
	}
	// Fixed integer multipliers keep provider fallback and SUM in credit units.
	creditedQuotaArgs[len(creditedQuotaArgs)-1] = int64(500000)
	netExpression, netArgs, err := trustPaidCreditSQL(DB.WithContext(ctx))
	if err != nil {
		return nil, err
	}
	selectClause := "user_id, COALESCE(SUM(" + netExpression + "),0) AS credited_quota, " +
		"COALESCE(MAX(CASE WHEN (" + netExpression + ") > 0 THEN " + activityExpression + " ELSE 0 END),0) AS last_paid_complete_at, " +
		"SUM(CASE WHEN (" + netExpression + ") > 0 THEN 1 ELSE 0 END) AS activation_complete_rows, " +
		"SUM(CASE WHEN (" + netExpression + ") IS NULL THEN 1 ELSE 0 END) AS projection_unknown"
	var selectArgs []interface{}
	for i := 0; i < 4; i++ {
		selectArgs = append(selectArgs, netArgs...)
	}
	query := DB.WithContext(ctx).Model(&TopUp{}).
		Select(selectClause, selectArgs...).
		Where("user_id IN ?", uniqueUserIDs).
		Where("("+creditedQuotaExpression+") > 0", creditedQuotaArgs...).
		Group("user_id")
	if err := successfulExternalPaidTopUpQuery(query).Scan(&summaries).Error; err != nil {
		return nil, err
	}
	for _, summary := range summaries {
		if summary.ProjectionUnknown > 0 {
			result[summary.UserId] = paidTopUpAggregate{ProjectionUnavailable: true}
			continue
		}
		if summary.CreditedQuota.IsNegative() || !summary.CreditedQuota.Equal(summary.CreditedQuota.Truncate(0)) || summary.CreditedQuota.GreaterThan(decimal.NewFromInt(math.MaxInt64)) {
			return nil, fmt.Errorf("invalid cumulative paid credit amount for user %d", summary.UserId)
		}
		paidCredits := summary.CreditedQuota.IntPart()
		paidAmountMicros := int64(math.MaxInt64)
		if paidCredits <= math.MaxInt64/2 {
			paidAmountMicros = paidCredits * 2
		}
		result[summary.UserId] = paidTopUpAggregate{
			PaidAmountMicros:   paidAmountMicros,
			PaidAmount:         float64(paidCredits) / 500000,
			CreditedQuota:      float64(paidCredits),
			PaidCredits:        paidCredits,
			LastPaidCompleteAt: summary.LastPaidCompleteAt,
			PaidRows:           summary.ActivationCompleteRows,
		}
	}
	return result, nil
}

func creditedQuotaToLegacyPolicyMicros(creditedQuota float64, legacyQuota decimal.Decimal) int64 {
	if creditedQuota <= 0 || !legacyQuota.IsPositive() {
		return 0
	}
	return decimal.NewFromFloat(creditedQuota).
		Div(legacyQuota).
		Mul(decimal.NewFromInt(1_000_000)).
		Round(0).
		IntPart()
}

// LegacyPolicyAmountUSD adds a truthful display projection without rewriting
// historical policy thresholds or changing trust/activation decisions. A
// missing basis remains null, including for zero amounts.
func LegacyPolicyAmountUSD(amount float64) *float64 {
	if math.IsNaN(amount) || math.IsInf(amount, 0) {
		return nil
	}
	anchor, err := common.CreditsPerUSD()
	if err != nil {
		return nil
	}
	legacy, err := common.LegacyPricingQuotaPerUnit()
	if err != nil {
		return nil
	}
	value := decimal.NewFromFloat(amount).Mul(legacy).Div(anchor).InexactFloat64()
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	return &value
}

// The USD payment display uses the unrounded credited sum. Policy micros may
// round a single credit to zero; that rounding must not erase real USD value.
func (aggregate paidTopUpAggregate) withUSDDisplay(info TrustLevelInfo) TrustLevelInfo {
	anchor, err := common.CreditsPerUSD()
	if err != nil || math.IsNaN(aggregate.CreditedQuota) || math.IsInf(aggregate.CreditedQuota, 0) {
		info.PaidAmountUSD, info.AmountToNextLevelUSD = nil, nil
		return info
	}
	value := decimal.NewFromInt(aggregate.PaidCredits).Div(anchor).InexactFloat64()
	if math.IsNaN(value) || math.IsInf(value, 0) {
		info.PaidAmountUSD, info.AmountToNextLevelUSD = nil, nil
		return info
	}
	info.PaidAmountUSD = &value
	if info.NextLevelPaidAmountUSD != nil {
		remaining := math.Max(0, *info.NextLevelPaidAmountUSD-value)
		if math.IsNaN(remaining) || math.IsInf(remaining, 0) {
			info.AmountToNextLevelUSD = nil
		} else {
			info.AmountToNextLevelUSD = &remaining
		}
	}
	return info
}

func invalidatePaidTopUpAggregate(userID int) {
	paidTopUpAggregateCache.Delete(paidTopUpAggregateCacheKey(userID))
}

// InvalidatePaidTopUpAggregate clears the bounded discount aggregate cache
// after a durable top-up state transition on this process. Other instances use
// fresh payment checks for activation; their discount cache may lag by at most
// trustAggregateTTL.
func InvalidatePaidTopUpAggregate(userID int) {
	invalidatePaidTopUpAggregate(userID)
}

func (topUp *TopUp) AfterSave(_ *gorm.DB) error {
	if topUp != nil && topUp.UserId > 0 {
		invalidatePaidTopUpAggregate(topUp.UserId)
	}
	return nil
}

func trustActivityAnchor(createdAt int64, lastAPIActivityAt int64, lastPaidCompleteAt int64) int64 {
	anchor := createdAt
	if lastAPIActivityAt > anchor {
		anchor = lastAPIActivityAt
	}
	if lastPaidCompleteAt > anchor {
		anchor = lastPaidCompleteAt
	}
	return anchor
}

func GetTrustLevelInfoForUser(user *User) (TrustLevelInfo, error) {
	if user == nil {
		return TrustLevelInfo{}, gorm.ErrInvalidData
	}
	if user.Role >= common.RoleAdminUser {
		return EvaluateTrustLevel(user.Role, nil, 0, 0, time.Now().Unix()), nil
	}
	if user.TrustLevelOverride != nil {
		return EvaluateTrustLevel(user.Role, user.TrustLevelOverride, 0, user.CreatedAt, time.Now().Unix()), nil
	}
	aggregate, err := getPaidTopUpAggregate(user.Id)
	if err != nil {
		return TrustLevelInfo{}, err
	}
	anchor := trustActivityAnchor(user.CreatedAt, user.LastAPIActivityAt, aggregate.LastPaidCompleteAt)
	policy := CurrentDeveloperAccessPolicy()
	paidActivationComplete := aggregate.paidActivationComplete(policy)
	return aggregate.withUSDDisplay(evaluateTrustLevelCredits(user.Role, user.TrustLevelOverride, aggregate.PaidCredits, aggregate.PaidAmount, paidActivationComplete || user.ConsoleActivatedAt > 0, anchor, time.Now().Unix(), policy.trustConfiguration)), nil
}

// GetFreshTrustLevelInfoForUser bypasses the bounded discount cache for
// account self-service responses immediately after a payment completes.
func GetFreshTrustLevelInfoForUser(user *User) (TrustLevelInfo, error) {
	snapshot, err := GetFreshUserAccessSnapshot(user)
	if err != nil {
		return TrustLevelInfo{}, err
	}
	return snapshot.TrustLevel, nil
}

func explicitDeveloperAccessDecision(role int, overrideLevel *int) (DeveloperAccessState, bool) {
	if role >= common.RoleAdminUser {
		return DeveloperAccessState{Granted: true}, true
	}
	if overrideLevel == nil {
		return DeveloperAccessState{}, false
	}
	return DeveloperAccessState{Granted: *overrideLevel >= TrustLevelMinUser+1 && *overrideLevel <= TrustLevelMaxUser}, true
}

// DeveloperAccessPolicy captures the immutable server-side input used by the
// developer-access decision. Its fields are deliberately private so callers
// cannot manufacture a client-controlled policy.
type DeveloperAccessPolicy struct {
	localAcceptance         bool
	paidActivationEnabled   bool
	paidActivationMinMicros int64
	trustConfiguration      TrustLevelConfiguration
}

func CurrentDeveloperAccessPolicy() DeveloperAccessPolicy {
	config := GetTrustLevelConfiguration()
	return DeveloperAccessPolicy{
		localAcceptance:         LocalAcceptanceDeveloperAccessEnabled(),
		paidActivationEnabled:   config.PaidActivationEnabled,
		paidActivationMinMicros: config.Tiers[1].MinPaidCredits * 2,
		trustConfiguration:      config,
	}
}

// paidActivationComplete decides whether recharge history on its own clears
// the L1 boundary. A threshold of zero keeps the historical rule that any
// successful real-money recharge qualifies, and disabling paid activation
// sends every account to manual review no matter how much it has spent.
func (policy DeveloperAccessPolicy) paidActivationComplete(paidRows int64, paidAmountMicros int64) bool {
	if !policy.paidActivationEnabled || paidRows <= 0 {
		return false
	}
	if policy.paidActivationMinMicros <= 0 {
		return true
	}
	return paidAmountMicros >= policy.paidActivationMinMicros
}

func ordinaryDeveloperAccessStateWithPolicy(paidActivationComplete, consoleActivated bool, policy DeveloperAccessPolicy) DeveloperAccessState {
	return DeveloperAccessState{
		Granted:                paidActivationComplete || consoleActivated || policy.localAcceptance,
		PaidActivationComplete: paidActivationComplete,
	}
}

func ordinaryDeveloperAccessState(paidActivationComplete bool, consoleActivated bool) DeveloperAccessState {
	return ordinaryDeveloperAccessStateWithPolicy(paidActivationComplete, consoleActivated, CurrentDeveloperAccessPolicy())
}

// GetFreshUserAccessSnapshot performs at most one bounded aggregate query for
// an ordinary user and none for administrator or explicit-override access.
func GetFreshUserAccessSnapshot(user *User) (UserAccessSnapshot, error) {
	if user == nil {
		return UserAccessSnapshot{}, gorm.ErrInvalidData
	}
	if access, explicit := explicitDeveloperAccessDecision(user.Role, user.TrustLevelOverride); explicit {
		return UserAccessSnapshot{
			TrustLevel:      EvaluateTrustLevel(user.Role, user.TrustLevelOverride, 0, user.CreatedAt, time.Now().Unix()),
			DeveloperAccess: access,
		}, nil
	}
	aggregate, err := getFreshPaidTopUpAggregate(user.Id)
	if err != nil {
		return UserAccessSnapshot{}, err
	}
	// One policy snapshot for the whole response keeps the trust level, the
	// access decision, and the onboarding stage from disagreeing if an
	// administrator edits the threshold mid-request.
	policy := CurrentDeveloperAccessPolicy()
	paidActivationComplete := aggregate.paidActivationComplete(policy)
	activationComplete := paidActivationComplete || user.ConsoleActivatedAt > 0
	anchor := trustActivityAnchor(user.CreatedAt, user.LastAPIActivityAt, aggregate.LastPaidCompleteAt)
	return UserAccessSnapshot{
		TrustLevel: aggregate.withUSDDisplay(evaluateTrustLevelCredits(
			user.Role, nil, aggregate.PaidCredits, aggregate.PaidAmount, activationComplete, anchor, time.Now().Unix(), policy.trustConfiguration,
		)),
		DeveloperAccess:        ordinaryDeveloperAccessStateWithPolicy(paidActivationComplete, user.ConsoleActivatedAt > 0, policy),
		PaidAmountMicros:       aggregate.PaidAmountMicros,
		LastPaidCompleteAt:     aggregate.LastPaidCompleteAt,
		PaidActivationComplete: paidActivationComplete,
	}, nil
}

func GetTrustLevelInfoForUserBase(user *UserBase) (TrustLevelInfo, error) {
	if user == nil {
		return TrustLevelInfo{}, gorm.ErrInvalidData
	}
	if user.Role >= common.RoleAdminUser {
		return EvaluateTrustLevel(user.Role, nil, 0, 0, time.Now().Unix()), nil
	}
	if user.TrustLevelOverride != nil {
		return EvaluateTrustLevel(user.Role, user.TrustLevelOverride, 0, user.CreatedAt, time.Now().Unix()), nil
	}
	aggregate, err := getPaidTopUpAggregate(user.Id)
	if err != nil {
		return TrustLevelInfo{}, err
	}
	anchor := trustActivityAnchor(user.CreatedAt, user.LastAPIActivityAt, aggregate.LastPaidCompleteAt)
	policy := CurrentDeveloperAccessPolicy()
	paidActivationComplete := aggregate.paidActivationComplete(policy)
	return aggregate.withUSDDisplay(evaluateTrustLevelCredits(user.Role, user.TrustLevelOverride, aggregate.PaidCredits, aggregate.PaidAmount, paidActivationComplete || user.ConsoleActivatedAt > 0, anchor, time.Now().Unix(), policy.trustConfiguration)), nil
}

func GetTrustLevelInfoByUserID(userID int) (TrustLevelInfo, error) {
	user, err := GetUserCache(userID)
	if err != nil {
		return TrustLevelInfo{}, err
	}
	return GetTrustLevelInfoForUserBase(user)
}

// DeveloperAccessState separates the durable activation facts from the
// effective access decision, which may be granted or denied by role/override.
type DeveloperAccessState struct {
	Granted                bool `json:"granted"`
	PaidActivationComplete bool `json:"paid_activation_complete"`
}

func developerAccessStateForUserBase(tx *gorm.DB, user *UserBase, policy DeveloperAccessPolicy) (DeveloperAccessState, error) {
	if user == nil {
		return DeveloperAccessState{}, gorm.ErrInvalidData
	}
	if state, explicit := explicitDeveloperAccessDecision(user.Role, user.TrustLevelOverride); explicit {
		// Access short-circuits without payment history. The paid-activation fact
		// remains intentionally unknown/false on this bounded path.
		return state, nil
	}
	if user.ConsoleActivatedAt > 0 {
		return ordinaryDeveloperAccessStateWithPolicy(false, true, policy), nil
	}
	if tx == nil {
		return DeveloperAccessState{}, gorm.ErrInvalidDB
	}
	facts, err := paidTopUpFactsWithTx(tx, user.Id, true)
	if err != nil {
		return DeveloperAccessState{}, err
	}
	paid := policy.paidActivationComplete(facts.Rows, facts.AmountMicros)
	return ordinaryDeveloperAccessStateWithPolicy(paid, false, policy), nil
}

func GetDeveloperAccessStateForUserBase(user *UserBase) (DeveloperAccessState, error) {
	state, err := developerAccessStateForUserBase(DB, user, CurrentDeveloperAccessPolicy())
	if err != nil {
		return DeveloperAccessState{}, fmt.Errorf("evaluate developer access: %w", err)
	}
	return state, nil
}

// GetDeveloperAccessStateForUserBaseWithTx evaluates the same authoritative
// policy as GetDeveloperAccessStateForUserBase while using the caller's
// transaction. Qualifying payment facts are locked through commit.
func GetDeveloperAccessStateForUserBaseWithTx(tx *gorm.DB, user *UserBase, policy DeveloperAccessPolicy) (DeveloperAccessState, error) {
	return developerAccessStateForUserBase(tx, user, policy)
}

func GetDeveloperAccessStateForUser(user *User) (DeveloperAccessState, error) {
	if user == nil {
		return DeveloperAccessState{}, gorm.ErrInvalidData
	}
	return GetDeveloperAccessStateForUserBase(user.ToBaseUser())
}

// OnboardingState is derived from durable account records so it cannot drift
// from the payment, credential, and request state it represents.
type OnboardingState struct {
	ActivationComplete     bool   `json:"activation_complete"`
	PaidActivationComplete bool   `json:"paid_activation_complete"`
	CredentialComplete     bool   `json:"credential_complete"`
	APIKeyCreated          bool   `json:"api_key_created"`
	FirstRequestComplete   bool   `json:"first_request_complete"`
	Stage                  string `json:"stage"`
}

func GetOnboardingStateForUser(user *User) (OnboardingState, error) {
	if user == nil {
		return OnboardingState{}, gorm.ErrInvalidData
	}
	snapshot, err := GetFreshUserAccessSnapshot(user)
	if err != nil {
		return OnboardingState{}, err
	}
	return GetOnboardingStateForUserSnapshot(user, snapshot)
}

func GetOnboardingStateForUserSnapshot(user *User, snapshot UserAccessSnapshot) (OnboardingState, error) {
	if user == nil {
		return OnboardingState{}, gorm.ErrInvalidData
	}
	access := snapshot.DeveloperAccess

	state := OnboardingState{
		ActivationComplete:     access.Granted,
		PaidActivationComplete: access.PaidActivationComplete,
	}
	if DB == nil {
		state.Stage = onboardingStage(state)
		return state, gorm.ErrInvalidDB
	}
	var credentials struct {
		Total  int64
		Manual int64
	}
	if err := DB.Model(&Token{}).
		Where("user_id = ? AND status = ?", user.Id, common.TokenStatusEnabled).
		Select("COUNT(*) AS total, COALESCE(SUM(CASE WHEN oauth_managed = ? THEN 1 ELSE 0 END), 0) AS manual", false).
		Scan(&credentials).Error; err != nil {
		state.Stage = onboardingStage(state)
		return state, err
	}
	state.CredentialComplete = credentials.Total > 0
	state.APIKeyCreated = credentials.Manual > 0
	state.FirstRequestComplete = state.CredentialComplete && user.LastAPIActivityAt > 0
	if user.Role >= common.RoleAdminUser {
		state.CredentialComplete = true
		state.FirstRequestComplete = true
	}
	state.Stage = onboardingStage(state)
	return state, nil
}

func onboardingStage(state OnboardingState) string {
	switch {
	case !state.ActivationComplete:
		return "activate"
	case !state.CredentialComplete:
		return "credential"
	case !state.FirstRequestComplete:
		return "first_request"
	default:
		return "complete"
	}
}

func EnrichUsersTrustLevels(users []*User) error {
	return EnrichUsersTrustLevelsContext(context.Background(), users)
}

func EnrichUsersTrustLevelsContext(ctx context.Context, users []*User) error {
	userIDs := make([]int, 0, len(users))
	for _, user := range users {
		if user != nil && user.Role < common.RoleAdminUser && user.TrustLevelOverride == nil {
			userIDs = append(userIDs, user.Id)
		}
	}
	aggregates, err := getPaidTopUpAggregatesContext(ctx, userIDs)
	if errors.Is(err, ErrPaidCreditProjectionUnavailable) {
		aggregates = make(map[int]paidTopUpAggregate, len(userIDs))
		for _, id := range userIDs {
			aggregates[id] = paidTopUpAggregate{ProjectionUnavailable: true}
		}
	} else if err != nil {
		return err
	}
	now := time.Now().Unix()
	policy := CurrentDeveloperAccessPolicy()
	for _, user := range users {
		if user == nil {
			continue
		}
		var info TrustLevelInfo
		if user.Role >= common.RoleAdminUser {
			info = EvaluateTrustLevel(user.Role, nil, 0, 0, now)
		} else if user.TrustLevelOverride != nil {
			info = EvaluateTrustLevel(user.Role, user.TrustLevelOverride, 0, user.CreatedAt, now)
		} else {
			aggregate := aggregates[user.Id]
			anchor := trustActivityAnchor(user.CreatedAt, user.LastAPIActivityAt, aggregate.LastPaidCompleteAt)
			info = aggregate.withUSDDisplay(evaluateTrustLevelCredits(
				user.Role,
				user.TrustLevelOverride,
				aggregate.PaidCredits,
				aggregate.PaidAmount,
				aggregate.paidActivationComplete(policy) || user.ConsoleActivatedAt > 0,
				anchor,
				now,
				policy.trustConfiguration,
			))
			if aggregate.ProjectionUnavailable {
				info.PaidCreditProjectionAvailable = false
				info.PaidCredits, info.NextLevelPaidCredits, info.CreditsToNextLevel = nil, nil, nil
				info.PaidAmountUSD, info.NextLevelPaidAmountUSD, info.AmountToNextLevelUSD = nil, nil, nil
			}
		}
		user.TrustLevelInfo = &info
	}
	return nil
}

func SetUserTrustLevelOverride(userID int, level *int) error {
	if userID <= 0 {
		return gorm.ErrInvalidData
	}
	if level != nil && (*level < TrustLevelMinUser || *level > TrustLevelMaxUser) {
		return gorm.ErrInvalidData
	}
	if level != nil && *level == TrustLevelMinUser {
		return resetUserToL0(userID, "admin_set_trust_level_l0")
	}
	result := DB.Model(&User{}).
		Where("id = ? AND role < ?", userID, common.RoleAdminUser).
		Update("trust_level_override", level)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return invalidateUserCache(userID)
}

// ResetUserToL0 is the explicit administrator-only test/support reset. The
// zero override is intentional: it temporarily blocks both paid and manual
// activation until an administrator clears the override or approves a new
// access request.
func ResetUserToL0(userID int) error {
	return resetUserToL0(userID, "admin_reset_onboarding")
}

func resetUserToL0(userID int, sessionReason string) error {
	if userID <= 0 {
		return gorm.ErrInvalidData
	}

	var (
		nextAuthVersion int64
		sessions        []UserSession
		tokens          []Token
	)
	err := DB.Transaction(func(tx *gorm.DB) error {
		var target User
		if err := lockForUpdate(tx).Select("id", "role").Where("id = ?", userID).First(&target).Error; err != nil {
			return err
		}
		if target.Role >= common.RoleAdminUser {
			return gorm.ErrRecordNotFound
		}

		var err error
		sessions, err = revokeAccountSessionsWithTx(tx, userID, sessionReason, common.GetTimestamp())
		if err != nil {
			return err
		}
		if common.RedisEnabled {
			if err := tx.Unscoped().Select("id", commonKeyCol).Where("user_id = ?", userID).Find(&tokens).Error; err != nil {
				return err
			}
		}

		nextAuthVersion, err = IncrementUserAuthVersionWithTx(tx, userID)
		if err != nil {
			return err
		}
		result := tx.Model(&User{}).
			Where("id = ? AND role < ?", userID, common.RoleAdminUser).
			Updates(map[string]interface{}{
				"console_activated_at": 0,
				"trust_level_override": 0,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return reopenDeveloperAccessRequestForUserWithTx(tx, userID)
	})
	if err != nil {
		return err
	}

	// This is the same fail-closed cache/session/token invalidation path used by
	// account security transitions. The new auth version is published before a
	// delayed old snapshot can repopulate any cache.
	applyAccountActionCacheInvalidation(userID, nextAuthVersion, sessions, tokens)
	return nil
}
