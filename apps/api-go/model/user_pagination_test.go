package model

import (
	"fmt"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func insertUsersForPaginationTest(t *testing.T, total int) {
	t.Helper()
	for id := 1; id <= total; id++ {
		user := &User{
			Id:          id,
			Username:    fmt.Sprintf("user%02d", id),
			Password:    "password123",
			DisplayName: fmt.Sprintf("User %02d", id),
			Email:       fmt.Sprintf("user%02d@example.com", id),
			Role:        common.RoleCommonUser,
			Status:      common.UserStatusEnabled,
			Group:       "default",
			AffCode:     fmt.Sprintf("aff%02d", id),
		}
		require.NoError(t, DB.Create(user).Error)
	}
}

func collectUserIDs(users []*User) []int {
	ids := make([]int, 0, len(users))
	for _, user := range users {
		ids = append(ids, user.Id)
	}
	return ids
}

func TestGetAllUsersSortsBeforePagination(t *testing.T) {
	installPaidPolicyCurrencyFixture(t, common.QuotaPerUnit)
	truncateTables(t)
	insertUsersForPaginationTest(t, 42)

	pageOne, total, err := GetAllUsers(&common.PageInfo{Page: 1, PageSize: 20}, false, NewUserSortOptions("id", "asc"))
	require.NoError(t, err)
	assert.Equal(t, int64(42), total)
	assert.Equal(t, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}, collectUserIDs(pageOne))

	pageTwo, total, err := GetAllUsers(&common.PageInfo{Page: 2, PageSize: 20}, false, NewUserSortOptions("id", "asc"))
	require.NoError(t, err)
	assert.Equal(t, int64(42), total)
	assert.Equal(t, []int{21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40}, collectUserIDs(pageTwo))

	pageThree, total, err := GetAllUsers(&common.PageInfo{Page: 3, PageSize: 20}, false, NewUserSortOptions("id", "asc"))
	require.NoError(t, err)
	assert.Equal(t, int64(42), total)
	assert.Equal(t, []int{41, 42}, collectUserIDs(pageThree))
}

func TestSearchUsersSortsBeforePagination(t *testing.T) {
	installPaidPolicyCurrencyFixture(t, common.QuotaPerUnit)
	truncateTables(t)
	insertUsersForPaginationTest(t, 42)

	users, total, err := SearchUsers("user", "", nil, nil, false, 20, 20, NewUserSortOptions("id", "asc"))
	require.NoError(t, err)
	assert.Equal(t, int64(42), total)
	assert.Equal(t, []int{21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40}, collectUserIDs(users))
}

