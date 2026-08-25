package model

import (
	"fmt"
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
	Level          int  `json:"level"`
	AutomaticLevel int  `json:"automatic_level"`
	OverrideLevel  *int `json:"override_level"`
	// PaidAmount is the eligible API credit amount in USD. It intentionally
	// does not represent the gateway's settlement amount.
	PaidAmount           float64  `json:"paid_amount"`
	DiscountRatio        float64  `json:"discount_ratio"`
	DiscountPercent      float64  `json:"discount_percent"`
	NextLevel            *int     `json:"next_level"`
	NextLevelPaidAmount  *float64 `json:"next_level_paid_amount"`
	AmountToNextLevel    *float64 `json:"amount_to_next_level"`
	NextDecayAt          *int64   `json:"next_decay_at"`
	InactivityDecaySteps int      `json:"inactivity_decay_steps"`
	DecayPeriodDays      int      `json:"decay_period_days"`
	Overridden           bool     `json:"overridden"`
}

type TrustLevelTier struct {
	Level                   int      `json:"level"`
	MinPaidAmount           float64  `json:"min_paid_amount"`
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
	if level < TrustLevelMinUser || level > TrustLevelMaxUser {
		return TrustLevelTier{}
	}
	benefits := append([]string(nil), trustLevelBenefits[level]...)
	return TrustLevelTier{
		Level:         level,
		MinPaidAmount: trustLevelThresholds[level],
		// L1 can be reached through a successful top-up or an approved
		// administrator unlock request, so payment is not a prerequisite.
		RequiresSuccessfulTopUp: false,
		DiscountPercent:         (1 - trustLevelDiscountRatios[level]) * 100,
		Benefits:                benefits,
		BenefitCount:            len(benefits),
	}
}

func GetTrustLevelTiers() []TrustLevelTier {
	tiers := make([]TrustLevelTier, 0, TrustLevelMaxUser-TrustLevelMinUser+1)
	for level := TrustLevelMinUser; level <= TrustLevelMaxUser; level++ {
		tiers = append(tiers, trustLevelTier(level))
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
	// PaidAmountMicros stores eligible credited API balance in USD micros, not
	// the amount charged by the external payment provider.
	PaidAmountMicros   int64
	PaidAmount         float64
	LastPaidCompleteAt int64
	ActivationComplete bool
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
		return int64(len(key) + 48)
	},
)

func paidTopUpAggregateCacheKey(userID int) string {
	return strconv.Itoa(userID)
}

