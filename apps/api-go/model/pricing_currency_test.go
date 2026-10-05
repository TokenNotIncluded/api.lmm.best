package model

import (
	"encoding/json"
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/pkg/billingexpr"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"maps"
	"testing"
)

func pricingCurrencyFixture(t *testing.T, q, k float64) {
	t.Helper()
	oldQ := common.QuotaPerUnit
	oldK, e := common.CreditsPerUSD()
	common.QuotaPerUnit = q
	require.NoError(t, common.SetCreditsPerUSD(decimal.NewFromFloat(k)))
	t.Cleanup(func() {
		common.QuotaPerUnit = oldQ
		if e != nil {
			common.ClearCreditsPerUSD()
		} else {
			require.NoError(t, common.SetCreditsPerUSD(oldK))
		}
	})
}

func TestUSDModelRatioLiteralQuotesIgnoreLegacyCalibration(t *testing.T) {
	for _, q := range []float64{500000, 1000000, 3000000} {
		t.Run(decimal.NewFromFloat(q).String(), func(t *testing.T) {
			pricingCurrencyFixture(t, q, 2500000)
			rows, err := NormalizePricingUSD([]Pricing{{ModelName: "literal", ModelRatio: 1.25, CompletionRatio: 2}})
			require.NoError(t, err)
			require.Equal(t, 0.5, *rows[0].InputPrice)
			require.Equal(t, 1.0, *rows[0].OutputPrice)
			require.Equal(t, 1.25, rows[0].ModelRatio)
			require.Equal(t, 2.0, rows[0].CompletionRatio)
		})
	}
}
func TestUSDPriceReadSavePreservesAllLegacyBytesAndLocks(t *testing.T) {
	setupPriceLockTest(t)
	pricingCurrencyFixture(t, 500000, 4500000)
	raw := priceOptionSnapshot()
	for k := range raw {
		raw[k] = ` { "unchanged" : 1.000000 , "tiny" : 0.000001 } `
	}
	raw["ModelPrice"] = ` { "fixed" : 0.05 , "translate":0.034, "transcribe":0.017, "jev":0.042, "tiny":0.000001 } `
	raw[operation_setting.ToolPriceOptionKey] = ` { "web_search" : 10.000000 , "tiny" : 0.000001 } `
	raw[ModelPriceLocksOptionKey] = ` { "fixed" : true } `
	raw["billing_setting.billing_mode"] = ` { "jev":"tiered_expr", "minute":"tiered_expr" } `
	raw["billing_setting.billing_expr"] = ` { "jev" : "tier(\"base\", p*0.042)", "minute":"tier(\"base\", audio_s * 0.05 * 1000000 / 60)" } `
	for k, v := range raw {
		require.NoError(t, DB.Create(&Option{Key: k, Value: v}).Error)
	}
	anchor, _ := common.CreditsPerUSD()
	require.NoError(t, DB.Create(&Option{Key: CreditsPerUSDOptionKey, Value: anchor.String()}).Error)
	before, e := GetUSDPriceConfig()
	require.NoError(t, e)
	r := USDPriceUpdate{SchemaVersion: 2, Currency: "USD", ExpectedRevision: before.Revision, Values: maps.Clone(before.Values)}
	preview, _, e := ValidateUSDPriceConfig(r)
	require.NoError(t, e)
	require.Equal(t, before.Revision, preview.Revision)
	after, res, e := UpdateUSDPriceConfig(r)
	require.NoError(t, e)
	require.Empty(t, res.LockedModels)
	require.Equal(t, before.Revision, after.Revision)
	for k, v := range raw {
		require.Equal(t, v, persistedPriceOption(t, k), k)
	}
	require.Equal(t, before.Values, after.Values)
	require.InDelta(t, 10.0/9, before.ToolPriceDefaults["web_search"], 1e-15)
}
func TestUSDPriceEditPreservesEntriesAndRejectsStaleRevision(t *testing.T) {
	setupPriceLockTest(t)
	pricingCurrencyFixture(t, 500000, 3000000)
	raw := priceOptionSnapshot()
	raw["ModelPrice"] = `{"locked":0.05000,"keep":0.017000,"edit":0.034,"tiny":0.000001}`
	raw[ModelPriceLocksOptionKey] = `{"locked":true}`
	for k, v := range raw {
		require.NoError(t, DB.Create(&Option{Key: k, Value: v}).Error)
	}
	anchor, _ := common.CreditsPerUSD()
	require.NoError(t, DB.Create(&Option{Key: CreditsPerUSDOptionKey, Value: anchor.String()}).Error)
	before, e := GetUSDPriceConfig()
	require.NoError(t, e)
	entries, e := rawPriceMap(before.Values["ModelPrice"])
	require.NoError(t, e)
	entries["locked"] = json.RawMessage(`2`)
	entries["edit"] = json.RawMessage(`0.042`)
	encoded, e := json.Marshal(entries)
	require.NoError(t, e)
	r := USDPriceUpdate{SchemaVersion: 2, Currency: "USD", ExpectedRevision: before.Revision, Values: map[string]string{"ModelPrice": string(encoded)}}
	after, res, e := UpdateUSDPriceConfig(r)
	require.NoError(t, e)
	require.Equal(t, []string{"locked"}, res.LockedModels)
	saved, e := rawPriceMap(persistedPriceOption(t, "ModelPrice"))
	require.NoError(t, e)
	require.Equal(t, `0.05000`, string(saved["locked"]))
	require.Equal(t, `0.017000`, string(saved["keep"]))
	require.Equal(t, `0.000001`, string(saved["tiny"]))
	require.JSONEq(t, `0.252`, string(saved["edit"]))
	require.NotEqual(t, before.Revision, after.Revision)
	_, _, e = UpdateUSDPriceConfig(r)
	require.ErrorIs(t, e, ErrPricingRevisionConflict)
	r.ExpectedRevision = after.Revision
	r.Values = map[string]string{"USDExchangeRate": "1"}
	_, _, e = UpdateUSDPriceConfig(r)
	require.Error(t, e)
}
func TestCanonicalPricesMatchActualTieredDebitAcrossUnitsAndDiscounts(t *testing.T) {
	oldFX, oldB := operation_setting.USDExchangeRate, operation_setting.TopUpPlatformUnitsPerCNY
	t.Cleanup(func() { operation_setting.USDExchangeRate = oldFX; operation_setting.TopUpPlatformUnitsPerCNY = oldB })
	secs, zero := 60.0, 0.0
	cases := []struct {
		name, expr string
		p          billingexpr.TokenParams
	}{
		{"jev", "tier(\"base\", p*0.042)", billingexpr.TokenParams{P: 1e6, Len: 1e6}},
		{"minute", "tier(\"base\", audio_s*0.05*1000000/60)", billingexpr.TokenParams{AudioSeconds: &secs}},
		{"transcribe", "tier(\"base\", audio_s*0.017*1000000/60)", billingexpr.TokenParams{AudioSeconds: &secs}},
		{"translate", "tier(\"base\", audio_s*0.034*1000000/60)", billingexpr.TokenParams{AudioSeconds: &secs}},
		{"image-cache", "tier(\"base\", p*5+c*10+img*8+img_o*32+cr_text*1.25+cr_img*2)", billingexpr.TokenParams{P: 1000, C: 25, Len: 1000, Img: 100, ImgO: 50, CRText: &zero, CRImg: &zero}},
	}
	for _, q := range []float64{500000, 1000000, 3000000} {
		for _, discount := range []float64{1, .485} {
			t.Run(decimal.NewFromFloat(q).String()+"-"+decimal.NewFromFloat(discount).String(), func(t *testing.T) {
				pricingCurrencyFixture(t, q, 4500000)
				for _, tc := range cases {
					snap := &billingexpr.BillingSnapshot{ExprString: tc.expr, ExprHash: billingexpr.ExprHashString(tc.expr), QuotaPerUnit: q, GroupRatio: discount, ExprVersion: 1}
					actual, e := billingexpr.ComputeTieredQuota(snap, tc.p)
					require.NoError(t, e, tc.name)
					quote, e := USDExpression(tc.expr)
					require.NoError(t, e)
					usd, trace, e := billingexpr.RunExpr(quote, tc.p)
					require.NoError(t, e, tc.name)
					credits, _ := common.QuotaRoundChecked(usd / 1e6 * 4500000 * discount)
					require.Equal(t, actual.ActualQuotaAfterGroup, credits, tc.name)
					require.Equal(t, tc.expr, snap.ExprString)
					require.Equal(t, "base", trace.MatchedTier)
					operation_setting.USDExchangeRate, operation_setting.TopUpPlatformUnitsPerCNY = 13, 99
					again, e := USDExpression(tc.expr)
					require.NoError(t, e)
					require.Equal(t, quote, again)
				}
				input, e := ModelRatioUSDPerMillion(1.25)
				require.NoError(t, e)
				require.InDelta(t, 1.25e6/4500000, input, 1e-15)
				fixed, e := LegacyPricingAmountUSD(.05)
				require.NoError(t, e)
				require.InDelta(t, .05*q, fixed*4500000, 1e-9)
			})
		}
	}
}
func TestUSDNormalizationDetachesLegacyCacheAndFailsClosed(t *testing.T) {
	pricingCurrencyFixture(t, 500000, 3000000)
	legacy := []Pricing{{ModelName: "test", ModelRatio: 1.25, CompletionRatio: 2, ModelPrice: .042, BillingExpr: "p*0.042"}}
	got, e := NormalizePricingUSD(legacy)
	require.NoError(t, e)
	require.Equal(t, 1.25, got[0].ModelRatio)
	require.Equal(t, 2.0, got[0].CompletionRatio)
	require.InDelta(t, .042/6, got[0].ModelPrice, 1e-15)
	require.InDelta(t, 1.25e6/3e6, *got[0].InputPrice, 1e-15)
	require.Equal(t, 2, got[0].PricingSchemaVersion)
	require.Equal(t, "USD", got[0].PricingCurrency)
	require.Equal(t, "p*0.042", legacy[0].BillingExpr)
	require.Equal(t, .042, legacy[0].ModelPrice)
	require.Nil(t, legacy[0].InputPrice)
	common.ClearCreditsPerUSD()
	_, e = NormalizePricingUSD(legacy)
	require.ErrorIs(t, e, common.ErrCreditUnitsUnavailable)
}

