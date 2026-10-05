package model

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func intPointer(value int) *int { return &value }

func TestEvaluateTrustLevelRoleMapping(t *testing.T) {
	assert.Equal(t, TrustLevelRoot, EvaluateTrustLevel(common.RoleRootUser, nil, 0, 0, 1).Level)
	assert.Equal(t, TrustLevelAdmin, EvaluateTrustLevel(common.RoleAdminUser, nil, 0, 0, 1).Level)
	assert.Equal(t, TrustLevelMinUser, EvaluateTrustLevel(common.RoleCommonUser, nil, 0, 0, 1).Level)
}

func TestGetTrustLevelTiersMatchesEvaluationPolicy(t *testing.T) {
	tiers := GetTrustLevelTiers()
	require.Len(t, tiers, TrustLevelMaxUser+1)
	for _, tier := range tiers {
		info := EvaluateTrustLevelWithActivation(
			common.RoleCommonUser,
			nil,
			tier.MinPaidAmount,
			tier.Level > TrustLevelMinUser,
			time.Now().Unix(),
			time.Now().Unix(),
		)
		assert.Equal(t, tier.Level, info.Level)
		assert.InDelta(t, tier.DiscountPercent, info.DiscountPercent, 0.0001)
	}
	assert.False(t, tiers[TrustLevelMinUser+1].RequiresSuccessfulTopUp)
}

func TestGetTrustLevelTierViewsHideHigherBenefits(t *testing.T) {
	viewer := GetTrustLevelTierViews(TrustLevelMinUser)
	require.Len(t, viewer, TrustLevelMaxUser+1)
	assert.NotEmpty(t, viewer[TrustLevelMinUser].Benefits)
	assert.False(t, viewer[TrustLevelMinUser].BenefitsHidden)
	assert.Empty(t, viewer[TrustLevelMinUser+1].Benefits)
	assert.Equal(t, 1, viewer[TrustLevelMinUser+1].BenefitCount)
	assert.True(t, viewer[TrustLevelMinUser+1].BenefitsHidden)
	assert.True(t, viewer[TrustLevelMinUser+1].DiscountHidden)

	admin := GetTrustLevelTierViews(TrustLevelAdmin)
	assert.NotEmpty(t, admin[TrustLevelMaxUser].Benefits)
	assert.False(t, admin[TrustLevelMaxUser].BenefitsHidden)
	assert.False(t, admin[TrustLevelMaxUser].DiscountHidden)
}

func TestEvaluateTrustLevelPaidActivationThresholdsAndDiscounts(t *testing.T) {
	now := time.Now().Unix()
	for _, test := range []struct {
		name      string
		paid      float64
		activated bool
		level     int
		discount  float64
	}{
		{name: "no payment", paid: 0, level: 0, discount: 0},
		{name: "credited amount without successful payment", paid: 2000, level: 0, discount: 0},
		{name: "any successful payment unlocks level one", paid: 0.01, activated: true, level: 1, discount: 0},
		{name: "level two", paid: 100, activated: true, level: 2, discount: 3},
		{name: "level three", paid: 500, activated: true, level: 3, discount: 6},
		{name: "level four", paid: 2000, activated: true, level: 4, discount: 10},
	} {
		t.Run(test.name, func(t *testing.T) {
			info := EvaluateTrustLevelWithActivation(common.RoleCommonUser, nil, test.paid, test.activated, now, now)
			assert.Equal(t, test.level, info.Level)
			assert.InDelta(t, test.discount, info.DiscountPercent, 0.0001)
		})
	}
}

func TestEvaluateTrustLevelDecaysEveryNinetyDays(t *testing.T) {
	period := int64(trustLevelDecayPeriod / time.Second)
	now := int64(1_800_000_000)
	info := EvaluateTrustLevelWithActivation(common.RoleCommonUser, nil, 2000, true, now-2*period-1, now)

	assert.Equal(t, 4, info.AutomaticLevel)
	assert.Equal(t, 2, info.Level)
	assert.Equal(t, 2, info.InactivityDecaySteps)
	assert.NotNil(t, info.NextDecayAt)
}

