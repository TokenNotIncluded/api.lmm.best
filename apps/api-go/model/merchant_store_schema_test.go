package model

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm/schema"
)

type merchantStoreColumnPlan struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Size       int    `json:"size,omitempty"`
	PrimaryKey bool   `json:"primary_key,omitempty"`
	NotNull    bool   `json:"not_null,omitempty"`
}
type merchantStoreIndexPlan struct {
	Name    string   `json:"name"`
	Unique  bool     `json:"unique,omitempty"`
	Columns []string `json:"columns"`
}
type merchantStoreTablePlan struct {
	Name    string                    `json:"name"`
	Columns []merchantStoreColumnPlan `json:"columns"`
	Indexes []merchantStoreIndexPlan  `json:"indexes"`
}

var merchantStoreExpectedTables = []string{
	"merchant_store_cart_items", "merchant_store_catalogue_metadata", "merchant_store_configs", "merchant_store_disclaimer_acceptances", "merchant_store_discount_codes", "merchant_store_email_deliveries", "merchant_store_email_verification_challenges", "merchant_store_events", "merchant_store_favorites", "merchant_store_gateways", "merchant_store_guest_email_verifications", "merchant_store_guests", "merchant_store_order_search_authorizations", "merchant_store_order_search_challenges", "merchant_store_orders", "merchant_store_payment_receipts", "merchant_store_products", "merchant_store_promotions", "merchant_store_refund_items", "merchant_store_refund_payment_bases", "merchant_store_refund_provider_attempts", "merchant_store_refunds", "merchant_store_seller_terms", "merchant_store_stocks", "merchant_store_terms_acceptances", "merchant_store_transfers", "merchant_store_variants", "merchant_store_verified_emails",
}

func merchantStoreSourceSchemaPlan(t *testing.T) []merchantStoreTablePlan {
	t.Helper()
	cache := &sync.Map{}
	plan := make([]merchantStoreTablePlan, 0, len(MerchantStoreModels()))
	for _, model := range MerchantStoreModels() {
		parsed, e := schema.Parse(model, cache, schema.NamingStrategy{})
		require.NoError(t, e)
		table := merchantStoreTablePlan{Name: parsed.Table, Columns: []merchantStoreColumnPlan{}, Indexes: []merchantStoreIndexPlan{}}
		for _, field := range parsed.Fields {
			if field.DBName == "" {
				continue
			}
			kind := string(field.DataType)
			if declared := field.TagSettings["TYPE"]; declared != "" {
				kind = declared
			}
			table.Columns = append(table.Columns, merchantStoreColumnPlan{Name: field.DBName, Type: kind, Size: field.Size, PrimaryKey: field.PrimaryKey, NotNull: field.NotNull})
		}
		indexes := parsed.ParseIndexes()
		names := make([]string, 0, len(indexes))
		for name := range indexes {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			index := indexes[name]
			entry := merchantStoreIndexPlan{Name: name, Unique: strings.EqualFold(index.Class, "UNIQUE"), Columns: []string{}}
			for _, field := range index.Fields {
				entry.Columns = append(entry.Columns, field.DBName)
			}
			table.Indexes = append(table.Indexes, entry)
		}
		plan = append(plan, table)
	}
	sort.Slice(plan, func(i, j int) bool { return plan[i].Name < plan[j].Name })
	return plan
}

