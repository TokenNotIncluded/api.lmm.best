package model

import (
	"fmt"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGetAssistantUsageSummaryAggregatesSafeBreakdowns(t *testing.T) {
	setupAssistantCurrencyTest(t)
	previousLogDB := LOG_DB
	previousLogDatabaseType := common.LogDatabaseType()
	common.SetDatabaseTypes(common.MainDatabaseType(), common.DatabaseTypeSQLite)
	initCol()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf(
		"file:%s?mode=memory&cache=shared",
		strings.ReplaceAll(t.Name(), "/", "_"),
	)), &gorm.Config{})
	require.NoError(t, err)
	LOG_DB = db
	require.NoError(t, db.AutoMigrate(&Log{}))

	t.Cleanup(func() {
		LOG_DB = previousLogDB
		common.SetDatabaseTypes(common.MainDatabaseType(), previousLogDatabaseType)
		initCol()
		sqlDB, closeErr := db.DB()
		if closeErr == nil {
			_ = sqlDB.Close()
		}
	})

	const userID = 42
	require.NoError(t, db.Create(&[]Log{
		{UserId: userID, CreatedAt: 100, Type: LogTypeConsume, ModelName: "model-a", Group: "group-a", PromptTokens: 10, CompletionTokens: 5, Quota: 1750000},
		{UserId: userID, CreatedAt: 110, Type: LogTypeConsume, ModelName: "model-a", Group: "group-b", PromptTokens: 20, CompletionTokens: 10, Quota: 1750000},
		{UserId: userID, CreatedAt: 120, Type: LogTypeSystem, ModelName: "ignored", Group: "ignored", Quota: 999},
		{UserId: userID + 1, CreatedAt: 130, Type: LogTypeConsume, ModelName: "other-user", Group: "group-a", Quota: 999},
	}).Error)

	summary, err := GetAssistantUsageSummary(userID, 90, 120, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(2), summary.Requests)
	assert.Equal(t, int64(30), summary.PromptTokens)
	assert.Equal(t, int64(15), summary.CompletionTokens)
	assert.Equal(t, int64(45), summary.TotalTokens)
	assert.Equal(t, int64(3500000), summary.Quota)
	assert.Equal(t, float64(1), summary.CostUSD)
	require.Len(t, summary.Models, 1)
	assert.Equal(t, "model-a", summary.Models[0].Name)
	assert.Equal(t, int64(2), summary.Models[0].Requests)
	assert.Equal(t, float64(1), summary.Models[0].CostUSD)
	require.Len(t, summary.Groups, 2)
	assert.Equal(t, "group-a", summary.Groups[0].Name)
	assert.Equal(t, "group-b", summary.Groups[1].Name)
	assert.Equal(t, float64(0.5), summary.Groups[0].CostUSD)
	assert.Equal(t, float64(0.5), summary.Groups[1].CostUSD)
}