func TestEvaluateTrustLevelActivatedAccountNeverDecaysBelowLevelOne(t *testing.T) {
	period := int64(trustLevelDecayPeriod / time.Second)
	now := int64(1_800_000_000)
	info := EvaluateTrustLevelWithActivation(common.RoleCommonUser, nil, 2000, true, now-10*period, now)

	assert.Equal(t, TrustLevelRoot-2, info.AutomaticLevel)
	assert.Equal(t, TrustLevelMinUser+1, info.Level)
	assert.Equal(t, TrustLevelRoot-3, info.InactivityDecaySteps)
	assert.Nil(t, info.NextDecayAt)
}

func TestEvaluateTrustLevelOverrideDisablesDecay(t *testing.T) {
	now := int64(1_800_000_000)
	info := EvaluateTrustLevelWithActivation(common.RoleCommonUser, intPointer(3), 10, true, 1, now)

	assert.Equal(t, 1, info.AutomaticLevel)
	assert.Equal(t, 3, info.Level)
	assert.True(t, info.Overridden)
	assert.Nil(t, info.NextDecayAt)
}

func TestEvaluateTrustLevelOverrideCanForceLevelZeroAfterActivation(t *testing.T) {
	info := EvaluateTrustLevelWithActivation(common.RoleCommonUser, intPointer(0), 2000, true, 1, time.Now().Unix())

	assert.Equal(t, TrustLevelMinUser, info.Level)
	assert.True(t, info.Overridden)
}

func TestEvaluateTrustLevelInvalidOrdinaryOverrideFailsClosed(t *testing.T) {
	info := EvaluateTrustLevelWithActivation(common.RoleCommonUser, intPointer(99), 2000, true, 1, time.Now().Unix())
	assert.Equal(t, TrustLevelMinUser, info.Level)
	assert.True(t, info.Overridden)
	assert.Equal(t, TrustLevelRoot, EvaluateTrustLevelWithActivation(common.RoleRootUser, intPointer(99), 0, false, 0, 1).Level)
	assert.Equal(t, TrustLevelAdmin, EvaluateTrustLevelWithActivation(common.RoleAdminUser, intPointer(99), 0, false, 0, 1).Level)
}

func TestGetTrustLevelInfoUsesCompletedExternalTopUps(t *testing.T) {
	installPaidPolicyCurrencyFixture(t, common.QuotaPerUnit)
	previousDB := DB
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	require.NoError(t, db.AutoMigrate(&User{}, &TopUp{}))
	t.Cleanup(func() {
		DB = previousDB
		common.RedisEnabled = previousRedis
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})

	user := User{Username: "trust-user", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&TopUp{UserId: user.Id, TradeNo: "pending", Amount: 2000 * int64(common.QuotaPerUnit), Money: 2000, Status: common.TopUpStatusPending, PaymentProvider: PaymentProviderStripe}).Error)
	require.NoError(t, db.Create(&TopUp{UserId: user.Id, TradeNo: "balance", Amount: 2000 * int64(common.QuotaPerUnit), Money: 2000, Status: common.TopUpStatusSuccess, PaymentMethod: PaymentMethodBalance, PaymentProvider: PaymentProviderBalance}).Error)
	require.NoError(t, db.Create(&TopUp{UserId: user.Id, TradeNo: "paid", Amount: 1, CreditedQuota: int64(common.QuotaPerUnit), Money: 0.01, Status: common.TopUpStatusSuccess, PaymentProvider: PaymentProviderStripe, CompleteTime: time.Now().Unix()}).Error)

	info, err := GetTrustLevelInfoForUser(&user)
	require.NoError(t, err)
	assert.Equal(t, 1, info.Level)
	assert.InDelta(t, 1, info.PaidAmount, 0.0001)
}

type topUpQueryCounter struct {
	logger.Interface
	queries  int
	countSQL string
}

func (counter *topUpQueryCounter) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	sql, rows := fc()
	if strings.Contains(strings.ToLower(sql), "top_ups") {
		counter.queries++
		counter.countSQL = sql
	}
	counter.Interface.Trace(ctx, begin, func() (string, int64) { return sql, rows }, err)
}

