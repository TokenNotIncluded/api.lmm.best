package controller

import (
	"encoding/json"
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestUsageProjectionSnapshotKeepsConcurrentHistoricalRefundConsistent(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "usage-snapshot.sqlite")), &gorm.Config{})
	require.NoError(t, err)
	var mode string
	require.NoError(t, db.Raw("PRAGMA journal_mode=WAL").Scan(&mode).Error)
	require.Equal(t, "wal", strings.ToLower(mode))
	testUsageSnapshotConcurrentRefund(t, db)
}

func TestUsageProjectionSnapshotPostgresKeepsConcurrentHistoricalRefundConsistent(t *testing.T) {
	dsn := os.Getenv("LMM_INCIDENT343_TEST_DSN")
	if dsn == "" {
		t.Skip("requires disposable loopback PostgreSQL test database")
	}
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1", parsed.Hostname(), "never connect this write fixture to production")
	require.NotNil(t, parsed.User)
	require.True(t, strings.HasPrefix(parsed.User.Username(), "lmm_test_"))
	require.True(t, strings.HasPrefix(strings.TrimPrefix(parsed.Path, "/"), "lmm_test_"))
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	schema := "lmm_test_usage_snapshot_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
	t.Cleanup(func() {
		require.NoError(t, admin.Exec("DROP SCHEMA "+schema+" CASCADE").Error)
		pool, _ := admin.DB()
		_ = pool.Close()
	})
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(parsed.String()), &gorm.Config{})
	require.NoError(t, err)
	testUsageSnapshotConcurrentRefund(t, db)
}

func testUsageSnapshotConcurrentRefund(t *testing.T, db *gorm.DB) {
	t.Helper()
	installIdentityCurrencyFixture(t)
	oldDB, oldRedis := model.DB, common.RedisEnabled
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	common.RedisEnabled = false
	common.SetDatabaseTypes(common.DatabaseType(db.Dialector.Name()), common.DatabaseType(db.Dialector.Name()))
	model.DB = db
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(5)
	t.Cleanup(func() {
		model.DB = oldDB
		common.RedisEnabled = oldRedis
		common.SetDatabaseTypes(oldMain, oldLog)
		_ = sqlDB.Close()
	})
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.ViolationFeeRecord{}, &model.ViolationFeeAppeal{}, &model.TopUp{}))
	installPublicCreditBoundaryFixture(t, "500000", db)
	user := model.User{Id: 1, Username: "snapshot-refund", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Quota: 500000, UsedQuota: 13420756}
	require.NoError(t, db.Create(&user).Error)
	fee := model.ViolationFeeRecord{ID: 7, UserID: user.Id, RequestID: "snapshot-refund", ChargedQuota: 6710363, CreatedAt: 900, Status: model.ViolationFeeRecordStatusCharged, ErrorCode: "legacy.violation"}
	require.NoError(t, db.Create(&fee).Error)
	plan := map[string]any{"version": 1, "kind": "offline_credit_balance_rebase_preview", "migration_id": "snapshot-refund", "user_ids": []int{user.Id}, "has_complete_history": true, "include_other_rights": true, "usd_credit_conversion": 500000, "divisor": "6.710363", "rounding": "half-away-from-zero", "snapshot_at": 1000, "user_sources": []map[string]any{{"id": user.Id, "used_quota": 13420726}}, "token_sources": []any{}, "other_credit_bases": []map[string]any{{"kind": "violation_fee_refund", "source_id": strconv.FormatUint(uint64(fee.ID), 10), "user_id": user.Id, "original_quota": 6710363, "rebased_quota": 1000000, "source": fee}}}
	raw, err := json.Marshal(plan)
	require.NoError(t, err)
	require.NoError(t, db.Exec("CREATE TABLE wallet_credit_rebases (plan TEXT NOT NULL)").Error)
	require.NoError(t, db.Exec("INSERT INTO wallet_credit_rebases(plan) VALUES (?)", string(raw)).Error)
	appeal, err := model.SubmitViolationFeeAppeal(user.Id, fee.ID, "fixture historical fee appeal")
	require.NoError(t, err)
	var checkpoint atomic.Bool
	refundDone := make(chan error, 1)
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("concurrent-historical-refund", func(tx *gorm.DB) {
		// Pause the real counter SELECT; refund commits on another connection using
		// the normal owner, before immutable audit/fee reads in the pinned snapshot.
		if tx.Statement.Table != "users" || len(tx.Statement.Selects) != 3 || tx.Statement.Selects[2] != "used_quota" || !checkpoint.CompareAndSwap(false, true) {
			return
		}
		if db.Dialector.Name() == "postgres" {
			var readonly, isolation string
			if err := tx.Raw("SHOW transaction_read_only").Scan(&readonly).Error; err != nil {
				tx.AddError(err)
				return
			}
			if err := tx.Raw("SHOW transaction_isolation").Scan(&isolation).Error; err != nil {
				tx.AddError(err)
				return
			}
			if readonly != "on" || isolation != "repeatable read" {
				tx.AddError(assertSnapshotIsolation{})
				return
			}
		}
		go func() {
			_, err := model.ReviewViolationFeeAppeal(99, appeal.ID, true, "fixture concurrent refund")
			refundDone <- err
		}()
		select {
		case err := <-refundDone:
			if err != nil {
				tx.AddError(err)
			}
		case <-time.After(5 * time.Second):
			tx.AddError(assertSnapshotTimeout{})
		}
	}))
	t.Cleanup(func() { db.Callback().Query().Remove("concurrent-historical-refund") })
	c, w := publicCreditTestContext(t, http.MethodGet, "/api/user/self", "", user.Id)
	GetSelf(c)
	data := publicCreditResponseData(t, w)
	require.True(t, checkpoint.Load(), "actual refund must land between counter and audit reads")
	require.Equal(t, true, data["usage_projection_available"])
	require.EqualValues(t, 13420756, data["used_quota"], "raw must share the pre-refund snapshot")
	require.EqualValues(t, 2000030, data["normalized_used_quota"])
	require.Equal(t, "2000030", data["public_credit_used"])
	require.EqualValues(t, 500000, data["quota"])
	// Prior split-read behavior with the same real committed refund inflates usage.
	projector, err := model.LoadUsageProjector(db)
	require.NoError(t, err)
	mixed, err := projector.User(user.Id, user.UsedQuota)
	require.NoError(t, err)
	require.Equal(t, 7710393, mixed.NormalizedUsedQuota)
	snapshot := loadUsageSnapshot([]*model.User{&user}, nil)
	current, p := snapshot.user(&user)
	normalized := projectUserUsage(p, current)
	require.True(t, normalized.UsageProjectionAvailable)
	require.Equal(t, 6710393, current.UsedQuota)
	require.Equal(t, 1500000, current.Quota)
	require.Equal(t, 1000030, *normalized.NormalizedUsedQuota)
	var stored model.ViolationFeeRecord
	require.NoError(t, db.First(&stored, fee.ID).Error)
	require.Equal(t, model.ViolationFeeRecordStatusReversed, stored.Status)
	require.Equal(t, 6710363, stored.ChargedQuota, "historical charge remains immutable")
}

type assertSnapshotTimeout struct{}

func (assertSnapshotTimeout) Error() string {
	return "concurrent refund did not commit before snapshot audit read"
}

type assertSnapshotIsolation struct{}

func (assertSnapshotIsolation) Error() string {
	return "usage snapshot must enforce read-only repeatable read"
}
