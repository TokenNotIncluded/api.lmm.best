package model

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	mysqlDriver "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// This requires a disposable MySQL server with CREATE/DROP DATABASE and PROCESS
// permission. Each run creates and removes only its own random database.
func marketDrawingMySQLDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("set TEST_MYSQL_DSN to run the real MySQL repeatable-read regression")
	}
	require.Equal(t, "1", os.Getenv("TEST_MYSQL_ISOLATED_DATABASE"), "acknowledge disposable database creation explicitly")
	config, err := mysqlDriver.ParseDSN(dsn)
	require.NoError(t, err)
	config.DBName = ""
	config.ParseTime = true
	config.Timeout, config.ReadTimeout, config.WriteTimeout = 5*time.Second, 20*time.Second, 20*time.Second
	open := func(config *mysqlDriver.Config) *gorm.DB {
		db, err := gorm.Open(mysql.Open(config.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		require.NoError(t, err)
		return db
	}
	admin := open(config)
	adminPool, err := admin.DB()
	require.NoError(t, err)
	name := fmt.Sprintf("lmm_market_drawing_%d_%d", os.Getpid(), time.Now().UnixNano())
	require.NoError(t, admin.Exec("CREATE DATABASE `"+name+"`").Error)
	t.Cleanup(func() {
		require.NoError(t, admin.Exec("DROP DATABASE IF EXISTS `"+name+"`").Error)
		require.NoError(t, adminPool.Close())
	})
	config.DBName = name
	db := open(config)
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(8)
	pool.SetMaxIdleConns(8)
	var version, isolation string
	require.NoError(t, db.Raw("SELECT VERSION(), @@transaction_isolation").Row().Scan(&version, &isolation))
	require.True(t, strings.HasPrefix(version, "8."), "this regression qualifies MySQL 8, not a substitute dialect")
	require.Equal(t, "REPEATABLE-READ", isolation)
	t.Logf("actual MySQL %s, isolation %s", version, isolation)
	oldDB, oldRedis := DB, common.RedisEnabled
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	DB, common.RedisEnabled = db, false
	common.SetDatabaseTypes(common.DatabaseTypeMySQL, oldLog)
	t.Cleanup(func() {
		DB, common.RedisEnabled = oldDB, oldRedis
		common.SetDatabaseTypes(oldMain, oldLog)
		require.NoError(t, pool.Close())
	})
	require.NoError(t, db.AutoMigrate(append([]interface{}{&User{}}, toolMarketModels()...)...))
	return db
}

func TestToolMarketDrawingExpiryReadsCommittedBillingMySQL(t *testing.T) {
	db := marketDrawingMySQLDB(t)
	user := marketTestUser(t, db, "mysql-drawing-buyer", 123456, common.RoleCommonUser)
	definition := ToolMarketBuiltinServiceInput{Key: "drawing", Name: "MySQL drawing fixture", Tools: []ToolMarketToolInput{{Name: "drawing.generate", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
	require.NoError(t, EnsureToolMarketBuiltinServices([]ToolMarketBuiltinServiceInput{definition}))
	detail, err := GetToolMarketDetail(user.Id, ToolMarketBuiltinServiceID("drawing"), false)
	require.NoError(t, err)
	require.Len(t, detail.Tools, 1)
	tool := detail.Tools[0]
	const client = "mysql-drawing-agent"
	require.NoError(t, SetToolMarketInstallation(user.Id, client, tool.ToolID, tool.VersionID, true))
	grant, err := CreateToolMarketGrant(user.Id, ToolMarketGrant{ClientID: client, ToolID: tool.ToolID, VersionID: tool.VersionID, MaxCalls: 1, ExpiresAt: common.GetTimestamp() + 3600})
	require.NoError(t, err)
	require.NoError(t, SetToolMarketBudget(user.Id, "account", "", 100))
	in := ToolMarketReserveInput{UserID: user.Id, ClientID: client, ToolID: tool.ToolID, VersionID: tool.VersionID, GrantID: grant.ID, RequestKey: "repeatable-read-expiry", Arguments: json.RawMessage(`{}`), ResolveBy: common.GetTimestamp() + 120}
	call, created, err := ReserveToolMarketCall(in)
	require.NoError(t, err)
	require.True(t, created)
	started, err := StartToolMarketCall(call.ID)
	require.NoError(t, err)
	require.True(t, started)
	payload := json.RawMessage(`{"content":[{"type":"text","text":"completed"}],"structuredContent":{"data":{"data":[{"url":"https://example.test/retained.png"}]}}}`)
	require.NoError(t, PrepareToolMarketBuiltinDrawingResult(call.ID, payload))
	require.NoError(t, db.Model(&ToolMarketCall{}).Where("id = ?", call.ID).Update("resolve_by", common.GetTimestamp()-1).Error)

	completeLocked := make(chan struct{})
	expirySnapshot := make(chan struct{})
	releaseComplete := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseComplete) }) }
	var completeConnection, expiryConnection atomic.Int64
	callback := "market-drawing-mysql-repeatable-read-barrier"
	require.NoError(t, db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Error != nil || tx.Statement.Table != "tool_market_calls" {
			return
		}
		var connection int64
		if err := tx.Statement.ConnPool.QueryRowContext(tx.Statement.Context, "SELECT CONNECTION_ID()").Scan(&connection); err != nil {
			tx.AddError(err)
			return
		}
		if _, locking := tx.Statement.Clauses["FOR"]; locking {
			if completeConnection.CompareAndSwap(0, connection) {
				close(completeLocked)
				select {
				case <-releaseComplete:
				case <-time.After(15 * time.Second):
					tx.AddError(fmt.Errorf("completion barrier timed out"))
				}
			}
		} else if completeConnection.Load() != 0 && connection != completeConnection.Load() && expiryConnection.CompareAndSwap(0, connection) {
			// marketCallTx's ordinary initial SELECT establishes the old RR
			// snapshot before this transaction waits for the service lock.
			close(expirySnapshot)
		}
	}))
	completeDone, expireDone := make(chan error, 1), make(chan error, 1)
	var workers sync.WaitGroup
	t.Cleanup(func() {
		release()
		workers.Wait()
		_ = db.Callback().Query().Remove(callback)
	})
	workers.Add(1)
	go func() {
		defer workers.Done()
		completeDone <- CompleteToolMarketBuiltinDrawingBilling(call.ID, true)
	}()
	select {
	case <-completeLocked:
	case err := <-completeDone:
		t.Fatalf("completion ended before taking its lock: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("completion did not reach its lock barrier")
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		expireDone <- ExpireToolMarketCall(call.ID)
	}()
	select {
	case <-expirySnapshot:
	case err := <-expireDone:
		t.Fatalf("expiry ended before creating its snapshot: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("expiry did not create its repeatable-read snapshot")
	}
	// Observe the actual InnoDB wait, rather than depending on a sleep or on
	// which goroutine happens to run first. Complete still owns the row locks.
	require.Eventually(t, func() bool {
		var waiting int64
		return db.Raw("SELECT COUNT(*) FROM information_schema.innodb_trx WHERE trx_mysql_thread_id = ? AND trx_state = 'LOCK WAIT'", expiryConnection.Load()).Scan(&waiting).Error == nil && waiting == 1
	}, 5*time.Second, 10*time.Millisecond, "expiry must be blocked behind completion after its old snapshot exists")
	t.Log("confirmed expiry's old snapshot and actual InnoDB LOCK WAIT before completing model billing")
	release()
	require.NoError(t, <-completeDone)
	require.NoError(t, <-expireDone)
	require.NoError(t, db.Callback().Query().Remove(callback))

	var outcome ToolMarketResult
	require.NoError(t, db.First(&outcome, "call_id = ?", call.ID).Error)
	require.False(t, outcome.BuiltinBillingPending, "the model billing completion committed before expiry continued")
	status, err := GetToolMarketCall(user.Id, client, call.ID)
	require.NoError(t, err)
	require.Equal(t, "succeeded", status.ExecutionStatus, "expiry must current-read the committed outcome, not its initial RR snapshot")
	require.Equal(t, "released", status.SettlementStatus)
	data, _, err := GetToolMarketResult(user.Id, client, call.ID)
	require.NoError(t, err)
	require.Equal(t, payload, data)
	var currentGrant ToolMarketGrant
	require.NoError(t, db.First(&currentGrant, "id = ?", grant.ID).Error)
	require.Zero(t, currentGrant.ReservedCalls)
	require.Zero(t, currentGrant.SuccessfulCalls, "expiry only releases the original authorization slot")
	require.Zero(t, currentGrant.SpentQuota)
	var budget ToolMarketBudget
	require.NoError(t, db.First(&budget, "user_id = ? AND scope = ?", user.Id, "account").Error)
	require.Zero(t, budget.ReservedQuota)
	require.Zero(t, budget.SpentQuota)
	var transfers int64
	require.NoError(t, db.Model(&ToolMarketTransfer{}).Count(&transfers).Error)
	require.Zero(t, transfers)
	require.Equal(t, user.Quota, marketTestBalance(t, db, user.Id))
	require.NoError(t, CompleteToolMarketBuiltinDrawingBilling(call.ID, true))
	count, err := RecoverToolMarketCalls(context.Background())
	require.NoError(t, err)
	require.Zero(t, count)
}
