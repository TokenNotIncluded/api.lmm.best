package model

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func storeWriterGateForTest(t *testing.T, value string) {
	t.Helper()
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", MerchantStoreWriterCapabilityOption).Update("value", value).Error)
}

func storeUnsupportedWriterGateForTest(t *testing.T) {
	t.Helper()
	storeWriterGateForTest(t, strconv.Itoa(MerchantStoreWriterCapability+1))
}

func storeWriterSnapshot(t *testing.T) []byte {
	t.Helper()
	all := make(map[string]any)
	for _, item := range []struct {
		name  string
		model any
	}{
		{"users", &User{}}, {"products", &MerchantStoreProduct{}}, {"stock", &MerchantStoreStock{}},
		{"orders", &MerchantStoreOrder{}}, {"transfers", &MerchantStoreTransfer{}}, {"events", &MerchantStoreEvent{}},
		{"gateways", &MerchantStoreGateway{}}, {"config", &MerchantStoreConfig{}}, {"promotions", &MerchantStorePromotion{}},
	} {
		var rows []map[string]any
		statement := &gorm.Statement{DB: DB}
		require.NoError(t, statement.Parse(item.model))
		// Raw table projection preserves serialized bytes and every stored
		// column; model serializers cannot scan those strings into map fields.
		require.NoError(t, DB.Table(statement.Table).Order("id").Find(&rows).Error)
		all[item.name] = rows
	}
	encoded, err := json.Marshal(all)
	require.NoError(t, err)
	return encoded
}

func TestMerchantStoreWriterGateMissingInvalidAndMigrationNeverRepair(t *testing.T) {
	db := marketTestDB(t)
	values := []string{"", "0", strconv.Itoa(MerchantStoreWriterCapability + 1), "01", " 1"}
	if MerchantStoreWriterCapability < 2 {
		values = append(values, "2")
	}
	for _, value := range values {
		storeWriterGateForTest(t, value)
		require.ErrorIs(t, db.Transaction(storeRequireWriter), ErrMerchantStoreWriterFrozen)
		status, _ := GetMerchantStoreWriterGateStatus(db)
		require.False(t, status.NewWritesAllowed)
		require.True(t, status.SupportsWriterGate)
		require.Equal(t, MerchantStoreWriterCapability, status.WriterCapability)
		require.ErrorIs(t, checkMerchantStoreWriterMigration(db), ErrMerchantStoreWriterFrozen)
	}
	require.NoError(t, db.Where("key = ?", MerchantStoreWriterCapabilityOption).Delete(&Option{}).Error)
	require.ErrorIs(t, db.Transaction(storeRequireWriter), ErrMerchantStoreWriterFrozen)
	require.NoError(t, checkMerchantStoreWriterMigration(db), "checking first apply does not itself repair the missing gate")
	var count int64
	require.NoError(t, db.Model(&Option{}).Where("key = ?", MerchantStoreWriterCapabilityOption).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, BootstrapMerchantStoreWriterGate(db), "only the explicit pre-variant bootstrap creates capability 1")
	require.NoError(t, db.Transaction(storeRequireWriter))
}

func TestMerchantStoreWriterGateBootstrapPreservesNewerGateAndRefusesSchemaDowngrade(t *testing.T) {
	db := marketTestDB(t)
	storeWriterGateForTest(t, "2")
	require.NoError(t, BootstrapMerchantStoreWriterGate(db))
	status, err := GetMerchantStoreWriterGateStatus(db)
	require.NoError(t, err)
	require.Equal(t, 2, status.RequiredCapability)
	require.Equal(t, MerchantStoreWriterCapability >= 2, status.NewWritesAllowed)
	require.NoError(t, db.Exec("CREATE TABLE merchant_store_variants (id TEXT PRIMARY KEY)").Error)
	require.NoError(t, db.Where("key = ?", MerchantStoreWriterCapabilityOption).Delete(&Option{}).Error)
	require.ErrorIs(t, BootstrapMerchantStoreWriterGate(db), ErrMerchantStoreWriterFrozen)
	var count int64
	require.NoError(t, db.Model(&Option{}).Where("key = ?", MerchantStoreWriterCapabilityOption).Count(&count).Error)
	require.Zero(t, count, "a missing post-activation gate must never silently become capability 1")
}

