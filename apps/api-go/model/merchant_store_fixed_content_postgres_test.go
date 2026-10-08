package model

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/internal/deploymentfence"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func storeFixedPhaseSixTest(t *testing.T, db *gorm.DB) {
	t.Helper()
	storeActivatePhaseFiveForSixTest(t, db)
	require.NoError(t, PrepareMerchantStorePhaseSix(db, 5))
	require.NoError(t, ActivateMerchantStorePhaseSix(db, 5))
	for _, item := range storePhaseSevenModels() {
		require.NoError(t, db.Migrator().DropTable(item))
	}
}

func TestMerchantStoreFixedContentHistoricalPreparationExcludesPhaseSeven(t *testing.T) {
	for _, floor := range []int{4, 5, 6} {
		t.Run(fmt.Sprint(floor), func(t *testing.T) {
			newStoreFixture(t, "balance")
			for _, item := range storePhaseSevenModels() {
				require.NoError(t, DB.Migrator().DropTable(item))
			}
			storeWriterGateForTest(t, fmt.Sprint(floor))
			require.NoError(t, storeCheckMerchantStoreSchema(DB, floor))
			require.NoError(t, PrepareMerchantStoreSchema(DB, floor))
			require.NoError(t, storeCheckMerchantStoreSchema(DB, floor))
			for _, item := range storePhaseSevenModels() {
				require.False(t, DB.Migrator().HasTable(item), "historical preparation excludes all four phase-seven tables")
			}
		})
	}
}

