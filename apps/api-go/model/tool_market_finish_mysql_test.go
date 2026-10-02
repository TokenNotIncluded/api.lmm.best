package model

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func marketPaidMySQLFixture(t *testing.T, maxCalls, grantLimit, budgetLimit int) marketFixture {
	t.Helper()
	db := marketDrawingMySQLDB(t)
	f := marketFixture{db: db, buyer: marketTestUser(t, db, "mysql-paid-buyer", 1000, common.RoleCommonUser), author: marketTestUser(t, db, "mysql-paid-author", 0, common.RoleCommonUser), root: marketTestUser(t, db, "mysql-paid-root", 0, common.RoleRootUser)}
	require.NoError(t, SetToolMarketConfig(f.root.Id, ToolMarketConfig{Enabled: true, FeeBPS: 1000, RecipientID: f.root.Id}))
	var err error
	f.service, err = SaveToolMarketDraft(f.author.Id, "", marketTestDraft(100))
	require.NoError(t, err)
	f.tool = marketTestPublish(t, db, f.root.Id, f.service)
	require.NoError(t, SetToolMarketInstallation(f.buyer.Id, "client-a", f.tool.ToolID, f.tool.VersionID, true))
	f.grant, err = CreateToolMarketGrant(f.buyer.Id, ToolMarketGrant{ClientID: "client-a", ToolID: f.tool.ToolID, VersionID: f.tool.VersionID, MaxPriceQuota: 100, MaxTotalQuota: grantLimit, MaxCalls: maxCalls, ExpiresAt: common.GetTimestamp() + 3600})
	require.NoError(t, err)
	for scope, id := range map[string]string{"account": "", "client": "client-a", "tool": f.tool.ToolID} {
		require.NoError(t, SetToolMarketBudget(f.buyer.Id, scope, id, budgetLimit))
	}
	return f
}

