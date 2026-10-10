package model

import (
	"context"
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestUserWalletRiskLifecycleAndGlobalSorting(t *testing.T) {
	installPaidPolicyCurrencyFixture(t, common.QuotaPerUnit)
	sender, recipient := transferUsers(t)
	require.NoError(t, DB.AutoMigrate(&Checkin{}))
	require.NoError(t, DB.Exec("DELETE FROM checkins").Error)
	t.Cleanup(func() { DB.Exec("DELETE FROM checkins"); DB.Exec("DELETE FROM wallet_transfers") })
	require.NoError(t, DB.Create(&Checkin{UserId: sender.Id, CheckinDate: "2026-10-01", QuotaAwarded: 1000}).Error)
	transfer, err := CreateWalletTransfer(sender.Id, 300, "risk-test-request-1")
	require.NoError(t, err)
	users := []*User{&sender, &recipient}
	require.NoError(t, PopulateUserWalletRiskContext(context.Background(), users))
	require.Equal(t, .95, sender.WalletRisk.Score)
	require.True(t, sender.WalletRisk.HighRisk)
	require.EqualValues(t, 300, sender.WalletRisk.PendingQuota)
	require.Zero(t, recipient.WalletRisk.Score)
	_, err = ClaimWalletTransfer(transfer.Token, recipient.Id)
	require.NoError(t, err)
	require.NoError(t, PopulateUserWalletRiskContext(context.Background(), users))
	require.InDelta(t, .855, recipient.WalletRisk.Score, .000001)
	require.True(t, recipient.WalletRisk.HighRisk)
	require.EqualValues(t, 1, recipient.WalletRisk.HighRiskSenders)
	require.EqualValues(t, 300, recipient.WalletRisk.ReceivedQuota)
	require.Zero(t, sender.WalletRisk.PendingQuota)
	page, total, err := GetAllUsers(&common.PageInfo{Page: 1, PageSize: 1}, false, NewUserSortOptions("risk_score", "desc"))
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.Equal(t, sender.Id, page[0].Id)
	page, _, err = GetAllUsers(&common.PageInfo{Page: 2, PageSize: 1}, false, NewUserSortOptions("risk_score", "desc"))
	require.NoError(t, err)
	require.Equal(t, recipient.Id, page[0].Id)
	ascending, _, err := GetAllUsers(&common.PageInfo{Page: 1, PageSize: 1}, false, NewUserSortOptions("risk_score", "asc"))
	require.NoError(t, err)
	require.Equal(t, recipient.Id, ascending[0].Id)
	high := .8
	options := NewUserSortOptions("transferred_quota", "desc")
	options.Filters.RiskMin = &high
	_, total, err = SearchUsers("", "", nil, nil, false, 0, 20, options)
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", sender.Id).Update("used_quota", 200).Error)
	require.NoError(t, PopulateUserWalletRiskContext(context.Background(), users))
	require.Zero(t, sender.WalletRisk.Score)
	require.Zero(t, recipient.WalletRisk.Score)
}

func TestUserWalletRiskCancelledTransfersAndNoRecursivePropagation(t *testing.T) {
	sender, recipient := transferUsers(t)
	require.NoError(t, DB.AutoMigrate(&Checkin{}))
	require.NoError(t, DB.Exec("DELETE FROM checkins").Error)
	t.Cleanup(func() { DB.Exec("DELETE FROM checkins"); DB.Exec("DELETE FROM wallet_transfers") })
	require.NoError(t, DB.Create(&Checkin{UserId: sender.Id, CheckinDate: "2026-10-01", QuotaAwarded: 1000}).Error)
	transfer, err := CreateWalletTransfer(sender.Id, 100, "risk-test-request-2")
	require.NoError(t, err)
	require.NoError(t, CancelWalletTransfer(transfer.Id, sender.Id))
	require.NoError(t, PopulateUserWalletRiskContext(context.Background(), []*User{&sender}))
	require.Equal(t, .25, sender.WalletRisk.Score)
	require.Zero(t, sender.WalletRisk.TransferredQuota)
	transfer, err = CreateWalletTransfer(sender.Id, 100, "risk-test-request-3")
	require.NoError(t, err)
	_, err = ClaimWalletTransfer(transfer.Token, recipient.Id)
	require.NoError(t, err)
	third := User{Username: "risk-third", AffCode: "risk-third", Quota: 0, Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&third).Error)
	forwarded, err := CreateWalletTransfer(recipient.Id, 100, "risk-test-request-4")
	require.NoError(t, err)
	_, err = ClaimWalletTransfer(forwarded.Token, third.Id)
	require.NoError(t, err)
	require.NoError(t, PopulateUserWalletRiskContext(context.Background(), []*User{&recipient, &third}))
	require.True(t, recipient.WalletRisk.HighRisk)
	require.Zero(t, third.WalletRisk.Score)
	options := NewUserSortOptions("id", "asc")
	options.Filters.Transfers = "none"
	_, total, err := GetAllUsers(&common.PageInfo{Page: 1, PageSize: 20}, false, options)
	require.NoError(t, err)
	require.Zero(t, total)
}

func TestUserWalletRiskPostgres(t *testing.T) {
	db := openIsolatedPostgresCacheTestDB(t, &User{}, &WalletTransfer{}, &Checkin{}, &TopUp{})
	previousDB, previousLog := DB, LOG_DB
	DB, LOG_DB = db, db
	t.Cleanup(func() { DB, LOG_DB = previousDB, previousLog })
	usePostgresDatabaseType(t)
	t.Run("lifecycle-sort-filter", TestUserWalletRiskLifecycleAndGlobalSorting)
	t.Run("cancel-association", TestUserWalletRiskCancelledTransfersAndNoRecursivePropagation)
}