func TestAdminUserTopupMoneySortsBeforePagination(t *testing.T) {
	installPaidPolicyCurrencyFixture(t, common.QuotaPerUnit)
	truncateTables(t)
	insertUsersForPaginationTest(t, 8)

	fixtures := []TopUp{
		{
			UserId:               1,
			TradeNo:              "user-topup-money-legacy",
			Amount:               1,
			CreditedQuota:        int64(common.QuotaPerUnit),
			Money:                999,
			ExpectedAmountMicros: 999_000_000,
			SettlementCurrency:   "USD",
			Status:               common.TopUpStatusSuccess,
			PaymentMethod:        PaymentMethodStripe,
			PaymentProvider:      PaymentProviderStripe,
		},
		{
			UserId:              2,
			TradeNo:             "user-topup-money-unknown-currency",
			Amount:              1,
			CreditedQuota:       int64(common.QuotaPerUnit),
			Money:               999,
			SettledAmountMicros: 900_000_000,
			Status:              common.TopUpStatusSuccess,
			PaymentMethod:       PaymentMethodStripe,
			PaymentProvider:     PaymentProviderStripe,
		},
		{
			UserId:              3,
			TradeNo:             "user-topup-money-cny-high",
			Amount:              1,
			CreditedQuota:       int64(common.QuotaPerUnit),
			Money:               999,
			SettledAmountMicros: 8_000_000,
			SettlementCurrency:  "CNY",
			Status:              common.TopUpStatusSuccess,
			PaymentMethod:       PaymentMethodStripe,
			PaymentProvider:     PaymentProviderStripe,
		},
		{
			UserId:              4,
			TradeNo:             "user-topup-money-multiple-usd",
			Amount:              1,
			CreditedQuota:       int64(common.QuotaPerUnit),
			SettledAmountMicros: 500_000_000,
			SettlementCurrency:  "USD",
			Status:              common.TopUpStatusSuccess,
			PaymentMethod:       PaymentMethodStripe,
			PaymentProvider:     PaymentProviderStripe,
		},
		{
			UserId:              4,
			TradeNo:             "user-topup-money-multiple-cny",
			Amount:              1,
			CreditedQuota:       int64(common.QuotaPerUnit),
			SettledAmountMicros: 500_000_000,
			SettlementCurrency:  "CNY",
			Status:              common.TopUpStatusSuccess,
			PaymentMethod:       PaymentMethodStripe,
			PaymentProvider:     PaymentProviderStripe,
		},
		{
			UserId:              5,
			TradeNo:             "user-topup-money-cny-low",
			Amount:              1,
			CreditedQuota:       int64(common.QuotaPerUnit),
			Money:               999,
			SettledAmountMicros: 1_000_000,
			SettlementCurrency:  "CNY",
			Status:              common.TopUpStatusSuccess,
			PaymentMethod:       PaymentMethodStripe,
			PaymentProvider:     PaymentProviderStripe,
		},
		{
			UserId:              6,
			TradeNo:             "user-topup-money-usd-low",
			Amount:              1,
			CreditedQuota:       int64(common.QuotaPerUnit),
			Money:               999,
			SettledAmountMicros: 5_000_000,
			SettlementCurrency:  "USD",
			Status:              common.TopUpStatusSuccess,
			PaymentMethod:       PaymentMethodStripe,
			PaymentProvider:     PaymentProviderStripe,
		},
		{
			UserId:              8,
			TradeNo:             "user-topup-money-usd-high-first",
			Amount:              1,
			CreditedQuota:       int64(common.QuotaPerUnit),
			Money:               999,
			SettledAmountMicros: 60_000_000,
			SettlementCurrency:  " usd ",
			Status:              common.TopUpStatusSuccess,
			PaymentMethod:       PaymentMethodStripe,
			PaymentProvider:     PaymentProviderStripe,
		},
		{
			UserId:              8,
			TradeNo:             "user-topup-money-usd-high-second",
			Amount:              1,
			CreditedQuota:       int64(common.QuotaPerUnit),
			Money:               999,
			SettledAmountMicros: 40_000_000,
			SettlementCurrency:  "USD",
			Status:              common.TopUpStatusSuccess,
			PaymentMethod:       PaymentMethodWaffo,
			PaymentProvider:     PaymentProviderWaffo,
		},
	}
	require.NoError(t, DB.Create(&fixtures).Error)

	for _, test := range []struct {
		order string
		known []int
	}{
		{order: "asc", known: []int{5, 3, 6, 8}},
		{order: "desc", known: []int{3, 5, 8, 6}},
	} {
		t.Run(test.order, func(t *testing.T) {
			sort := NewUserSortOptions("topup_money", test.order)
			for _, list := range []struct {
				name string
				page func(int) ([]*User, int64, error)
			}{
				{
					name: "all",
					page: func(page int) ([]*User, int64, error) {
						return GetAllUsers(&common.PageInfo{Page: page, PageSize: 2}, false, sort)
					},
				},
				{
					name: "search",
					page: func(page int) ([]*User, int64, error) {
						return SearchUsers("user", "", nil, nil, false, (page-1)*2, 2, sort)
					},
				},
			} {
				t.Run(list.name, func(t *testing.T) {
					var ordered []*User
					for page := 1; page <= 4; page++ {
						users, total, err := list.page(page)
						require.NoError(t, err)
						assert.Equal(t, int64(8), total)
						require.Len(t, users, 2)
						if page <= 2 {
							assert.Equal(t, test.known[(page-1)*2:page*2], collectUserIDs(users))
						}
						ordered = append(ordered, users...)
					}
					assert.ElementsMatch(t, []int{1, 2, 4, 7}, collectUserIDs(ordered[4:]))
					require.NoError(t, PopulateUserTopups(ordered))
					for _, user := range ordered {
						require.NotNil(t, user.TopupSummary)
						switch user.Id {
						case 1:
							assert.EqualValues(t, 999_000_000, user.TopupSummary.MoneyMicros)
							assert.Zero(t, user.TopupSummary.SettledMoneyMicros)
							assert.EqualValues(t, 999_000_000, user.TopupSummary.HistoricalMoneyMicros)
							assert.Equal(t, "historical", user.TopupSummary.PaymentBasis)
						case 2:
							assert.EqualValues(t, 900_000_000, user.TopupSummary.MoneyMicros)
							assert.Zero(t, user.TopupSummary.SettledMoneyMicros)
							assert.Equal(t, "UNKNOWN", user.TopupSummary.Currency)
							assert.Equal(t, "settled", user.TopupSummary.PaymentBasis)
							require.Len(t, user.TopupSummary.Methods, 1)
							assert.EqualValues(t, 900_000_000, user.TopupSummary.Methods[0].SettledMoneyMicros)
						case 3:
							assert.EqualValues(t, 8_000_000, user.TopupSummary.MoneyMicros)
							assert.Equal(t, "CNY", user.TopupSummary.Currency)
						case 5:
							assert.EqualValues(t, 1_000_000, user.TopupSummary.MoneyMicros)
							assert.Equal(t, "CNY", user.TopupSummary.Currency)
						case 6:
							assert.EqualValues(t, 5_000_000, user.TopupSummary.MoneyMicros)
							assert.Equal(t, "USD", user.TopupSummary.Currency)
						case 8:
							assert.EqualValues(t, 100_000_000, user.TopupSummary.MoneyMicros)
							assert.Equal(t, "USD", user.TopupSummary.Currency)
						case 4:
							assert.Zero(t, user.TopupSummary.MoneyMicros)
							assert.Zero(t, user.TopupSummary.SettledMoneyMicros)
							assert.Equal(t, "MULTIPLE", user.TopupSummary.Currency)
						case 7:
							assert.Zero(t, user.TopupSummary.MoneyMicros)
							assert.Zero(t, user.TopupSummary.SettledMoneyMicros)
							assert.Equal(t, "none", user.TopupSummary.PaymentBasis)
						}
					}
				})
			}
		})
	}
}

