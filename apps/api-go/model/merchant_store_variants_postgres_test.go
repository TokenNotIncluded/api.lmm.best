package model

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func merchantStorePGVariantSchema(t *testing.T, db *gorm.DB, name string) {
	t.Helper()
	var columns []struct {
		TableName, ColumnName, DataType, IsNullable string
		CharacterMaximumLength                      *int64
		ColumnDefault                               *string
	}
	require.NoError(t, db.Raw(`SELECT table_name,column_name,data_type,is_nullable,character_maximum_length,column_default FROM information_schema.columns WHERE table_schema=? AND ((table_name='merchant_store_variants' AND column_name='price_quota') OR (table_name='merchant_store_stocks' AND column_name='variant_id') OR (table_name='merchant_store_orders' AND column_name IN ('variant_id','variant_name')))`, name).Scan(&columns).Error)
	require.Len(t, columns, 4)
	for _, column := range columns {
		if column.ColumnName == "price_quota" {
			require.Equal(t, "bigint", column.DataType)
			continue
		}
		require.Equal(t, "character varying", column.DataType)
		require.NotNil(t, column.CharacterMaximumLength)
		size := int64(36)
		if column.ColumnName == "variant_name" {
			size = 200
		}
		require.Equal(t, size, *column.CharacterMaximumLength)
		if column.TableName == "merchant_store_stocks" {
			require.Equal(t, "YES", column.IsNullable)
			require.Nil(t, column.ColumnDefault)
		} else {
			require.Equal(t, "NO", column.IsNullable)
			require.NotNil(t, column.ColumnDefault)
			require.Contains(t, *column.ColumnDefault, "''")
		}
	}
	var indexes []struct{ Indexname, Indexdef string }
	require.NoError(t, db.Raw(`SELECT indexname,indexdef FROM pg_indexes WHERE schemaname=? AND tablename='merchant_store_stocks' AND indexname IN ('store_stock_available','store_stock_variant_available')`, name).Scan(&indexes).Error)
	require.Len(t, indexes, 2)
	for _, index := range indexes {
		ordered := "product_id, state, position"
		if index.Indexname == "store_stock_variant_available" {
			ordered = "product_id, variant_id, state, position"
		}
		require.Contains(t, strings.ReplaceAll(index.Indexdef, `"`, ""), ordered)
	}
	t.Log("actual PostgreSQL variant BIGINT, nullable legacy stock, empty historical order defaults and retained/new indexes verified")
}

func merchantStorePGVariant(t *testing.T, f storeFixture, name string, price int, items []string) *MerchantStoreVariant {
	t.Helper()
	v, err := SaveMerchantStoreVariant(f.seller.Id, f.product.ID, "", MerchantStoreVariantInput{Name: name, PriceQuota: price, Template: "card-key", Enabled: true})
	require.NoError(t, err)
	_, err = AddMerchantStoreVariantStock(f.seller.Id, f.product.ID, v.ID, items)
	require.NoError(t, err)
	require.NoError(t, SubmitMerchantStoreProduct(f.seller.Id, f.product.ID))
	require.NoError(t, ReviewMerchantStoreProduct(f.root.Id, f.product.ID, true, "isolated variant fixture"))
	return v
}

func merchantStorePGVariantOrders(t *testing.T, db *gorm.DB, f storeFixture, expected int) []MerchantStoreOrder {
	t.Helper()
	var orders []MerchantStoreOrder
	require.NoError(t, db.Where("product_id = ?", f.product.ID).Order("id").Find(&orders).Error)
	require.Len(t, orders, expected)
	price, fees := 0, 0
	for _, order := range orders {
		require.Equal(t, "paid", order.Status)
		price += order.PriceQuota
		fees += order.FeeQuota
	}
	storeBalance(t, f.buyer.Id, 10000000-price)
	storeBalance(t, f.seller.Id, 10000000+price-fees)
	storeBalance(t, f.root.Id, fees)
	return orders
}

