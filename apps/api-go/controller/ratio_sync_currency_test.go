package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/pkg/billingexpr"
	"github.com/LIghtJUNction/api.lmm.best/setting/billing_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/config"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

var ratioSyncOptionKeys = map[string]string{
	"model_ratio": "ModelRatio", "completion_ratio": "CompletionRatio", "cache_ratio": "CacheRatio",
	"create_cache_ratio": "CreateCacheRatio", "image_ratio": "ImageRatio", "audio_ratio": "AudioRatio",
	"audio_completion_ratio": "AudioCompletionRatio", "model_price": "ModelPrice",
	"billing_mode": "billing_setting.billing_mode", "billing_expr": "billing_setting.billing_expr",
}

// Every fetch sees the same complete durable currency basis as a real node;
// the legacy 500k calibration must never silently become the USD anchor.
func ratioSyncCurrencyFixture(t *testing.T, creditsPerUSD int64) *gorm.DB {
	t.Helper()
	oldDB, oldLog, oldRedis := model.DB, model.LOG_DB, common.RedisEnabled
	oldMainType, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
	oldQ := common.QuotaPerUnit
	oldK, oldKErr := common.CreditsPerUSD()
	oldLegacy, oldLegacyErr := common.LegacyPricingQuotaPerUnit()
	oldConfig := config.GlobalConfig.ExportAllConfigs()
	setters := map[string]func(string) error{
		"ModelRatio": ratio_setting.UpdateModelRatioByJSONString, "CompletionRatio": ratio_setting.UpdateCompletionRatioByJSONString,
		"ModelPrice": ratio_setting.UpdateModelPriceByJSONString, "CacheRatio": ratio_setting.UpdateCacheRatioByJSONString,
		"CreateCacheRatio": ratio_setting.UpdateCreateCacheRatioByJSONString, "ImageRatio": ratio_setting.UpdateImageRatioByJSONString,
		"AudioRatio": ratio_setting.UpdateAudioRatioByJSONString, "AudioCompletionRatio": ratio_setting.UpdateAudioCompletionRatioByJSONString,
	}
	previous := map[string]string{
		"ModelRatio": ratio_setting.ModelRatio2JSONString(), "CompletionRatio": ratio_setting.CompletionRatio2JSONString(),
		"ModelPrice": ratio_setting.ModelPrice2JSONString(), "CacheRatio": ratio_setting.CacheRatio2JSONString(),
		"CreateCacheRatio": ratio_setting.CreateCacheRatio2JSONString(), "ImageRatio": ratio_setting.ImageRatio2JSONString(),
		"AudioRatio": ratio_setting.AudioRatio2JSONString(), "AudioCompletionRatio": ratio_setting.AudioCompletionRatio2JSONString(),
	}
	common.OptionMapRWMutex.Lock()
	oldOptions := common.OptionMap
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.Log{}, &model.Ability{}, &model.Channel{}, &model.RatioNotification{}, &model.RatioDelivery{}))
	t.Cleanup(func() {
		for key, value := range previous {
			require.NoError(t, setters[key](value))
		}
		require.NoError(t, config.GlobalConfig.LoadFromDB(oldConfig))
		common.OptionMapRWMutex.Lock()
		common.OptionMap = oldOptions
		common.OptionMapRWMutex.Unlock()
		model.DB, model.LOG_DB, common.RedisEnabled = oldDB, oldLog, oldRedis
		common.SetDatabaseTypes(oldMainType, oldLogType)
		common.QuotaPerUnit = oldQ
		if oldKErr != nil || oldLegacyErr != nil {
			common.ClearCreditsPerUSD()
		} else {
			require.NoError(t, common.SetCreditCurrencyBasis(oldK, oldLegacy))
		}
		model.InvalidatePricingCache()
	})
	common.QuotaPerUnit = 500000
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(creditsPerUSD), decimal.NewFromInt(500000)))
	require.NoError(t, db.Create(&[]model.Option{
		{Key: "QuotaPerUnit", Value: "500000"}, {Key: model.LegacyPricingQuotaPerUnitOptionKey, Value: "500000"},
		{Key: model.CreditsPerUSDOptionKey, Value: strconv.FormatInt(creditsPerUSD, 10)},
	}).Error)
	initial := map[string]string{
		"ModelRatio": `{"quote":1,"keep":1.234500,"locked":7.70000}`, "CompletionRatio": `{"quote":1,"locked":2}`,
		"CacheRatio": `{"quote":1}`, "CreateCacheRatio": `{}`, "ImageRatio": `{}`, "AudioRatio": `{}`, "AudioCompletionRatio": `{}`,
		"ModelPrice":                         `{"keep-fixed":0.017000,"locked":0.05000}`,
		"billing_setting.billing_mode":       `{"keep-expr":"tiered_expr"}`,
		"billing_setting.billing_expr":       `{"keep-expr":"v1:tier(\"keep\", p * 1.234500)"}`,
		operation_setting.ToolPriceOptionKey: `{"web_search":10.000000}`, model.ModelPriceLocksOptionKey: `{}`,
	}
	require.NoError(t, model.UpdateOptionsBulk(initial))
	_, err := model.UpdateModelPriceLock("locked", true)
	require.NoError(t, err)
	return db
}

