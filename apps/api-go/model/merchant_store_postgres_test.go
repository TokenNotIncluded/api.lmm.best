package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// These tests never fall back to the production DSN, Unix sockets, DNS hostnames
// or existing namespaces. The operator must explicitly supply a loopback URL.
func merchantStoreLocalPostgresDSN(raw string) (string, error) {
	u, e := url.Parse(raw)
	if e != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() == "" || u.Path == "" || u.Path == "/" {
		return "", errors.New("merchant PostgreSQL test requires a loopback PostgreSQL URL with database name")
	}
	if ip := net.ParseIP(u.Hostname()); ip == nil || !ip.IsLoopback() {
		return "", errors.New("merchant PostgreSQL test DSN must use a literal loopback IP")
	}
	cfg, e := pgx.ParseConfig(raw)
	if e != nil {
		return "", errors.New("invalid merchant PostgreSQL test configuration")
	}
	hosts := []string{cfg.Host}
	for _, fallback := range cfg.Fallbacks {
		hosts = append(hosts, fallback.Host)
	}
	for _, host := range hosts {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return "", errors.New("merchant PostgreSQL test configuration contains a non-loopback endpoint")
		}
	}
	return u.String(), nil
}
func TestMerchantStorePostgresDSNGuard(t *testing.T) {
	for _, dsn := range []string{"postgres://u@10.0.0.1/db", "postgres://u@database.example/db", "postgres://u@localhost/db", "host=127.0.0.1 dbname=test", "postgres://u@127.0.0.1/", "postgres://u@127.0.0.1/db?host=10.0.0.1"} {
		_, e := merchantStoreLocalPostgresDSN(dsn)
		require.Error(t, e)
	}
	for _, dsn := range []string{"postgres://u@127.0.0.1:25599/store_test?sslmode=disable", "postgres://u@[::1]:25599/store_test?sslmode=disable"} {
		_, e := merchantStoreLocalPostgresDSN(dsn)
		require.NoError(t, e)
	}
}
func merchantStorePGFingerprint(t *testing.T, db *gorm.DB, schemaName string) string {
	t.Helper()
	parts := []string{}
	for _, query := range []string{
		`SELECT row_to_json(u)::text FROM users AS u WHERE username = 'legacy-fixture'`,
		`SELECT row_to_json(w)::text FROM wallet_transfers AS w WHERE request_key = 'legacy-store-test-fixture'`,
		`SELECT COALESCE(string_agg(format('%s:%s:%s:%s:%s', c.relname,a.attname,format_type(a.atttypid,a.atttypmod),a.attnotnull,COALESCE(pg_get_expr(d.adbin,d.adrelid),'')), E'\n' ORDER BY c.relname,a.attnum),'') FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped LEFT JOIN pg_attrdef d ON d.adrelid=c.oid AND d.adnum=a.attnum WHERE n.nspname=? AND c.relname IN ('users','wallet_transfers')`,
		`SELECT COALESCE(string_agg(indexdef, E'\n' ORDER BY tablename,indexname),'') FROM pg_indexes WHERE schemaname=? AND tablename IN ('users','wallet_transfers')`,
		`SELECT COALESCE(string_agg(pg_get_constraintdef(k.oid), E'\n' ORDER BY c.relname,k.conname),'') FROM pg_constraint k JOIN pg_class c ON c.oid=k.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=? AND c.relname IN ('users','wallet_transfers')`,
	} {
		var value string
		args := []any{}
		if strings.Contains(query, "nspname=?") || strings.Contains(query, "schemaname=?") {
			args = append(args, schemaName)
		}
		require.NoError(t, db.Raw(query, args...).Scan(&value).Error)
		parts = append(parts, value)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}
func merchantStorePGTables(t *testing.T, db *gorm.DB, schemaName string) []string {
	t.Helper()
	var names []string
	require.NoError(t, db.Raw("SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=? AND c.relkind IN ('r','p') ORDER BY c.relname", schemaName).Scan(&names).Error)
	return names
}
func merchantStorePGDB(t *testing.T) (*gorm.DB, string, *gorm.DB, string) {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("MERCHANT_STORE_POSTGRES_TEST_DSN"))
	if raw == "" {
		t.Skip("set MERCHANT_STORE_POSTGRES_TEST_DSN to a disposable literal-loopback PostgreSQL URL to run merchant database concurrency tests")
	}
	dsn, e := merchantStoreLocalPostgresDSN(raw)
	require.NoError(t, e)
	cfg := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	base, e := gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), cfg)
	require.NoError(t, e, "connect explicitly selected local PostgreSQL test database")
	baseSQL, e := base.DB()
	require.NoError(t, e)
	baseSQL.SetMaxOpenConns(2)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	require.NoError(t, baseSQL.PingContext(ctx))
	cancel()
	schemaName := "lmm_merchant_store_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quoted := pgx.Identifier{schemaName}.Sanitize()
	created := false
	t.Cleanup(func() {
		if created {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if e := base.WithContext(ctx).Exec("DROP SCHEMA " + quoted + " CASCADE").Error; e != nil {
				t.Errorf("remove only the test-owned schema: %v", e)
			}
		}
		_ = baseSQL.Close()
	})
	require.NoError(t, base.Exec("CREATE SCHEMA "+quoted).Error)
	created = true
	parsed, e := url.Parse(dsn)
	require.NoError(t, e)
	q := parsed.Query()
	q.Set("search_path", schemaName)
	q.Set("application_name", schemaName)
	q.Set("statement_timeout", "15000")
	q.Set("lock_timeout", "10000")
	parsed.RawQuery = q.Encode()
	db, e := gorm.Open(postgres.New(postgres.Config{DSN: parsed.String(), PreferSimpleProtocol: true}), cfg)
	require.NoError(t, e)
	sqlDB, e := db.DB()
	require.NoError(t, e)
	sqlDB.SetMaxOpenConns(16)
	sqlDB.SetMaxIdleConns(16)
	t.Cleanup(func() { _ = sqlDB.Close() })
	oldDB, oldLog, oldRedis := DB, LOG_DB, common.RedisEnabled
	DB, LOG_DB, common.RedisEnabled = db, db, false
	t.Cleanup(func() { DB, LOG_DB, common.RedisEnabled = oldDB, oldLog, oldRedis })
	usePostgresDatabaseType(t)
	t.Setenv("MERCHANT_STORE_ENCRYPTION_KEY", "C5wmMzDh1QsVZb0saEW9ulAPzVN87Boqv3DK6eIrKXc2YLfg")
	require.NoError(t, db.AutoMigrate(&User{}, &WalletTransfer{}))
	legacy := marketTestUser(t, db, "legacy-fixture", 1234567, common.RoleCommonUser)
	require.NoError(t, db.Create(&WalletTransfer{SenderID: legacy.Id, RequestKey: "legacy-store-test-fixture", Token: strings.Repeat("a", 64), Quota: 34567, Status: "claimed", CreatedAt: 100, ClaimedAt: 200, RecipientID: legacy.Id, RecipientEmail: "legacy@example.test"}).Error)
	before := merchantStorePGFingerprint(t, db, schemaName)
	oldTables := merchantStorePGTables(t, db, schemaName)
	require.NoError(t, db.AutoMigrate(MerchantStoreModels()...))
	require.Equal(t, before, merchantStorePGFingerprint(t, db, schemaName), "merchant table creation preserves legacy fixture rows, columns, defaults, indexes and constraints")
	tables := merchantStorePGTables(t, db, schemaName)
	added := []string{}
	existing := map[string]bool{}
	for _, name := range oldTables {
		existing[name] = true
	}
	for _, name := range tables {
		if !existing[name] {
			added = append(added, name)
		}
	}
	require.Equal(t, merchantStoreExpectedTables, added, "exactly the declared 13 merchant tables were added")
	t.Cleanup(func() {
		require.Equal(t, before, merchantStorePGFingerprint(t, db, schemaName), "all concurrent merchant transactions preserve the unrelated legacy fixture")
	})
	t.Logf("isolated schema=%s, actual PostgreSQL pool max connections=%d, legacy fingerprint=%s, additions=%d", schemaName, sqlDB.Stats().MaxOpenConnections, before, len(added))
	return db, schemaName, base, before
}
func merchantStorePGFixture(t *testing.T, db *gorm.DB, methods ...string) storeFixture {
	t.Helper()
	f := storeFixture{buyer: marketTestUser(t, db, "pg-buyer", 10000000, common.RoleCommonUser), seller: marketTestUser(t, db, "pg-seller", 10000000, common.RoleCommonUser), root: marketTestUser(t, db, "pg-root", 0, common.RoleRootUser)}
	require.NoError(t, SetMerchantStoreConfig(f.root.Id, MerchantStoreConfig{FeeBPS: 100, RecipientID: f.root.Id, PromotionQuota: 500000}))
	f.product = merchantStorePGProduct(t, f, "Concurrent cards", methods...)
	for _, method := range methods {
		config := ""
		if strings.HasPrefix(method, "external:") {
			config = `{"key":"isolated-fixture"}`
		}
		_, e := SaveMerchantStoreGateway(f.seller.Id, method, true, config)
		require.NoError(t, e)
	}
	require.NoError(t, AcceptMerchantStoreDisclaimer(f.buyer.Id, MerchantStoreDisclaimerVersion))
	return f
}
func merchantStorePGProduct(t *testing.T, f storeFixture, title string, methods ...string) *MerchantStoreProduct {
	t.Helper()
	p, e := SaveMerchantStoreProduct(f.seller.Id, "", MerchantStoreProductInput{Title: title, PriceQuota: 500000, PaymentMethods: methods})
	require.NoError(t, e)
	require.NoError(t, SubmitMerchantStoreProduct(f.seller.Id, p.ID))
	require.NoError(t, ReviewMerchantStoreProduct(f.root.Id, p.ID, true, "isolated PostgreSQL fixture"))
	return p
}