// The waiter must be the gate's FOR UPDATE, blocked by the exact live SHARE
// transaction. An unrelated PostgreSQL lock or elapsed sleep proves nothing.
func merchantStorePGActivateAfterCheckoutShare(t *testing.T, db, observer *gorm.DB, name string, checkout func() error) {
	t.Helper()
	held, release := make(chan struct{}), make(chan struct{})
	var first atomic.Bool
	var once sync.Once
	unlock := func() { once.Do(func() { close(release) }) }
	var holderPID int
	callback := "merchant-store-variant-gate-barrier"
	require.NoError(t, db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		locking, ok := tx.Statement.Clauses["FOR"].Expression.(clause.Locking)
		if tx.Error != nil || tx.Statement.Table != "options" || !ok || locking.Strength != "SHARE" || !first.CompareAndSwap(false, true) {
			return
		}
		if err := tx.Statement.ConnPool.QueryRowContext(tx.Statement.Context, "SELECT pg_backend_pid()").Scan(&holderPID); err != nil {
			tx.AddError(err)
			return
		}
		close(held)
		select {
		case <-release:
		case <-time.After(12 * time.Second):
			tx.AddError(fmt.Errorf("gate SHARE barrier timed out"))
		}
	}))
	var wg sync.WaitGroup
	checkoutDone, activationDone := make(chan error, 1), make(chan error, 1)
	defer func() { unlock(); wg.Wait(); require.NoError(t, db.Callback().Query().Remove(callback)) }()
	wg.Add(1)
	go func() { defer wg.Done(); checkoutDone <- checkout() }()
	select {
	case <-held:
	case err := <-checkoutDone:
		t.Fatalf("checkout ended before obtaining its gate SHARE: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("checkout did not acquire the gate SHARE")
	}
	wg.Add(1)
	go func() { defer wg.Done(); activationDone <- ActivateMerchantStoreVariants(db, 1) }()
	observed := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var count int64
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := observer.WithContext(ctx).Raw(`SELECT COUNT(*) FROM pg_stat_activity WHERE datname=current_database() AND application_name=? AND wait_event_type='Lock' AND query LIKE '%"options"%' AND query LIKE '%FOR UPDATE%' AND ? = ANY(pg_blocking_pids(pid))`, name, holderPID).Scan(&count).Error
		cancel()
		require.NoError(t, err)
		if count != 0 {
			observed = true
			t.Logf("observed options FOR UPDATE waiter blocked by exact gate SHARE holder PID=%d; waiters=%d", holderPID, count)
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.True(t, observed, "activation waits for the actual gate SHARE transaction")
	select {
	case err := <-activationDone:
		t.Fatalf("activation completed while the SHARE transaction remained held: %v", err)
	default:
	}
	gate, err := storeWriterGateRow(db, "")
	require.NoError(t, err)
	require.Equal(t, 1, gate, "the exclusive transition cannot publish before the old writer commits")
	unlock()
	for _, done := range []chan error{checkoutDone, activationDone} {
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(20 * time.Second):
			t.Fatal("gate activation or held checkout did not finish")
		}
	}
}