type ratioSyncCurrencyResponse struct {
	Success bool `json:"success"`
	Data    struct {
		PricingConfig model.USDPriceConfig `json:"pricing_config"`
		Differences   map[string]map[string]struct {
			Current    any             `json:"current"`
			Upstreams  map[string]any  `json:"upstreams"`
			Confidence map[string]bool `json:"confidence"`
		} `json:"differences"`
		Results []struct{ Status, Message string } `json:"test_results"`
	} `json:"data"`
}

func ratioSyncRunHandler(t *testing.T, method, path string, payload any, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	var body []byte
	var err error
	if payload != nil {
		body, err = json.Marshal(payload)
		require.NoError(t, err)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, path, bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("id", 1)
	handler(c)
	return w
}

func ratioSyncFetchCurrency(t *testing.T, payload any) ratioSyncCurrencyResponse {
	t.Helper()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			t.Errorf("encode localhost pricing fixture: %v", err)
		}
	}))
	t.Cleanup(source.Close)
	w := ratioSyncRunHandler(t, http.MethodPost, "/api/ratio_sync/fetch", map[string]any{
		"timeout": 2, "upstreams": []map[string]any{{"name": "source", "base_url": source.URL, "endpoint": "/api/pricing"}},
	}, FetchUpstreamRatios)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var result ratioSyncCurrencyResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	require.True(t, result.Success, w.Body.String())
	return result
}

func ratioSyncCurrencySelection(t *testing.T, fetched ratioSyncCurrencyResponse, selected ...string) model.USDPriceUpdate {
	t.Helper()
	values := maps.Clone(fetched.Data.PricingConfig.Values)
	selectedModels := map[string]bool{}
	for _, name := range selected {
		selectedModels[name] = true
	}
	for name, fields := range fetched.Data.Differences {
		if !selectedModels[name] {
			continue
		}
		for field, difference := range fields {
			value, exists := difference.Upstreams["source"]
			if !exists || value == nil || value == "same" {
				continue
			}
			key := ratioSyncOptionKeys[field]
			require.NotEmpty(t, key, field)
			var entries map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(values[key]), &entries), key)
			entries[name], _ = json.Marshal(value)
			encoded, err := json.Marshal(entries)
			require.NoError(t, err)
			values[key] = string(encoded)
		}
	}
	return model.USDPriceUpdate{SchemaVersion: 2, Currency: "USD", ExpectedRevision: fetched.Data.PricingConfig.Revision, Values: values}
}

func ratioSyncStoredOptions(t *testing.T, db *gorm.DB) map[string]string {
	t.Helper()
	var rows []model.Option
	require.NoError(t, db.Find(&rows).Error)
	result := map[string]string{}
	for _, row := range rows {
		result[row.Key] = row.Value
	}
	return result
}

