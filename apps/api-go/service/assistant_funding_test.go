package service

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupAssistantFundingTestDB(t *testing.T, quota int) (*gorm.DB, int) {
	t.Helper()
	previousDB := model.DB
	previousRedisEnabled := common.RedisEnabled
	previousBatchUpdateEnabled := common.BatchUpdateEnabled
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.User{}))
	user := model.User{Username: "assistant-root", Password: "password", Quota: quota, Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	t.Cleanup(func() {
		model.DB = previousDB
		common.RedisEnabled = previousRedisEnabled
		common.BatchUpdateEnabled = previousBatchUpdateEnabled
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	return db, user.Id
}

func TestAssistantFundingChargesSuperAdministratorWallet(t *testing.T) {
	_, userId := setupAssistantFundingTestDB(t, 200)
	funding := NewAssistantFunding(userId)

	require.NoError(t, funding.PreConsume(120))
	assert.Equal(t, 120, funding.consumed)
	quota, err := model.GetUserQuota(userId, true)
	require.NoError(t, err)
	assert.Equal(t, 80, quota)

	require.NoError(t, funding.Settle(-50))
	assert.Equal(t, 70, funding.consumed)
	quota, err = model.GetUserQuota(userId, true)
	require.NoError(t, err)
	assert.Equal(t, 130, quota)
}

func TestAssistantFundingSettlesAdditionalUsage(t *testing.T) {
	_, userId := setupAssistantFundingTestDB(t, 100)
	funding := NewAssistantFunding(userId)

	require.NoError(t, funding.PreConsume(30))
	require.NoError(t, funding.Settle(40))
	assert.Equal(t, 70, funding.consumed)
	quota, err := model.GetUserQuota(userId, true)
	require.NoError(t, err)
	assert.Equal(t, 30, quota)
}

func TestAssistantFundingRejectsWhenSuperAdministratorBalanceIsInsufficient(t *testing.T) {
	_, userId := setupAssistantFundingTestDB(t, 10)
	funding := NewAssistantFunding(userId)

	err := funding.PreConsume(20)
	assert.True(t, errors.Is(err, ErrAssistantBalanceInsufficient))
	quota, quotaErr := model.GetUserQuota(userId, true)
	require.NoError(t, quotaErr)
	assert.Equal(t, 10, quota)
}

func TestAssistantFundingRefundsSuperAdministratorWallet(t *testing.T) {
	_, userId := setupAssistantFundingTestDB(t, 100)
	funding := NewAssistantFunding(userId)
	require.NoError(t, funding.PreConsume(80))
	require.NoError(t, funding.Refund())

	quota, err := model.GetUserQuota(userId, true)
	require.NoError(t, err)
	assert.Equal(t, 100, quota)
}

func TestAssistantFundingRefundOverflowLeavesWalletAndSettlementStateUnchanged(t *testing.T) {
	db, userId := setupAssistantFundingTestDB(t, common.MaxWalletQuota)
	funding := NewAssistantFunding(userId)
	require.NoError(t, funding.PreConsume(1))
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", userId).Update("quota", common.MaxWalletQuota).Error)

	err := funding.Refund()
	require.Error(t, err)
	assert.Equal(t, 1, funding.consumed)
	quota, quotaErr := model.GetUserQuota(userId, true)
	require.NoError(t, quotaErr)
	assert.Equal(t, common.MaxWalletQuota, quota)
}

func TestAssistantFundingRejectsNonRootOrDisabledAccount(t *testing.T) {
	db, userId := setupAssistantFundingTestDB(t, 100)
	funding := NewAssistantFunding(userId)

	require.NoError(t, db.Model(&model.User{}).Where("id = ?", userId).Update("status", common.UserStatusDisabled).Error)
	assert.ErrorIs(t, funding.PreConsume(10), ErrAssistantBillingAccountUnavailable)

	require.NoError(t, db.Model(&model.User{}).Where("id = ?", userId).Updates(map[string]any{
		"status": common.UserStatusEnabled,
		"role":   common.RoleCommonUser,
	}).Error)
	assert.ErrorIs(t, funding.PreConsume(10), ErrAssistantBillingAccountUnavailable)
	quota, err := model.GetUserQuota(userId, true)
	require.NoError(t, err)
	assert.Equal(t, 100, quota)
}

func TestAssistantFundingRefundsOriginalAccountAfterItIsDisabled(t *testing.T) {
	db, userId := setupAssistantFundingTestDB(t, 100)
	funding := NewAssistantFunding(userId)
	require.NoError(t, funding.PreConsume(40))
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", userId).Update("status", common.UserStatusDisabled).Error)
	require.NoError(t, funding.Refund())

	quota, err := model.GetUserQuota(userId, true)
	require.NoError(t, err)
	assert.Equal(t, 100, quota)
}
