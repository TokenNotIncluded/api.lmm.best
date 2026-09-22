package model

import (
	"errors"
	"fmt"
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestBackendJourneyPaymentContactNeverBindsLoginEmail(t *testing.T) {
	for _, tc := range []struct{ name, accountEmail, payerEmail string }{
		{"duplicate", "", "owner@example.com"},
		{"duplicate normalized", "", " OWNER@EXAMPLE.COM "},
		{"new payment contact", "", "payer@example.com"},
		{"empty contact", "", ""},
		{"existing binding", "verified@example.com", "owner@example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupExternalTopUpSettlementDB(t, 1)
			require.NoError(t, db.Create(&User{Username: "email-owner", Email: "owner@example.com", Password: "unused", AffCode: "owner"}).Error)
			user, order, evidence := createSettlementFixture(t, db, "email-boundary")
			require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("email", tc.accountEmail).Error)
			require.NoError(t, db.Model(&order).Updates(map[string]interface{}{"payment_provider": PaymentProviderCreem, "payment_method": PaymentMethodCreem}).Error)
			evidence.PaymentProvider, evidence.PaymentMethod = PaymentProviderCreem, PaymentMethodCreem
			evidence.StripeCustomer = ""
			evidence.CustomerEmail = tc.payerEmail
			for retry := 0; retry < 2; retry++ {
				completed, err := CompleteExternalTopUp(evidence)
				require.NoError(t, err)
				require.Equal(t, common.TopUpStatusSuccess, completed.Status)
			}
			var stored User
			require.NoError(t, db.First(&stored, user.Id).Error)
			assert.Equal(t, tc.accountEmail, stored.Email)
			assert.Equal(t, user.AuthVersion, stored.AuthVersion)
			assert.Equal(t, user.Password, stored.Password)
			assert.EqualValues(t, user.Quota+int(order.CreditedQuota), stored.Quota, "duplicate callback must not credit twice")
			count, err := CountUsersByEmail("owner@example.com")
			require.NoError(t, err)
			assert.EqualValues(t, 1, count)
		})
	}
}

func TestBackendJourneyL0FilterMatchesLiveActivationPolicy(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	oldQuota := common.QuotaPerUnit
	settings := operation_setting.GetDeveloperAccessSetting()
	oldSettings := *settings
	oldLocal := LocalAcceptanceDeveloperAccessEnabled()
	common.QuotaPerUnit = 500000
	SetLocalAcceptanceDeveloperAccess(false)
	t.Cleanup(func() {
		common.QuotaPerUnit = oldQuota
		*settings = oldSettings
		SetLocalAcceptanceDeveloperAccess(oldLocal)
	})
	zero, one, invalid := 0, 1, 99
	users := []User{
		{Username: "unpaid"}, {Username: "below"}, {Username: "cumulative"},
		{Username: "pending"}, {Username: "internal"}, {Username: "linuxdo-credit"},
		{Username: "approved", ConsoleActivatedAt: 1},
		{Username: "override-l0", TrustLevelOverride: &zero},
		{Username: "override-l1", TrustLevelOverride: &one},
		{Username: "invalid-override", TrustLevelOverride: &invalid},
		{Username: "administrator", Role: common.RoleAdminUser},
	}
	for i := range users {
		users[i].Password = "unused"
		users[i].AffCode = users[i].Username
		users[i].Status = common.UserStatusEnabled
		if users[i].Role == 0 {
			users[i].Role = common.RoleCommonUser
		}
		require.NoError(t, db.Create(&users[i]).Error)
	}
	orders := []TopUp{
		{UserId: users[1].Id, CreditedQuota: 100000},
		{UserId: users[2].Id, CreditedQuota: 250000},
		{UserId: users[2].Id, CreditedQuota: 250000},
		{UserId: users[3].Id, CreditedQuota: 1000000, Status: common.TopUpStatusPending},
		{UserId: users[4].Id, CreditedQuota: 1000000, PaymentProvider: "admin"},
		{UserId: users[5].Id, CreditedQuota: 1000000, PaymentProvider: PaymentProviderEpay, PaymentMethod: "ldc"},
		{UserId: users[7].Id, CreditedQuota: 1000000},
	}
	for i := range orders {
		orders[i].TradeNo = fmt.Sprintf("journey-%d", i)
		orders[i].Money = 1
		if orders[i].Status == "" {
			orders[i].Status = common.TopUpStatusSuccess
		}
		if orders[i].PaymentProvider == "" {
			orders[i].PaymentProvider = PaymentProviderStripe
		}
		require.NoError(t, db.Create(&orders[i]).Error)
	}
	for _, tc := range []struct {
		name    string
		enabled bool
		minimum float64
	}{
		{"any real recharge", true, 0}, {"one credited dollar", true, 1}, {"higher threshold", true, 10}, {"manual review only", false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings.PaidActivationEnabled, settings.PaidActivationMinAmount = tc.enabled, tc.minimum
			var expected []int
			for _, user := range users {
				access, err := GetDeveloperAccessStateForUser(&user)
				require.NoError(t, err)
				if user.Role < common.RoleAdminUser && !access.Granted {
					expected = append(expected, user.Id)
				}
			}
			var count int64
			require.NoError(t, applyL0UserFilter(db, db.Model(&User{})).Count(&count).Error)
			require.EqualValues(t, len(expected), count, "count must use the same policy as access")
			var actual []int
			for offset := 0; offset < int(count); offset += 2 {
				var page []int
				require.NoError(t, applyL0UserFilter(db, db.Model(&User{})).Order("users.id").Offset(offset).Limit(2).Pluck("users.id", &page).Error)
				actual = append(actual, page...)
			}
			assert.Equal(t, expected, actual, "filter before pagination")
		})
	}
}