const ratioSyncSplitUSD = `v1:tier("split", p*4+c*20+cr_text*1.6+cr_img*2+cr_audio*3)`
const ratioSyncMinuteUSD = `v1:tier("minute", audio_s*0.05*1000000/60)`

func ratioSyncCurrencySource(format string) map[string]any {
	if format == "canonical-array" {
		return map[string]any{"success": true, "pricing_schema_version": 2, "pricing_currency": "USD", "data": []map[string]any{
			{"model_name": "quote", "quota_type": 0, "input_price": 4, "output_price": 20, "model_ratio": 999, "completion_ratio": 5, "cache_ratio": .4, "create_cache_ratio": 1.25, "image_ratio": 2, "audio_ratio": 8, "audio_completion_ratio": 2, "pricing_schema_version": 2, "pricing_currency": "USD"},
			{"model_name": "locked", "quota_type": 1, "model_price": .01, "pricing_schema_version": 2, "pricing_currency": "USD"},
			{"model_name": "fixed", "quota_type": 1, "model_price": .01, "pricing_schema_version": 2, "pricing_currency": "USD"},
			{"model_name": "free", "quota_type": 1, "model_price": 0, "pricing_schema_version": 2, "pricing_currency": "USD"},
			{"model_name": "micro", "quota_type": 1, "model_price": 1e-14, "pricing_schema_version": 2, "pricing_currency": "USD"},
			{"model_name": "micro-token", "quota_type": 0, "input_price": 1e-14, "completion_ratio": 5, "pricing_schema_version": 2, "pricing_currency": "USD"},
			{"model_name": "split", "quota_type": 0, "input_price": 4, "completion_ratio": 5, "billing_mode": "tiered_expr", "billing_expr": ratioSyncSplitUSD, "pricing_schema_version": 2, "pricing_currency": "USD"},
			{"model_name": "minute", "quota_type": 0, "input_price": 4, "completion_ratio": 5, "billing_mode": "tiered_expr", "billing_expr": ratioSyncMinuteUSD, "pricing_schema_version": 2, "pricing_currency": "USD"},
		}}
	}
	sourceK, scale := 500000.0, 1.0
	if format == "explicit-legacy" {
		sourceK, scale = 3000000, 6
	}
	expr := func(body string) string {
		if scale == 1 {
			return body
		}
		return "v1:(" + body[len("v1:"):] + ") * 6"
	}
	data := map[string]any{
		"model_ratio":      map[string]float64{"quote": sourceK * 4 / 1e6, "split": sourceK * 4 / 1e6, "minute": sourceK * 4 / 1e6, "micro-token": sourceK * 1e-14 / 1e6, "legacy-fallback": 37.5},
		"completion_ratio": map[string]float64{"quote": 5, "split": 5, "minute": 5, "micro-token": 5, "legacy-fallback": 1},
		"cache_ratio":      map[string]float64{"quote": .4}, "create_cache_ratio": map[string]float64{"quote": 1.25},
		"image_ratio": map[string]float64{"quote": 2}, "audio_ratio": map[string]float64{"quote": 8}, "audio_completion_ratio": map[string]float64{"quote": 2},
		"model_price":  map[string]float64{"locked": .01 * scale, "fixed": .01 * scale, "free": 0, "micro": 1e-14 * scale},
		"billing_mode": map[string]string{"split": "tiered_expr", "minute": "tiered_expr"},
		"billing_expr": map[string]string{"split": expr(ratioSyncSplitUSD), "minute": expr(ratioSyncMinuteUSD)},
	}
	result := map[string]any{"success": true, "data": data}
	if format == "explicit-legacy" {
		result["pricing_storage_basis"] = "legacy_pricing_unit"
		result["credits_per_usd"], result["legacy_pricing_quota_per_unit"], result["legacy_pricing_units_per_usd"] = sourceK, 500000, scale
	}
	return result
}

