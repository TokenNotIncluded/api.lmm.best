package model

import (
	"context"
	"fmt"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

// Exercise the real risk SQL, including its nested top-up aggregate joins.
// A display projection without audit evidence must not erase paid funding.
func TestAdminUserTopupRawFundingAndWalletRiskBeforePagination(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	require.NoError(t, db.AutoMigrate(&WalletTransfer{}, &Checkin{}))
	require.True(t, db.Migrator().HasTable(&WalletTransfer{}))
	require.True(t, db.Migrator().HasTable(&Checkin{}))
	require.False(t, db.Migrator().HasTable("wallet_credit_rebases"))
	var users []*User
	paid := map[int]bool{1: true, 3: true, 5: true, 7: true}
	checkin := map[int]bool{1: true, 2: true, 3: true, 4: true, 7: true, 8: true}
	sent := map[int]bool{1: true, 2: true, 7: true, 8: true}
	for id := 1; id <= 8; id++ {
		user := &User{Id: id, Username: fmt.Sprintf("aggregate-regression-%d", id),
			AffCode: fmt.Sprintf("aggregate-regression-%d", id), Role: common.RoleCommonUser,
			Status: common.UserStatusEnabled, Quota: 1_000}
		if id == 5 {
			user.UsedQuota, user.RequestCount = 10, 1
		}
		require.NoError(t, db.Create(user).Error)
		users = append(users, user)
		if paid[id] {
			require.NoError(t, db.Create(&TopUp{UserId: id, TradeNo: fmt.Sprintf("aggregate-paid-%d", id),
				Amount: 10, CreditedQuota: 5_000_000, Money: 10,
				ExpectedAmountMicros: 10_000_000, SettledAmountMicros: 10_000_000,
				SettlementCurrency: "USD", PaymentProvider: PaymentProviderStripe,
				PaymentMethod: PaymentMethodStripe, Status: common.TopUpStatusSuccess,
				CreateTime: 100, CompleteTime: 200}).Error)
		}
		if checkin[id] {
			require.NoError(t, db.Create(&Checkin{UserId: id, CheckinDate: "2026-10-01", QuotaAwarded: 1_000}).Error)
		}
		if sent[id] {
			transfer := WalletTransfer{SenderID: id, Quota: 300, Status: "pending",
				RequestKey: fmt.Sprintf("aggregate-request-%d", id), Token: fmt.Sprintf("aggregate-token-%d", id)}
			if id == 8 {
				transfer.Status, transfer.RecipientID = "claimed", 6
			}
			require.NoError(t, db.Create(&transfer).Error)
		}
	}
	require.NoError(t, db.Create(&WalletTransfer{SenderID: 4, Quota: 300, Status: "cancelled",
		RequestKey: "aggregate-cancelled-4", Token: "aggregate-cancelled-token-4"}).Error)
	require.NoError(t, PopulateUserTopups(users))
	for _, user := range users {
		require.NotNil(t, user.TopupSummary)
		if paid[user.Id] {
			require.EqualValues(t, 5_000_000, user.TopupSummary.Quota)
			require.False(t, user.TopupSummary.QuotaProjectionAvailable)
			require.Zero(t, user.TopupSummary.NormalizedQuota)
		} else {
			require.Zero(t, user.TopupSummary.Quota)
		}
	}
	require.NoError(t, PopulateUserWalletRiskContext(context.Background(), users))
	for _, user := range users {
		require.NotNil(t, user.WalletRisk)
		want := map[int]float64{1: .35, 2: .95, 3: 0, 4: .25, 5: 0, 6: .855, 7: .35, 8: .95}[user.Id]
		require.InDelta(t, want, user.WalletRisk.Score, .000001, "user %d", user.Id)
		if sent[user.Id] && user.Id != 8 {
			require.EqualValues(t, 300, user.WalletRisk.PendingQuota)
		}
		if user.Id == 6 {
			require.EqualValues(t, 300, user.WalletRisk.ReceivedQuota)
			require.EqualValues(t, 1, user.WalletRisk.HighRiskSenders)
		}
		if user.Id == 4 {
			require.Zero(t, user.WalletRisk.TransferredQuota, "cancelled transfer must not raise risk")
		}
	}
	high, low := .8, .4
	for _, test := range []struct {
		name    string
		sort    string
		order   string
		filters UserListFilters
		want    []int
	}{
		{"default list", "", "", UserListFilters{}, []int{8, 7, 6, 5, 4, 3, 2, 1}},
		{"id list", "id", "asc", UserListFilters{}, []int{1, 2, 3, 4, 5, 6, 7, 8}},
		{"paid", "id", "asc", UserListFilters{Funding: "paid"}, []int{1, 3, 5, 7}},
		{"unpaid", "id", "asc", UserListFilters{Funding: "unpaid"}, []int{2, 4, 6, 8}},
		{"risk sort", "risk_score", "desc", UserListFilters{}, []int{8, 2, 6, 7, 1, 4, 5, 3}},
		{"paid risk sort", "risk_score", "desc", UserListFilters{Funding: "paid"}, []int{7, 1, 5, 3}},
		{"unpaid risk sort", "risk_score", "desc", UserListFilters{Funding: "unpaid"}, []int{8, 2, 6, 4}},
		{"high risk", "id", "asc", UserListFilters{RiskMin: &high}, []int{2, 6, 8}},
		{"paid high risk empty", "id", "asc", UserListFilters{Funding: "paid", RiskMin: &high}, []int{}},
		{"unpaid high risk", "id", "asc", UserListFilters{Funding: "unpaid", RiskMin: &high}, []int{2, 6, 8}},
		{"paid low risk", "id", "asc", UserListFilters{Funding: "paid", RiskMax: &low}, []int{1, 3, 5, 7}},
		{"unpaid low risk", "id", "asc", UserListFilters{Funding: "unpaid", RiskMax: &low}, []int{4}},
		{"paid sender", "id", "asc", UserListFilters{Funding: "paid", Transfers: "sent"}, []int{1, 7}},
		{"unpaid sender", "id", "asc", UserListFilters{Funding: "unpaid", Transfers: "sent"}, []int{2, 8}},
		{"paid checkin", "id", "asc", UserListFilters{Funding: "paid", Checkin: "yes"}, []int{1, 3, 7}},
		{"unpaid checkin", "id", "asc", UserListFilters{Funding: "unpaid", Checkin: "yes"}, []int{2, 4, 8}},
	} {
		t.Run(test.name, func(t *testing.T) {
			options := NewUserSortOptions(test.sort, test.order)
			options.Filters = test.filters
			for _, list := range []struct {
				name string
				page func(int) ([]*User, int64, error)
			}{
				{"list", func(offset int) ([]*User, int64, error) {
					return GetAllUsers(&common.PageInfo{Page: offset + 1, PageSize: 1}, false, options)
				}},
				{"search", func(offset int) ([]*User, int64, error) {
					return SearchUsers("aggregate-regression", "", nil, nil, false, offset, 1, options)
				}},
			} {
				t.Run(list.name, func(t *testing.T) {
					for offset := 0; offset <= len(test.want); offset++ {
						page, total, err := list.page(offset)
						require.NoError(t, err)
						require.EqualValues(t, len(test.want), total, "total must precede pagination")
						// Match controller enrichment order even when the list query
						// itself has no risk sort/filter and skips the risk joins.
						require.NoError(t, PopulateUserTopups(page))
						require.NoError(t, PopulateUserWalletRiskContext(context.Background(), page))
						if offset == len(test.want) {
							require.Empty(t, page)
						} else {
							require.Equal(t, []int{test.want[offset]}, collectUserIDs(page))
							require.NotNil(t, page[0].WalletRisk)
							if paid[page[0].Id] {
								require.EqualValues(t, 5_000_000, page[0].TopupSummary.Quota)
								require.False(t, page[0].TopupSummary.QuotaProjectionAvailable)
								require.Zero(t, page[0].TopupSummary.NormalizedQuota)
								if sent[page[0].Id] {
									require.InDelta(t, .35, page[0].WalletRisk.Score, .000001)
								}
							}
						}
					}
				})
			}
		})
	}
}