func TestAdminUserTopupSummaryDoesNotAddDifferentFiatCurrencies(t *testing.T) {
	truncateTables(t)
	insertUsersForPaginationTest(t, 1)
	require.NoError(t, DB.Create(&[]TopUp{
		{
			UserId: 1, TradeNo: "usd-order", Amount: 1,
			CreditedQuota: int64(common.QuotaPerUnit), SettledAmountMicros: 1_000_000,
			SettlementCurrency: "USD", Status: common.TopUpStatusSuccess,
			PaymentMethod: PaymentMethodStripe, PaymentProvider: PaymentProviderStripe,
		},
		{
			UserId: 1, TradeNo: "cny-order", Amount: 1,
			CreditedQuota: int64(common.QuotaPerUnit), SettledAmountMicros: 6_800_000,
			SettlementCurrency: "CNY", Status: common.TopUpStatusSuccess,
			PaymentMethod: "alipay", PaymentProvider: PaymentProviderEpay,
		},
	}).Error)

	users := []*User{{Id: 1}}
	require.NoError(t, PopulateUserTopups(users))
	require.NotNil(t, users[0].TopupSummary)
	assert.Equal(t, "MULTIPLE", users[0].TopupSummary.Currency)
	assert.Zero(t, users[0].TopupSummary.MoneyMicros)
	require.Len(t, users[0].TopupSummary.Methods, 2)
	assert.ElementsMatch(t, []string{"USD", "CNY"}, []string{
		users[0].TopupSummary.Methods[0].SettlementCurrency,
		users[0].TopupSummary.Methods[1].SettlementCurrency,
	})
}