func TestRatioSyncCurrencyPublicFetchCASRoundTrip(t *testing.T) {
	for _, k := range []int64{500000, 3000000, 3359744} {
		for _, format := range []string{"standard-legacy", "explicit-legacy", "canonical-array"} {
			t.Run(fmt.Sprintf("K%d/%s", k, format), func(t *testing.T) {
				db := ratioSyncCurrencyFixture(t, k)
				user := model.User{Id: 1, Username: "ratio-sync-owner", Quota: 1234567, UsedQuota: 54321}
				token := model.Token{UserId: 1, Key: "synthetic-ratio-sync-key", Name: "preserved", RemainQuota: 765432, UsedQuota: 321}
				require.NoError(t, db.Create(&user).Error)
				require.NoError(t, db.Create(&token).Error)
				before := ratioSyncStoredOptions(t, db)
				fetched := ratioSyncFetchCurrency(t, ratioSyncCurrencySource(format))
				require.Len(t, fetched.Data.Results, 1)
				require.Equal(t, "success", fetched.Data.Results[0].Status)
				require.Equal(t, 2, fetched.Data.PricingConfig.SchemaVersion)
				require.Equal(t, "USD", fetched.Data.PricingConfig.Currency)
				require.Equal(t, float64(k), fetched.Data.PricingConfig.CreditsPerUSD)
				direct, err := model.GetUSDPriceConfig()
				require.NoError(t, err)
				require.Equal(t, direct, fetched.Data.PricingConfig, "differences and CAS base come from one durable snapshot")
				update := ratioSyncCurrencySelection(t, fetched, "quote", "locked", "fixed", "free", "micro", "micro-token", "split", "minute")
				w := ratioSyncRunHandler(t, http.MethodPost, "/api/option/pricing/bulk", update, USDPriceOptionsBulk)
				require.Equal(t, http.StatusOK, w.Code, w.Body.String())
				var saved struct {
					Success      bool
					Data         model.USDPriceConfig
					LockedModels []string `json:"locked_models"`
				}
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &saved))
				require.True(t, saved.Success, w.Body.String())
				require.Contains(t, saved.LockedModels, "locked")
				read := ratioSyncRunHandler(t, http.MethodGet, "/api/option/pricing", nil, GetUSDPriceOptions)
				var readback struct {
					Success bool
					Data    model.USDPriceConfig
				}
				require.NoError(t, json.Unmarshal(read.Body.Bytes(), &readback))
				require.True(t, readback.Success)
				require.Equal(t, saved.Data, readback.Data)
				var fixed map[string]float64
				require.NoError(t, json.Unmarshal([]byte(readback.Data.Values["ModelPrice"]), &fixed))
				require.InDelta(t, .01, fixed["fixed"], 1e-15)
				require.Zero(t, fixed["free"])
				require.InDelta(t, 1e-14, fixed["micro"], 1e-28)
				require.Greater(t, fixed["micro"], 0.0)
				m, found, _ := ratio_setting.GetModelRatio("quote")
				require.True(t, found)
				a, ac := ratio_setting.GetAudioRatio("quote"), ratio_setting.GetAudioCompletionRatio("quote")
				quotes, err := model.NormalizePricingUSD([]model.Pricing{{ModelName: "quote", ModelRatio: m, CompletionRatio: ratio_setting.GetCompletionRatio("quote"), AudioRatio: &a, AudioCompletionRatio: &ac}})
				require.NoError(t, err)
				require.InDelta(t, 4, *quotes[0].InputPrice, 1e-12)
				require.InDelta(t, 20, *quotes[0].OutputPrice, 1e-12)
				require.InDelta(t, 1.6, *quotes[0].CacheReadPrice, 1e-12)
				require.InDelta(t, 32, *quotes[0].AudioInputPrice, 1e-12)
				require.InDelta(t, 64, *quotes[0].AudioOutputPrice, 1e-12)
				microRatio, exists, _ := ratio_setting.GetModelRatio("micro-token")
				require.True(t, exists)
				require.Greater(t, microRatio, 0.0)
				microQuote, err := model.NormalizePricingUSD([]model.Pricing{{ModelName: "micro-token", ModelRatio: microRatio, CompletionRatio: 5}})
				require.NoError(t, err)
				require.InDelta(t, 1e-14, *microQuote[0].InputPrice, 1e-28)
				require.Greater(t, *microQuote[0].InputPrice, 0.0)
				text, img, audio, seconds := 250000.0, 0.0, 0.0, 90.0
				for _, exprCase := range []struct {
					name    string
					params  billingexpr.TokenParams
					wantUSD float64
				}{
					{"split", billingexpr.TokenParams{P: 1e6, C: 100000, Len: 1e6, CRText: &text, CRImg: &img, CRAudio: &audio}, 6.4},
					{"minute", billingexpr.TokenParams{AudioSeconds: &seconds}, .075},
				} {
					raw, ok := billing_setting.GetBillingExpr(exprCase.name)
					require.True(t, ok)
					canonical, err := model.USDExpression(raw)
					require.NoError(t, err)
					amount, trace, err := billingexpr.RunExpr(canonical, exprCase.params)
					require.NoError(t, err)
					require.InDelta(t, exprCase.wantUSD*1e6, amount, 1e-8)
					require.Equal(t, exprCase.name, trace.MatchedTier)
					actual, err := billingexpr.ComputeTieredQuota(&billingexpr.BillingSnapshot{ExprString: raw, ExprHash: billingexpr.ExprHashString(raw), QuotaPerUnit: 500000, GroupRatio: .485, ExprVersion: 1}, exprCase.params)
					require.NoError(t, err)
					want, clamp := common.QuotaFromDecimalChecked(decimal.NewFromFloat(exprCase.wantUSD).Mul(decimal.NewFromInt(k)).Mul(decimal.RequireFromString(".485")))
					require.Nil(t, clamp)
					require.Equal(t, want, actual.ActualQuotaAfterGroup)
				}
				after := ratioSyncStoredOptions(t, db)
				require.Equal(t, before[operation_setting.ToolPriceOptionKey], after[operation_setting.ToolPriceOptionKey])
				require.Equal(t, before[model.ModelPriceLocksOptionKey], after[model.ModelPriceLocksOptionKey])
				for key, entry := range map[string]string{"ModelRatio": "keep", "ModelPrice": "locked", "billing_setting.billing_expr": "keep-expr"} {
					var b, a map[string]json.RawMessage
					require.NoError(t, json.Unmarshal([]byte(before[key]), &b))
					require.NoError(t, json.Unmarshal([]byte(after[key]), &a))
					require.Equal(t, b[entry], a[entry], key)
				}
				var storedUser model.User
				var storedToken model.Token
				require.NoError(t, db.First(&storedUser, user.Id).Error)
				require.NoError(t, db.First(&storedToken, token.Id).Error)
				require.Equal(t, user.Quota, storedUser.Quota)
				require.Equal(t, user.UsedQuota, storedUser.UsedQuota)
				require.Equal(t, token.RemainQuota, storedToken.RemainQuota)
				require.Equal(t, token.UsedQuota, storedToken.UsedQuota)
			})
		}
	}
}