func TestMerchantStoreFixedContentPostgresPreparation(t *testing.T) {
	t.Run("four_tables_preserve_frozen_phase_six_and_paid_facts", func(t *testing.T) {
		db, name, _, _ := merchantStorePGDB(t)
		f := merchantStorePGFixture(t, db, "balance")
		_, err := AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{"old-paid-card", "old-available-card"})
		require.NoError(t, err)
		in := f.checkout("before-fixed-schema", "balance")
		in.PickupCode, in.PickupEmail = "", ""
		_, _, err = CreateMerchantStoreOrder(in)
		require.NoError(t, err)
		storeFixedPhaseSixTest(t, db)
		before := storePhaseSixOldFacts(t, db)
		oldTables := merchantStorePGTables(t, db, name)
		require.ErrorIs(t, VerifyMerchantStoreFixedContent(db), ErrMerchantStoreWriterFrozen)
		require.ErrorIs(t, ActivateMerchantStoreFixedContent(db, 6), ErrMerchantStoreWriterFrozen)
		require.ErrorIs(t, PrepareMerchantStoreFixedContent(db, 5), ErrMerchantStoreWriterFrozen)
		require.NoError(t, PrepareMerchantStoreSchema(db, 6))
		for _, item := range storePhaseSevenModels() {
			require.False(t, db.Migrator().HasTable(item), "historical preparation never installs phase seven")
		}
		require.Equal(t, before.rows, storePreparationFacts(t, db, before).rows)
		require.NoError(t, PrepareMerchantStoreFixedContent(db, 6))
		require.NoError(t, VerifyMerchantStoreFixedContent(db))
		require.NoError(t, PrepareMerchantStoreFixedContent(db, 6))
		require.Equal(t, before.rows, storePreparationFacts(t, db, before).rows)
		var added []string
		for _, table := range merchantStorePGTables(t, db, name) {
			found := false
			for _, old := range oldTables {
				found = found || old == table
			}
			if !found {
				added = append(added, table)
			}
		}
		require.Equal(t, []string{"merchant_store_fixed_contents", "merchant_store_order_fixed_deliveries", "merchant_store_product_traffic_days", "merchant_store_product_traffic_receipts"}, added)
		require.False(t, MerchantStoreFixedContentSupported(), "installation does not raise the floor")
		require.ErrorIs(t, db.Transaction(storeRequireFixedContentWriter), ErrMerchantStoreWriterFrozen)
		require.NoError(t, ActivateMerchantStoreFixedContent(db, 6))
		require.NoError(t, ActivateMerchantStoreFixedContent(db, 7))
		require.True(t, MerchantStoreFixedContentSupported())
		require.True(t, MerchantStoreCategoriesSupported(), "phase six remains available at the next floor")
		require.NoError(t, VerifyMerchantStoreFixedContent(db))
		require.NoError(t, db.Exec("ALTER TABLE merchant_store_fixed_contents ALTER COLUMN ciphertext DROP NOT NULL").Error)
		require.ErrorIs(t, VerifyMerchantStoreFixedContent(db), ErrMerchantStoreWriterFrozen)
		require.ErrorIs(t, ActivateMerchantStoreFixedContent(db, 7), ErrMerchantStoreWriterFrozen, "retry verifies the actual catalogue and never repairs it")
	})
	for _, failure := range []struct{ name, table string }{
		{"failed_second_table_DDL_rolls_back_all_additions", "merchant_store_order_fixed_deliveries"},
		{"failed_fourth_analytics_table_DDL_rolls_back_all_additions", "merchant_store_product_traffic_receipts"},
	} {
		t.Run(failure.name, func(t *testing.T) {
			db, name, _, _ := merchantStorePGDB(t)
			merchantStorePGFixture(t, db, "balance")
			storeFixedPhaseSixTest(t, db)
			before := storePhaseSixOldFacts(t, db)
			trigger := "fixed_seven_" + strings.TrimPrefix(name, "lmm_merchant_store_test_")
			function := trigger + "_reject"
			sql := fmt.Sprintf(`CREATE FUNCTION %s() RETURNS event_trigger LANGUAGE plpgsql AS $$ BEGIN IF EXISTS (SELECT 1 FROM pg_event_trigger_ddl_commands() WHERE schema_name = '%s' AND object_identity LIKE '%%%s') THEN RAISE EXCEPTION 'private-phase-seven-DDL-failure'; END IF; END $$`, function, name, failure.table)
			require.NoError(t, db.Exec(sql).Error)
			require.NoError(t, db.Exec("CREATE EVENT TRIGGER "+trigger+" ON ddl_command_end WHEN TAG IN ('CREATE TABLE') EXECUTE FUNCTION "+name+"."+function+"()").Error)
			t.Cleanup(func() {
				require.NoError(t, db.Exec("DROP EVENT TRIGGER IF EXISTS "+trigger).Error)
				require.NoError(t, db.Exec("DROP FUNCTION IF EXISTS "+function+"()").Error)
			})
			require.ErrorContains(t, PrepareMerchantStoreFixedContent(db, 6), "private-phase-seven-DDL-failure", "injected failure must occur at the declared table")
			for _, item := range storePhaseSevenModels() {
				require.False(t, db.Migrator().HasTable(item))
			}
			require.Equal(t, before.rows, storePreparationFacts(t, db, before).rows)
			floor, err := storeWriterGateRow(db, "")
			require.NoError(t, err)
			require.Equal(t, 6, floor)
		})
	}
	t.Run("durable_deployment_owner_blocks_every_phase_seven_operation", func(t *testing.T) {
		db, _, _, _ := merchantStorePGDB(t)
		merchantStorePGFixture(t, db, "balance")
		storeFixedPhaseSixTest(t, db)
		require.NoError(t, db.Create(&Option{Key: deploymentfence.OptionPrefix + "fixed-test:owner", Value: "malformed"}).Error)
		require.ErrorIs(t, PrepareMerchantStoreFixedContent(db, 6), ErrMerchantStoreWriterFrozen)
		require.ErrorIs(t, VerifyMerchantStoreFixedContent(db), ErrMerchantStoreWriterFrozen)
		require.ErrorIs(t, ActivateMerchantStoreFixedContent(db, 6), ErrMerchantStoreWriterFrozen)
		for _, item := range storePhaseSevenModels() {
			require.False(t, db.Migrator().HasTable(item))
		}
	})
	t.Run("activation_waits_for_existing_writer_floor_SHARE", func(t *testing.T) {
		db, name, observer, _ := merchantStorePGDB(t)
		merchantStorePGFixture(t, db, "balance")
		storeFixedPhaseSixTest(t, db)
		require.NoError(t, PrepareMerchantStoreFixedContent(db, 6))
		tx := db.Begin()
		require.NoError(t, tx.Error)
		defer tx.Rollback()
		require.NoError(t, storeRequireWriter(tx))
		activated := make(chan error, 1)
		go func() { activated <- ActivateMerchantStoreFixedContent(db, 6) }()
		require.Eventually(t, func() bool {
			var held int64
			err := observer.Raw("SELECT COUNT(*) FROM pg_catalog.pg_stat_activity WHERE application_name = ? AND wait_event_type = 'Lock'", name).Scan(&held).Error
			return err == nil && held > 0
		}, 5*time.Second, 20*time.Millisecond, "activation must wait on the real writer's row lock")
		select {
		case err := <-activated:
			t.Fatalf("activation bypassed the existing writer: %v", err)
		default:
		}
		require.NoError(t, tx.Commit().Error)
		select {
		case err := <-activated:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("activation failed to resume after the existing writer committed")
		}
		floor, err := storeWriterGateRow(db, "")
		require.NoError(t, err)
		require.Equal(t, 7, floor)
	})
}