func TestGetAssistantFundingSummaryFiltersBillingSource(t *testing.T) {
	setupAssistantCurrencyTest(t)
	previousLogDB := LOG_DB
	previousLogDatabaseType := common.LogDatabaseType()
	common.SetDatabaseTypes(common.MainDatabaseType(), common.DatabaseTypeSQLite)
	initCol()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf(
		"file:%s?mode=memory&cache=shared",
		strings.ReplaceAll(t.Name(), "/", "_"),
	)), &gorm.Config{})
	require.NoError(t, err)
	LOG_DB = db
	require.NoError(t, db.AutoMigrate(&Log{}))

	t.Cleanup(func() {
		LOG_DB = previousLogDB
		common.SetDatabaseTypes(common.MainDatabaseType(), previousLogDatabaseType)
		initCol()
		sqlDB, closeErr := db.DB()
		if closeErr == nil {
			_ = sqlDB.Close()
		}
	})

	const billingUserID = 7
	require.NoError(t, db.Create(&[]Log{
		{UserId: billingUserID, CreatedAt: 100, Type: LogTypeConsume, PromptTokens: 10, CompletionTokens: 5, Quota: 1750000, Other: `{"billing_source":"assistant"}`},
		{UserId: billingUserID, CreatedAt: 110, Type: LogTypeConsume, PromptTokens: 20, CompletionTokens: 10, Quota: 1750000, Other: `{"billing_source": "assistant"}`},
		{UserId: billingUserID, CreatedAt: 120, Type: LogTypeConsume, PromptTokens: 30, CompletionTokens: 15, Quota: 300, Other: `{"billing_source":"wallet"}`},
		{UserId: billingUserID + 1, CreatedAt: 130, Type: LogTypeConsume, Quota: 999, Other: `{"billing_source":"assistant"}`},
	}).Error)

	summary, err := GetAssistantFundingSummary(billingUserID, 90, 120)
	require.NoError(t, err)
	assert.Equal(t, int64(2), summary.Requests)
	assert.Equal(t, int64(30), summary.PromptTokens)
	assert.Equal(t, int64(15), summary.CompletionTokens)
	assert.Equal(t, int64(45), summary.TotalTokens)
	assert.Equal(t, int64(3500000), summary.Quota)
	assert.Equal(t, float64(1), summary.CostUSD)
}

func TestAssistantUsageMissingCurrencyBasisRejectsEvenEmptyWindow(t *testing.T) {
	setupAssistantCurrencyTest(t)
	oldLogDB := LOG_DB
	LOG_DB = &gorm.DB{} // Missing basis must reject before touching a query.
	t.Cleanup(func() { LOG_DB = oldLogDB })
	common.ClearCreditsPerUSD()
	_, err := GetAssistantUsageSummary(7, 0, 100, 20)
	require.ErrorIs(t, err, common.ErrCreditUnitsUnavailable)
	_, err = GetAssistantFundingSummary(7, 0, 100)
	require.ErrorIs(t, err, common.ErrCreditUnitsUnavailable)
}

func TestAssistantUsageUSDUsesFixedCreditsDespiteFXAndBonusChanges(t *testing.T) {
	setupAssistantCurrencyTest(t)
	for _, fx := range []float64{7, 7.2, 8} {
		operation_setting.USDExchangeRate = fx
		operation_setting.TopUpPlatformUnitsPerCNY = 99
		usd, err := usageCostUSD(3500000)
		require.NoError(t, err)
		assert.Equal(t, float64(1), usd)
	}
}

func TestAssistantUsageUnrepresentableProjectionReturnsError(t *testing.T) {
	setupAssistantCurrencyTest(t)
	oldLogDB := LOG_DB
	oldLogType := common.LogDatabaseType()
	common.SetDatabaseTypes(common.MainDatabaseType(), common.DatabaseTypeSQLite)
	initCol()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Log{}))
	LOG_DB = db
	t.Cleanup(func() {
		LOG_DB = oldLogDB
		common.SetDatabaseTypes(common.MainDatabaseType(), oldLogType)
		initCol()
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	require.NoError(t, db.Create(&Log{UserId: 7, Type: LogTypeConsume, CreatedAt: 10, Quota: 9007199254740991, Other: `{"billing_source":"assistant"}`}).Error)
	for _, anchor := range []string{"1e-500", "1e-300"} {
		require.NoError(t, common.SetCreditCurrencyBasis(decimal.RequireFromString(anchor), decimal.NewFromInt(500000)))
		_, err := usageCostUSD(9007199254740991)
		require.ErrorIs(t, err, common.ErrCreditUnitsUnavailable)
		_, err = GetAssistantUsageSummary(7, 0, 20, 20)
		require.ErrorIs(t, err, common.ErrCreditUnitsUnavailable)
		_, err = GetAssistantFundingSummary(7, 0, 20)
		require.ErrorIs(t, err, common.ErrCreditUnitsUnavailable)
	}
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(3500000), decimal.NewFromInt(500000)))
	zero, err := usageCostUSD(0)
	require.NoError(t, err)
	assert.Equal(t, float64(0), zero)
}