func TestExplicitDeveloperAccessAndTrustPathsSkipTopUpHistory(t *testing.T) {
	previousDB := DB
	baseLogger := logger.Default.LogMode(logger.Silent)
	counter := &topUpQueryCounter{Interface: baseLogger}
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))), &gorm.Config{Logger: counter})
	require.NoError(t, err)
	DB = db
	t.Cleanup(func() {
		DB = previousDB
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})

	levelOne := 1
	invalidLevel := 99
	for _, test := range []struct {
		user    *User
		granted bool
	}{
		{user: &User{Id: 1, Role: common.RoleAdminUser}, granted: true},
		{user: &User{Id: 2, Role: common.RoleRootUser}, granted: true},
		{user: &User{Id: 3, Role: common.RoleCommonUser, TrustLevelOverride: &levelOne}, granted: true},
		{user: &User{Id: 4, Role: common.RoleCommonUser, TrustLevelOverride: &invalidLevel}},
	} {
		snapshot, snapshotErr := GetFreshUserAccessSnapshot(test.user)
		require.NoError(t, snapshotErr)
		assert.Equal(t, test.granted, snapshot.DeveloperAccess.Granted)
		_, accessErr := GetDeveloperAccessStateForUser(test.user)
		require.NoError(t, accessErr)
		info, trustErr := GetTrustLevelInfoForUser(test.user)
		require.NoError(t, trustErr)
		assert.Equal(t, snapshot.TrustLevel.Level, info.Level)
		baseInfo, baseErr := GetTrustLevelInfoForUserBase(test.user.ToBaseUser())
		require.NoError(t, baseErr)
		assert.Equal(t, snapshot.TrustLevel.Level, baseInfo.Level)
	}
	assert.Zero(t, counter.queries)
}

func TestExplicitAccessSnapshotTreatsPaidActivationAsUnqueried(t *testing.T) {
	levelOne := 1
	snapshot, err := GetFreshUserAccessSnapshot(&User{Id: 99, Role: common.RoleCommonUser, TrustLevelOverride: &levelOne})
	require.NoError(t, err)
	assert.True(t, snapshot.DeveloperAccess.Granted)
	assert.False(t, snapshot.DeveloperAccess.PaidActivationComplete)
	assert.False(t, snapshot.PaidActivationComplete)
}

func TestLocalAcceptanceDeveloperAccessPreservesPaidActivationFact(t *testing.T) {
	previousCapability := LocalAcceptanceDeveloperAccessEnabled()
	SetLocalAcceptanceDeveloperAccess(true)
	t.Cleanup(func() {
		SetLocalAcceptanceDeveloperAccess(previousCapability)
	})

	state := ordinaryDeveloperAccessState(false, false)
	assert.True(t, state.Granted)
	assert.False(t, state.PaidActivationComplete)

	paidState := ordinaryDeveloperAccessState(true, false)
	assert.True(t, paidState.Granted)
	assert.True(t, paidState.PaidActivationComplete)
}

func TestFreshUserAccessSnapshotUsesOneBoundedAggregateQuery(t *testing.T) {
	installPaidPolicyCurrencyFixture(t, common.QuotaPerUnit)
	previousDB := DB
	baseLogger := logger.Default.LogMode(logger.Silent)
	counter := &topUpQueryCounter{Interface: baseLogger}
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))), &gorm.Config{Logger: counter})
	require.NoError(t, err)
	DB = db
	require.NoError(t, db.AutoMigrate(&TopUp{}))
	require.NoError(t, db.Create(&TopUp{
		UserId: 17, TradeNo: "snapshot-payment", Amount: 1, CreditedQuota: int64(common.QuotaPerUnit),
		SettledAmountMicros: 10_000_000, Money: 10, Status: common.TopUpStatusSuccess, PaymentProvider: PaymentProviderStripe,
	}).Error)
	counter.queries = 0
	t.Cleanup(func() {
		DB = previousDB
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})

	snapshot, err := GetFreshUserAccessSnapshot(&User{Id: 17, Role: common.RoleCommonUser})
	require.NoError(t, err)
	assert.True(t, snapshot.DeveloperAccess.Granted)
	assert.True(t, snapshot.PaidActivationComplete)
	assert.Equal(t, TrustLevelMinUser+1, snapshot.TrustLevel.Level)
	assert.Equal(t, 1, counter.queries)
	assert.Contains(t, strings.ToLower(counter.countSQL), "sum(")
	assert.NotContains(t, strings.ToLower(counter.countSQL), "select *")
}