func TestBackendJourneyL0ThresholdRoundsTheCumulativeCredit(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	settings := operation_setting.GetDeveloperAccessSetting()
	oldSettings, oldQuota := *settings, common.QuotaPerUnit
	oldLocal := LocalAcceptanceDeveloperAccessEnabled()
	SetLocalAcceptanceDeveloperAccess(false)
	t.Cleanup(func() {
		*settings = oldSettings
		common.QuotaPerUnit = oldQuota
		SetLocalAcceptanceDeveloperAccess(oldLocal)
	})
	settings.PaidActivationEnabled, settings.PaidActivationMinAmount = true, 0.000001
	common.QuotaPerUnit = 3000000
	user := User{Username: "rounding", Password: "unused", AffCode: "rounding", Role: common.RoleCommonUser}
	require.NoError(t, db.Create(&user).Error)
	for i := 0; i < 2; i++ {
		require.NoError(t, db.Create(&TopUp{UserId: user.Id, TradeNo: fmt.Sprintf("round-%d", i), Money: 1, CreditedQuota: 1, PaymentProvider: PaymentProviderStripe, Status: common.TopUpStatusSuccess}).Error)
		access, err := GetDeveloperAccessStateForUser(&user)
		require.NoError(t, err)
		var count int64
		require.NoError(t, applyL0UserFilter(db, db.Model(&User{})).Count(&count).Error)
		assert.Equal(t, !access.Granted, count == 1)
		assert.Equal(t, i == 1, access.Granted)
	}
}

func TestBackendJourneyCheckinStatsPropagateEveryReadFailure(t *testing.T) {
	for _, stage := range []string{"records", "today", "total count", "total quota"} {
		t.Run(stage, func(t *testing.T) {
			db := setupExternalTopUpSettlementDB(t, 1)
			require.NoError(t, db.AutoMigrate(&Checkin{}))
			require.NoError(t, db.Create(&Checkin{UserId: 1, CheckinDate: time.Now().Format("2006-01-02"), QuotaAwarded: 17}).Error)
			injected := errors.New("injected checkin read failure")
			queryNumber := map[string]int{"records": 1, "today": 2, "total count": 3}[stage]
			seen := 0
			require.NoError(t, db.Callback().Query().Before("gorm:query").Register("journey:query-failure", func(tx *gorm.DB) {
				if tx.Statement.Table == "checkins" {
					seen++
					if seen == queryNumber {
						tx.AddError(injected)
					}
				}
			}))
			require.NoError(t, db.Callback().Row().Before("gorm:row").Register("journey:row-failure", func(tx *gorm.DB) {
				if stage == "total quota" && tx.Statement.Table == "checkins" {
					tx.AddError(injected)
				}
			}))
			stats, err := GetUserCheckinStats(1, time.Now().Format("2006-01"))
			assert.ErrorIs(t, err, injected)
			assert.Nil(t, stats, "partial statistics must not look like successful zero-valued data")
		})
	}
}

func TestBackendJourneyCheckinStatsKeepSuccessfulHistory(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	require.NoError(t, db.AutoMigrate(&Checkin{}))
	now := time.Now()
	require.NoError(t, db.Create(&Checkin{UserId: 1, CheckinDate: now.Format("2006-01-02"), QuotaAwarded: 17}).Error)
	stats, err := GetUserCheckinStats(1, now.Format("2006-01"))
	require.NoError(t, err)
	assert.Equal(t, true, stats["checked_in_today"])
	assert.EqualValues(t, 1, stats["total_checkins"])
	assert.EqualValues(t, 17, stats["total_quota"])
	assert.EqualValues(t, 1, stats["checkin_count"])
}
