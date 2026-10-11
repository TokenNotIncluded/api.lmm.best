//go:build rt11

// Copyright (C) 2026 LIghtJUNction
// SPDX-License-Identifier: AGPL-3.0-or-later
//
// RT-11: opt-in integration tests for main, not a replacement wallet model.
// Base: 2f4164978cf27f6e0e605b0589fbe631aef2c441; related audit: #711.
// Run ONLY through the supplied local runner. The database driver is real
// PostgreSQL; commit fault injection wraps real transactions, not SQL results.
// This file was formatted and parsed, but NOT compiled or business-tested in
// the authoring environment. See the accompanying execution report.

package model

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var rt11Injected = errors.New("RT11 injected local storage/acknowledgement failure")
var rt11Sequence atomic.Int64

type rt11Marker struct {
	Nonce       string `json:"nonce"`
	Root        string `json:"root"`
	PostgresDSN string `json:"postgres_dsn"`
	RedisSocket string `json:"redis_socket"`
	ParentNetNS string `json:"parent_netns"`
}

// A fresh, owner-only directory and a different network namespace are required.
// Merely pointing TEST_POSTGRES_DSN at localhost is not sufficient authorization.
func rt11RequireLocalFixture(t *testing.T) rt11Marker {
	t.Helper()
	root := os.Getenv("RT11_FIXTURE_ROOT")
	require.NotEmpty(t, root, "use tools/run_rt11.py; do not supply an existing database")
	require.True(t, filepath.IsAbs(root))
	resolved, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	require.Equal(t, root, resolved)
	info, err := os.Stat(root)
	require.NoError(t, err)
	require.True(t, info.IsDir())
	require.Zero(t, info.Mode().Perm()&0077, "fixture directory must be private")
	raw, err := os.ReadFile(filepath.Join(root, "fixture.json"))
	require.NoError(t, err)
	var marker rt11Marker
	require.NoError(t, json.Unmarshal(raw, &marker))
	require.Equal(t, root, marker.Root)
	require.GreaterOrEqual(t, len(marker.Nonce), 32)
	require.Equal(t, marker.Nonce, os.Getenv("RT11_FIXTURE_NONCE"))
	require.Equal(t, marker.PostgresDSN, os.Getenv("TEST_POSTGRES_DSN"))
	require.Equal(t, "1", os.Getenv("TEST_POSTGRES_ISOLATED_SCHEMA"))
	dsn, err := url.Parse(marker.PostgresDSN)
	require.NoError(t, err)
	require.Equal(t, "postgresql", dsn.Scheme)
	require.Empty(t, dsn.Host)
	require.Equal(t, filepath.Join(root, "pg-socket"), dsn.Query().Get("host"))
	require.Equal(t, filepath.Join(root, "redis.sock"), marker.RedisSocket)
	netNS, err := os.Readlink("/proc/self/ns/net")
	require.NoError(t, err)
	require.NotEmpty(t, marker.ParentNetNS)
	require.NotEqual(t, marker.ParentNetNS, netNS, "outbound network must be isolated")
	return marker
}

type rt11Actors struct{ A, B, C, Root User }

func rt11Users(t *testing.T, a, b, c int) rt11Actors {
	t.Helper()
	serial := rt11Sequence.Add(1)
	makeUser := func(suffix string, quota, role int) User {
		name := fmt.Sprintf("rt11-%d-%s", serial, suffix)
		user := User{Username: name, AffCode: name, Quota: quota,
			Role: role, Status: common.UserStatusEnabled, AuthVersion: 1}
		require.NoError(t, DB.Create(&user).Error)
		return user
	}
	return rt11Actors{makeUser("a", a, common.RoleCommonUser),
		makeUser("b", b, common.RoleCommonUser), makeUser("c", c, common.RoleCommonUser),
		makeUser("root", 0, common.RoleRootUser)}
}
func (f rt11Actors) ids() []int { return []int{f.A.Id, f.B.Id, f.C.Id, f.Root.Id} }

type rt11TransferRow struct {
	Id          int    `json:"id"`
	SenderID    int    `json:"sender_id"`
	RecipientID int    `json:"recipient_id"`
	Quota       int64  `json:"quota"`
	Status      string `json:"status"`
}
type rt11Snapshot struct {
	Wallets   map[string]int64  `json:"wallets_quota"`
	Affiliate map[string]int64  `json:"affiliate_signed_quota"`
	Pending   int64             `json:"pending_transfers_quota"`
	FeeHeld   int64             `json:"store_fee_held_quota"`
	Transfers []rt11TransferRow `json:"transfers_without_tokens"`
	Total     int64             `json:"wallet_plus_pending_plus_fee_hold"`
}

