package model

import (
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func redPacketSchemaTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := DB
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "schema.db")), &gorm.Config{})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	DB = db
	t.Cleanup(func() {
		DB = previousDB
		require.NoError(t, pool.Close())
	})
	return db
}

func TestRedPacketModelsBelongToPrimaryMigrationInventory(t *testing.T) {
	for _, expected := range []interface{}{&RedPacket{}, &RedPacketItem{}, &RedPacketClaim{}} {
		matches := 0
		for _, candidate := range mainMigrationModels() {
			if reflect.TypeOf(candidate) == reflect.TypeOf(expected) {
				matches++
			}
		}
		require.Equal(t, 1, matches, "CLI apply and verify must include %T exactly once", expected)
	}
}

func TestRedPacketRouteSchemaCheckNeverCreatesTables(t *testing.T) {
	for _, mode := range []DBMigrationMode{DBMigrationModeApply, DBMigrationModeVerify} {
		for _, master := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/master=%t", mode, master), func(t *testing.T) {
				db := redPacketSchemaTestDB(t)
				t.Setenv(dbMigrationModeEnv, string(mode))
				previousMaster := common.IsMasterNode
				common.IsMasterNode = master
				t.Cleanup(func() { common.IsMasterNode = previousMaster })
				require.EqualError(t, EnsureRedPacketSchemaAtStartup(), "red packet schema verification failed: missing table red_packets")
				for _, candidate := range []interface{}{&RedPacket{}, &RedPacketItem{}, &RedPacketClaim{}} {
					require.False(t, db.Migrator().HasTable(candidate))
				}
			})
		}
	}
}

func TestRedPacketRouteSchemaCheckAcceptsMigratedTables(t *testing.T) {
	db := redPacketSchemaTestDB(t)
	require.NoError(t, db.AutoMigrate(&RedPacket{}, &RedPacketItem{}, &RedPacketClaim{}))
	for _, mode := range []DBMigrationMode{DBMigrationModeApply, DBMigrationModeVerify} {
		t.Run(string(mode), func(t *testing.T) {
			t.Setenv(dbMigrationModeEnv, string(mode))
			require.NoError(t, EnsureRedPacketSchemaAtStartup())
		})
	}
}

func TestRedPacketRouteSchemaCheckDoesNotRepairMissingColumn(t *testing.T) {
	for _, mode := range []DBMigrationMode{DBMigrationModeApply, DBMigrationModeVerify} {
		t.Run(string(mode), func(t *testing.T) {
			db := redPacketSchemaTestDB(t)
			t.Setenv(dbMigrationModeEnv, string(mode))
			require.NoError(t, db.AutoMigrate(&RedPacket{}, &RedPacketItem{}, &RedPacketClaim{}))
			require.NoError(t, db.Migrator().DropColumn(&RedPacket{}, "description"))
			require.EqualError(t, EnsureRedPacketSchemaAtStartup(), "red packet schema verification failed: missing red_packets.description")
			require.False(t, db.Migrator().HasColumn(&RedPacket{}, "description"))
		})
	}
}

func TestRedPacketSchemaRequiresSoftDeleteColumn(t *testing.T) {
	db := redPacketSchemaTestDB(t)
	require.NoError(t, db.AutoMigrate(&RedPacket{}, &RedPacketItem{}, &RedPacketClaim{}))
	require.NoError(t, db.Migrator().DropColumn(&RedPacket{}, "deleted_at"))
	require.EqualError(t, EnsureRedPacketSchemaAtStartup(), "red packet schema verification failed: missing red_packets.deleted_at")
	require.NoError(t, db.AutoMigrate(&RedPacket{}))
	require.NoError(t, EnsureRedPacketSchemaAtStartup())
}