func TestMerchantStoreFixedContentPostgresAnalyticsSchemaQualification(t *testing.T) {
	db, _, _, _ := merchantStorePGDB(t)
	merchantStorePGFixture(t, db, "balance")
	storeFixedPhaseSixTest(t, db)
	require.NoError(t, PrepareMerchantStoreFixedContent(db, 6))
	installed := runtimeVerificationPostgresFacts(t, db)
	require.NoError(t, VerifyMerchantStoreFixedContent(db))
	require.Equal(t, installed, runtimeVerificationPostgresFacts(t, db), "verification before activation is read-only")
	require.NoError(t, ActivateMerchantStoreFixedContent(db, 6))
	activated := runtimeVerificationPostgresFacts(t, db)
	require.NoError(t, ActivateMerchantStoreFixedContent(db, 7))
	require.Equal(t, activated, runtimeVerificationPostgresFacts(t, db), "activation retry is read-only")
	for _, damage := range []struct {
		name               string
		ddl, restore       []string
		recreateTableModel any
	}{
		{name: "missing-days", ddl: []string{"DROP TABLE merchant_store_product_traffic_days"}, recreateTableModel: &MerchantStoreProductTrafficDay{}},
		{name: "missing-receipts", ddl: []string{"DROP TABLE merchant_store_product_traffic_receipts"}, recreateTableModel: &MerchantStoreProductTrafficReceipt{}},
		{name: "counter-not-bigint", ddl: []string{"ALTER TABLE merchant_store_product_traffic_days ALTER COLUMN impressions TYPE integer"}, restore: []string{"ALTER TABLE merchant_store_product_traffic_days ALTER COLUMN impressions TYPE bigint"}},
		{name: "nullable-counter", ddl: []string{"ALTER TABLE merchant_store_product_traffic_days ALTER COLUMN clicks DROP NOT NULL"}, restore: []string{"ALTER TABLE merchant_store_product_traffic_days ALTER COLUMN clicks SET NOT NULL"}},
		{name: "reordered-composite-primary-key", ddl: []string{"ALTER TABLE merchant_store_product_traffic_days DROP CONSTRAINT merchant_store_product_traffic_days_pkey", "ALTER TABLE merchant_store_product_traffic_days ADD PRIMARY KEY (day, product_id)"}, restore: []string{"ALTER TABLE merchant_store_product_traffic_days DROP CONSTRAINT merchant_store_product_traffic_days_pkey", "ALTER TABLE merchant_store_product_traffic_days ADD PRIMARY KEY (product_id, day)"}},
		{name: "narrow-receipt-kind", ddl: []string{"ALTER TABLE merchant_store_product_traffic_receipts ALTER COLUMN kind TYPE varchar(8)"}, restore: []string{"ALTER TABLE merchant_store_product_traffic_receipts ALTER COLUMN kind TYPE varchar(16)"}},
		{name: "non-string-receipt-kind", ddl: []string{"ALTER TABLE merchant_store_product_traffic_receipts ALTER COLUMN kind TYPE bigint USING 0"}, restore: []string{"ALTER TABLE merchant_store_product_traffic_receipts ALTER COLUMN kind TYPE varchar(16) USING kind::varchar(16)"}},
		{name: "nullable-receipt-kind", ddl: []string{"ALTER TABLE merchant_store_product_traffic_receipts ALTER COLUMN kind DROP NOT NULL"}, restore: []string{"ALTER TABLE merchant_store_product_traffic_receipts ALTER COLUMN kind SET NOT NULL"}},
		{name: "wrong-product-index", ddl: []string{"DROP INDEX idx_merchant_store_product_traffic_receipts_product_id", "CREATE INDEX idx_merchant_store_product_traffic_receipts_product_id ON merchant_store_product_traffic_receipts (kind)"}, restore: []string{"DROP INDEX idx_merchant_store_product_traffic_receipts_product_id", "CREATE INDEX idx_merchant_store_product_traffic_receipts_product_id ON merchant_store_product_traffic_receipts (product_id)"}},
		{name: "missing-receipt-time-index", ddl: []string{"DROP INDEX idx_merchant_store_product_traffic_receipts_created_at"}, restore: []string{"CREATE INDEX idx_merchant_store_product_traffic_receipts_created_at ON merchant_store_product_traffic_receipts (created_at)"}},
	} {
		t.Run(damage.name, func(t *testing.T) {
			// Activation owns a physical session, so expose the intentional DDL
			// to that session before invoking its read-only qualification.
			for _, ddl := range damage.ddl {
				require.NoError(t, db.Exec(ddl).Error)
			}
			missing := runtimeVerificationPostgresFacts(t, db)
			require.ErrorIs(t, VerifyMerchantStoreFixedContent(db), ErrMerchantStoreWriterFrozen)
			require.ErrorIs(t, ActivateMerchantStoreFixedContent(db, 7), ErrMerchantStoreWriterFrozen)
			require.NoError(t, storeCheckMerchantStoreSchema(db, 6), "old-floor qualification excludes every phase-seven table")
			require.Equal(t, missing, runtimeVerificationPostgresFacts(t, db), "qualification never repairs schema or rows, or changes the floor")
			if damage.recreateTableModel != nil {
				require.NoError(t, db.AutoMigrate(damage.recreateTableModel))
			}
			for _, ddl := range damage.restore {
				require.NoError(t, db.Exec(ddl).Error)
			}
			require.NoError(t, VerifyMerchantStoreFixedContent(db))
			require.NoError(t, ActivateMerchantStoreFixedContent(db, 7))
			require.Equal(t, activated, runtimeVerificationPostgresFacts(t, db), "test-only restoration returns the exact qualified catalogue")
		})
	}
}