func TestUSDPriceCASRejectsDifferentNodeCalibration(t *testing.T) {
	setupPriceLockTest(t)
	pricingCurrencyFixture(t, 500000, 3500000)
	for key, value := range priceOptionSnapshot() {
		require.NoError(t, DB.Create(&Option{Key: key, Value: value}).Error)
	}
	require.NoError(t, DB.Create(&Option{Key: CreditsPerUSDOptionKey, Value: "3500000"}).Error)
	require.NoError(t, DB.Create(&Option{Key: "QuotaPerUnit", Value: "500000"}).Error)
	before, err := GetUSDPriceConfig()
	require.NoError(t, err)
	// Node B (or an old rolling binary) changes the durable scale while A stays stale.
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", "QuotaPerUnit").Update("value", "1000000").Error)
	request := USDPriceUpdate{SchemaVersion: 2, Currency: "USD", ExpectedRevision: before.Revision, Values: map[string]string{"ModelPrice": `{"new":1}`}}
	_, _, err = UpdateUSDPriceConfig(request)
	require.ErrorIs(t, err, ErrPricingUnitsStale)
	require.Equal(t, before.Values["ModelPrice"], persistedPriceOption(t, "ModelPrice"))
	_, err = GetUSDPriceConfig()
	require.ErrorIs(t, err, ErrPricingUnitsStale)
}

