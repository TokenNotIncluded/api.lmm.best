package controller

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Public amount DTOs read durable configuration, so their fixtures include
// the same complete currency basis as a initialized database.
func persistCreditDenominationFixture(t *testing.T, db *gorm.DB) {
	t.Helper()
	ledger, err := common.LedgerQuotaPerUSD()
	require.NoError(t, err)
	legacy, err := common.LegacyPricingQuotaPerUnit()
	require.NoError(t, err)
	public, err := common.PublicCreditsPerUSD()
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Option{}))
	rows := []model.Option{{Key: model.CreditsPerUSDOptionKey, Value: ledger.String()}, {Key: model.LegacyPricingQuotaPerUnitOptionKey, Value: legacy.String()}, {Key: "QuotaPerUnit", Value: legacy.String()}}
	if common.ValidatePublicCreditsPerUSD(public) == nil {
		rows = append(rows, model.Option{Key: model.PublicCreditsPerUSDOptionKey, Value: public.String()})
	}
	require.NoError(t, db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value"})}).Create(&rows).Error)
}