func TestMerchantStoreFixedContentPostgresMixedVariantsLimitsAndRefunds(t *testing.T) {
	db, name, observer, _ := merchantStorePGDB(t)
	f := merchantStorePGFixture(t, db, "balance")
	storeActivateFixedTest(t, db)
	_, err := SaveMerchantStoreSellerTerms(f.seller.Id, MerchantStoreTermsInput{Content: "Each paid order receives its selected variant."})
	require.NoError(t, err)
	content := storeFixedTestContent
	v, err := SaveMerchantStoreVariant(f.seller.Id, f.product.ID, "", MerchantStoreVariantInput{Name: "Shared tutorial", PriceQuota: 500000, Template: MerchantStoreFixedContentTemplate, FixedContent: &content, Enabled: true})
	require.NoError(t, err)
	_, err = AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{"unique-first", "unique-second", "unique-third"})
	require.NoError(t, err)
	require.NoError(t, SubmitMerchantStoreProduct(f.seller.Id, f.product.ID))
	require.NoError(t, ReviewMerchantStoreProduct(f.root.Id, f.product.ID, true, ""))
	require.NoError(t, db.Model(f.product).Updates(map[string]any{"max_quantity_per_buyer": 2, "max_quantity_per_order": 1, "sale_limit": 3}).Error)
	card := storeFixedCheckout(t, f, "one-card", "balance")
	card.VariantID, card.PickupCode, card.PickupEmail = MerchantStoreDefaultVariantID(f.product.ID), "", ""
	cardOrder, _, err := CreateMerchantStoreOrder(card)
	require.NoError(t, err)
	inputs := make([]MerchantStoreCheckoutInput, 8)
	ops := make([]func() error, len(inputs))
	for i := range inputs {
		in := storeFixedCheckout(t, f, fmt.Sprintf("fixed-contend-%d", i), "balance")
		in.VariantID, in.PickupCode, in.PickupEmail = v.ID, "", ""
		inputs[i] = in
		ops[i] = func() error { _, _, err := CreateMerchantStoreOrder(in); return err }
	}
	success := 0
	for _, err := range merchantStorePGContend(t, db, observer, name, ops) {
		if err == nil {
			success++
		} else {
			require.ErrorIs(t, err, ErrMerchantStorePurchaseLimit)
		}
	}
	require.Equal(t, 1, success, "unique cards and stockless text share the same buyer cap")
	var fixed MerchantStoreOrder
	require.NoError(t, db.Where("product_id = ? AND delivery_template = ?", f.product.ID, MerchantStoreFixedContentTemplate).First(&fixed).Error)
	var stock int64
	require.NoError(t, db.Model(&MerchantStoreStock{}).Where("product_id = ? AND state = ?", f.product.ID, "available").Count(&stock).Error)
	require.EqualValues(t, 2, stock, "shared delivery never borrows another variant's cards")
	refund, err := RequestMerchantStoreRefund(f.buyer.Id, fixed.ID, refundFull("return-shared"))
	require.NoError(t, err)
	require.Empty(t, refund.StockIDs)
	refundApprove(t, f.seller.Id, &fixed, refund)
	in := storeFixedCheckout(t, f, "after-completed-refund", "balance")
	in.VariantID, in.PickupCode, in.PickupEmail = v.ID, "", ""
	_, _, err = CreateMerchantStoreOrder(in)
	require.NoError(t, err, "only completed quantity refunds release buyer capacity")
	_, _, err = CreateMerchantStoreOrder(storeFixedCheckout(t, f, "lifetime-cap", "balance"))
	require.True(t, errors.Is(err, ErrMerchantStoreVariantRequired) || errors.Is(err, ErrMerchantStoreStock))
	in.RequestKey = "lifetime-cap-explicit"
	_, _, err = CreateMerchantStoreOrder(in)
	require.ErrorIs(t, err, ErrMerchantStoreStock, "refunds do not reset lifetime sales")
	var items int64
	require.NoError(t, db.Model(&MerchantStoreRefundItem{}).Where("order_id = ?", fixed.ID).Count(&items).Error)
	require.Zero(t, items)
	token, err := GetMerchantStoreOrderPickupToken(f.buyer.Id, cardOrder.ID)
	require.NoError(t, err)
	claim, err := ClaimMerchantStoreOrder(token, "", f.buyer.Id)
	require.NoError(t, err)
	require.Equal(t, []string{"unique-first"}, claim.Items, "existing per-item delivery remains unchanged")
}