func TestRatioSyncCurrencyStaleSnapshotRejectsEverySelectedWrite(t *testing.T) {
	db := ratioSyncCurrencyFixture(t, 3359744)
	fetched := ratioSyncFetchCurrency(t, ratioSyncCurrencySource("canonical-array"))
	update := ratioSyncCurrencySelection(t, fetched, "quote", "fixed", "split", "minute")
	winner := model.USDPriceUpdate{SchemaVersion: 2, Currency: "USD", ExpectedRevision: fetched.Data.PricingConfig.Revision, Values: map[string]string{"ImageRatio": `{"other-writer":3}`}}
	w := ratioSyncRunHandler(t, http.MethodPost, "/api/option/pricing/bulk", winner, USDPriceOptionsBulk)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	before := ratioSyncStoredOptions(t, db)
	w = ratioSyncRunHandler(t, http.MethodPost, "/api/option/pricing/bulk", update, USDPriceOptionsBulk)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	require.Equal(t, before, ratioSyncStoredOptions(t, db), "a stale batch must not save any selected map")
}

func TestRatioSyncCurrencyRejectsExplicitLegacyWithoutCalibration(t *testing.T) {
	db := ratioSyncCurrencyFixture(t, 3359744)
	before := ratioSyncStoredOptions(t, db)
	for _, metadata := range []map[string]any{
		{"pricing_storage_basis": "legacy_pricing_unit"},
		{"pricing_storage_basis": "legacy_pricing_unit", "credits_per_usd": 3000000},
		{"pricing_storage_basis": "legacy_pricing_unit", "legacy_pricing_quota_per_unit": 500000},
	} {
		payload := map[string]any{"success": true, "data": map[string]any{"model_ratio": map[string]float64{"quote": 2}, "model_price": map[string]float64{"fixed": .01}}}
		maps.Copy(payload, metadata)
		fetched := ratioSyncFetchCurrency(t, payload)
		require.Len(t, fetched.Data.Results, 1)
		require.Equal(t, "error", fetched.Data.Results[0].Status)
		require.Empty(t, fetched.Data.Differences)
		require.Equal(t, before, ratioSyncStoredOptions(t, db))
	}
}