func TestMerchantStorePostgresVariants(t *testing.T) {
	t.Run("two-skus-share-one-cumulative-cap", func(t *testing.T) {
		db, name, observer, before := merchantStorePGDB(t)
		f := merchantStorePGFixture(t, db, "balance")
		merchantStorePGVariantSchema(t, db, name)
		require.NoError(t, ActivateMerchantStoreVariants(db, 1))
		_, err := AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{"BASE-1", "BASE-2", "BASE-3", "BASE-4"})
		require.NoError(t, err)
		v := merchantStorePGVariant(t, f, "Plus", 1000000, []string{"PLUS-1", "PLUS-2", "PLUS-3", "PLUS-4"})
		require.NoError(t, SetMerchantStoreProductSaleLimit(f.seller.Id, f.product.ID, storeLimit(3)))
		ops := make([]func() error, 8)
		for i := range ops {
			i := i
			ops[i] = func() error {
				input := f.checkout(fmt.Sprintf("shared-cap-%d", i), "balance")
				input.PickupCode = ""
				input.VariantID = MerchantStoreDefaultVariantID(f.product.ID)
				if i%2 != 0 {
					input.VariantID = v.ID
				}
				_, _, err := CreateMerchantStoreOrder(input)
				return err
			}
		}
		success := 0
		for _, err := range merchantStorePGContend(t, db, observer, name, ops) {
			if err == nil {
				success++
			} else {
				require.ErrorIs(t, err, ErrMerchantStoreStock)
			}
		}
		require.Equal(t, 3, success)
		for _, order := range merchantStorePGVariantOrders(t, db, f, 3) {
			prefix, price := "BASE-", 500000
			if order.VariantID == v.ID {
				prefix, price = "PLUS-", 1000000
			} else {
				require.Equal(t, MerchantStoreDefaultVariantID(f.product.ID), order.VariantID)
			}
			require.Equal(t, price, order.UnitPriceQuota)
			token, err := GetMerchantStoreOrderPickupToken(f.buyer.Id, order.ID)
			require.NoError(t, err)
			claim, err := ClaimMerchantStoreOrder(token, "", f.buyer.Id)
			require.NoError(t, err)
			require.Len(t, claim.Items, 1)
			require.True(t, strings.HasPrefix(claim.Items[0], prefix), "fulfillment matches only the frozen SKU")
		}
		product, err := GetMerchantStoreProduct(f.seller.Id, f.product.ID)
		require.NoError(t, err)
		require.EqualValues(t, 5, product.InventoryTotal)
		require.EqualValues(t, 3, product.PaidQuantity)
		require.Zero(t, product.SaleAvailable)
		_, err = AddMerchantStoreVariantStock(f.seller.Id, f.product.ID, v.ID, []string{"REPLENISH-WITHOUT-RESETTING-CAP"})
		require.NoError(t, err)
		input := f.checkout("cap-after-replenishment", "balance")
		input.PickupCode, input.VariantID = "", v.ID
		_, _, err = CreateMerchantStoreOrder(input)
		require.ErrorIs(t, err, ErrMerchantStoreStock)
		merchantStorePGVariantOrders(t, db, f, 3)
		require.Equal(t, before, merchantStorePGFingerprint(t, db, name))
		t.Logf("owners buyer=%d seller=%d recipient=%d; aggregate cap=3 paid=3 remaining actual inventory=5; old fixture fingerprint=%s", f.buyer.Id, f.seller.Id, f.root.Id, before)
	})
	t.Run("eight-checkouts-never-borrow-another-skus-stock", func(t *testing.T) {
		db, name, observer, before := merchantStorePGDB(t)
		f := merchantStorePGFixture(t, db, "balance")
		require.NoError(t, ActivateMerchantStoreVariants(db, 1))
		_, err := AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{"UNTOUCHED-BASE-1", "UNTOUCHED-BASE-2", "UNTOUCHED-BASE-3"})
		require.NoError(t, err)
		var untouchedBefore []MerchantStoreStock
		require.NoError(t, storeVariantStock(db, f.product.ID, MerchantStoreDefaultVariantID(f.product.ID)).Order("id").Find(&untouchedBefore).Error)
		v := merchantStorePGVariant(t, f, "Plus only", 1000000, []string{"ONLY-PLUS-1", "ONLY-PLUS-2", "ONLY-PLUS-3"})
		ops := make([]func() error, 8)
		for i := range ops {
			i := i
			ops[i] = func() error {
				input := f.checkout(fmt.Sprintf("same-sku-%d", i), "balance")
				input.PickupCode, input.VariantID = "", v.ID
				_, _, err := CreateMerchantStoreOrder(input)
				return err
			}
		}
		success := 0
		for _, err := range merchantStorePGContend(t, db, observer, name, ops) {
			if err == nil {
				success++
			} else {
				require.ErrorIs(t, err, ErrMerchantStoreStock)
			}
		}
		require.Equal(t, 3, success)
		received := make(map[string]bool)
		for _, order := range merchantStorePGVariantOrders(t, db, f, 3) {
			require.Equal(t, v.ID, order.VariantID)
			require.Equal(t, 1000000, order.UnitPriceQuota)
			token, err := GetMerchantStoreOrderPickupToken(f.buyer.Id, order.ID)
			require.NoError(t, err)
			claim, err := ClaimMerchantStoreOrder(token, "", f.buyer.Id)
			require.NoError(t, err)
			require.Len(t, claim.Items, 1)
			require.True(t, strings.HasPrefix(claim.Items[0], "ONLY-PLUS-"))
			require.False(t, received[claim.Items[0]], "no two orders receive the same unit")
			received[claim.Items[0]] = true
			repeated, err := ClaimMerchantStoreOrder(token, "", f.buyer.Id)
			require.NoError(t, err)
			require.Equal(t, claim.Items, repeated.Items)
		}
		require.Len(t, received, 3)
		var untouchedAfter []MerchantStoreStock
		require.NoError(t, storeVariantStock(db, f.product.ID, MerchantStoreDefaultVariantID(f.product.ID)).Order("id").Find(&untouchedAfter).Error)
		require.Equal(t, untouchedBefore, untouchedAfter, "other SKU stock IDs/ciphertext/state/order/position/association stay intact")
		var untouched int64
		require.NoError(t, storeVariantStock(db.Model(&MerchantStoreStock{}), f.product.ID, MerchantStoreDefaultVariantID(f.product.ID)).Where("state = ?", "available").Count(&untouched).Error)
		require.EqualValues(t, 3, untouched)
		require.Equal(t, before, merchantStorePGFingerprint(t, db, name))
		t.Logf("owners buyer=%d seller=%d recipient=%d; requested SKU paid=3 other SKU available=3; old fixture fingerprint=%s", f.buyer.Id, f.seller.Id, f.root.Id, before)
	})
	t.Run("activation-waits-for-share-then-capability-one-freezes", func(t *testing.T) {
		db, name, observer, before := merchantStorePGDB(t)
		f := merchantStorePGFixture(t, db, "balance")
		_, err := AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{"PAID-BEFORE-ACTIVATION", "AVAILABLE-AFTER-ACTIVATION"})
		require.NoError(t, err)
		var paid *MerchantStoreOrder
		merchantStorePGActivateAfterCheckoutShare(t, db, observer, name, func() error {
			input := f.checkout("began-before-activation", "balance")
			input.PickupCode = ""
			var err error
			paid, _, err = CreateMerchantStoreOrder(input)
			return err
		})
		require.NotNil(t, paid)
		require.Equal(t, "paid", paid.Status)
		status, err := GetMerchantStoreWriterGateStatus(db)
		require.NoError(t, err)
		require.Equal(t, 2, status.RequiredCapability)
		merchantStorePGVariant(t, f, "Activated Plus", 1000000, []string{"ACTIVATED-PLUS-STOCK"})
		binary := os.Getenv("MERCHANT_STORE_CAP1_TEST_BINARY")
		require.True(t, filepath.IsAbs(binary), "explicit capability-one synthetic model probe is required; no simulated version constant")
		info, err := os.Lstat(binary)
		require.NoError(t, err)
		require.True(t, info.Mode().IsRegular() && info.Mode()&0111 != 0)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		probe := exec.CommandContext(ctx, binary, "-test.run=^TestMerchantStoreCapabilityOneVariantProbe$", "-test.v", "-test.count=1")
		probe.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "TZ=UTC", "GOMAXPROCS=2", "MERCHANT_STORE_POSTGRES_TEST_DSN=" + os.Getenv("MERCHANT_STORE_POSTGRES_TEST_DSN"), "MERCHANT_STORE_CAP1_PROBE_SCHEMA=" + name, "MERCHANT_STORE_CAP1_PROBE_PRODUCT=" + f.product.ID, "MERCHANT_STORE_CAP1_PROBE_BUYER=" + strconv.Itoa(f.buyer.Id), "MERCHANT_STORE_CAP1_PROBE_SELLER=" + strconv.Itoa(f.seller.Id), "MERCHANT_STORE_CAP1_PROBE_PAID_ORDER=" + paid.ID}
		output, err := probe.CombinedOutput()
		require.NoError(t, err, "real capability-one probe: %s", output)
		require.Contains(t, string(output), "--- PASS: TestMerchantStoreCapabilityOneVariantProbe")
		require.NotContains(t, string(output), "--- SKIP:")
		t.Logf("actual isolated capability-one model binary proof:\n%s", output)
		require.Equal(t, before, merchantStorePGFingerprint(t, db, name))
		t.Logf("owners buyer=%d seller=%d recipient=%d; actual SHARE/exclusive contention; gate now=2; old fixture fingerprint=%s", f.buyer.Id, f.seller.Id, f.root.Id, before)
	})
}