func TestMerchantStoreWriterGateGenericOptionsCannotChangeReservedCapability(t *testing.T) {
	db := marketTestDB(t)
	for _, key := range []string{MerchantStoreWriterCapabilityOption, strings.ToLower(MerchantStoreWriterCapabilityOption), " " + MerchantStoreWriterCapabilityOption + " "} {
		for _, value := range []string{"", "1", "2"} {
			require.ErrorIs(t, validateOptionValue(key, value), ErrMerchantStoreWriterGateReserved)
			require.ErrorIs(t, UpdateOption(key, value), ErrMerchantStoreWriterGateReserved)
			_, err := UpdateOptionsBulkWithWarnings(map[string]string{key: value, "unused_test_option": "must not commit"})
			require.ErrorIs(t, err, ErrMerchantStoreWriterGateReserved)
		}
	}
	status, err := GetMerchantStoreWriterGateStatus(db)
	require.NoError(t, err)
	require.Equal(t, 1, status.RequiredCapability)
	var count int64
	require.NoError(t, db.Model(&Option{}).Where("key = ?", "unused_test_option").Count(&count).Error)
	require.Zero(t, count)
}

func TestMerchantStoreWriterGateFreezesEveryNewShopWriterWithoutSideEffects(t *testing.T) {
	f := newStoreFixture(t, "balance")
	var stock MerchantStoreStock
	require.NoError(t, DB.Where("product_id = ? AND state = ?", f.product.ID, "available").First(&stock).Error)
	config, err := GetMerchantStoreConfig()
	require.NoError(t, err)
	storeUnsupportedWriterGateForTest(t)
	before := storeWriterSnapshot(t)
	for name, action := range map[string]func() error{
		"checkout": func() error { _, _, err := CreateMerchantStoreOrder(f.checkout("frozen-new", "balance")); return err },
		"create": func() error {
			_, err := SaveMerchantStoreProduct(f.seller.Id, "", MerchantStoreProductInput{Title: "New", Description: "Description", PriceQuota: 500000})
			return err
		},
		"edit": func() error {
			_, err := SaveMerchantStoreProduct(f.seller.Id, f.product.ID, MerchantStoreProductInput{Title: "Edited", Description: "Description", PriceQuota: 500000})
			return err
		},
		"submit":  func() error { return SubmitMerchantStoreProduct(f.seller.Id, f.product.ID) },
		"review":  func() error { return ReviewMerchantStoreProduct(f.root.Id, f.product.ID, true, "must not publish") },
		"pause":   func() error { return SetMerchantStoreProductPaused(f.seller.Id, f.product.ID, true) },
		"resume":  func() error { return SetMerchantStoreProductPaused(f.seller.Id, f.product.ID, false) },
		"listing": func() error { return SetMerchantStoreProductListed(f.seller.Id, f.product.ID, true) },
		"cap": func() error {
			cap := int64(10)
			return SetMerchantStoreProductSaleLimit(f.seller.Id, f.product.ID, &cap)
		},
		"import": func() error {
			_, err := AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{"NEW-SECRET-MUST-NOT-COMMIT"})
			return err
		},
		"remove":     func() error { return RemoveMerchantStoreStock(f.seller.Id, f.product.ID, stock.ID) },
		"gateway":    func() error { _, err := SaveMerchantStoreGateway(f.seller.Id, "balance", false, ""); return err },
		"categories": func() error { return SetMerchantStorePaymentCategories(f.seller.Id, MerchantStorePaymentCategories{}) },
		"promotion": func() error {
			_, err := PurchaseMerchantStorePromotion(f.seller.Id, f.product.ID, 1, "frozen-promotion")
			return err
		},
		"config": func() error { return SetMerchantStoreConfig(f.root.Id, config) },
		"config-http-patch": func() error {
			price := 750000
			return PatchMerchantStoreConfig(f.root.Id, MerchantStoreConfigPatch{MinimumUnitPriceQuota: &price})
		},
		"promotion-policy": func() error { return SetMerchantStorePromotionPrice(f.root.Id, 1000000) },
	} {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, action(), ErrMerchantStoreWriterFrozen)
			require.Equal(t, before, storeWriterSnapshot(t))
		})
	}
}