func TestRatioSyncCurrencyFetchFailsWithoutDurableBasis(t *testing.T) {
	db := ratioSyncCurrencyFixture(t, 3359744)
	require.NoError(t, db.Delete(&model.Option{}, "key = ?", model.CreditsPerUSDOptionKey).Error)
	w := ratioSyncRunHandler(t, http.MethodPost, "/api/ratio_sync/fetch", map[string]any{"upstreams": []map[string]any{{"name": "unused", "base_url": "http://127.0.0.1:1"}}}, FetchUpstreamRatios)
	require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
}

func TestRatioSyncCurrencyExportProvidesCompleteUSDContract(t *testing.T) {
	var exported map[string]any
	t.Run("source", func(t *testing.T) {
		ratioSyncCurrencyFixture(t, 3359744)
		oldExpose := ratio_setting.IsExposeRatioEnabled()
		ratio_setting.SetExposeRatioEnabled(true)
		t.Cleanup(func() { ratio_setting.SetExposeRatioEnabled(oldExpose) })
		w := ratioSyncRunHandler(t, http.MethodGet, "/api/ratio_config", nil, GetRatioConfig)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &exported))
		require.Equal(t, float64(2), exported["pricing_schema_version"])
		require.Equal(t, "USD", exported["pricing_currency"])
		require.Equal(t, "legacy_pricing_unit", exported["pricing_storage_basis"])
		require.Equal(t, float64(3359744), exported["credits_per_usd"])
		require.Equal(t, float64(500000), exported["legacy_pricing_quota_per_unit"])
		require.Equal(t, 6.719488, exported["legacy_pricing_units_per_usd"])
		direct, err := model.GetUSDPriceConfig()
		require.NoError(t, err)
		require.Equal(t, direct.ModelRatioUSDPerMillion, exported["model_ratio_usd_per_million"])
		data := exported["data"].(map[string]any)
		for field, key := range ratioSyncOptionKeys {
			require.Contains(t, data, field, "export must include all ten maps, even empty ones")
			var want map[string]any
			require.NoError(t, json.Unmarshal([]byte(direct.Values[key]), &want))
			require.Equal(t, want, data[field], field)
		}
		require.NotContains(t, data, "tool_price_setting.prices")
	})
	t.Run("different destination", func(t *testing.T) {
		ratioSyncCurrencyFixture(t, 500000)
		fetched := ratioSyncFetchCurrency(t, exported)
		require.Len(t, fetched.Data.Results, 1)
		require.Equal(t, "success", fetched.Data.Results[0].Status)
		update := ratioSyncCurrencySelection(t, fetched, "quote", "keep-fixed", "keep-expr")
		w := ratioSyncRunHandler(t, http.MethodPost, "/api/option/pricing/bulk", update, USDPriceOptionsBulk)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		m, exists, _ := ratio_setting.GetModelRatio("quote")
		require.True(t, exists)
		quote, err := model.NormalizePricingUSD([]model.Pricing{{ModelName: "quote", ModelRatio: m, CompletionRatio: 1}})
		require.NoError(t, err)
		require.InDelta(t, .2976417250838159, *quote[0].InputPrice, 1e-14, "source 1 credit/token at K3359744 retains its real USD rate")
		price, exists := ratio_setting.GetModelPrice("keep-fixed", false)
		require.True(t, exists)
		fixed, err := model.LegacyPricingAmountUSD(price)
		require.NoError(t, err)
		require.InDelta(t, .002529954663212435, fixed, 1e-15, "canonical export must not scale fixed USD a second time")
		raw, exists := billing_setting.GetBillingExpr("keep-expr")
		require.True(t, exists)
		canonical, err := model.USDExpression(raw)
		require.NoError(t, err)
		cost, trace, err := billingexpr.RunExpr(canonical, billingexpr.TokenParams{P: 1e6})
		require.NoError(t, err)
		require.Equal(t, "keep", trace.MatchedTier)
		require.InDelta(t, 183719.35480798537, cost, 1e-8, "the exported USD expression keeps its value at a different destination K")
	})
}