// Pause the first transaction after its row lock. The second does its initial
// ordinary read and then actually blocks on that lock. The server's default is
// RR; inspect InnoDB to prove each marketplace transaction uses RC instead.
func marketMySQLInterleave(t *testing.T, db *gorm.DB, initialTable, lockTable string, first, second func() error) (error, error) {
	t.Helper()
	firstLocked, secondRead, releaseFirst := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseFirst) }) }
	var firstConnection, secondConnection atomic.Int64
	callback := "market-mysql-transaction-barrier"
	require.NoError(t, db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Error != nil || (tx.Statement.Table != initialTable && tx.Statement.Table != lockTable) {
			return
		}
		var connection int64
		if err := tx.Statement.ConnPool.QueryRowContext(tx.Statement.Context, "SELECT CONNECTION_ID()").Scan(&connection); err != nil {
			tx.AddError(err)
			return
		}
		_, locking := tx.Statement.Clauses["FOR"]
		if locking && tx.Statement.Table == lockTable {
			if firstConnection.CompareAndSwap(0, connection) {
				close(firstLocked)
				select {
				case <-releaseFirst:
				case <-time.After(15 * time.Second):
					tx.AddError(fmt.Errorf("first marketplace transaction barrier timed out"))
				}
			}
		} else if !locking && tx.Statement.Table == initialTable && firstConnection.Load() != 0 && connection != firstConnection.Load() && secondConnection.CompareAndSwap(0, connection) {
			close(secondRead)
		}
	}))
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	var workers sync.WaitGroup
	t.Cleanup(func() {
		release()
		workers.Wait()
		_ = db.Callback().Query().Remove(callback)
	})
	workers.Add(1)
	go func() { defer workers.Done(); firstDone <- first() }()
	select {
	case <-firstLocked:
	case err := <-firstDone:
		t.Fatalf("first operation ended before its lock barrier: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("first operation did not reach its row lock")
	}
	workers.Add(1)
	go func() { defer workers.Done(); secondDone <- second() }()
	select {
	case <-secondRead:
	case err := <-secondDone:
		t.Fatalf("second operation ended before its initial read: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("second operation did not perform its initial read")
	}
	require.Eventually(t, func() bool {
		var waiting int64
		return db.Raw("SELECT COUNT(*) FROM information_schema.innodb_trx WHERE trx_mysql_thread_id = ? AND trx_state = 'LOCK WAIT'", secondConnection.Load()).Scan(&waiting).Error == nil && waiting == 1
	}, 5*time.Second, 10*time.Millisecond, "second operation must actually wait after its initial read")
	var firstIsolation, secondIsolation string
	require.NoError(t, db.Raw("SELECT trx_isolation_level FROM information_schema.innodb_trx WHERE trx_mysql_thread_id = ?", firstConnection.Load()).Scan(&firstIsolation).Error)
	require.NoError(t, db.Raw("SELECT trx_isolation_level FROM information_schema.innodb_trx WHERE trx_mysql_thread_id = ?", secondConnection.Load()).Scan(&secondIsolation).Error)
	t.Logf("confirmed InnoDB LOCK WAIT; actual transaction isolation: first=%s second=%s", firstIsolation, secondIsolation)
	release()
	firstErr, secondErr := <-firstDone, <-secondDone
	require.NoError(t, db.Callback().Query().Remove(callback))
	assert.Equal(t, "READ COMMITTED", firstIsolation)
	assert.Equal(t, "READ COMMITTED", secondIsolation)
	var sessionIsolation string
	require.NoError(t, db.Raw("SELECT @@transaction_isolation").Scan(&sessionIsolation).Error)
	require.Equal(t, "REPEATABLE-READ", sessionIsolation, "market isolation must not change the pool's default for unrelated transactions")
	return firstErr, secondErr
}

func TestToolMarketConcurrentFinishCountersMySQL(t *testing.T) {
	f := marketPaidMySQLFixture(t, 2, 200, 200)
	var calls [2]*ToolMarketCall
	for index := range calls {
		call, created, err := ReserveToolMarketCall(f.input(fmt.Sprintf("parallel-finish-%d", index)))
		require.NoError(t, err)
		require.True(t, created)
		started, err := StartToolMarketCall(call.ID)
		require.NoError(t, err)
		require.True(t, started)
		require.NoError(t, RecordToolMarketResult(call.ID, true, json.RawMessage(`{"text":"durable paid result"}`)))
		calls[index] = call
	}
	firstErr, secondErr := marketMySQLInterleave(t, f.db, "tool_market_calls", "tool_market_calls", func() error { return FinishToolMarketCall(calls[0].ID, true) }, func() error { return FinishToolMarketCall(calls[1].ID, true) })
	require.NoError(t, firstErr)
	require.NoError(t, secondErr)
	for _, call := range calls {
		var status ToolMarketCall
		require.NoError(t, f.db.First(&status, "id = ?", call.ID).Error)
		assert.Equal(t, "succeeded", status.ExecutionStatus)
		assert.Equal(t, "settled", status.SettlementStatus)
	}
	var grant ToolMarketGrant
	require.NoError(t, f.db.First(&grant, "id = ?", f.grant.ID).Error)
	assert.Equal(t, 2, grant.SuccessfulCalls)
	assert.Zero(t, grant.ReservedCalls)
	assert.Equal(t, 200, grant.SpentQuota)
	assert.Zero(t, grant.ReservedQuota)
	var budgets []ToolMarketBudget
	require.NoError(t, f.db.Where("user_id = ?", f.buyer.Id).Order("scope, scope_id").Find(&budgets).Error)
	require.Len(t, budgets, 3)
	for _, budget := range budgets {
		assert.Equal(t, 200, budget.SpentQuota, "both completions belong in the "+budget.Scope+" budget")
		assert.Zero(t, budget.ReservedQuota, "settled calls must not leave a phantom "+budget.Scope+" hold")
	}
	assert.Equal(t, 800, marketTestBalance(t, f.db, f.buyer.Id))
	assert.Equal(t, 180, marketTestBalance(t, f.db, f.author.Id))
	assert.Equal(t, 20, marketTestBalance(t, f.db, f.root.Id))
	var ledger struct {
		Count int
		Quota int
	}
	require.NoError(t, f.db.Model(&ToolMarketTransfer{}).Select("COUNT(*) AS count, SUM(quota) AS quota").Scan(&ledger).Error)
	assert.Equal(t, 4, ledger.Count)
	assert.Equal(t, 200, ledger.Quota)
}

func TestToolMarketConcurrentReserveLimitsMySQL(t *testing.T) {
	for _, limits := range []struct {
		name                    string
		maxCalls, grant, budget int
	}{
		{"grant-limit", 1, 100, 200},
		{"budget-limit", 2, 200, 100},
	} {
		t.Run(limits.name, func(t *testing.T) {
			f := marketPaidMySQLFixture(t, limits.maxCalls, limits.grant, limits.budget)
			var firstCreated, secondCreated bool
			firstErr, secondErr := marketMySQLInterleave(t, f.db, "tool_market_tools", "tool_market_services", func() error {
				_, created, err := ReserveToolMarketCall(f.input("parallel-reserve-first"))
				firstCreated = created
				return err
			}, func() error {
				_, created, err := ReserveToolMarketCall(f.input("parallel-reserve-second"))
				secondCreated = created
				return err
			})
			require.NoError(t, firstErr)
			assert.ErrorIs(t, secondErr, ErrToolMarketBudget)
			assert.True(t, firstCreated)
			assert.False(t, secondCreated)
			var calls int64
			require.NoError(t, f.db.Model(&ToolMarketCall{}).Count(&calls).Error)
			assert.EqualValues(t, 1, calls)
			assert.Equal(t, 900, marketTestBalance(t, f.db, f.buyer.Id))
			assert.Zero(t, marketTestBalance(t, f.db, f.author.Id))
			assert.Zero(t, marketTestBalance(t, f.db, f.root.Id))
			var grant ToolMarketGrant
			require.NoError(t, f.db.First(&grant, "id = ?", f.grant.ID).Error)
			assert.Equal(t, 1, grant.ReservedCalls)
			assert.Equal(t, 100, grant.ReservedQuota)
			assert.Zero(t, grant.SuccessfulCalls)
			assert.Zero(t, grant.SpentQuota)
			var budgets []ToolMarketBudget
			require.NoError(t, f.db.Where("user_id = ?", f.buyer.Id).Order("scope, scope_id").Find(&budgets).Error)
			require.Len(t, budgets, 3)
			for _, budget := range budgets {
				assert.Equal(t, 100, budget.ReservedQuota, "only one hold belongs in the "+budget.Scope+" budget")
				assert.Zero(t, budget.SpentQuota)
			}
			var transfers int64
			require.NoError(t, f.db.Model(&ToolMarketTransfer{}).Count(&transfers).Error)
			assert.Zero(t, transfers)
		})
	}
}
