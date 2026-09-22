package model

import (
	"fmt"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBackendJourneyL0PostgresPolicyAndRounding(t *testing.T) {
	db := openIsolatedPostgresCacheTestDB(t, &User{}, &TopUp{})
	previousDB, previousRedis := DB, common.RedisEnabled
	DB, common.RedisEnabled = db, false
	usePostgresDatabaseType(t)
	settings := operation_setting.GetDeveloperAccessSetting()
	oldSettings, oldQuota := *settings, common.QuotaPerUnit
	oldLocal := LocalAcceptanceDeveloperAccessEnabled()
	SetLocalAcceptanceDeveloperAccess(false)
	t.Cleanup(func() {
		DB, common.RedisEnabled = previousDB, previousRedis
		*settings, common.QuotaPerUnit = oldSettings, oldQuota
		SetLocalAcceptanceDeveloperAccess(oldLocal)
	})
	common.QuotaPerUnit = 3000000
	user := User{Username: "journey-pg", Password: "unused", AffCode: "journey-pg", Role: common.RoleCommonUser}
	require.NoError(t, db.Create(&user).Error)
	for i := 0; i < 2; i++ {
		require.NoError(t, db.Create(&TopUp{UserId: user.Id, TradeNo: fmt.Sprintf("pg-round-%d", i), Money: 1, CreditedQuota: 1, PaymentProvider: PaymentProviderStripe, Status: common.TopUpStatusSuccess}).Error)
		for _, tc := range []struct {
			name    string
			enabled bool
			minimum float64
			granted bool
		}{
			{"any recharge", true, 0, true},
			{"cumulative rounding", true, 0.000001, i == 1},
			{"below threshold", true, 1, false},
			{"manual review only", false, 0, false},
		} {
			t.Run(fmt.Sprintf("%d/%s", i, tc.name), func(t *testing.T) {
				settings.PaidActivationEnabled, settings.PaidActivationMinAmount = tc.enabled, tc.minimum
				access, err := GetDeveloperAccessStateForUser(&user)
				require.NoError(t, err)
				require.Equal(t, tc.granted, access.Granted)
				var count int64
				require.NoError(t, applyL0UserFilter(db, db.Model(&User{})).Count(&count).Error)
				assert.Equal(t, !access.Granted, count == 1)
				var ids []int
				require.NoError(t, applyL0UserFilter(db, db.Model(&User{})).Order("users.id").Limit(1).Pluck("users.id", &ids).Error)
				assert.EqualValues(t, count, len(ids))
			})
		}
	}
}
