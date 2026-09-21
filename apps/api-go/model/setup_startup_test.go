package model

import (
	"errors"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSetupStartupPropagatesCreateFailure(t *testing.T) {
	originalDB, originalSetup := DB, constant.IsSetup()
	t.Cleanup(func() { DB = originalDB; constant.SetSetup(originalSetup) })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&Setup{}, &User{}))
	require.NoError(t, db.Create(&User{Username: "fixture-root", Role: common.RoleRootUser}).Error)
	DB = db
	constant.SetSetup(false)
	injected := errors.New("fixture setup persistence failure")
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:fail_setup", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Setup" {
			tx.AddError(injected)
		}
	}))
	err = CheckSetupForStartup(true)
	t.Logf("startup_error=%v initialized=%t", err, constant.IsSetup())
	require.ErrorIs(t, err, injected)
	require.False(t, constant.IsSetup())
	var count int64
	require.NoError(t, db.Model(&Setup{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestSetupStartupPreservesInitializedAndFreshStates(t *testing.T) {
	for _, tc := range []struct {
		name        string
		root, setup bool
	}{
		{"fresh", false, false},
		{"legacy-root", true, false},
		{"initialized", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			originalDB, originalSetup := DB, constant.IsSetup()
			t.Cleanup(func() { DB = originalDB; constant.SetSetup(originalSetup) })
			db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { _ = sqlDB.Close() })
			require.NoError(t, db.AutoMigrate(&Setup{}, &User{}))
			if tc.root {
				require.NoError(t, db.Create(&User{Username: "fixture-root", Role: common.RoleRootUser}).Error)
			}
			if tc.setup {
				require.NoError(t, db.Create(&Setup{ID: SetupSingletonID, Version: "fixture", InitializedAt: 123}).Error)
			}
			DB = db
			constant.SetSetup(!tc.root)
			for i := 0; i < 2; i++ {
				require.NoError(t, CheckSetupForStartup(true))
				require.Equal(t, tc.root, constant.IsSetup())
			}
			var rows []Setup
			require.NoError(t, db.Find(&rows).Error)
			if !tc.root {
				require.Empty(t, rows)
				return
			}
			require.Len(t, rows, 1)
			require.Equal(t, SetupSingletonID, rows[0].ID)
			if tc.setup {
				require.Equal(t, int64(123), rows[0].InitializedAt)
				require.Equal(t, "fixture", rows[0].Version)
			}
		})
	}
}