func TestMerchantStoreWriterGateUnixAdvisoryUsesOneBoundSessionAndReturnsPool(t *testing.T) {
	pool, _, state := openMigrationUnixTestDB(t, migrationUnixEndpointFor("7654321"))
	pool.SetMaxOpenConns(1)
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: pool}), &gorm.Config{DisableAutomaticPing: true, Logger: logger.Discard})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, storeWithPostgresActivationSession(db.WithContext(ctx), func(bound *gorm.DB) error {
		conn, ok := bound.Statement.ConnPool.(*sql.Conn)
		require.True(t, ok, "work is bound to the exact advisory-lock connection")
		identity, err := storePostgresGateIdentity(bound.Statement.Context, conn)
		require.NoError(t, err)
		require.Equal(t, "7654321", identity.SystemIdentifier)
		require.Equal(t, pool, db.Statement.ConnPool, "the original Gorm pool must not be overwritten")
		return nil
	}))
	require.Equal(t, 0, pool.Stats().InUse)
	state.mu.Lock()
	require.Equal(t, 1, state.connections, "Unix identity must not acquire a second pool connection")
	require.Zero(t, state.lockOwner)
	for _, query := range state.queries {
		require.Equal(t, 1, query.connection)
	}
	state.mu.Unlock()
	conn, err := pool.Conn(ctx)
	require.NoError(t, err, "the original pool remains usable after releasing the operation")
	require.NoError(t, conn.Close())
}

func TestMerchantStoreWriterGateAdvisoryPoolWaitHonorsCallerDeadline(t *testing.T) {
	pool, _, _ := openMigrationUnixTestDB(t, migrationUnixEndpointFor("7654321"))
	pool.SetMaxOpenConns(1)
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: pool}), &gorm.Config{DisableAutomaticPing: true, Logger: logger.Discard})
	require.NoError(t, err)
	holder, err := pool.Conn(context.Background())
	require.NoError(t, err)
	defer holder.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	ran := false
	err = storeWithPostgresActivationSession(db.WithContext(ctx), func(*gorm.DB) error { ran = true; return nil })
	require.ErrorIs(t, err, ErrMerchantStoreWriterFrozen)
	require.False(t, ran)
	require.Less(t, time.Since(start), time.Second, "pool acquisition cannot outlive the caller deadline")
}

func TestMerchantStoreWriterGateKeepsFrozenPaymentCancellationReplayAndClaim(t *testing.T) {
	f := newStoreFixture(t, "platform:waffo_pancake")
	_, err := AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{"CARD-THIRD"})
	require.NoError(t, err)
	first := f.checkout("before-activation", "platform:waffo_pancake")
	paid, _, err := CreateMerchantStoreOrder(first)
	require.NoError(t, err)
	require.NoError(t, BindMerchantStorePaymentQuote(paid.ID, 671, "CNY", "6.71"))
	require.NoError(t, BindMerchantStorePaymentContext(paid.ID, "frozen-payment-context"))
	storeUnsupportedWriterGateForTest(t)
	replayed, created, err := CreateMerchantStoreOrder(first)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, paid.ID, replayed.ID)
	require.NoError(t, CompleteMerchantStorePayment(paid.ID, "authentic-frozen-receipt"))
	require.NoError(t, CompleteMerchantStorePayment(paid.ID, "authentic-frozen-receipt"))
	storeBalance(t, f.seller.Id, 10495000)
	storeBalance(t, f.root.Id, 5000)
	token, err := GetMerchantStoreOrderPickupToken(f.buyer.Id, paid.ID)
	require.NoError(t, err)
	claim, err := ClaimMerchantStoreOrder(token, first.PickupCode, f.buyer.Id)
	require.NoError(t, err)
	require.Equal(t, []string{"CARD-SECRET-FIRST"}, claim.Items)
	// Missing gate is also no excuse to drop already-paid deliveries.
	require.NoError(t, DB.Where("key = ?", MerchantStoreWriterCapabilityOption).Delete(&Option{}).Error)
	_, err = ClaimMerchantStoreOrder(token, first.PickupCode, f.buyer.Id)
	require.NoError(t, err)
	_, _, err = CreateMerchantStoreOrder(f.checkout("missing-gate-new", "platform:waffo_pancake"))
	require.ErrorIs(t, err, ErrMerchantStoreWriterFrozen)
}

