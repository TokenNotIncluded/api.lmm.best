package model

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// This is the complete persisted ProfileShare shape from go-v0.2.69,
// commit 185785ec795c97f8da7428502f0b3d665f0eced5. Keeping the old GORM tags
// also exercises the original primary key, token uniqueness and reader mapping.
// Both versions use gorm v1.25.2 and the PostgreSQL driver v1.5.2.
type profileShareGo69 struct {
	UserID            int    `json:"-" gorm:"primaryKey;autoIncrement:false"`
	Token             string `json:"token" gorm:"size:48;not null;uniqueIndex"`
	CreatedAt         int64  `json:"created_at" gorm:"not null"`
	ModelUsageEnabled bool   `json:"model_usage_enabled" gorm:"not null;default:false"`
}

func (profileShareGo69) TableName() string { return "profile_shares" }

func TestProfileShareAggregatePostgresGo69MigrationAndRollback(t *testing.T) {
	usePostgresDatabaseType(t)
	db := openIsolatedPostgresCacheTestDB(t, &profileShareGo69{})
	previousDB, previousLogDB := DB, LOG_DB
	previousRedis, previousMemory := common.RedisEnabled, common.MemoryCacheEnabled
	DB, LOG_DB = db, db
	common.RedisEnabled, common.MemoryCacheEnabled = false, false
	t.Cleanup(func() {
		DB, LOG_DB = previousDB, previousLogDB
		common.RedisEnabled, common.MemoryCacheEnabled = previousRedis, previousMemory
	})

	identity, err := loadPostgresRuntimeIdentity(db)
	require.NoError(t, err)
	require.NoError(t, verifyPostgresRuntimeIdentity(identity))
	columns, err := db.Migrator().ColumnTypes(&profileShareGo69{})
	require.NoError(t, err)
	require.Len(t, columns, 4, "the fixture starts with the actual Go 0.2.69 shape")
	require.False(t, db.Migrator().HasColumn(&profileShareGo69{}, "aggregate_usage_enabled"))
	require.False(t, db.Migrator().HasColumn(&profileShareGo69{}, "linked_profiles"))
	legacyRows := []profileShareGo69{
		{UserID: 301, Token: strings.Repeat("a", 48), CreatedAt: 1780000001, ModelUsageEnabled: true},
		{UserID: 302, Token: strings.Repeat("b", 48), CreatedAt: 1780000002},
	}
	require.NoError(t, db.Create(&legacyRows).Error)

	// Select the real registered model, rather than inventing a separate new
	// schema list. A missing registration must fail this migration regression.
	var registeredProfileModels []interface{}
	for _, candidate := range mainMigrationModels() {
		if _, ok := candidate.(*ProfileShare); ok {
			registeredProfileModels = append(registeredProfileModels, candidate)
		}
	}
	require.Len(t, registeredProfileModels, 1)
	profileInventory, err := buildPostgresSchemaInventory(db, identity.SchemaName, registeredProfileModels)
	require.NoError(t, err)
	require.ErrorContains(t, verifyPostgresSchemaInventory(db, profileInventory), "profile_shares.aggregate_usage_enabled")
	require.False(t, db.Migrator().HasColumn(&profileShareGo69{}, "aggregate_usage_enabled"), "verification must not repair an old schema")

	// These are the actual native startup migration and option normalization,
	// followed by its full verifier (whose inventory is mainMigrationModels).
	require.NoError(t, migrateDB())
	require.NoError(t, MigrateRetiredFrontendOptions())
	require.NoError(t, verifyPostgresRuntimeAndSchema(db))
	fullInventory, err := buildPostgresSchemaInventory(db, identity.SchemaName, append(mainMigrationModels(), &SubscriptionPlan{}))
	require.NoError(t, err)
	for _, column := range []string{"aggregate_usage_enabled", "linked_profiles"} {
		found := false
		for _, object := range fullInventory.Objects {
			found = found || object.table == "profile_shares" && object.column == column
		}
		require.True(t, found, "the native automatic inventory must include %s", column)
	}
	t.Logf("migrated Go69 profile rows through %d native models; verified %d schema objects in %s", len(mainMigrationModels()), len(fullInventory.Objects), identity.SchemaName)

	for _, old := range legacyRows {
		share, err := GetProfileShare(old.UserID)
		require.NoError(t, err)
		require.Equal(t, old.Token, share.Token)
		require.Equal(t, old.CreatedAt, share.CreatedAt)
		require.Equal(t, old.ModelUsageEnabled, share.ModelUsageEnabled)
		require.False(t, share.AggregateUsageEnabled)
		require.Nil(t, share.LinkedProfiles)
	}
	var nullRows int64
	require.NoError(t, db.Table("profile_shares").Where("linked_profiles IS NULL").Count(&nullRows).Error)
	require.EqualValues(t, len(legacyRows), nullRows, "old links must remain SQL NULL, not an invented JSON snapshot")
	var contracts []struct {
		ColumnName    string
		DataType      string
		IsNullable    string
		ColumnDefault sql.NullString
	}
	require.NoError(t, db.Raw(`SELECT column_name, data_type, is_nullable, column_default
		FROM information_schema.columns WHERE table_schema = ? AND table_name = 'profile_shares'
		AND column_name IN ('aggregate_usage_enabled', 'linked_profiles') ORDER BY column_name`, identity.SchemaName).Scan(&contracts).Error)
	require.Len(t, contracts, 2)
	require.Equal(t, "aggregate_usage_enabled", contracts[0].ColumnName)
	require.Equal(t, "boolean", contracts[0].DataType)
	require.Equal(t, "NO", contracts[0].IsNullable)
	require.Equal(t, sql.NullString{String: "false", Valid: true}, contracts[0].ColumnDefault)
	require.Equal(t, "linked_profiles", contracts[1].ColumnName)
	require.Equal(t, "text", contracts[1].DataType)
	require.Equal(t, "YES", contracts[1].IsNullable)
	require.False(t, contracts[1].ColumnDefault.Valid)

	// Damage a new, still-empty column to prove the full native verifier derives
	// its requirements from the model and remains read-only. Repair uses the
	// real migration again; no test-owned ALTER ADD or schema manifest is used.
	require.NoError(t, db.Exec("ALTER TABLE profile_shares DROP COLUMN linked_profiles").Error)
	require.ErrorContains(t, verifyPostgresRuntimeAndSchema(db), "profile_shares.linked_profiles")
	require.False(t, db.Migrator().HasColumn(&ProfileShare{}, "linked_profiles"))
	require.NoError(t, migrateDB())
	require.NoError(t, verifyPostgresRuntimeAndSchema(db))

	owner := User{Id: legacyRows[0].UserID, Username: "aggregate-pg-owner", AffCode: "aggregate-pg-owner", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&owner).Error)
	share, err := GetProfileShare(owner.Id)
	require.NoError(t, err)
	enabled := true
	tokens, zero := int64(117_000_000_000), int64(0)
	links := []ProfileLinkedProfile{{
		Provider: "chatgpt", URL: "https://chatgpt.com/u/pg-fixture", Label: "explicit owner snapshot",
		Snapshot: &ProfileUsageSnapshot{Tokens: &tokens, Messages: &zero, Period: "all", ObservedAt: "2026-10-03T18:46:00Z", Approximate: true, Source: "owner"},
	}}
	share, err = SetProfileShareSettings(share, nil, &enabled, &links)
	require.NoError(t, err)
	require.True(t, share.ModelUsageEnabled, "aggregate consent must not change the old model consent")
	require.True(t, share.AggregateUsageEnabled)
	require.Equal(t, links, share.LinkedProfiles)
	share, err = GetProfileShare(owner.Id)
	require.NoError(t, err)
	require.Equal(t, links, share.LinkedProfiles, "serializer JSON must survive a real PostgreSQL reload")
	require.Nil(t, share.LinkedProfiles[0].Snapshot.Requests, "unknown is distinct from a reported zero")
	require.Equal(t, int64(0), *share.LinkedProfiles[0].Snapshot.Messages)
	require.Equal(t, legacyRows[0].Token, share.Token)
	require.Equal(t, legacyRows[0].CreatedAt, share.CreatedAt)

	// The old reader performs SELECT *, exactly as Go69 GetProfileShare did.
	// PostgreSQL returns both new columns; old GORM mapping must ignore them
	// while preserving every field the rollback version knows about.
	var rollbackRead profileShareGo69
	query := db.Session(&gorm.Session{DryRun: true}).Where("user_id = ?", owner.Id).Take(&rollbackRead)
	require.Contains(t, query.Statement.SQL.String(), "SELECT *")
	require.NoError(t, db.Where("user_id = ?", owner.Id).Take(&rollbackRead).Error)
	require.Equal(t, legacyRows[0], rollbackRead)
	disabled := false
	share, err = SetProfileShareSettings(share, nil, &disabled, nil)
	require.NoError(t, err)
	require.False(t, share.AggregateUsageEnabled, "an explicit false update must persist")
	require.True(t, share.ModelUsageEnabled)
	require.Equal(t, links, share.LinkedProfiles)
	require.NoError(t, migrateDB())
	require.NoError(t, verifyPostgresRuntimeAndSchema(db))
	reloaded, err := GetProfileShare(owner.Id)
	require.NoError(t, err)
	require.Equal(t, share, reloaded, "repeat migration must not rewrite token, consent or serialized links")
	publicOwner, err := GetProfileShareOwner(share.Token)
	require.NoError(t, err)
	require.Equal(t, owner.Id, publicOwner.Id)

	revoked := *share
	require.NoError(t, DisableProfileShare(owner.Id))
	_, err = GetProfileShareOwner(revoked.Token)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = SetProfileShareSettings(&revoked, nil, &enabled, &links)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound, "an update cannot recreate a revoked public URL")
	newShare, err := EnableProfileShare(owner.Id)
	require.NoError(t, err)
	require.Len(t, newShare.Token, 48)
	require.NotEqual(t, revoked.Token, newShare.Token)
	require.False(t, newShare.ModelUsageEnabled)
	require.False(t, newShare.AggregateUsageEnabled)
	require.Nil(t, newShare.LinkedProfiles)
	_, err = SetProfileShareSettings(&revoked, nil, &enabled, &links)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound, "old token cannot modify a re-enabled share")
	_, err = GetProfileShareOwner(revoked.Token)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	require.ErrorIs(t, db.Where("token = ?", revoked.Token).Take(&profileShareGo69{}).Error, gorm.ErrRecordNotFound)
	rollbackRead = profileShareGo69{}
	require.NoError(t, db.Where("user_id = ?", owner.Id).Take(&rollbackRead).Error)
	require.Equal(t, newShare.Token, rollbackRead.Token)
	require.False(t, rollbackRead.ModelUsageEnabled)
	current, err := GetProfileShare(owner.Id)
	require.NoError(t, err)
	require.Equal(t, newShare, current)
	var resetLinksNull bool
	require.NoError(t, db.Raw("SELECT linked_profiles IS NULL FROM profile_shares WHERE user_id = ?", owner.Id).Scan(&resetLinksNull).Error)
	require.True(t, resetLinksNull)
	t.Log("legacy rows and model consent preserved; new defaults false/NULL; settings and revocation persist; Go69 SELECT * readers ignore new columns")
}
