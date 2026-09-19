package model

import (
	"context"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func TestAcquisitionCostSameCohortPaymentsAndMissingSpend(t *testing.T) {
	db := acquisitionDB(t)
	require.NoError(t, db.AutoMigrate(&AcquisitionCost{}))
	ctx := context.Background()
	now := time.Now().Unix()
	link, err := SaveAcquisitionLink(ctx, AcquisitionLink{Name: "Cost", Source: "community", Campaign: "launch", Target: "/"})
	require.NoError(t, err)
	scope := AcquisitionCostScope{LinkID: link.ID, FromAt: now - 30*86400, ToAt: now - 20*86400, ObservationDays: 7, Currency: "USD"}
	user := User{Username: "cohort-cost", AffCode: "cohort-cost", CreatedAt: now - 25*86400}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&AcquisitionAccount{UserID: user.Id, RegistrationLinkID: link.ID, CreatedAt: now}).Error)
	outside := User{Username: "other-cohort", AffCode: "other-cohort", CreatedAt: now - 2*86400}
	require.NoError(t, db.Create(&outside).Error)
	require.NoError(t, db.Create(&AcquisitionAccount{UserID: outside.Id, RegistrationLinkID: link.ID, CreatedAt: now}).Error)
	for _, payment := range []TopUp{
		{UserId: user.Id, TradeNo: "cost-cash", PaymentMethod: "stripe", PaymentProvider: "stripe", SettledAmountMicros: 1_000_000, SettlementCurrency: "USD", CompleteTime: user.CreatedAt + 86400},
		{UserId: user.Id, TradeNo: "cost-repeat", PaymentMethod: "stripe", PaymentProvider: "stripe", SettledAmountMicros: 2_000_000, SettlementCurrency: "CNY", CompleteTime: user.CreatedAt + 2*86400},
		{UserId: outside.Id, TradeNo: "cost-outside", PaymentMethod: "stripe", PaymentProvider: "stripe", SettledAmountMicros: 1_000_000, SettlementCurrency: "USD", CompleteTime: now - 86400},
	} {
		payment.Status = common.TopUpStatusSuccess
		require.NoError(t, db.Create(&payment).Error)
	}
	report, err := GetAcquisitionCostReport(ctx, scope)
	require.NoError(t, err)
	require.Nil(t, report.Spend)
	require.Nil(t, report.CostPerRegistrationMicros)
	require.Nil(t, report.CostPerFirstPayerMicros)
	require.EqualValues(t, 1, report.Registrations)
	require.EqualValues(t, 1, report.FirstPayingAccounts)
	require.False(t, report.Observing)
	_, err = SaveAcquisitionCost(ctx, scope, 4_000_000, 1)
	require.NoError(t, err)
	report, err = GetAcquisitionCostReport(ctx, scope)
	require.NoError(t, err)
	require.Equal(t, float64(4_000_000), *report.CostPerFirstPayerMicros)
	_, err = SaveAcquisitionCost(ctx, scope, 0, 1)
	require.NoError(t, err)
	report, err = GetAcquisitionCostReport(ctx, scope)
	require.NoError(t, err)
	require.NotNil(t, report.Spend)
	require.Equal(t, float64(0), *report.CostPerRegistrationMicros)
	var count int64
	require.NoError(t, db.Model(&AcquisitionCost{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	// Different currencies never silently share a spend record.
	otherScope := scope
	otherScope.Currency = "CNY"
	report, err = GetAcquisitionCostReport(ctx, otherScope)
	require.NoError(t, err)
	require.Nil(t, report.Spend)
	// An unknown successful settlement blocks a definitive payer cost.
	missing := TopUp{UserId: user.Id, TradeNo: "unknown-cost", Status: common.TopUpStatusSuccess, CompleteTime: 0, PaymentProvider: "stripe"}
	require.NoError(t, db.Create(&missing).Error)
	report, err = GetAcquisitionCostReport(ctx, scope)
	require.NoError(t, err)
	require.EqualValues(t, 1, report.PaymentRowsUnclassified)
	require.Nil(t, report.CostPerFirstPayerMicros)
	observing := scope
	observing.ToAt = now
	report, err = GetAcquisitionCostReport(ctx, observing)
	require.NoError(t, err)
	require.True(t, report.Observing)
	require.Nil(t, report.CostPerFirstPayerMicros)
	_, err = SaveAcquisitionCost(ctx, scope, -1, 1)
	require.ErrorIs(t, err, ErrAcquisitionInvalid)
}

func TestAcquisitionCostExcludesGiftsAndPaymentsOutsideObservation(t *testing.T) {
	db := acquisitionDB(t)
	require.NoError(t, db.AutoMigrate(&AcquisitionCost{}))
	ctx := context.Background()
	now := time.Now().Unix()
	link, err := SaveAcquisitionLink(ctx, AcquisitionLink{Name: "Cohort", Source: "community", Target: "/"})
	require.NoError(t, err)
	scope := AcquisitionCostScope{LinkID: link.ID, FromAt: now - 30*86400, ToAt: now - 20*86400, ObservationDays: 7, Currency: "USD"}
	user := User{Username: "gift-only", AffCode: "gift-only", CreatedAt: now - 25*86400}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&AcquisitionAccount{UserID: user.Id, RegistrationLinkID: link.ID, CreatedAt: now}).Error)
	for _, payment := range []TopUp{
		{UserId: user.Id, TradeNo: "gift-source", Status: "success", PaymentMethod: "gift", SettledAmountMicros: 1_000_000, SettlementCurrency: "USD", CompleteTime: user.CreatedAt + 86400},
		{UserId: user.Id, TradeNo: "late-source", Status: "success", PaymentMethod: "stripe", SettledAmountMicros: 1_000_000, SettlementCurrency: "USD", CompleteTime: user.CreatedAt + 8*86400},
	} {
		require.NoError(t, db.Create(&payment).Error)
	}
	_, err = SaveAcquisitionCost(ctx, scope, 1_000_000, 1)
	require.NoError(t, err)
	report, err := GetAcquisitionCostReport(ctx, scope)
	require.NoError(t, err)
	require.EqualValues(t, 1, report.Registrations)
	require.Zero(t, report.FirstPayingAccounts)
	require.Nil(t, report.CostPerFirstPayerMicros)
	other, err := SaveAcquisitionLink(ctx, AcquisitionLink{Name: "Different content", Source: "community", Target: "/"})
	require.NoError(t, err)
	scope.LinkID = other.ID
	report, err = GetAcquisitionCostReport(ctx, scope)
	require.NoError(t, err)
	require.Nil(t, report.Spend)
	require.Zero(t, report.Registrations)
}
