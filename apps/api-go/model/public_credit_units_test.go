package model

import (
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestPublicDenominationOptionKeepsExistingLedgerAndQuotes(t *testing.T) {
	setupCreditUnitsDB(t)
	require.NoError(t, DB.Create(&Option{Key: CreditsPerUSDOptionKey, Value: "500000"}).Error)
	user := User{Username: "public-unit-fixture", Quota: 500000, Password: "password"}
	require.NoError(t, DB.Create(&user).Error)
	order := TopUp{UserId: user.Id, TradeNo: "public-unit-existing-paid", CreditedQuota: 500000, SettlementCurrency: "USD", ExpectedAmountMicros: 1000000, SettledAmountMicros: 1000000, Status: common.TopUpStatusSuccess}
	require.NoError(t, DB.Create(&order).Error)
	pending := TopUp{UserId: user.Id, TradeNo: "public-unit-existing-pending", CreditedQuota: 500000, SettlementCurrency: "USD", ExpectedAmountMicros: 1000000, Status: common.TopUpStatusPending}
	require.NoError(t, DB.Create(&pending).Error)
	require.NoError(t, InitializeCreditUnits(t.Context()))
	require.Error(t, UpdateOption(PublicCreditsPerUSDOptionKey, "200000"))
	require.NoError(t, UpdateOption(PublicCreditsPerUSDOptionKey, "500000.0"))
	public, err := common.PublicCreditsPerUSD()
	require.NoError(t, err)
	require.Equal(t, "500000", public.String())
	ledger, err := common.LedgerQuotaPerUSD()
	require.NoError(t, err)
	require.Equal(t, "500000", ledger.String())
	usd, err := common.CreditsToUSD(int64(user.Quota))
	require.NoError(t, err)
	require.Equal(t, "1", usd.String())
	var storedUser User
	var storedPaid, storedPending TopUp
	require.NoError(t, DB.First(&storedUser, user.Id).Error)
	require.Equal(t, user.Quota, storedUser.Quota)
	require.NoError(t, DB.First(&storedPaid, order.Id).Error)
	require.Equal(t, order, storedPaid)
	require.NoError(t, DB.First(&storedPending, pending.Id).Error)
	require.Equal(t, pending, storedPending)
	require.Error(t, UpdateOption(CreditsPerUSDOptionKey, "100000"))
	require.Error(t, UpdateOption("QuotaPerUnit", "100000"))
	require.Error(t, UpdateOptionsBulk(map[string]string{PublicCreditsPerUSDOptionKey: "0", "USDExchangeRate": "9"}))
	require.NoError(t, VerifyCreditUnits(t.Context()))
	public, err = common.PublicCreditsPerUSD()
	require.NoError(t, err)
	require.True(t, public.Equal(decimal.NewFromInt(500000)))
}

func TestPublicDenominationVerificationOfOldSchemaDoesNotWrite(t *testing.T) {
	setupCreditUnitsDB(t)
	require.NoError(t, DB.Create(&Option{Key: CreditsPerUSDOptionKey, Value: "500000"}).Error)
	require.NoError(t, DB.Create(&Option{Key: LegacyPricingQuotaPerUnitOptionKey, Value: "500000"}).Error)
	require.NoError(t, VerifyCreditUnits(t.Context()))
	var count int64
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", PublicCreditsPerUSDOptionKey).Count(&count).Error)
	require.Zero(t, count)
	public, err := common.PublicCreditsPerUSD()
	require.NoError(t, err)
	require.Equal(t, "500000", public.String())
	require.NoError(t, InitializeCreditUnits(t.Context()))
	public, err = common.PublicCreditsPerUSD()
	require.NoError(t, err)
	require.Equal(t, "500000", public.String())
}