func TestRatioSyncCurrencyLegacyFallbackConfidenceSurvivesNormalization(t *testing.T) {
	ratioSyncCurrencyFixture(t, 3359744)
	fetched := ratioSyncFetchCurrency(t, ratioSyncCurrencySource("standard-legacy"))
	require.Contains(t, fetched.Data.Differences, "legacy-fallback")
	confidence := fetched.Data.Differences["legacy-fallback"]["model_ratio"].Confidence
	require.Contains(t, confidence, "source")
	require.False(t, confidence["source"], "37.5/1 legacy fallback must be classified before changing the numeric denomination")
}

func TestRatioSyncCurrencyExpressionSelectionKeepsSameSourceModePair(t *testing.T) {
	for _, modeChanges := range []bool{false, true} {
		t.Run(fmt.Sprint(modeChanges), func(t *testing.T) {
			ratioSyncCurrencyFixture(t, 3359744)
			mode := "tiered_expr"
			if modeChanges {
				mode = "ratio"
			}
			modes, _ := json.Marshal(map[string]string{"pair": mode})
			require.NoError(t, model.UpdateOptionsBulk(map[string]string{"billing_setting.billing_mode": string(modes), "billing_setting.billing_expr": `{"pair":"v1:tier(\"old\", p*2)"}`}))
			current, err := model.GetUSDPriceConfig()
			require.NoError(t, err)
			var currentExprs map[string]string
			require.NoError(t, json.Unmarshal([]byte(current.Values["billing_setting.billing_expr"]), &currentExprs))
			expr := `v1:tier("new",p*4)`
			if modeChanges {
				expr = currentExprs["pair"]
			}
			fetched := ratioSyncFetchCurrency(t, map[string]any{"success": true, "pricing_schema_version": 2, "pricing_currency": "USD", "data": []map[string]any{{"model_name": "pair", "quota_type": 0, "input_price": 4, "completion_ratio": 5, "billing_mode": "tiered_expr", "billing_expr": expr, "pricing_schema_version": 2, "pricing_currency": "USD"}}})
			pair := fetched.Data.Differences["pair"]
			require.Contains(t, pair, "billing_mode")
			require.Contains(t, pair, "billing_expr")
			require.Equal(t, "tiered_expr", pair["billing_mode"].Upstreams["source"])
			require.Equal(t, expr, pair["billing_expr"].Upstreams["source"])
			w := ratioSyncRunHandler(t, http.MethodPost, "/api/option/pricing/bulk", ratioSyncCurrencySelection(t, fetched, "pair"), USDPriceOptionsBulk)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.Equal(t, "tiered_expr", billing_setting.GetBillingMode("pair"))
		})
	}
}

