package controller

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupUserOnboardingTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := model.DB
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.TopUp{}))
	t.Cleanup(func() {
		model.DB = previousDB
		common.RedisEnabled = previousRedisEnabled
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	return db
}

func TestBuildSelfUserDataReportsServerDerivedOnboardingStages(t *testing.T) {
	db := setupUserOnboardingTestDB(t)
	user := model.User{Username: "onboarding-user", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)

	assertOnboarding := func(expectedStage string, activation, paidActivation, credential, request bool) {
		t.Helper()
		data := buildSelfUserData(&user)
		onboarding := data["onboarding"].(gin.H)
		assert.Equal(t, activation, onboarding["activation_complete"])
		assert.Equal(t, paidActivation, onboarding["paid_activation_complete"])
		assert.Equal(t, credential, onboarding["credential_complete"])
		assert.Equal(t, request, onboarding["first_request_complete"])
		assert.Equal(t, expectedStage, onboarding["stage"])
		assert.Equal(t, activation, data["developer_access_granted"])
		permissions := data["permissions"].(map[string]interface{})
		assert.Equal(t, activation, permissions["docs_access"])
	}

	assertOnboarding("activate", false, false, false, false)
	require.NoError(t, db.Create(&model.TopUp{UserId: user.Id, TradeNo: "paid-onboarding", Amount: int64(common.QuotaPerUnit) / 100, Money: 0.01, Status: common.TopUpStatusSuccess, PaymentProvider: model.PaymentProviderStripe}).Error)
	assertOnboarding("credential", true, true, false, false)
	levelZero := 0
	user.TrustLevelOverride = &levelZero
	require.NoError(t, db.Model(&user).Update("trust_level_override", levelZero).Error)
	assertOnboarding("activate", false, false, false, false)
	user.TrustLevelOverride = nil
	require.NoError(t, db.Model(&user).Update("trust_level_override", nil).Error)
	disabledToken := model.Token{UserId: user.Id, Key: "onboarding-disabled-token", Status: common.TokenStatusDisabled}
	require.NoError(t, db.Create(&disabledToken).Error)
	deletedToken := model.Token{UserId: user.Id, Key: "onboarding-deleted-token", Status: common.TokenStatusEnabled}
	require.NoError(t, db.Create(&deletedToken).Error)
	require.NoError(t, db.Delete(&deletedToken).Error)
	assertOnboarding("credential", true, true, false, false)
	require.NoError(t, db.Create(&model.Token{UserId: user.Id, Key: "onboarding-token", Status: common.TokenStatusEnabled}).Error)
	assertOnboarding("first_request", true, true, true, false)
	user.LastAPIActivityAt = 123
	require.NoError(t, db.Model(&user).Update("last_api_activity_at", user.LastAPIActivityAt).Error)
	assertOnboarding("complete", true, true, true, true)
}

func TestBuildSelfUserDataLocalAcceptanceUsesOrdinaryCredentialMilestones(t *testing.T) {
	previousCapability := model.LocalAcceptanceDeveloperAccessEnabled()
	model.SetLocalAcceptanceDeveloperAccess(true)
	t.Cleanup(func() {
		model.SetLocalAcceptanceDeveloperAccess(previousCapability)
	})

	db := setupUserOnboardingTestDB(t)
	user := model.User{Username: "acceptance-onboarding-user", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)

	data := buildSelfUserData(&user)
	onboarding := data["onboarding"].(gin.H)
	assert.True(t, data["developer_access_granted"].(bool))
	assert.True(t, onboarding["activation_complete"].(bool))
	assert.False(t, onboarding["paid_activation_complete"].(bool))
	assert.False(t, onboarding["credential_complete"].(bool))
	assert.Equal(t, "credential", onboarding["stage"])

	require.NoError(t, db.Create(&model.Token{UserId: user.Id, Key: "acceptance-onboarding-token", Status: common.TokenStatusEnabled}).Error)
	data = buildSelfUserData(&user)
	onboarding = data["onboarding"].(gin.H)
	assert.True(t, data["developer_access_granted"].(bool))
	assert.False(t, onboarding["paid_activation_complete"].(bool))
	assert.True(t, onboarding["credential_complete"].(bool))
	assert.False(t, onboarding["first_request_complete"].(bool))
	assert.Equal(t, "first_request", onboarding["stage"])
}

func TestBuildSelfUserDataCompletesOnboardingForAdministrators(t *testing.T) {
	db := setupUserOnboardingTestDB(t)
	for _, role := range []int{common.RoleAdminUser, common.RoleRootUser} {
		user := model.User{Username: fmt.Sprintf("onboarding-admin-%d", role), Password: "password", AffCode: fmt.Sprintf("onboarding-admin-aff-%d", role), Role: role, Status: common.UserStatusEnabled}
		require.NoError(t, db.Create(&user).Error)
		data := buildSelfUserData(&user)
		onboarding := data["onboarding"].(gin.H)
		assert.True(t, onboarding["activation_complete"].(bool))
		assert.False(t, onboarding["paid_activation_complete"].(bool))
		assert.True(t, onboarding["credential_complete"].(bool))
		assert.True(t, onboarding["first_request_complete"].(bool))
		assert.Equal(t, "complete", onboarding["stage"])
		assert.True(t, data["developer_access_granted"].(bool))
	}
}

func TestBuildSelfUserDataDistinguishesOAuthBillingFromManualKey(t *testing.T) {
	db := setupUserOnboardingTestDB(t)
	level := 1
	user := model.User{Username: "oauth-onboarding", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, TrustLevelOverride: &level}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&model.Token{UserId: user.Id, Key: "oauth_managed_test", OAuthManaged: true, Status: common.TokenStatusEnabled}).Error)
	state := buildSelfUserData(&user)["onboarding"].(gin.H)
	assert.Equal(t, true, state["details_available"])
	assert.Equal(t, true, state["credential_complete"])
	assert.Equal(t, false, state["api_key_created"])
	manual := model.Token{UserId: user.Id, Key: "manual_test", Status: common.TokenStatusEnabled}
	require.NoError(t, db.Create(&manual).Error)
	assert.Equal(t, true, buildSelfUserData(&user)["onboarding"].(gin.H)["api_key_created"])
	require.NoError(t, db.Delete(&manual).Error)
	assert.Equal(t, false, buildSelfUserData(&user)["onboarding"].(gin.H)["api_key_created"])
}

