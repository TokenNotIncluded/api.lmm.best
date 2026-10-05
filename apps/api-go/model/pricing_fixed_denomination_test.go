package model

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPricingFixedDenominationRejectsObsoleteDurableAnchor(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	oldDB := DB
	DB = db
	t.Cleanup(func() {
		DB = oldDB
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	require.NoError(t, db.AutoMigrate(&Option{}))
	pricingCurrencyFixture(t, 500000, 500000)
	persistPricingCurrencyFixture(t)
	require.NoError(t, validateAuthoritativePricingUnits(db))
	for _, key := range []string{CreditsPerUSDOptionKey, LegacyPricingQuotaPerUnitOptionKey, "QuotaPerUnit"} {
		require.NoError(t, db.Model(&Option{}).Where("key = ?", key).Update("value", "3359744").Error)
		require.ErrorIs(t, validateAuthoritativePricingUnits(db), ErrPricingUnitsStale, key)
		require.NoError(t, db.Model(&Option{}).Where("key = ?", key).Update("value", "500000").Error)
	}
}
