package model

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

func acquisitionDB(t *testing.T) *gorm.DB {
	t.Helper()
	old := DB
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:acquisition-%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	require.NoError(t, db.AutoMigrate(&User{}, &AcquisitionLink{}, &AcquisitionVisitor{}, &AcquisitionVisit{}, &AcquisitionAccount{}, &AcquisitionConfig{}, &AcquisitionActivity{}, &AcquisitionActivityState{}, &AcquisitionConsent{}, &AcquisitionSelfReport{}, &AcquisitionActivityGap{}, &FinanceLedgerEntry{}, &TopUp{}, &SubscriptionOrder{}, &SubscriptionPaymentEvent{}))
	t.Cleanup(func() { DB = old; sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	return db
}
func TestAcquisitionSanitizesURLsAndSeparatesEvidence(t *testing.T) {
	require.Equal(t, "社区教程推广活动的中文名称可以完整保留", AcquisitionLabel("社区教程推广活动的中文名称可以完整保留"))
	input := AcquisitionInput{Consent: true, Nonce: strings.Repeat("a", 32), Landing: "/guide?api_key=SECRET#private", Referrer: "https://github.com/org/repo?token=SECRET", Source: "community", Campaign: "readme"}
	visit, err := NormalizeAcquisition(input, []string{"api.lmm.best"}, 123)
	require.NoError(t, err)
	encoded, _ := json.Marshal(visit)
	require.NotContains(t, string(encoded), "SECRET")
	require.Equal(t, "/guide", visit.Landing)
	require.Equal(t, "github.com", visit.ReferrerHost)
	require.Equal(t, "campaign_parameters", visit.Evidence)
	input.Source = "sk-secret"
	input.Referrer = "https://api.lmm.best/wallet"
	visit, err = NormalizeAcquisition(input, []string{"api.lmm.best"}, 123)
	require.NoError(t, err)
	require.Equal(t, "unknown", visit.Source)
	input.Landing = "/oauth/github?code=private"
	_, err = NormalizeAcquisition(input, nil, 123)
	require.Error(t, err)
	input.Landing = "/"
	input.Test = true
	_, err = NormalizeAcquisition(input, nil, 123)
	require.Error(t, err)
}
func TestAcquisitionRegistrationIsIdempotentAndDoesNotTransferAccounts(t *testing.T) {
	db := acquisitionDB(t)
	ctx := context.Background()
	visitor := AcquisitionVisitorHash("visitor")
	now := time.Now().Unix()
	first := User{Username: "one", AffCode: "one", CreatedAt: now}
	second := User{Username: "two", AffCode: "two", CreatedAt: now}
	require.NoError(t, db.Create(&first).Error)
	require.NoError(t, db.Create(&second).Error)
	link, err := SaveAcquisitionLink(ctx, AcquisitionLink{Name: "Readme", Source: "github", Campaign: "launch", Target: "/guide"})
	require.NoError(t, err)
	input := AcquisitionInput{Consent: true, Nonce: strings.Repeat("b", 32), Landing: "/guide", LinkID: link.ID, Referrer: "https://forum.example/post/5"}
	a, err := ObserveAcquisition(ctx, visitor, 0, input, nil)
	require.NoError(t, err)
	b, err := ObserveAcquisition(ctx, visitor, 0, input, nil)
	require.NoError(t, err)
	require.Equal(t, a.ID, b.ID)
	require.NoError(t, AttributeAcquisitionRegistration(ctx, first.Id, visitor))
	require.NoError(t, AttributeAcquisitionRegistration(ctx, first.Id, visitor))
	var account AcquisitionAccount
	require.NoError(t, db.First(&account, first.Id).Error)
	require.Equal(t, "github", account.RegistrationSource)
	require.Equal(t, "promotion_link", account.RegistrationEvidence)
	require.Equal(t, "forum.example", a.ReferrerHost)
	require.NoError(t, AttributeAcquisitionRegistration(ctx, second.Id, visitor))
	var other AcquisitionAccount
	require.NoError(t, db.First(&other, second.Id).Error)
	require.Equal(t, "unknown", other.RegistrationSource)
	_, err = ObserveAcquisition(ctx, visitor, second.Id, input, nil)
	require.Error(t, err)
	link.Source = "unrelated"
	link.Name = "Renamed"
	saved, err := SaveAcquisitionLink(ctx, link)
	require.NoError(t, err)
	require.Equal(t, "github", saved.Source)
}
func TestAcquisitionReportSeparatesCurrenciesAndExcludesGifts(t *testing.T) {
	db := acquisitionDB(t)
	now := time.Now().Unix()
	u := User{Username: "payer", AffCode: "payer", CreatedAt: now - 100}
	require.NoError(t, db.Create(&u).Error)
	require.NoError(t, db.Create(&AcquisitionAccount{UserID: u.Id, RegistrationSource: "github", RegistrationAt: u.CreatedAt, CreatedAt: now}).Error)

	payment := TopUp{UserId: u.Id, TradeNo: "cash", Status: "success", SettledAmountMicros: 10_000_000, SettlementCurrency: "USD", CompleteTime: now - 10, PaymentProvider: "stripe", PaymentMethod: "stripe"}
	require.NoError(t, db.Create(&payment).Error)
	refund := FinanceLedgerEntry{UserId: &u.Id, EntryType: FinanceEntryRevenue, SourceType: FinanceSourceRefund, AmountMicros: 2_000_000, Direction: -1, Currency: "USD", OccurredAt: now - 5, IdempotencyKey: "refund", PaymentMethod: "stripe", PaymentProvider: "stripe"}
	require.NoError(t, db.Create(&refund).Error)
	gift := payment
	gift.Id = 0
	gift.TradeNo = "gift"
	gift.PaymentMethod = "bounty"
	gift.SettledAmountMicros = 99_000_000
	require.NoError(t, db.Create(&gift).Error)
	report, err := GetAcquisitionReport(context.Background(), now-1000, now+1)
	require.NoError(t, err)
	require.Len(t, report.Payments, 1)
	require.Equal(t, int64(10_000_000), report.Payments[0].PaidMicros)
	require.Equal(t, int64(2_000_000), report.Payments[0].RefundMicros)
	require.Equal(t, int64(8_000_000), report.Payments[0].NetMicros)
	require.Equal(t, int64(1), report.Channels[0].Registrations)
}

func TestAcquisitionLookbackExpiryAndCleanup(t *testing.T) {
	db := acquisitionDB(t)
	ctx := context.Background()
	now := time.Now().Unix()
	user := User{Username: "late", AffCode: "late", CreatedAt: now}
	require.NoError(t, db.Create(&user).Error)
	visitor := AcquisitionVisitorHash("old")
	visit, err := ObserveAcquisition(ctx, visitor, 0, AcquisitionInput{Consent: true, Nonce: strings.Repeat("c", 32), Source: "old-campaign", Landing: "/"}, nil)
	require.NoError(t, err)
	require.NoError(t, db.Model(&visit).Update("created_at", now-31*86400).Error)
	require.NoError(t, AttributeAcquisitionRegistration(ctx, user.Id, visitor))
	var account AcquisitionAccount
	require.NoError(t, db.First(&account, user.Id).Error)
	require.Equal(t, "unknown", account.RegistrationSource)
	require.Equal(t, "old-campaign", account.FirstSource)
	require.NoError(t, db.Model(&visit).Update("created_at", now-91*86400).Error)
	require.NoError(t, PurgeAcquisition(ctx))
	var count int64
	require.NoError(t, db.Model(&AcquisitionVisit{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.First(&account, user.Id).Error)
	require.Equal(t, "old-campaign", account.FirstSource)
}

func TestAcquisitionCleanupPreservesOwnerForRetainedVisits(t *testing.T) {
	db := acquisitionDB(t)
	now := time.Now().Unix()
	visitor := AcquisitionVisitor{ID: AcquisitionVisitorHash("owned"), UserID: 1, CreatedAt: now - 91*86400}
	require.NoError(t, db.Create(&visitor).Error)
	require.NoError(t, db.Create(&AcquisitionVisit{VisitorID: visitor.ID, Nonce: strings.Repeat("d", 32), Source: "github", CreatedAt: now}).Error)
	require.NoError(t, PurgeAcquisition(context.Background()))
	var kept AcquisitionVisitor
	require.NoError(t, db.First(&kept, "id = ?", visitor.ID).Error)
	require.Equal(t, 1, kept.UserID)
	_, err := ObserveAcquisition(context.Background(), visitor.ID, 2, AcquisitionInput{Consent: true, Nonce: strings.Repeat("e", 32), Landing: "/", Source: "forum"}, nil)
	require.Error(t, err)
}

func TestAcquisitionPaymentsExcludeSubscriptionMirrorsAndKeepRenewals(t *testing.T) {
	db := acquisitionDB(t)
	now := time.Now().Unix()
	u := User{Username: "renewal", AffCode: "renewal", CreatedAt: now - 100}
	require.NoError(t, db.Create(&u).Error)
	require.NoError(t, db.Create(&AcquisitionAccount{UserID: u.Id, RegistrationSource: "github", CreatedAt: now}).Error)
	order := SubscriptionOrder{UserId: u.Id, TradeNo: "sub", Status: "success", ExpectedAmountMicros: 5_000_000, SettlementCurrency: "USD", PaymentProvider: "stripe", PaymentMethod: "stripe", CompleteTime: now - 10}
	require.NoError(t, db.Create(&order).Error)
	mirror := TopUp{UserId: u.Id, TradeNo: "sub", Status: "success", SettledAmountMicros: 5_000_000, SettlementCurrency: "USD", PaymentProvider: "stripe", PaymentMethod: "stripe", CompleteTime: now - 10}
	require.NoError(t, db.Create(&mirror).Error)
	for i := 1; i <= 2; i++ {
		event := SubscriptionPaymentEvent{SubscriptionOrderId: order.Id, PaymentProvider: "stripe", ProviderEventId: fmt.Sprint("e", i), ProviderTransactionId: fmt.Sprint("t", i), SettlementCurrency: "USD", SettlementAmountMicros: 5_000_000, PeriodStart: int64(i), PeriodEnd: int64(i + 1), CreatedTime: now - 10 + int64(i)}
		require.NoError(t, db.Create(&event).Error)
	}
	cny := TopUp{UserId: u.Id, TradeNo: "cny", Status: "success", SettledAmountMicros: 7_000_000, SettlementCurrency: "CNY", PaymentProvider: "epay", PaymentMethod: "alipay", CompleteTime: now - 3}
	require.NoError(t, db.Create(&cny).Error)
	unknown := cny
	unknown.Id = 0
	unknown.TradeNo = "legacy"
	unknown.SettlementCurrency = ""
	require.NoError(t, db.Create(&unknown).Error)
	report, err := GetAcquisitionReport(context.Background(), now-1000, now+1)
	require.NoError(t, err)
	require.Len(t, report.Payments, 2)
	require.Equal(t, "CNY", report.Payments[0].Currency)
	require.Equal(t, int64(7_000_000), report.Payments[0].PaidMicros)
	require.Equal(t, "USD", report.Payments[1].Currency)
	require.Equal(t, int64(10_000_000), report.Payments[1].PaidMicros)
	require.Equal(t, int64(1), report.Payments[1].PayingAccounts)
	require.Equal(t, int64(1), report.UnclassifiedPaymentRows)
}