func TestManualConsoleActivationUnlocksL1WithoutPaidTopUp(t *testing.T) {
	installPaidPolicyCurrencyFixture(t, common.QuotaPerUnit)
	previousDB := DB
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	require.NoError(t, db.AutoMigrate(&TopUp{}))
	t.Cleanup(func() {
		DB = previousDB
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})

	user := &User{Id: 42, Role: common.RoleCommonUser, ConsoleActivatedAt: 1}
	snapshot, err := GetFreshUserAccessSnapshot(user)
	require.NoError(t, err)
	assert.Equal(t, TrustLevelMinUser+1, snapshot.TrustLevel.Level)
	assert.True(t, snapshot.DeveloperAccess.Granted)
	assert.False(t, snapshot.DeveloperAccess.PaidActivationComplete)

	info, err := GetTrustLevelInfoForUser(user)
	require.NoError(t, err)
	assert.Equal(t, TrustLevelMinUser+1, info.Level)
}

func TestEnrichUsersTrustLevelsQueriesOnlyOrdinaryUsersWithoutOverrides(t *testing.T) {
	installPaidPolicyCurrencyFixture(t, common.QuotaPerUnit)
	previousDB := DB
	counter := &topUpQueryCounter{Interface: logger.Default.LogMode(logger.Silent)}
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))), &gorm.Config{Logger: counter})
	require.NoError(t, err)
	DB = db
	require.NoError(t, db.AutoMigrate(&TopUp{}))
	validOverride := 3
	invalidOverride := 99
	users := []*User{
		{Id: 31, Role: common.RoleCommonUser, TrustLevelOverride: &validOverride},
		{Id: 32, Role: common.RoleCommonUser, TrustLevelOverride: &invalidOverride},
		{Id: 33, Role: common.RoleCommonUser},
		{Id: 34, Role: common.RoleAdminUser},
		{Id: 35, Role: common.RoleRootUser},
	}
	require.NoError(t, db.Create(&TopUp{
		UserId: 33, TradeNo: "batch-no-override", Amount: 1, CreditedQuota: int64(common.QuotaPerUnit),
		SettledAmountMicros: 10_000_000, Money: 10, Status: common.TopUpStatusSuccess, PaymentProvider: PaymentProviderStripe,
	}).Error)
	counter.queries = 0
	counter.countSQL = ""
	t.Cleanup(func() {
		DB = previousDB
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})

	require.NoError(t, EnrichUsersTrustLevels(users))
	assert.Equal(t, 3, users[0].TrustLevelInfo.Level)
	assert.Equal(t, TrustLevelMinUser, users[1].TrustLevelInfo.Level)
	assert.Equal(t, TrustLevelMinUser+1, users[2].TrustLevelInfo.Level)
	assert.Equal(t, TrustLevelAdmin, users[3].TrustLevelInfo.Level)
	assert.Equal(t, TrustLevelRoot, users[4].TrustLevelInfo.Level)
	assert.Equal(t, 1, counter.queries)
	assert.Contains(t, counter.countSQL, "user_id IN (33)")
	assert.NotContains(t, counter.countSQL, "31,32")
	assert.NotContains(t, counter.countSQL, "34")
	assert.NotContains(t, counter.countSQL, "35")
}

func TestEnrichUsersTrustLevelsHonorsConsoleActivation(t *testing.T) {
	installPaidPolicyCurrencyFixture(t, common.QuotaPerUnit)
	previousDB := DB
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	require.NoError(t, db.AutoMigrate(&TopUp{}))
	t.Cleanup(func() {
		DB = previousDB
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})

	users := []*User{{Id: 36, Role: common.RoleCommonUser, ConsoleActivatedAt: 1}}
	require.NoError(t, EnrichUsersTrustLevels(users))
	require.NotNil(t, users[0].TrustLevelInfo)
	assert.Equal(t, TrustLevelMinUser+1, users[0].TrustLevelInfo.Level)
	assert.Equal(t, TrustLevelMinUser+1, users[0].TrustLevelInfo.AutomaticLevel)
}