func TestBuildSelfUserDataPreservesGrantedAccessWhenMilestoneQueryFails(t *testing.T) {
	db := setupUserOnboardingTestDB(t)
	levelOne := 1
	user := model.User{Username: "onboarding-query-failure", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, TrustLevelOverride: &levelOne}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Migrator().DropTable(&model.Token{}))

	data := buildSelfUserData(&user)
	onboarding := data["onboarding"].(gin.H)
	assert.True(t, onboarding["activation_complete"].(bool))
	assert.False(t, onboarding["credential_complete"].(bool))
	assert.False(t, onboarding["first_request_complete"].(bool))
	assert.Equal(t, "credential", onboarding["stage"])
	assert.True(t, data["developer_access_granted"].(bool))
	permissions := data["permissions"].(map[string]interface{})
	assert.True(t, permissions["docs_access"].(bool))
}

type selfTopUpQueryCounter struct {
	logger.Interface
	queries  int
	countSQL string
}

func (counter *selfTopUpQueryCounter) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	sql, rows := fc()
	if strings.Contains(strings.ToLower(sql), "top_ups") {
		counter.queries++
		counter.countSQL = sql
	}
	counter.Interface.Trace(ctx, begin, func() (string, int64) { return sql, rows }, err)
}

func TestBuildSelfUserDataDoesNotDuplicateTopUpAggregate(t *testing.T) {
	previousDB := model.DB
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	counter := &selfTopUpQueryCounter{Interface: logger.Default.LogMode(logger.Silent)}
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))), &gorm.Config{Logger: counter})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.TopUp{}))
	user := model.User{Username: "self-query-count", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&model.TopUp{
		UserId: user.Id, TradeNo: "self-query-payment", Amount: 1, CreditedQuota: int64(common.QuotaPerUnit),
		SettledAmountMicros: 10_000_000, Money: 10, Status: common.TopUpStatusSuccess, PaymentProvider: model.PaymentProviderStripe,
	}).Error)
	counter.queries = 0
	t.Cleanup(func() {
		model.DB = previousDB
		common.RedisEnabled = previousRedisEnabled
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})

	data := buildSelfUserData(&user)
	assert.True(t, data["developer_access_granted"].(bool))
	assert.Equal(t, 1, counter.queries)
	assert.Contains(t, strings.ToLower(counter.countSQL), "sum(")
}