func TestMerchantStoreWriterGateKeepsUnissuedFeeRefundAndIssuedClosure(t *testing.T) {
	for _, issued := range []bool{false, true} {
		t.Run(map[bool]string{false: "unissued", true: "issued-closed"}[issued], func(t *testing.T) {
			f := newStoreFixture(t, "external:epay")
			o, _, err := CreateMerchantStoreOrder(f.checkout("before-freeze", "external:epay"))
			require.NoError(t, err)
			if issued {
				require.NoError(t, BindMerchantStorePaymentQuote(o.ID, 671, "CNY", "6.71"))
				require.NoError(t, BindMerchantStorePaymentContext(o.ID, "frozen-gateway-context"))
			}
			storeUnsupportedWriterGateForTest(t)
			if issued {
				require.NoError(t, ConfirmMerchantStoreOrderPaymentClosed(o.ID, "authentic-provider-closure"))
			} else {
				require.NoError(t, CancelMerchantStoreOrder(f.buyer.Id, o.ID))
			}
			storeBalance(t, f.seller.Id, 10000000)
			storeBalance(t, f.root.Id, 0)
			var stock int64
			require.NoError(t, DB.Model(&MerchantStoreStock{}).Where("product_id = ? AND state = ?", f.product.ID, "available").Count(&stock).Error)
			require.EqualValues(t, 2, stock)
		})
	}
}

func TestMerchantStoreWriterGateStopsAsynchronousAIProductPublication(t *testing.T) {
	db, seller, root, p := marketAIProduct(t, setting.MarketAIReviewAuto)
	job := marketAIClaim(t)
	storeUnsupportedWriterGateForTest(t)
	var before, after ModerationJob
	require.NoError(t, db.First(&before, job.ID).Error)
	wrongLease := *job
	wrongLease.LeaseOwner = "another-worker"
	require.ErrorIs(t, marketAIComplete(t, &wrongLease, false, true), ErrModerationLeaseLost)
	require.NoError(t, db.First(&after, job.ID).Error)
	require.Equal(t, before, after, "a frozen writer must not let another worker consume a valid lease")
	require.NoError(t, marketAIComplete(t, job, false, true))
	require.NoError(t, db.First(p, "id = ?", p.ID).Error)
	require.Equal(t, "pending", p.Status)
	require.NoError(t, db.First(job, "id = ?", job.ID).Error)
	require.Equal(t, ModerationJobCompleted, job.Status)
	require.Equal(t, "manual_required", job.MarketOutcome)
	require.Equal(t, "market_review_writer_upgrade", job.ErrorMessage)
	require.Empty(t, job.Payload)
	require.Empty(t, job.LeaseOwner)
	require.Zero(t, job.LeaseUntil)
	marketAINoFees(t, seller, root)
}

func TestMerchantStoreWriterGateActivationRequiresSchemaAndNeverDowngrades(t *testing.T) {
	db := marketTestDB(t)
	require.NoError(t, db.Exec("CREATE TABLE merchant_store_stocks (id TEXT PRIMARY KEY)").Error)
	require.NoError(t, db.Exec("CREATE TABLE merchant_store_orders (id TEXT PRIMARY KEY)").Error)
	require.ErrorIs(t, ActivateMerchantStoreVariants(db, 1), ErrMerchantStoreWriterFrozen)
	for _, statement := range []string{
		"CREATE TABLE merchant_store_variants (id TEXT PRIMARY KEY, product_id TEXT, name TEXT, price_quota BIGINT, template TEXT, enabled BOOLEAN)",
		"ALTER TABLE merchant_store_stocks ADD COLUMN variant_id TEXT NULL",
		"ALTER TABLE merchant_store_orders ADD COLUMN variant_id TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE merchant_store_orders ADD COLUMN variant_name TEXT NOT NULL DEFAULT ''",
	} {
		require.NoError(t, db.Exec(statement).Error)
	}
	require.ErrorIs(t, ActivateMerchantStoreVariants(db, 2), ErrMerchantStoreWriterFrozen)
	require.NoError(t, ActivateMerchantStoreVariants(db, 1))
	require.NoError(t, ActivateMerchantStoreVariants(db, 1))
	require.NoError(t, ActivateMerchantStoreVariants(db, 2))
	require.NoError(t, BootstrapMerchantStoreWriterGate(db))
	status, err := GetMerchantStoreWriterGateStatus(db)
	require.NoError(t, err)
	require.Equal(t, 2, status.RequiredCapability)
	require.Equal(t, MerchantStoreWriterCapability >= 2, status.NewWritesAllowed)
	if MerchantStoreWriterCapability < 2 {
		require.ErrorIs(t, checkMerchantStoreWriterMigration(db), ErrMerchantStoreWriterFrozen)
	} else {
		require.NoError(t, checkMerchantStoreWriterMigration(db))
	}
}
