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
	for _, item := range storeFixedContentModels() {
		require.NoError(t, db.Migrator().DropTable(item))
	}
}

func TestMerchantStoreFixedContentPostgresPreparation(t *testing.T) {
	t.Run("two_private_tables_preserve_frozen_phase_six_and_paid_facts", func(t *testing.T) {
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
		for _, item := range storeFixedContentModels() {
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
		require.Equal(t, []string{"merchant_store_fixed_contents", "merchant_store_order_fixed_deliveries"}, added)
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
	t.Run("failed_second_table_DDL_rolls_back_all_additions", func(t *testing.T) {
		db, name, _, _ := merchantStorePGDB(t)
		merchantStorePGFixture(t, db, "balance")
		storeFixedPhaseSixTest(t, db)
		before := storePhaseSixOldFacts(t, db)
		trigger := "fixed_seven_" + strings.TrimPrefix(name, "lmm_merchant_store_test_")
		function := trigger + "_reject"
		sql := fmt.Sprintf(`CREATE FUNCTION %s() RETURNS event_trigger LANGUAGE plpgsql AS $$ BEGIN IF EXISTS (SELECT 1 FROM pg_event_trigger_ddl_commands() WHERE schema_name = '%s' AND object_identity LIKE '%%merchant_store_order_fixed_deliveries') THEN RAISE EXCEPTION 'private-fixed-content-DDL-failure'; END IF; END $$`, function, name)
		require.NoError(t, db.Exec(sql).Error)
		require.NoError(t, db.Exec("CREATE EVENT TRIGGER "+trigger+" ON ddl_command_end WHEN TAG IN ('CREATE TABLE') EXECUTE FUNCTION "+name+"."+function+"()").Error)
		t.Cleanup(func() {
			require.NoError(t, db.Exec("DROP EVENT TRIGGER IF EXISTS "+trigger).Error)
			require.NoError(t, db.Exec("DROP FUNCTION IF EXISTS "+function+"()").Error)
		})
		require.Error(t, PrepareMerchantStoreFixedContent(db, 6))
		for _, item := range storeFixedContentModels() {
			require.False(t, db.Migrator().HasTable(item))
		}
		require.Equal(t, before.rows, storePreparationFacts(t, db, before).rows)
		floor, err := storeWriterGateRow(db, "")
		require.NoError(t, err)
		require.Equal(t, 6, floor)
	})
	t.Run("durable_deployment_owner_blocks_every_phase_seven_operation", func(t *testing.T) {
		db, _, _, _ := merchantStorePGDB(t)
		merchantStorePGFixture(t, db, "balance")
		storeFixedPhaseSixTest(t, db)
		require.NoError(t, db.Create(&Option{Key: deploymentfence.OptionPrefix + "fixed-test:owner", Value: "malformed"}).Error)
		require.ErrorIs(t, PrepareMerchantStoreFixedContent(db, 6), ErrMerchantStoreWriterFrozen)
		require.ErrorIs(t, VerifyMerchantStoreFixedContent(db), ErrMerchantStoreWriterFrozen)
		require.ErrorIs(t, ActivateMerchantStoreFixedContent(db, 6), ErrMerchantStoreWriterFrozen)
		for _, item := range storeFixedContentModels() {
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