// The selected fixtures have no affiliate awards or subscription grants. Those
// are asserted absent, not silently added as cash. A store balance sale is
// already in wallets; its face price and refund reservations are NOT assets.
func rt11SnapshotOf(t *testing.T, f rt11Actors, stage string) rt11Snapshot {
	t.Helper()
	s := rt11Snapshot{Wallets: map[string]int64{}, Affiliate: map[string]int64{}}
	for label, id := range map[string]int{"A": f.A.Id, "B": f.B.Id, "C": f.C.Id, "fee_recipient": f.Root.Id} {
		var user User
		require.NoError(t, DB.First(&user, id).Error)
		s.Wallets[label], s.Affiliate[label] = int64(user.Quota), int64(user.AffQuota)
		require.GreaterOrEqual(t, user.Quota, 0)
		require.Zero(t, user.AffQuota, "reward-bearing cases need their own signed grant/debt equation")
		s.Total += int64(user.Quota)
	}
	require.NoError(t, DB.Model(&WalletTransfer{}).
		Select("id,sender_id,recipient_id,quota,status").
		Where("sender_id IN ?", f.ids()).Order("id").Find(&s.Transfers).Error)
	for _, row := range s.Transfers {
		if row.Status == "pending" {
			s.Pending += row.Quota
		}
	}
	if DB.Migrator().HasTable(&MerchantStoreOrder{}) {
		require.NoError(t, DB.Model(&MerchantStoreOrder{}).
			Where("seller_id IN ? AND fee_held = ?", f.ids(), true).
			Select("COALESCE(SUM(fee_quota),0)").Scan(&s.FeeHeld).Error)
	}
	s.Total += s.Pending + s.FeeHeld
	evidence, err := json.Marshal(struct {
		Stage    string       `json:"stage"`
		Snapshot rt11Snapshot `json:"snapshot"`
	}{stage, s})
	require.NoError(t, err)
	t.Log(string(evidence))
	return s
}
func rt11Total(t *testing.T, f rt11Actors, stage string, expected int64) rt11Snapshot {
	t.Helper()
	s := rt11SnapshotOf(t, f, stage)
	require.Equal(t, expected, s.Total, "internal quota equation; not a provider cash assertion")
	return s
}
func rt11Race(left, right func() error) (error, error) {
	var ready, finished sync.WaitGroup
	ready.Add(2)
	finished.Add(2)
	start := make(chan struct{})
	var a, b error
	go func() { defer finished.Done(); ready.Done(); <-start; a = left() }()
	go func() { defer finished.Done(); ready.Done(); <-start; b = right() }()
	ready.Wait()
	close(start)
	finished.Wait()
	return a, b
}
func rt11Move(t *testing.T, from, to, quota int, key string) *WalletTransfer {
	t.Helper()
	transfer, err := CreateWalletTransfer(from, quota, key)
	require.NoError(t, err)
	_, err = ClaimWalletTransfer(transfer.Token, to)
	require.NoError(t, err)
	return transfer
}

func rt11SeedSettledTopUp(t *testing.T, f rt11Actors, suffix string) TopUp {
	t.Helper()
	// This is an explicit already-settled starting fixture. It does NOT verify
	// a recharge endpoint, a gateway signature, or an external refund request.
	order := TopUp{UserId: f.A.Id, TradeNo: fmt.Sprintf("rt11-%d-%s", f.A.Id, suffix),
		Status: common.TopUpStatusSuccess, CreditedQuota: 1000,
		SettledAmountMicros: 1_000_000, ExpectedAmountMicros: 1_000_000,
		SettlementCurrency: "USD", Money: 1,
		PaymentMethod: PaymentMethodStripe, PaymentProvider: PaymentProviderStripe}
	require.NoError(t, DB.Create(&order).Error)
	return order
}
func rt11Refund(order TopUp, event string) (PaymentRefundResult, error) {
	return ApplyPaymentRefund(order.TradeNo, false, 1_000_000, "USD", event,
		PaymentMethodStripe, PaymentProviderStripe, "RT11 local verified-event boundary", order.UserId)
}
func rt11RefundState(t *testing.T, order TopUp, quota, cash, count int64) {
	t.Helper()
	var stored TopUp
	require.NoError(t, DB.First(&stored, order.Id).Error)
	require.Equal(t, quota, stored.RefundedQuota)
	require.Equal(t, cash, stored.RefundedAmountMicros)
	var entries []FinanceLedgerEntry
	require.NoError(t, DB.Where("user_id = ? AND source_type = ?", order.UserId, FinanceSourceRefund).Find(&entries).Error)
	require.Len(t, entries, int(count))
	var total int64
	for _, row := range entries {
		total += row.AmountMicros
		require.Equal(t, "USD", row.Currency)
	}
	require.Equal(t, cash, total)
	t.Logf("order_id=%d refunded_quota=%d refunded_cash_micros=%d ledger_rows=%d", stored.Id, quota, cash, count)
}