// This manifest describes source-intended additions. It is not a replacement
// for applying the final binary to the independent production-database clone.
func TestMerchantStoreSchemaPlan(t *testing.T) {
	plan := merchantStoreSourceSchemaPlan(t)
	names := make([]string, len(plan))
	for i, p := range plan {
		names[i] = p.Name
	}
	require.Equal(t, merchantStoreExpectedTables, names)
	byTable := map[string]merchantStoreTablePlan{}
	for _, table := range plan {
		byTable[table.Name] = table
	}
	for _, entry := range []struct {
		Table, Column string
		Size          int
	}{{"merchant_store_orders", "status", 32}, {"merchant_store_email_deliveries", "state", 32}, {"merchant_store_configs", "linux_do_units_per_usd", 64}} {
		found := false
		for _, column := range byTable[entry.Table].Columns {
			if column.Name == entry.Column {
				found = true
				require.GreaterOrEqual(t, column.Size, entry.Size)
			}
		}
		require.True(t, found, "column %s.%s exists", entry.Table, entry.Column)
	}
	for _, entry := range []struct{ Table, Column string }{{"merchant_store_products", "price_quota"}, {"merchant_store_products", "max_quantity_per_order"}, {"merchant_store_products", "max_quantity_per_buyer"}, {"merchant_store_variants", "price_quota"}, {"merchant_store_orders", "unit_price_quota"}, {"merchant_store_orders", "price_quota"}, {"merchant_store_orders", "original_price_quota"}, {"merchant_store_orders", "discount_quota"}, {"merchant_store_orders", "fee_quota"}, {"merchant_store_orders", "amount_minor"}, {"merchant_store_configs", "promotion_quota"}, {"merchant_store_configs", "minimum_unit_price_quota"}, {"merchant_store_transfers", "quota"}, {"merchant_store_refunds", "principal_quota"}, {"merchant_store_refunds", "amount_minor"}, {"merchant_store_refunds", "retained_fee_quota"}, {"merchant_store_refund_payment_bases", "amount_minor"}, {"merchant_store_refund_provider_attempts", "lease_until"}, {"merchant_store_refund_provider_attempts", "next_check_at"}} {
		found := false
		for _, column := range byTable[entry.Table].Columns {
			if column.Name == entry.Column {
				found = true
				require.Equal(t, "bigint", column.Type)
			}
		}
		require.True(t, found)
	}
	configSchema, e := schema.Parse(&MerchantStoreConfig{}, &sync.Map{}, schema.NamingStrategy{})
	require.NoError(t, e)
	minimum := configSchema.LookUpField("MinimumUnitPriceQuota")
	require.NotNil(t, minimum)
	require.True(t, minimum.NotNull)
	require.True(t, minimum.HasDefaultValue)
	require.Equal(t, "500000", minimum.DefaultValue)
	// Legacy stock stays nullable and untouched. Historical orders acquire empty
	// snapshots, rather than an invented association to today's default variant.
	stockSchema, e := schema.Parse(&MerchantStoreStock{}, &sync.Map{}, schema.NamingStrategy{})
	require.NoError(t, e)
	stockVariant := stockSchema.LookUpField("VariantID")
	require.NotNil(t, stockVariant)
	require.False(t, stockVariant.NotNull)
	require.False(t, stockVariant.HasDefaultValue)
	require.Equal(t, "varchar(36)", stockVariant.TagSettings["TYPE"])
	orderSchema, e := schema.Parse(&MerchantStoreOrder{}, &sync.Map{}, schema.NamingStrategy{})
	require.NoError(t, e)
	for _, fieldName := range []string{"VariantID", "VariantName"} {
		field := orderSchema.LookUpField(fieldName)
		require.NotNil(t, field)
		require.True(t, field.NotNull)
		require.True(t, field.HasDefaultValue)
		require.Empty(t, field.DefaultValue)
	}
	for _, fieldName := range []string{"OriginalPriceQuota", "DiscountQuota", "DiscountBPS", "PromotionID", "PromotionCode"} {
		field := orderSchema.LookUpField(fieldName)
		require.NotNil(t, field)
		require.True(t, field.NotNull)
		require.True(t, field.HasDefaultValue, "historical orders retain empty promotion snapshots")
	}
	stockIndexes := map[string][]string{}
	for _, index := range byTable["merchant_store_stocks"].Indexes {
		stockIndexes[index.Name] = index.Columns
	}
	require.Equal(t, []string{"product_id", "state", "position"}, stockIndexes["store_stock_available"], "retain the N-1 lookup index")
	require.Equal(t, []string{"product_id", "variant_id", "state", "position"}, stockIndexes["store_stock_variant_available"])
	encoded, e := json.MarshalIndent(struct {
		Scope  string                   `json:"scope"`
		Tables []merchantStoreTablePlan `json:"tables"`
	}{Scope: "Merchant-store model set including one guest/address email verification table; includes the exact access, catalogue and guest-email groups (28 tables); no user, wallet or financial-history DDL", Tables: plan}, "", "  ")
	require.NoError(t, e)
	if output := os.Getenv("MERCHANT_STORE_SCHEMA_PLAN_OUTPUT"); output != "" {
		file, e := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		require.NoError(t, e, "manifest output must be a new file")
		_, e = file.Write(append(encoded, '\n'))
		require.NoError(t, e)
		require.NoError(t, file.Close())
	}
	t.Log(string(encoded))
}