func TestUSDExpressionPreservesVersionAndRequestRules(t *testing.T) {
	pricingCurrencyFixture(t, 500000, 7000000)
	legacy := `v1:(tier("base", p*28+c*112)) * (param("service_tier") == "fast" ? 2 : 1) * (has(header("anthropic-beta"), "fast-mode") ? 2.5 : 1)`
	quote, err := USDExpression(legacy)
	require.NoError(t, err)
	require.Equal(t, `v1:((tier("base", p*28+c*112)) * (param("service_tier") == "fast" ? 2 : 1) * (has(header("anthropic-beta"), "fast-mode") ? 2.5 : 1)) / (14)`, quote)
	params := billingexpr.TokenParams{P: 1000, C: 100}
	request := billingexpr.RequestInput{Body: []byte(`{"service_tier":"fast"}`), Headers: map[string]string{"Anthropic-Beta": "fast-mode"}}
	snap := &billingexpr.BillingSnapshot{ExprString: legacy, ExprHash: billingexpr.ExprHashString(legacy), QuotaPerUnit: 500000, GroupRatio: .485, ExprVersion: 1}
	actual, err := billingexpr.ComputeTieredQuotaWithRequest(snap, params, request)
	require.NoError(t, err)
	cost, trace, err := billingexpr.RunExprWithRequest(quote, params, request)
	require.NoError(t, err)
	require.Equal(t, 14000.0, cost)
	require.Equal(t, 47530, actual.ActualQuotaAfterGroup)
	credits, _ := common.QuotaRoundChecked(cost / 1e6 * 7000000 * .485)
	require.Equal(t, actual.ActualQuotaAfterGroup, credits)
	require.Equal(t, actual.RequestRules, trace.RequestRules)
	require.Equal(t, "base", trace.MatchedTier)
}

