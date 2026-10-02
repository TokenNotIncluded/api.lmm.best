package model

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestToolMarketBuiltinVerifyIsReadOnlyAndRequiresRegisteredCatalog(t *testing.T) {
	db := marketTestDB(t)
	definition := marketTestBuiltinDefinition()
	require.NoError(t, db.Exec("PRAGMA query_only = ON").Error)
	require.ErrorIs(t, VerifyToolMarketBuiltinServices(context.Background(), []ToolMarketBuiltinServiceInput{definition}), gorm.ErrRecordNotFound)
	var count int64
	require.NoError(t, db.Model(&ToolMarketService{}).Count(&count).Error)
	require.Zero(t, count, "a worker must not register a missing catalog")
	require.NoError(t, db.Exec("PRAGMA query_only = OFF").Error)
	require.NoError(t, EnsureToolMarketBuiltinServices([]ToolMarketBuiltinServiceInput{definition}))
	require.NoError(t, db.Exec("PRAGMA query_only = ON").Error)
	require.NoError(t, VerifyToolMarketBuiltinServices(context.Background(), []ToolMarketBuiltinServiceInput{definition}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, VerifyToolMarketBuiltinServices(ctx, []ToolMarketBuiltinServiceInput{definition}), context.Canceled)
}

func TestToolMarketBuiltinVerifyRejectsStoredDescriptorOrAuthorityChanges(t *testing.T) {
	cases := []struct {
		name  string
		model any
		field string
		value any
	}{
		{"owner", &ToolMarketService{}, "owner_id", 42},
		{"draft", &ToolMarketService{}, "draft_version_id", "unexpected-draft"},
		{"service-status", &ToolMarketService{}, "status", "paused"},
		{"visibility", &ToolMarketVersion{}, "visibility", "private"},
		{"endpoint", &ToolMarketVersion{}, "endpoint", "https://example.com/mcp"},
		{"digest", &ToolMarketVersion{}, "digest", "wrong"},
		{"version-name", &ToolMarketVersion{}, "name", "Other service"},
		{"tool-price", &ToolMarketToolVersion{}, "price_quota", 1},
		{"tool-schema", &ToolMarketToolVersion{}, "input_schema", `{"type":"object"}`},
		{"tool-output", &ToolMarketToolVersion{}, "output_schema", `{"type":"object"}`},
		{"tool-permissions", &ToolMarketToolVersion{}, "permissions", `["write"]`},
		{"tool-description", &ToolMarketToolVersion{}, "description", "Changed"},
		{"tool-identity", &ToolMarketTool{}, "name", "system.other"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := marketTestDB(t)
			definition := marketTestBuiltinDefinition()
			require.NoError(t, EnsureToolMarketBuiltinServices([]ToolMarketBuiltinServiceInput{definition}))
			require.NoError(t, db.Model(tc.model).Where("1 = 1").Update(tc.field, tc.value).Error)
			require.NoError(t, db.Exec("PRAGMA query_only = ON").Error)
			require.ErrorIs(t, VerifyToolMarketBuiltinServices(context.Background(), []ToolMarketBuiltinServiceInput{definition}), ErrToolMarketConflict)
		})
	}
}

func TestToolMarketBuiltinVerifyOldWorkerCannotDowngradeNewCatalog(t *testing.T) {
	db := marketTestDB(t)
	old := marketTestBuiltinDefinition()
	require.NoError(t, EnsureToolMarketBuiltinServices([]ToolMarketBuiltinServiceInput{old}))
	current := marketTestBuiltinDefinition()
	current.Tools[0].Description = "A revised compiled handler descriptor"
	require.NoError(t, EnsureToolMarketBuiltinServices([]ToolMarketBuiltinServiceInput{current}))
	expected, err := ToolMarketBuiltinVersionID(current)
	require.NoError(t, err)
	require.NoError(t, db.Exec("PRAGMA query_only = ON").Error)
	require.ErrorIs(t, VerifyToolMarketBuiltinServices(context.Background(), []ToolMarketBuiltinServiceInput{old}), ErrToolMarketConflict)
	require.NoError(t, VerifyToolMarketBuiltinServices(context.Background(), []ToolMarketBuiltinServiceInput{current}))
	var service ToolMarketService
	require.NoError(t, db.First(&service, "id = ?", ToolMarketBuiltinServiceID(old.Key)).Error)
	require.Equal(t, expected, service.LiveVersionID)
}