func automaticTrustLevel(paidAmount float64, activationComplete bool) int {
	if !activationComplete {
		return TrustLevelMinUser
	}
	for level := TrustLevelMaxUser; level >= TrustLevelMinUser+2; level-- {
		if paidAmount >= trustLevelThresholds[level] {
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
	if now <= 0 {
		now = time.Now().Unix()
	}
	if role == common.RoleRootUser {
		return administratorTrustLevelInfo(TrustLevelRoot)
	}
	if role >= common.RoleAdminUser {
		return administratorTrustLevelInfo(TrustLevelAdmin)
	}

	automaticLevel := automaticTrustLevel(paidAmount, activationComplete)
	effectiveLevel := automaticLevel
	decaySteps := 0
	var nextDecayAt *int64
	if automaticLevel > 0 && activityAnchor > 0 && now > activityAnchor {
		periodSeconds := int64(trustLevelDecayPeriod / time.Second)
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

	info := TrustLevelInfo{
		Level:                effectiveLevel,
		AutomaticLevel:       automaticLevel,
		OverrideLevel:        overrideLevel,
		PaidAmount:           paidAmount,
		DiscountRatio:        trustLevelDiscountRatios[effectiveLevel],
		DiscountPercent:      (1 - trustLevelDiscountRatios[effectiveLevel]) * 100,
		NextDecayAt:          nextDecayAt,
		InactivityDecaySteps: decaySteps,
		DecayPeriodDays:      int(trustLevelDecayPeriod / (24 * time.Hour)),
		Overridden:           overridden,
	}
	if automaticLevel < TrustLevelMaxUser {
		next := automaticLevel + 1
		threshold := trustLevelThresholds[next]
		remaining := threshold - paidAmount
		if remaining < 0 {
			remaining = 0
		}
		info.NextLevel = &next
		info.NextLevelPaidAmount = &threshold
		info.AmountToNextLevel = &remaining
	}
	return info
}

func administratorTrustLevelInfo(level int) TrustLevelInfo {
	return TrustLevelInfo{
		Level:           level,
		AutomaticLevel:  level,
		DiscountRatio:   trustLevelDiscountRatios[TrustLevelMaxUser],
		DiscountPercent: (1 - trustLevelDiscountRatios[TrustLevelMaxUser]) * 100,
		DecayPeriodDays: int(trustLevelDecayPeriod / (24 * time.Hour)),
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
	return aggregates[userID], nil
}

func getPaidTopUpAggregates(userIDs []int) (map[int]paidTopUpAggregate, error) {
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

	fresh, err := getFreshPaidTopUpAggregates(missing)
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
	return aggregates[userID], nil
}

func getFreshPaidTopUpAggregates(userIDs []int) (map[int]paidTopUpAggregate, error) {
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
		CreditedQuota          float64
		LastPaidCompleteAt     int64
		ActivationCompleteRows int64
	}
	var summaries []paidTopUpSummary
	activityExpression := "CASE WHEN complete_time > 0 THEN complete_time ELSE create_time END"
	creditedQuotaExpression, creditedQuotaArgs := positiveNormalizedCreditedQuotaSQL()
	selectClause := "user_id, " +
		"COALESCE(SUM(" + creditedQuotaExpression + "), 0) AS credited_quota, " +
		"COALESCE(MAX(" + activityExpression + "), 0) AS last_paid_complete_at, " +
		"COUNT(*) AS activation_complete_rows"
	query := DB.Model(&TopUp{}).
		Select(selectClause, creditedQuotaArgs...).
		Where("user_id IN ?", uniqueUserIDs).
		Where("("+creditedQuotaExpression+") > 0", creditedQuotaArgs...).
		Group("user_id")
	if err := successfulExternalPaidTopUpQuery(query).Scan(&summaries).Error; err != nil {
		return nil, err
	}
	for _, summary := range summaries {
		paidAmountMicros := creditedQuotaToUSDMicros(summary.CreditedQuota)
		result[summary.UserId] = paidTopUpAggregate{
			PaidAmountMicros:   paidAmountMicros,
			PaidAmount:         float64(paidAmountMicros) / 1_000_000,
			LastPaidCompleteAt: summary.LastPaidCompleteAt,
			ActivationComplete: summary.ActivationCompleteRows > 0,
		}
	}
	return result, nil
}

func creditedQuotaToUSDMicros(creditedQuota float64) int64 {
	if creditedQuota <= 0 || common.QuotaPerUnit <= 0 {
		return 0
	}
	return decimal.NewFromFloat(creditedQuota).
		Div(decimal.NewFromFloat(common.QuotaPerUnit)).
		Mul(decimal.NewFromInt(1_000_000)).
		Round(0).
		IntPart()
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
	return EvaluateTrustLevelWithActivation(user.Role, user.TrustLevelOverride, aggregate.PaidAmount, aggregate.ActivationComplete || user.ConsoleActivatedAt > 0, anchor, time.Now().Unix()), nil
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
	localAcceptance bool
}

func CurrentDeveloperAccessPolicy() DeveloperAccessPolicy {
	return DeveloperAccessPolicy{localAcceptance: LocalAcceptanceDeveloperAccessEnabled()}
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
	activationComplete := aggregate.ActivationComplete || user.ConsoleActivatedAt > 0
	anchor := trustActivityAnchor(user.CreatedAt, user.LastAPIActivityAt, aggregate.LastPaidCompleteAt)
	return UserAccessSnapshot{
		TrustLevel: EvaluateTrustLevelWithActivation(
			user.Role, nil, aggregate.PaidAmount, activationComplete, anchor, time.Now().Unix(),
		),
		DeveloperAccess:        ordinaryDeveloperAccessState(aggregate.ActivationComplete, user.ConsoleActivatedAt > 0),
		PaidAmountMicros:       aggregate.PaidAmountMicros,
		LastPaidCompleteAt:     aggregate.LastPaidCompleteAt,
		PaidActivationComplete: aggregate.ActivationComplete,
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
	return EvaluateTrustLevelWithActivation(user.Role, user.TrustLevelOverride, aggregate.PaidAmount, aggregate.ActivationComplete || user.ConsoleActivatedAt > 0, anchor, time.Now().Unix()), nil
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
	paid, err := HasSuccessfulPaidTopUpWithTx(tx, user.Id, true)
	if err != nil {
		return DeveloperAccessState{}, err
	}
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
	if user.Role >= common.RoleAdminUser {
		return OnboardingState{
			ActivationComplete:     access.Granted,
			PaidActivationComplete: access.PaidActivationComplete,
			CredentialComplete:     true,
			FirstRequestComplete:   true,
			Stage:                  "complete",
		}, nil
	}
	state := OnboardingState{
		ActivationComplete:     access.Granted,
		PaidActivationComplete: access.PaidActivationComplete,
	}
	if DB == nil {
		state.Stage = onboardingStage(state)
		return state, gorm.ErrInvalidDB
	}
	var activeCredentialCount int64
	if err := DB.Model(&Token{}).
		Where("user_id = ? AND status = ?", user.Id, common.TokenStatusEnabled).
		Count(&activeCredentialCount).Error; err != nil {
		state.Stage = onboardingStage(state)
		return state, err
	}
	state.CredentialComplete = activeCredentialCount > 0
	state.FirstRequestComplete = state.CredentialComplete && user.LastAPIActivityAt > 0
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
	userIDs := make([]int, 0, len(users))
	for _, user := range users {
		if user != nil && user.Role < common.RoleAdminUser && user.TrustLevelOverride == nil {
			userIDs = append(userIDs, user.Id)
		}
	}
	aggregates, err := getPaidTopUpAggregates(userIDs)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
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
			info = EvaluateTrustLevelWithActivation(
				user.Role,
				user.TrustLevelOverride,
				aggregate.PaidAmount,
				aggregate.ActivationComplete || user.ConsoleActivatedAt > 0,
				anchor,
				now,
			)
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
