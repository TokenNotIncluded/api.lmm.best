package model

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/alicebob/miniredis/v2"
	"github.com/glebarez/sqlite"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// These tests must stay serial: the application uses global database and cache
// handles. The database is always a new local file; no environment DSN is read.
func bi10TokenDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB, previousRedis, previousRDB := DB, common.RedisEnabled, common.RDB
	previousType := common.MainDatabaseType()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "bi10.sqlite")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	DB, common.RedisEnabled, common.RDB = db, false, nil
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		DB, common.RedisEnabled, common.RDB = previousDB, previousRedis, previousRDB
		common.SetMainDatabaseType(previousType)
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, db.AutoMigrate(&Token{}))
	return db
}

func bi10Token(t *testing.T, userID int, label string) Token {
	t.Helper()
	token := Token{
		UserId: userID, Key: "bi10-test-only-" + label, Name: "bi10-" + label,
		Status: common.TokenStatusEnabled, ExpiredTime: -1,
		RemainQuota: 100, CreationSource: TokenCreationSourceManual,
	}
	require.NoError(t, DB.Create(&token).Error)
	return token
}

func bi10ReadToken(t *testing.T, id int) Token {
	t.Helper()
	var token Token
	require.NoError(t, DB.Unscoped().First(&token, id).Error)
	return token
}

func bi10Cache(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
	common.RedisEnabled, common.RDB = true, client
	t.Cleanup(func() {
		common.RedisEnabled, common.RDB = false, nil
		require.NoError(t, client.Close())
	})
	return server
}

func TestBI10TokenOwnerIsolation(t *testing.T) {
	bi10TokenDB(t)
	ownerA, ownerB := 101, 202
	a, b := bi10Token(t, ownerA, "owner-a"), bi10Token(t, ownerB, "owner-b")
	beforeB := bi10ReadToken(t, b.Id)

	listed, err := GetAllUserTokens(ownerA, 0, 100)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, a.Id, listed[0].Id)
	require.Equal(t, ownerA, listed[0].UserId)

	found, total, err := SearchUserTokens(ownerA, b.Name, "", 0, 100)
	require.NoError(t, err)
	require.Zero(t, total)
	require.Empty(t, found)
	_, err = GetTokenByIds(b.Id, ownerA)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	exported, err := GetTokenKeysByIds([]int{a.Id, b.Id}, ownerA)
	require.NoError(t, err)
	require.Len(t, exported, 1)
	require.Equal(t, a.Id, exported[0].Id)
	require.Equal(t, a.Key, exported[0].Key)
	require.ErrorIs(t, DeleteTokenById(b.Id, ownerA), gorm.ErrRecordNotFound)
	require.Equal(t, beforeB, bi10ReadToken(t, b.Id))

	// A mixed-owner batch may remove the caller's rows, but never the other
	// owner's row. This follows the existing batch filtering contract.
	deleted, err := BatchDeleteTokens([]int{a.Id, b.Id}, ownerA)
	require.NoError(t, err)
	require.Equal(t, 1, deleted)
	require.True(t, bi10ReadToken(t, a.Id).DeletedAt.Valid)
	require.Equal(t, beforeB, bi10ReadToken(t, b.Id))
}

func TestBI10TokenExportPreservesHiddenKeyBoundaries(t *testing.T) {
	bi10TokenDB(t)
	visible := bi10Token(t, 101, "visible")
	oneTime := bi10Token(t, 101, "one-time")
	managed := bi10Token(t, 101, "oauth-managed")
	require.NoError(t, DB.Model(&oneTime).Update("one_time_reveal", true).Error)
	require.NoError(t, DB.Model(&managed).Update("oauth_managed", true).Error)
	keys, err := GetTokenKeysByIds([]int{visible.Id, oneTime.Id, managed.Id}, 101)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	require.Equal(t, visible.Id, keys[0].Id)
	require.Equal(t, visible.Key, keys[0].GetFullKey())
}

func bi10TokenMutations() []struct {
	name  string
	apply func(*Token) error
} {
	return []struct {
		name  string
		apply func(*Token) error
	}{
		{"update", func(token *Token) error { token.Status = common.TokenStatusDisabled; return token.Update() }},
		{"select-update", func(token *Token) error { token.Status = common.TokenStatusDisabled; return token.SelectUpdate() }},
		{"delete", func(token *Token) error { return token.Delete() }},
		{"batch-delete", func(token *Token) error {
			_, err := BatchDeleteTokens([]int{token.Id}, token.UserId)
			return err
		}},
	}
}

func TestBI10TokenMutationStopsOnCacheFailure(t *testing.T) {
	for _, mutation := range bi10TokenMutations() {
		t.Run(mutation.name, func(t *testing.T) {
			bi10TokenDB(t)
			server := bi10Cache(t)
			token := bi10Token(t, 101, mutation.name)
			before := bi10ReadToken(t, token.Id)
			require.NoError(t, cacheSetToken(token))

			// Keep a warm, readable old cache row. Fail only invalidation while
			// invoking the real mutation code, then restore cache service.
			server.SetError("ERR BI10 injected cache failure")
			err := mutation.apply(&token)
			server.SetError("")
			assert.Error(t, err, "a failed cache invalidation must not report a completed mutation")
			assert.Equal(t, before, bi10ReadToken(t, token.Id), "database changed despite failed invalidation")
			if t.Failed() {
				return
			}
			_, err = ValidateUserToken(before.Key)
			require.NoError(t, err, "an explicitly failed operation must leave the original token intact")

			// A retry with working storage must apply the mutation and stop the
			// old key on the real validation path, not just remove its cache hash.
			token = before
			require.NoError(t, mutation.apply(&token))
			_, err = ValidateUserToken(before.Key)
			require.ErrorIs(t, err, ErrTokenInvalid)
		})
	}
}

func TestBI10TokenMutationRollsBackOnDatabaseFailure(t *testing.T) {
	for _, mutation := range bi10TokenMutations() {
		t.Run(mutation.name, func(t *testing.T) {
			db := bi10TokenDB(t)
			bi10Cache(t)
			token := bi10Token(t, 101, mutation.name)
			before := bi10ReadToken(t, token.Id)
			require.NoError(t, cacheSetToken(token))
			// Soft deletion is an UPDATE. This trigger aborts all four writes
			// inside the database, without replacing the storage implementation.
			require.NoError(t, db.Exec(`CREATE TRIGGER bi10_reject_token_update
				BEFORE UPDATE ON tokens BEGIN SELECT RAISE(ABORT, 'BI10 injected database failure'); END`).Error)
			err := mutation.apply(&token)
			require.Error(t, err)
			require.Equal(t, before, bi10ReadToken(t, token.Id))
			require.NoError(t, db.Exec("DROP TRIGGER bi10_reject_token_update").Error)
			token = before
			require.NoError(t, mutation.apply(&token))
			_, err = ValidateUserToken(before.Key)
			require.ErrorIs(t, err, ErrTokenInvalid)
		})
	}
}

func TestBI10ExpiredTokenIsRejected(t *testing.T) {
	bi10TokenDB(t)
	bi10Cache(t)
	token := bi10Token(t, 101, "expired")
	token.ExpiredTime = time.Now().Add(-time.Minute).Unix()
	require.NoError(t, token.Update())
	_, err := ValidateUserToken(token.Key)
	require.ErrorIs(t, err, ErrTokenInvalid)
}