// The wrapper always delegates SQL to a real *sql.DB/*sql.Tx. Only the point at
// which the caller receives a failure changes. No balance or query is faked.
type rt11Pool struct {
	*sql.DB
	next atomic.Int32
}

func (p *rt11Pool) GetDBConn() (*sql.DB, error) { return p.DB, nil }
func (p *rt11Pool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := p.DB.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &rt11Tx{Tx: tx, pool: p}, nil
}

type rt11Tx struct {
	*sql.Tx
	pool *rt11Pool
}

func (tx *rt11Tx) Commit() error {
	switch tx.pool.next.Swap(0) {
	case 1:
		if err := tx.Tx.Rollback(); err != nil {
			return err
		}
		return rt11Injected
	case 2:
		if err := tx.Tx.Commit(); err != nil {
			return err
		}
		return rt11Injected // Durable commit; acknowledgement lost.
	default:
		return tx.Tx.Commit()
	}
}

var _ gorm.ConnPoolBeginner = (*rt11Pool)(nil)
var _ gorm.Tx = (*rt11Tx)(nil)

func rt11WithCommitFault(t *testing.T, db *gorm.DB, mode int32, operation func() error) error {
	t.Helper()
	raw, err := db.DB()
	require.NoError(t, err)
	pool := &rt11Pool{DB: raw}
	pool.next.Store(mode)
	wrapped, err := gorm.Open(postgres.New(postgres.Config{Conn: pool, PreferSimpleProtocol: true}),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	previous := DB
	DB = wrapped
	defer func() { DB = previous }()
	err = operation()
	require.Zero(t, pool.next.Load(), "fault must reach the real commit boundary")
	return err
}

func rt11StatementFault(t *testing.T, db *gorm.DB, kind, table string) (func(), *atomic.Bool) {
	t.Helper()
	fired := new(atomic.Bool)
	name := "rt11:once:" + kind + ":" + table
	callback := func(tx *gorm.DB) {
		if tx.Error == nil && tx.Statement.Table == table && fired.CompareAndSwap(false, true) {
			tx.AddError(rt11Injected)
		}
	}
	var remove func() error
	if kind == "create" {
		require.NoError(t, db.Callback().Create().After("gorm:create").Register(name, callback))
		remove = func() error { return db.Callback().Create().Remove(name) }
	} else {
		require.NoError(t, db.Callback().Update().After("gorm:update").Register(name, callback))
		remove = func() error { return db.Callback().Update().Remove(name) }
	}
	var once sync.Once
	stop := func() { once.Do(func() { require.NoError(t, remove()) }) }
	t.Cleanup(stop)
	return stop, fired
}

func TestRT11MoneyComposition(t *testing.T) {
	marker := rt11RequireLocalFixture(t)
	db := openIsolatedPostgresCacheTestDB(t, &User{}, &WalletTransfer{}, &TopUp{}, &FinanceLedgerEntry{},
		&SubscriptionOrder{}, &RedPacket{}, &RedPacketItem{}, &RedPacketClaim{}, &Redemption{}, &DiscountCode{}, &Log{})
	previousDB, previousLog := DB, LOG_DB
	db = db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
	DB, LOG_DB = db, db
	t.Cleanup(func() { DB, LOG_DB = previousDB, previousLog })
	usePostgresDatabaseType(t)
	common.RDB = redis.NewClient(&redis.Options{Network: "unix", Addr: marker.RedisSocket,
		DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second})
	require.NoError(t, common.RDB.Ping(context.Background()).Err())
	common.RedisEnabled = true
	// Dedicated test process only. Do not close/swap this client or flip the
	// flag while asynchronous invalidation workers can still be running.
	t.Run("claim-cancel-terminal-and-replay", func(t *testing.T) {
		for i := 0; i < 4; i++ {
			f := rt11Users(t, 1000, 0, 0)
			tr, err := CreateWalletTransfer(f.A.Id, 1000, "rt11-claim-cancel-key")
			require.NoError(t, err)
			rt11Total(t, f, "reserved", 1000)
			claimErr, cancelErr := rt11Race(func() error { _, e := ClaimWalletTransfer(tr.Token, f.B.Id); return e },
				func() error { return CancelWalletTransfer(tr.Id, f.A.Id) })
			require.NotEqual(t, claimErr == nil, cancelErr == nil, "one terminal transition must win")
			stored, err := InspectWalletTransfer(tr.Token)
			require.NoError(t, err)
			if stored.Status == "claimed" {
				require.NoError(t, claimErr)
				require.ErrorIs(t, cancelErr, ErrWalletTransferUnavailable)
				_, err = ClaimWalletTransfer(tr.Token, f.B.Id)
				require.NoError(t, err)
				require.ErrorIs(t, CancelWalletTransfer(tr.Id, f.A.Id), ErrWalletTransferUnavailable)
			} else {
				require.Equal(t, "cancelled", stored.Status)
				require.NoError(t, cancelErr)
				require.ErrorIs(t, claimErr, ErrWalletTransferUnavailable)
				require.NoError(t, CancelWalletTransfer(tr.Id, f.A.Id))
			}
			s := rt11Total(t, f, "recovered-with-original-transfer", 1000)
			require.Zero(t, s.Pending)
		}
	})
	t.Run("shared-wallet-two-creates", func(t *testing.T) {
		f := rt11Users(t, 1000, 0, 0)
		e1, e2 := rt11Race(func() error { _, e := CreateWalletTransfer(f.A.Id, 700, "rt11-two-creates-one"); return e },
			func() error { _, e := CreateWalletTransfer(f.A.Id, 700, "rt11-two-creates-two"); return e })
		require.NotEqual(t, e1 == nil, e2 == nil)
		s := rt11Total(t, f, "two-competing-debits", 1000)
		require.Len(t, s.Transfers, 1)
		require.EqualValues(t, 700, s.Pending)
	})
	t.Run("refund-versus-transfer-local-boundary", func(t *testing.T) {
		for i := 0; i < 4; i++ {
			f := rt11Users(t, 1000, 0, 0)
			order := rt11SeedSettledTopUp(t, f, "race")
			event := fmt.Sprintf("rt11-refund-%d", f.A.Id)
			var tr *WalletTransfer
			transferErr, refundErr := rt11Race(func() error {
				var e error
				tr, e = CreateWalletTransfer(f.A.Id, 1000, "rt11-refund-race-key")
				return e
			},
				func() error { _, e := rt11Refund(order, event); return e })
			require.NotEqual(t, transferErr == nil, refundErr == nil)
			if transferErr == nil {
				require.ErrorIs(t, refundErr, ErrRefundWalletQuotaInsufficient)
				rt11Total(t, f, "transfer-won-local-refund-rejected", 1000)
				rt11RefundState(t, order, 0, 0, 0)
				require.NoError(t, CancelWalletTransfer(tr.Id, f.A.Id))
				_, err := rt11Refund(order, event)
				require.NoError(t, err)
			}
			replay, err := rt11Refund(order, event)
			require.NoError(t, err)
			require.False(t, replay.Created)
			require.Zero(t, replay.QuotaDebited)
			rt11Total(t, f, "same-event-recovered", 0)
			rt11RefundState(t, order, 1000, 1_000_000, 1)
		}
	})
	t.Run("claimed-funds-return-through-B-C-A", func(t *testing.T) {
		f := rt11Users(t, 1000, 0, 0)
		order := rt11SeedSettledTopUp(t, f, "chain")
		event := fmt.Sprintf("rt11-chain-refund-%d", f.A.Id)
		rt11Move(t, f.A.Id, f.B.Id, 1000, "rt11-original-a-to-b")
		rt11Total(t, f, "A-to-B-claimed", 1000)
		_, err := rt11Refund(order, event)
		require.ErrorIs(t, err, ErrRefundWalletQuotaInsufficient)
		rt11RefundState(t, order, 0, 0, 0)
		// These are genuine return transfers, not replacement IDs for the
		// original refund. No direct SQL refill and no new outside funding.
		rt11Move(t, f.B.Id, f.C.Id, 1000, "rt11-return-b-to-c")
		rt11Move(t, f.C.Id, f.A.Id, 1000, "rt11-return-c-to-a")
		rt11Total(t, f, "original-money-returned", 1000)
		_, err = rt11Refund(order, event)
		require.NoError(t, err)
		rt11Total(t, f, "original-event-recovered", 0)
		rt11RefundState(t, order, 1000, 1_000_000, 1)
	})
	t.Run("statement-boundary-rollback", func(t *testing.T) {
		for _, point := range []struct{ kind, table string }{{"update", "users"}, {"create", "wallet_transfers"}} {
			t.Run(point.table, func(t *testing.T) {
				f := rt11Users(t, 1000, 0, 0)
				stop, fired := rt11StatementFault(t, db, point.kind, point.table)
				_, err := CreateWalletTransfer(f.A.Id, 300, "rt11-storage-boundary")
				stop()
				require.True(t, fired.Load())
				require.ErrorIs(t, err, rt11Injected)
				s := rt11Total(t, f, "failed-transaction", 1000)
				require.Empty(t, s.Transfers)
				require.EqualValues(t, 1000, s.Wallets["A"])
				tr, err := CreateWalletTransfer(f.A.Id, 300, "rt11-storage-boundary")
				require.NoError(t, err)
				retry, err := CreateWalletTransfer(f.A.Id, 300, "rt11-storage-boundary")
				require.NoError(t, err)
				require.Equal(t, tr.Id, retry.Id)
				s = rt11Total(t, f, "same-key-recovered", 1000)
				require.Len(t, s.Transfers, 1)
				require.EqualValues(t, 300, s.Pending)
			})
		}
		t.Run("refund-ledger", func(t *testing.T) {
			f := rt11Users(t, 1000, 0, 0)
			order := rt11SeedSettledTopUp(t, f, "ledger")
			event := fmt.Sprintf("rt11-ledger-refund-%d", f.A.Id)
			stop, fired := rt11StatementFault(t, db, "create", "finance_ledger_entries")
			_, err := rt11Refund(order, event)
			stop()
			require.True(t, fired.Load())
			require.ErrorIs(t, err, rt11Injected)
			rt11Total(t, f, "ledger-write-rolled-back", 1000)
			rt11RefundState(t, order, 0, 0, 0)
			_, err = rt11Refund(order, event)
			require.NoError(t, err)
			rt11Total(t, f, "ledger-retry-original-event", 0)
			rt11RefundState(t, order, 1000, 1_000_000, 1)
		})
	})
	t.Run("commit-outcomes-with-original-key", func(t *testing.T) {
		for _, mode := range []int32{1, 2} {
			t.Run(strconv.Itoa(int(mode)), func(t *testing.T) {
				f := rt11Users(t, 1000, 0, 0)
				warm, err := GetUserCache(f.A.Id)
				require.NoError(t, err)
				require.Equal(t, 1000, warm.Quota)
				err = rt11WithCommitFault(t, db, mode, func() error { _, e := CreateWalletTransfer(f.A.Id, 300, "rt11-commit-outcome-key"); return e })
				require.ErrorIs(t, err, rt11Injected)
				before := rt11Total(t, f, "commit-returned-error", 1000)
				if mode == 1 {
					require.Empty(t, before.Transfers)
				} else {
					require.Len(t, before.Transfers, 1)
				}
				tr, err := CreateWalletTransfer(f.A.Id, 300, "rt11-commit-outcome-key")
				require.NoError(t, err)
				if mode == 2 {
					require.Equal(t, before.Transfers[0].Id, tr.Id)
				}
				after := rt11Total(t, f, "recovered-original-key", 1000)
				require.Len(t, after.Transfers, 1)
				require.EqualValues(t, 700, after.Wallets["A"])
				// A distinct attempted spend checks the DB guard. It does not
				// replace the unknown operation's ID during recovery.
				_, err = CreateWalletTransfer(f.A.Id, 800, "rt11-distinct-spend-probe")
				require.Error(t, err)
				if mode == 2 {
					current, err := GetUserCache(f.A.Id)
					require.NoError(t, err)
					require.EqualValues(t, after.Wallets["A"], current.Quota,
						"lost-ack recovery must not keep pre-commit spendable quota in cache; a failure here alone is NOT proof of double spending")
				}
			})
		}
	})
	t.Run("terminal-commit-outcomes", func(t *testing.T) {
		for _, claim := range []bool{false, true} {
			for _, mode := range []int32{1, 2} {
				t.Run(fmt.Sprintf("claim-%t-mode-%d", claim, mode), func(t *testing.T) {
					f := rt11Users(t, 1000, 0, 0)
					tr, err := CreateWalletTransfer(f.A.Id, 300, "rt11-terminal-commit")
					require.NoError(t, err)
					operation := func() error {
						if claim {
							_, e := ClaimWalletTransfer(tr.Token, f.B.Id)
							return e
						}
						return CancelWalletTransfer(tr.Id, f.A.Id)
					}
					err = rt11WithCommitFault(t, db, mode, operation)
					require.ErrorIs(t, err, rt11Injected)
					state, err := InspectWalletTransfer(tr.Token)
					require.NoError(t, err)
					if mode == 1 {
						require.Equal(t, "pending", state.Status)
					}
					if mode == 2 && claim {
						require.Equal(t, "claimed", state.Status)
					}
					if mode == 2 && !claim {
						require.Equal(t, "cancelled", state.Status)
					}
					rt11Total(t, f, "terminal-commit-returned-error", 1000)
					require.NoError(t, operation())
					require.NoError(t, operation())
					after := rt11Total(t, f, "terminal-original-identifier-recovered", 1000)
					require.Zero(t, after.Pending)
					if claim {
						require.EqualValues(t, 700, after.Wallets["A"])
						require.EqualValues(t, 300, after.Wallets["B"])
					} else {
						require.EqualValues(t, 1000, after.Wallets["A"])
						require.Zero(t, after.Wallets["B"])
					}
				})
			}
		}
	})
	t.Run("lost-model-return-claim-cancel", func(t *testing.T) {
		for _, claim := range []bool{false, true} {
			f := rt11Users(t, 1000, 0, 0)
			tr, err := CreateWalletTransfer(f.A.Id, 300, "rt11-lost-model-return")
			require.NoError(t, err)
			for attempt := 0; attempt < 2; attempt++ {
				if claim {
					_, err = ClaimWalletTransfer(tr.Token, f.B.Id)
				} else {
					err = CancelWalletTransfer(tr.Id, f.A.Id)
				}
				require.NoError(t, err)
			}
			s := rt11Total(t, f, "same-terminal-operation-replayed", 1000)
			require.Zero(t, s.Pending)
			// This drops/ignores a model result, not an HTTP connection.
		}
	})
	t.Run("red-packet-claim-delete-redeem", rt11RedPacketComposition)
	t.Run("store-balance-composition", rt11StoreComposition)
}

func rt11RedPacketComposition(t *testing.T) {
	f := rt11Users(t, 1000, 0, 0)
	source := Redemption{UserId: f.Root.Id, Key: fmt.Sprintf("rt11-reward-%d", f.A.Id),
		Status: common.RedemptionCodeStatusEnabled, Name: "RT11 authorized promotional grant", Quota: 100,
		RewardType: RedemptionRewardQuota, CreatedTime: common.GetTimestamp()}
	require.NoError(t, DB.Create(&source).Error)
	packet := RedPacket{Title: "RT11 local packet", DrawMode: RedPacketDrawSequence, PerUserLimit: 1, Enabled: true, CreatedBy: f.Root.Id}
	inputs := []RedPacketItemInput{{ItemType: RedPacketItemRedemption, SourceId: source.Id, Weight: 1}}
	require.NoError(t, CreateRedPacket(&packet, inputs))
	rt11Total(t, f, "packet-created-no-wallet-hold", 1000)
	claimErr, deleteErr := rt11Race(func() error { _, e := ClaimRedPacket(packet.Slug, f.B.Id); return e }, func() error { return DeleteRedPacket(packet.Id) })
	require.NoError(t, deleteErr)
	next := RedPacket{Title: "RT11 released source", DrawMode: RedPacketDrawSequence, PerUserLimit: 1, Enabled: true, CreatedBy: f.Root.Id}
	if claimErr == nil {
		require.Error(t, CreateRedPacket(&next, inputs), "claimed source cannot be rebound")
	} else {
		require.ErrorIs(t, claimErr, ErrRedPacketNotFound)
		require.NoError(t, CreateRedPacket(&next, inputs))
		_, err := ClaimRedPacket(next.Slug, f.B.Id)
		require.NoError(t, err)
		packet = next
	}
	// Claim has no request_key. Recover a lost response by querying history;
	// never invent an idempotency header or blindly claim another reward.
	history, err := ListUserRedPacketClaims(packet.Slug, f.B.Id)
	require.NoError(t, err)
	require.Len(t, history, 1)
	again, err := ListUserRedPacketClaims(packet.Slug, f.B.Id)
	require.NoError(t, err)
	require.Len(t, again, 1)
	require.Equal(t, history[0].ClaimId, again[0].ClaimId)
	rt11Total(t, f, "claim-history-recovered-no-wallet-credit", 1000)
	e1, e2 := rt11Race(func() error { _, e := RedeemWithResult(history[0].Code, f.B.Id); return e }, func() error { _, e := RedeemWithResult(history[0].Code, f.C.Id); return e })
	require.NotEqual(t, e1 == nil, e2 == nil, "one source code can grant quota once")
	rt11Total(t, f, "one-authorized-grant-not-an-exploit", 1100)
}

func rt11StoreComposition(t *testing.T) {
	// Use main's schema bootstrap and public model operations, not the existing
	// SQLite newStoreFixture helper. No email address, pickup mail or worker.
	require.NoError(t, DB.AutoMigrate(append([]interface{}{&Option{}, &ModerationJob{}}, toolMarketModels()...)...))
	require.NoError(t, BootstrapMerchantStoreWriterGate(DB))
	require.NoError(t, DB.AutoMigrate(MerchantStoreModels()...))
	t.Setenv("MERCHANT_STORE_ENCRYPTION_KEY", "C5wmMzDh1QsVZb0saEW9ulAPzVN87Boqv3DK6eIrKXc2YLfg")
	for _, transferFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("transfer-first-%t", transferFirst), func(t *testing.T) {
			f := rt11Users(t, 500000, 5000, 0)
			require.NoError(t, SetMerchantStoreConfig(f.Root.Id, MerchantStoreConfig{FeeBPS: 100, RecipientID: f.Root.Id, PromotionQuota: 500000}))
			_, err := SaveMerchantStoreGateway(f.B.Id, "balance", true, "")
			require.NoError(t, err)
			p, err := SaveMerchantStoreProduct(f.B.Id, "", MerchantStoreProductInput{Title: "RT11 local item", PriceQuota: 500000, PaymentMethods: []string{"balance"}, PickupLoginRequired: true})
			require.NoError(t, err)
			require.NoError(t, SubmitMerchantStoreProduct(f.B.Id, p.ID))
			require.NoError(t, ReviewMerchantStoreProduct(f.Root.Id, p.ID, true, "RT11 local review"))
			_, err = AddMerchantStoreStock(f.B.Id, p.ID, []string{"RT11-NO-VALUE-TEST-ITEM"})
			require.NoError(t, err)
			require.NoError(t, AcceptMerchantStoreDisclaimer(f.A.Id, MerchantStoreDisclaimerVersion))
			in := MerchantStoreCheckoutInput{BuyerID: f.A.Id, ProductID: p.ID, Quantity: 1, RequestKey: "rt11-purchase-original", PaymentMethod: "balance"}
			rt11Total(t, f, "before-purchase", 505000)
			if transferFirst {
				stop, fired := rt11StatementFault(t, DB, "create", "merchant_store_transfers")
				_, _, failed := CreateMerchantStoreOrder(in)
				stop()
				require.True(t, fired.Load())
				require.ErrorIs(t, failed, rt11Injected)
				state := rt11Total(t, f, "store-ledger-failure-rolled-back", 505000)
				require.EqualValues(t, 500000, state.Wallets["A"])
				require.EqualValues(t, 5000, state.Wallets["B"])
				var rows int64
				require.NoError(t, DB.Model(&MerchantStoreOrder{}).Where("product_id = ?", p.ID).Count(&rows).Error)
				require.Zero(t, rows)
				require.NoError(t, DB.Model(&MerchantStoreTransfer{}).Where("from_user_id IN ? OR to_user_id IN ?", f.ids(), f.ids()).Count(&rows).Error)
				require.Zero(t, rows)
				require.NoError(t, DB.Model(&MerchantStoreStock{}).Where("product_id = ? AND state = ?", p.ID, "available").Count(&rows).Error)
				require.EqualValues(t, 1, rows)
			}
			order, created, err := CreateMerchantStoreOrder(in)
			require.NoError(t, err)
			require.True(t, created)
			require.Equal(t, "paid", order.Status)
			require.False(t, order.FeeHeld)
			after := rt11Total(t, f, "immediate-settlement-no-extra-receivable", 505000)
			require.EqualValues(t, 500000, after.Wallets["B"])
			require.EqualValues(t, 5000, after.Wallets["fee_recipient"])
			retry, made, err := CreateMerchantStoreOrder(in)
			require.NoError(t, err)
			require.False(t, made)
			require.Equal(t, order.ID, retry.ID)
			// Existing refund tests explicitly activate the reviewed writer
			// floor in the fixture. No production option is being changed.
			r := DB.Model(&Option{}).Where("key = ?", MerchantStoreWriterCapabilityOption).Update("value", "4")
			require.NoError(t, r.Error)
			require.EqualValues(t, 1, r.RowsAffected)
			request := MerchantStoreRefundInput{RequestKey: "rt11-store-refund-original", Mode: "full", Reason: "RT11 local product fault"}
			refund, err := RequestMerchantStoreRefund(f.A.Id, order.ID, request)
			require.NoError(t, err)
			approve := func() error {
				_, e := DecideMerchantStoreRefund(f.B.Id, order.ID, refund.ID, MerchantStoreRefundDecision{Decision: "approve"})
				return e
			}
			if transferFirst {
				rt11Move(t, f.B.Id, f.C.Id, 500000, "rt11-seller-to-c-first")
				require.ErrorIs(t, approve(), ErrMerchantStoreBalance)
				rt11Total(t, f, "seller-transfer-refund-rejected", 505000)
				rt11Move(t, f.C.Id, f.B.Id, 500000, "rt11-seller-return-from-c")
				require.NoError(t, approve())
			} else {
				var transfer *WalletTransfer
				te, re := rt11Race(func() error {
					var e error
					transfer, e = CreateWalletTransfer(f.B.Id, 500000, "rt11-seller-race-key")
					return e
				}, approve)
				require.NotEqual(t, te == nil, re == nil)
				if te == nil {
					require.ErrorIs(t, re, ErrMerchantStoreBalance)
					require.NoError(t, CancelWalletTransfer(transfer.Id, f.B.Id))
					require.NoError(t, approve())
				}
			}
			require.NoError(t, approve()) // Same refund ID, not a new refund.
			view, err := GetMerchantStoreRefunds(f.A.Id, order.ID)
			require.NoError(t, err)
			require.Equal(t, 500000, view.RefundedQuota)
			require.Zero(t, view.ReservedQuota)
			require.Zero(t, view.RemainingQuota)
			final := rt11Total(t, f, "refund-recovered-fee-retained", 505000)
			require.EqualValues(t, 500000, final.Wallets["A"])
			require.Zero(t, final.Wallets["B"])
			require.Zero(t, final.Wallets["C"])
			require.EqualValues(t, 5000, final.Wallets["fee_recipient"])
			var count int64
			require.NoError(t, DB.Model(&MerchantStoreTransfer{}).Where("order_id = ? AND kind = ?", order.ID, "refund").Count(&count).Error)
			require.EqualValues(t, 1, count)
			var ledger []MerchantStoreTransfer
			require.NoError(t, DB.Where("order_id = ?", order.ID).Order("kind").Find(&ledger).Error)
			require.Len(t, ledger, 3)
			expected := map[string]struct{ from, to, quota int }{
				"sale":   {f.A.Id, f.B.Id, 500000},
				"fee":    {f.B.Id, f.Root.Id, 5000},
				"refund": {f.B.Id, f.A.Id, 500000},
			}
			for _, row := range ledger {
				value, exists := expected[row.Kind]
				require.True(t, exists, "unexpected or duplicate ledger kind")
				require.Equal(t, value.from, row.FromUserID)
				require.Equal(t, value.to, row.ToUserID)
				require.Equal(t, value.quota, row.Quota)
				delete(expected, row.Kind)
			}
			require.Empty(t, expected)
			var stored MerchantStoreOrder
			require.NoError(t, DB.First(&stored, "id = ?", order.ID).Error)
			require.Equal(t, "refunded", stored.Status)
			require.NoError(t, DB.Model(&MerchantStoreEmailDelivery{}).Where("order_id = ?", order.ID).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}
