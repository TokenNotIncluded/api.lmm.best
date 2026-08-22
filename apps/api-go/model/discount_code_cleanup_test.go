package model

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func testDeleteExhaustedDiscountCodes(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&DiscountCode{}))
	require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&DiscountCode{}).Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&DiscountCode{}).Error)
	})

	codes := []DiscountCode{
		{Code: "LMM-CLEANUP-EXHAUSTED-1", Name: "exhausted", DiscountPercent: 10, Status: 1, UsedCount: 1, MaxUses: 1},
		{Code: "LMM-CLEANUP-EXHAUSTED-2", Name: "exhausted over limit", DiscountPercent: 10, Status: 1, UsedCount: 4, MaxUses: 3},
		{Code: "LMM-CLEANUP-PARTIAL", Name: "partial", DiscountPercent: 10, Status: 1, UsedCount: 1, MaxUses: 3},
		{Code: "LMM-CLEANUP-UNLIMITED", Name: "unlimited", DiscountPercent: 10, Status: 1, UsedCount: 8, MaxUses: 0},
		{Code: "LMM-CLEANUP-UNUSED", Name: "unused", DiscountPercent: 10, Status: 1, UsedCount: 0, MaxUses: 1},
	}
	require.NoError(t, DB.Create(&codes).Error)

	deleted, err := DeleteExhaustedDiscountCodes()
	require.NoError(t, err)
	require.Equal(t, int64(2), deleted)

	var activeCodes []DiscountCode
	require.NoError(t, DB.Where("id > 0").Order("code asc").Find(&activeCodes).Error)
	require.Len(t, activeCodes, 3)
	require.Equal(t, []string{"LMM-CLEANUP-PARTIAL", "LMM-CLEANUP-UNLIMITED", "LMM-CLEANUP-UNUSED"}, []string{activeCodes[0].Code, activeCodes[1].Code, activeCodes[2].Code})

	var deletedCodes int64
	require.NoError(t, DB.Unscoped().Where("deleted_at IS NOT NULL").Model(&DiscountCode{}).Count(&deletedCodes).Error)
	require.Equal(t, int64(2), deletedCodes)
}

// pi-lens-ignore: ast-grep:go-test-functions
func TestDiscountCodeCleanup(t *testing.T) {
	t.Run("deletes only exhausted finite-use codes", testDeleteExhaustedDiscountCodes)
}