func TestUSDExpressionVersionedReadSaveAndEdit(t *testing.T) {
	setupPriceLockTest(t)
	pricingCurrencyFixture(t, 500000, 7000000)
	raw := priceOptionSnapshot()
	raw["billing_setting.billing_expr"] = ` { "versioned" : "v1:tier(\"base\", p*28+c*112)" } `
	for key, value := range raw {
		require.NoError(t, DB.Create(&Option{Key: key, Value: value}).Error)
	}
	require.NoError(t, DB.Create(&Option{Key: CreditsPerUSDOptionKey, Value: "7000000"}).Error)
	before, err := GetUSDPriceConfig()
	require.NoError(t, err)
	request := USDPriceUpdate{SchemaVersion: 2, Currency: "USD", ExpectedRevision: before.Revision, Values: maps.Clone(before.Values)}
	_, _, err = UpdateUSDPriceConfig(request)
	require.NoError(t, err)
	require.Equal(t, raw["billing_setting.billing_expr"], persistedPriceOption(t, "billing_setting.billing_expr"))
	request.Values = map[string]string{"billing_setting.billing_expr": `{"versioned":"v1:tier(\"base\", p*0.042)"}`}
	_, _, err = UpdateUSDPriceConfig(request)
	require.NoError(t, err)
	entries, err := rawPriceMap(persistedPriceOption(t, "billing_setting.billing_expr"))
	require.NoError(t, err)
	var stored string
	require.NoError(t, json.Unmarshal(entries["versioned"], &stored))
	require.Equal(t, `v1:(tier("base", p*0.042)) * (14)`, stored)
	snap := &billingexpr.BillingSnapshot{ExprString: stored, ExprHash: billingexpr.ExprHashString(stored), QuotaPerUnit: 500000, GroupRatio: 1, ExprVersion: 1}
	actual, err := billingexpr.ComputeTieredQuota(snap, billingexpr.TokenParams{P: 1000000})
	require.NoError(t, err)
	require.Equal(t, 294000, actual.ActualQuotaAfterGroup)
}

func TestUSDToolPriceWriterRetainsLegacyValidation(t *testing.T) {
	require.Error(t, validateModelPriceValues(map[string]string{operation_setting.ToolPriceOptionKey: `{"web_search":-1}`}))
	require.NoError(t, validateModelPriceValues(map[string]string{operation_setting.ToolPriceOptionKey: `{"web_search":0}`}))
}