// The first real FOR UPDATE is held until pg_stat_activity observes another
// connection waiting on a database lock. No sleep is used as concurrency proof.
func merchantStorePGContend(t *testing.T, db, observer *gorm.DB, applicationName string, ops []func() error) []error {
	return merchantStorePGContendAt(t, db, observer, applicationName, "merchant_store_products", ops)
}
func merchantStorePGContendAt(t *testing.T, db, observer *gorm.DB, applicationName, lockTable string, ops []func() error) []error {
	t.Helper()
	require.GreaterOrEqual(t, len(ops), 2)
	held := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unlock := func() { releaseOnce.Do(func() { close(release) }) }
	var first atomic.Bool
	callback := "merchant-store-pg-barrier-" + uuid.NewString()
	require.NoError(t, db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Error != nil || tx.Statement.Table != lockTable {
			return
		}
		if _, ok := tx.Statement.Clauses["FOR"]; !ok {
			return
		}
		if first.CompareAndSwap(false, true) {
			close(held)
			select {
			case <-release:
			case <-time.After(12 * time.Second):
				tx.AddError(errors.New("merchant PostgreSQL lock barrier timed out"))
			}
		}
	}))
	var wg sync.WaitGroup
	results := make([]error, len(ops))
	done := make(chan struct{})
	launch := func(i int) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = ops[i]()
			if i == 0 {
				close(done)
			}
		}()
	}
	defer func() { unlock(); wg.Wait(); require.NoError(t, db.Callback().Query().Remove(callback)) }()
	launch(0)
	select {
	case <-held:
	case <-done:
		t.Fatalf("first operation ended before locking product: %v", results[0])
	case <-time.After(10 * time.Second):
		t.Fatal("first operation did not acquire its product row lock")
	}
	for i := 1; i < len(ops); i++ {
		launch(i)
	}
	observed := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var count int64
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		e := observer.WithContext(ctx).Raw("SELECT COUNT(*) FROM pg_stat_activity WHERE datname=current_database() AND application_name=? AND wait_event_type='Lock'", applicationName).Scan(&count).Error
		cancel()
		require.NoError(t, e)
		if count > 0 {
			observed = true
			t.Logf("observed %d PostgreSQL connection(s) blocked on row locks", count)
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.True(t, observed, "actual concurrent PostgreSQL lock wait observed")
	unlock()
	allDone := make(chan struct{})
	go func() { wg.Wait(); close(allDone) }()
	select {
	case <-allDone:
	case <-time.After(20 * time.Second):
		t.Fatal("merchant PostgreSQL concurrent transactions failed to finish without deadlock")
	}
	return results
}
func TestMerchantStorePostgresConcurrency(t *testing.T) {
	t.Run("same-request-settles-once", func(t *testing.T) {
		db, name, observer, _ := merchantStorePGDB(t)
		f := merchantStorePGFixture(t, db, "balance")
		_, e := AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{"one", "two"})
		require.NoError(t, e)
		var created atomic.Int32
		ops := make([]func() error, 8)
		// This product has no pickup-code requirement; keep the input consistent.
		for i := range ops {
			ops[i] = func() error {
				in := f.checkout("same", "balance")
				in.PickupCode = ""
				_, made, e := CreateMerchantStoreOrder(in)
				if made {
					created.Add(1)
				}
				return e
			}
		}
		for _, e := range merchantStorePGContend(t, db, observer, name, ops) {
			require.NoError(t, e)
		}
		require.EqualValues(t, 1, created.Load())
		storeBalance(t, f.buyer.Id, 9500000)
		storeBalance(t, f.seller.Id, 10495000)
		storeBalance(t, f.root.Id, 5000)
		var n int64
		require.NoError(t, db.Model(&MerchantStoreTransfer{}).Count(&n).Error)
		require.EqualValues(t, 2, n)
	})
	t.Run("distinct-checkouts-never-oversell", func(t *testing.T) {
		db, name, observer, _ := merchantStorePGDB(t)
		f := merchantStorePGFixture(t, db, "balance")
		_, e := AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{"one", "two", "three", "four"})
		require.NoError(t, e)
		ops := make([]func() error, 8)
		for i := range ops {
			i := i
			ops[i] = func() error {
				in := f.checkout(fmt.Sprintf("unique-%d", i), "balance")
				in.PickupCode = ""
				_, _, e := CreateMerchantStoreOrder(in)
				return e
			}
		}
		success := 0
		for _, e := range merchantStorePGContend(t, db, observer, name, ops) {
			if e == nil {
				success++
			} else {
				require.ErrorIs(t, e, ErrMerchantStoreStock)
			}
		}
		require.Equal(t, 4, success)
		storeBalance(t, f.buyer.Id, 8000000)
		storeBalance(t, f.seller.Id, 11980000)
		storeBalance(t, f.root.Id, 20000)
		var n int64
		require.NoError(t, db.Model(&MerchantStoreStock{}).Where("state = ?", "delivered").Count(&n).Error)
		require.EqualValues(t, 4, n)
	})
	t.Run("cross-product-buyer-pending-limit", func(t *testing.T) {
		db, name, observer, _ := merchantStorePGDB(t)
		f := merchantStorePGFixture(t, db, "platform:waffo_pancake")
		products := []*MerchantStoreProduct{f.product}
		for i := 0; i < 7; i++ {
			products = append(products, merchantStorePGProduct(t, f, fmt.Sprintf("product-%d", i), "platform:waffo_pancake"))
		}
		for _, p := range products {
			_, e := AddMerchantStoreStock(f.seller.Id, p.ID, []string{"one"})
			require.NoError(t, e)
		}
		ops := make([]func() error, len(products))
		for i, p := range products {
			i, p := i, p
			ops[i] = func() error {
				in := f.checkout(fmt.Sprintf("pending-%d", i), "platform:waffo_pancake")
				in.ProductID = p.ID
				in.PickupCode = ""
				_, _, e := CreateMerchantStoreOrder(in)
				return e
			}
		}
		success := 0
		for _, e := range merchantStorePGContendAt(t, db, observer, name, "users", ops) {
			if e == nil {
				success++
			} else {
				require.ErrorIs(t, e, ErrMerchantStorePendingLimit)
			}
		}
		require.Equal(t, 3, success)
		var n int64
		require.NoError(t, db.Model(&MerchantStoreOrder{}).Where("status = ?", "pending").Count(&n).Error)
		require.EqualValues(t, 3, n)
		storeBalance(t, f.seller.Id, 9985000)
		storeBalance(t, f.root.Id, 0)
		storeBalance(t, f.buyer.Id, 10000000)
	})
	t.Run("verified-callback-cancel-and-expiry", func(t *testing.T) {
		db, name, observer, _ := merchantStorePGDB(t)
		f := merchantStorePGFixture(t, db, "platform:waffo_pancake")
		_, e := AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{"one"})
		require.NoError(t, e)
		in := f.checkout("callback", "platform:waffo_pancake")
		in.PickupCode = ""
		o, _, e := CreateMerchantStoreOrder(in)
		require.NoError(t, e)
		require.NoError(t, BindMerchantStorePaymentQuote(o.ID, 100, "USD", "1"))
		require.NoError(t, BindMerchantStorePaymentContext(o.ID, "v1:isolated-issued-context"))
		require.NoError(t, db.Model(&MerchantStoreOrder{}).Where("id = ?", o.ID).Update("expires_at", common.GetTimestamp()-1).Error)
		results := merchantStorePGContend(t, db, observer, name, []func() error{func() error { return CompleteMerchantStorePayment(o.ID, "verified-receipt") }, func() error { return CancelMerchantStoreOrder(f.buyer.Id, o.ID) }, func() error { _, e := ExpireMerchantStoreOrders(30); return e }, func() error { return CompleteMerchantStorePayment(o.ID, "verified-receipt") }})
		require.NoError(t, results[0])
		require.ErrorIs(t, results[1], ErrMerchantStoreConflict)
		require.NoError(t, results[2])
		require.NoError(t, results[3])
		fresh, e := GetMerchantStorePaymentOrder(o.ID)
		require.NoError(t, e)
		require.Equal(t, "paid", fresh.Status)
		storeBalance(t, f.seller.Id, 10495000)
		storeBalance(t, f.root.Id, 5000)
	})
	t.Run("receipt-belongs-to-one-order", func(t *testing.T) {
		db, _, _, _ := merchantStorePGDB(t)
		f := merchantStorePGFixture(t, db, "platform:waffo_pancake")
		other := merchantStorePGProduct(t, f, "second product", "platform:waffo_pancake")
		orders := []*MerchantStoreOrder{}
		for i, p := range []*MerchantStoreProduct{f.product, other} {
			_, e := AddMerchantStoreStock(f.seller.Id, p.ID, []string{"one"})
			require.NoError(t, e)
			in := f.checkout(fmt.Sprintf("receipt-%d", i), "platform:waffo_pancake")
			in.ProductID = p.ID
			in.PickupCode = ""
			o, _, e := CreateMerchantStoreOrder(in)
			require.NoError(t, e)
			require.NoError(t, BindMerchantStorePaymentQuote(o.ID, 100, "USD", "1"))
			orders = append(orders, o)
		}
		start := make(chan struct{})
		results := make([]error, 2)
		var wg sync.WaitGroup
		for i, o := range orders {
			i, o := i, o
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				results[i] = CompleteMerchantStorePayment(o.ID, "same-provider-transaction")
			}()
		}
		close(start)
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Fatal("receipt race deadlocked")
		}
		success := 0
		for _, e := range results {
			if e == nil {
				success++
			}
		}
		require.Equal(t, 1, success)
		var n int64
		require.NoError(t, db.Model(&MerchantStorePaymentReceipt{}).Count(&n).Error)
		require.EqualValues(t, 1, n)
		require.NoError(t, db.Model(&MerchantStoreOrder{}).Where("status = ?", "paid").Count(&n).Error)
		require.EqualValues(t, 1, n)
		storeBalance(t, f.root.Id, 5000)
		storeBalance(t, f.seller.Id, 10490000)
	})
	t.Run("wallet-boundary-full-rollback", func(t *testing.T) {
		db, _, _, _ := merchantStorePGDB(t)
		f := merchantStorePGFixture(t, db, "balance")
		_, e := AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{"one"})
		require.NoError(t, e)
		require.NoError(t, db.Model(&User{}).Where("id = ?", f.root.Id).Update("quota", common.MaxWalletQuota).Error)
		in := f.checkout("boundary", "balance")
		in.PickupCode = ""
		_, _, e = CreateMerchantStoreOrder(in)
		require.ErrorIs(t, e, ErrWalletQuotaOutOfRange)
		storeBalance(t, f.buyer.Id, 10000000)
		storeBalance(t, f.seller.Id, 10000000)
		var n int64
		require.NoError(t, db.Model(&MerchantStoreOrder{}).Count(&n).Error)
		require.Zero(t, n)
		require.NoError(t, db.Model(&MerchantStoreStock{}).Where("state = ?", "available").Count(&n).Error)
		require.EqualValues(t, 1, n)
	})
}