func TestRatioSyncCurrencyGPTWholeUSDExpressionPreservesLenTierBoundary(t *testing.T) {
	ratioSyncCurrencyFixture(t, 3359744)
	// Exercise the canonical whole-expression/len/tier shape, including the
	// legacy storage metadata carried beside a schema-2 public USD quote.
	const expr = `len <= 272000 ? tier("standard_short", p * 4 + c * 20 + cr * 0.4 + cc * 5) : tier("standard_long", p * 8 + c * 30 + cr * 0.8 + cc * 10)`
	const modelName = "gpt-5.6-sol"
	fetched := ratioSyncFetchCurrency(t, map[string]any{
		"success": true, "pricing_schema_version": 2, "pricing_currency": "USD",
		"pricing_storage_basis": "legacy_pricing_unit", "credits_per_usd": 3359744,
		"legacy_pricing_quota_per_unit": 500000, "legacy_pricing_units_per_usd": 6.719488,
		"data": []map[string]any{{
			"model_name": modelName, "quota_type": 0, "input_price": 4, "completion_ratio": 5,
			"billing_mode": "tiered_expr", "billing_expr": expr,
			"pricing_schema_version": 2, "pricing_currency": "USD",
		}},
	})
	require.Len(t, fetched.Data.Results, 1)
	require.Equal(t, "success", fetched.Data.Results[0].Status)
	require.Equal(t, expr, fetched.Data.Differences[modelName]["billing_expr"].Upstreams["source"], "schema-2 USD is already canonical even when storage is legacy")
	w := ratioSyncRunHandler(t, http.MethodPost, "/api/option/pricing/bulk", ratioSyncCurrencySelection(t, fetched, modelName), USDPriceOptionsBulk)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	read := ratioSyncRunHandler(t, http.MethodGet, "/api/option/pricing", nil, GetUSDPriceOptions)
	require.Equal(t, http.StatusOK, read.Code, read.Body.String())
	var readback struct {
		Success bool
		Data    model.USDPriceConfig
	}
	require.NoError(t, json.Unmarshal(read.Body.Bytes(), &readback))
	require.True(t, readback.Success)
	var expressions map[string]string
	require.NoError(t, json.Unmarshal([]byte(readback.Data.Values["billing_setting.billing_expr"]), &expressions))
	raw, exists := billing_setting.GetBillingExpr(modelName)
	require.True(t, exists)
	for _, test := range []struct {
		context float64
		tier    string
		usd     float64
	}{
		{272000, "standard_short", .00635},
		{272001, "standard_long", .0117},
	} {
		t.Run(strconv.FormatFloat(test.context, 'f', 0, 64), func(t *testing.T) {
			params := billingexpr.TokenParams{P: 1000, C: 100, CR: 250, CC: 50, Len: test.context}
			cost, trace, err := billingexpr.RunExpr(expressions[modelName], params)
			require.NoError(t, err)
			require.Equal(t, test.tier, trace.MatchedTier)
			require.InDelta(t, test.usd*1e6, cost, 1e-8, "readback must retain the whole USD expression rather than expr/S")
			actual, err := billingexpr.ComputeTieredQuota(&billingexpr.BillingSnapshot{
				ExprString: raw, ExprHash: billingexpr.ExprHashString(raw), QuotaPerUnit: 500000, GroupRatio: 1, ExprVersion: 1,
			}, params)
			require.NoError(t, err)
			want, clamp := common.QuotaFromDecimalChecked(decimal.NewFromFloat(test.usd).Mul(decimal.NewFromInt(3359744)))
			require.Nil(t, clamp)
			require.Equal(t, want, actual.ActualQuotaAfterGroup)
		})
	}
}