func TestAdminUserTopupSummaryExcludesLinuxDOCredit(t *testing.T) {
	installPaidPolicyCurrencyFixture(t, common.QuotaPerUnit)
	truncateTables(t)
	insertUsersForPaginationTest(t, 2)

	require.NoError(t, DB.Create(&[]TopUp{
		{
			UserId:          1,
			TradeNo:         "user-topup-linuxdo-credit",
			Amount:          500,
			CreditedQuota:   int64(common.QuotaPerUnit) * 500,
			Money:           500,
			Status:          common.TopUpStatusSuccess,
			PaymentMethod:   "epay",
			PaymentProvider: PaymentProviderEpay,
		},
		{
			UserId:              2,
			TradeNo:             "user-topup-real-money",
			Amount:              10,
			CreditedQuota:       int64(common.QuotaPerUnit) * 10,
			Money:               10,
			SettledAmountMicros: 10_000_000,
			SettlementCurrency:  "USD",
			Status:              common.TopUpStatusSuccess,
			PaymentMethod:       PaymentMethodStripe,
			PaymentProvider:     PaymentProviderStripe,
		},
	}).Error)

	users, total, err := GetAllUsers(
		&common.PageInfo{Page: 1, PageSize: 2},
		false,
		NewUserSortOptions("topup_money", "desc"),
	)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Equal(t, []int{2, 1}, collectUserIDs(users))
	require.NoError(t, PopulateUserTopups(users))
	assert.EqualValues(t, 10_000_000, users[0].TopupSummary.MoneyMicros)
	assert.EqualValues(t, 0, users[1].TopupSummary.MoneyMicros)
	assert.Empty(t, users[1].TopupSummary.Methods)
}

func TestUserListsFilterL0BeforePagination(t *testing.T) {
	installPaidPolicyCurrencyFixture(t, common.QuotaPerUnit)
	truncateTables(t)
	for userID := 1; userID <= 8; userID++ {
		invalidatePaidTopUpAggregate(userID)
	}
	t.Cleanup(func() {
		for userID := 1; userID <= 8; userID++ {
			invalidatePaidTopUpAggregate(userID)
		}
	})
	levelZero := TrustLevelMinUser
	levelOne := TrustLevelMinUser + 1
	invalidLevel := TrustLevelMaxUser + 1
	users := []*User{
		{Id: 1, Username: "filter-fresh", Password: "password123", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AffCode: "filter-fresh"},
		{Id: 2, Username: "filter-activated", Password: "password123", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AffCode: "filter-activated", ConsoleActivatedAt: 100},
		{Id: 3, Username: "filter-reset", Password: "password123", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AffCode: "filter-reset", ConsoleActivatedAt: 100, TrustLevelOverride: &levelZero},
		{Id: 4, Username: "filter-paid", Password: "password123", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AffCode: "filter-paid"},
		{Id: 5, Username: "filter-credit", Password: "password123", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AffCode: "filter-credit"},
		{Id: 6, Username: "filter-invalid", Password: "password123", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AffCode: "filter-invalid", TrustLevelOverride: &invalidLevel},
		{Id: 7, Username: "filter-manual-l1", Password: "password123", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AffCode: "filter-manual-l1", TrustLevelOverride: &levelOne},
		{Id: 8, Username: "filter-admin", Password: "password123", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, AffCode: "filter-admin"},
	}
	require.NoError(t, DB.Create(&users).Error)
	require.NoError(t, DB.Create(&TopUp{
		UserId:          4,
		TradeNo:         "filter-paid",
		Amount:          1,
		CreditedQuota:   int64(common.QuotaPerUnit),
		Money:           1,
		Status:          common.TopUpStatusSuccess,
		PaymentProvider: PaymentProviderStripe,
	}).Error)
	require.NoError(t, DB.Create(&TopUp{
		UserId:          5,
		TradeNo:         "filter-linuxdo-credit",
		Amount:          1,
		CreditedQuota:   int64(common.QuotaPerUnit),
		Money:           1,
		Status:          common.TopUpStatusSuccess,
		PaymentMethod:   "epay",
		PaymentProvider: PaymentProviderEpay,
	}).Error)

	pageOne, total, err := GetAllUsers(
		&common.PageInfo{Page: 1, PageSize: 2},
		true,
		NewUserSortOptions("id", "asc"),
	)
	require.NoError(t, err)
	assert.Equal(t, int64(4), total)
	assert.Equal(t, []int{1, 3}, collectUserIDs(pageOne))
	for _, user := range pageOne {
		require.NotNil(t, user.TrustLevelInfo)
		assert.Equal(t, TrustLevelMinUser, user.TrustLevelInfo.Level)
	}

	pageTwo, total, err := SearchUsers(
		"filter",
		"",
		nil,
		nil,
		true,
		2,
		2,
		NewUserSortOptions("id", "asc"),
	)
	require.NoError(t, err)
	assert.Equal(t, int64(4), total)
	assert.Equal(t, []int{5, 6}, collectUserIDs(pageTwo))
}
