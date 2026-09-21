package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigrateLegacySubscriptionPlanCurrenciesIsVersioned(t *testing.T) {
	const (
		legacyID   = 981001
		explicitID = 981002
	)
	t.Cleanup(func() {
		_ = DB.Where("id IN ?", []int{legacyID, explicitID}).Delete(&SubscriptionPlan{}).Error
	})

	require.NoError(t, DB.Exec(`
		INSERT INTO subscription_plans
			(id, title, price_amount, currency, price_currency_version, duration_unit, duration_value)
		VALUES (?, ?, ?, ?, ?, ?, ?), (?, ?, ?, ?, ?, ?, ?)
	`,
		legacyID, "Legacy platform price", 6.8, "USD", 0, "month", 1,
		explicitID, "Explicit USD price", 1.0, "USD", 1, "month", 1,
	).Error)

	require.NoError(t, migrateLegacySubscriptionPlanCurrencies())

	var legacy SubscriptionPlan
	require.NoError(t, DB.First(&legacy, legacyID).Error)
	require.Equal(t, "CNY", legacy.Currency)
	require.Equal(t, 1, legacy.PriceCurrencyVersion)

	var explicit SubscriptionPlan
	require.NoError(t, DB.First(&explicit, explicitID).Error)
	require.Equal(t, "USD", explicit.Currency)
	require.Equal(t, 1, explicit.PriceCurrencyVersion)
}
