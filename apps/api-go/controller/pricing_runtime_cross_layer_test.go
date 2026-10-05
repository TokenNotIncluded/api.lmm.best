package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/pkg/billingexpr"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	relayhelper "github.com/LIghtJUNction/api.lmm.best/relay/helper"
	relaytypes "github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting/config"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// This crosses the administrator USD endpoint, durable legacy storage, actual
// pre-consume helper and final settlement. A canonical readback is never fed
// directly into the raw runtime snapshot.
func TestUSDPriceSaveReserveSettleReadbackKeepsLedgerCalibration(t *testing.T) {
	db := ratioSyncCurrencyFixture(t, 500000)
	require.NoError(t, common.SetPublicCreditsPerUSD(decimal.NewFromInt(100000)))
	t.Cleanup(common.ClearPublicCreditsPerUSD)
	persistCreditDenominationFixture(t, db)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{"group_ratio_setting.group_ratio": `{"default":1}`}))
	initial, err := model.GetUSDPriceConfig()
	require.NoError(t, err)
	const flat = "runtime-usd-flat"
	const tiered = "runtime-usd-cache"
	const tierUSD = `v1:len <= 272000 ? tier("short", p*4+c*20+cr_text*0.25+cr_img*0.5+cr_audio*0.75+(cc+cc1h)*4) : tier("long", p*8+c*30+cr_text*0.5+cr_img*1+cr_audio*1.5+(cc+cc1h)*8)`
	var expressions, modes map[string]string
	require.NoError(t, json.Unmarshal([]byte(initial.Values["billing_setting.billing_expr"]), &expressions))
	require.NoError(t, json.Unmarshal([]byte(initial.Values["billing_setting.billing_mode"]), &modes))
	expressions[flat], expressions[tiered] = "p * 4", tierUSD
	modes[flat], modes[tiered] = "tiered_expr", "tiered_expr"
	encode := func(value any) string { raw, e := json.Marshal(value); require.NoError(t, e); return string(raw) }
	write := func(update model.USDPriceUpdate) model.USDPriceConfig {
		w := ratioSyncRunHandler(t, http.MethodPost, "/api/option/pricing/bulk", update, USDPriceOptionsBulk)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var result struct {
			Success bool
			Data    model.USDPriceConfig
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
		require.True(t, result.Success)
		return result.Data
	}
	saved := write(model.USDPriceUpdate{SchemaVersion: 2, Currency: "USD", ExpectedRevision: initial.Revision, Values: map[string]string{"billing_setting.billing_expr": encode(expressions), "billing_setting.billing_mode": encode(modes)}})
	rawBefore := ratioSyncStoredOptions(t, db)
	var stored map[string]string
	require.NoError(t, json.Unmarshal([]byte(rawBefore["billing_setting.billing_expr"]), &stored))
	require.Contains(t, stored[flat], "* (1)", "USD save keeps the fixed dollar-to-credit denomination")
	rawMillion, _, err := billingexpr.RunExpr(stored[flat], billingexpr.TokenParams{P: 1e6, Len: 1e6})
	require.NoError(t, err)
	require.Equal(t, 4e6, rawMillion)
	var quotes map[string]string
	require.NoError(t, json.Unmarshal([]byte(saved.Values["billing_setting.billing_expr"]), &quotes))
	quotedMillion, _, err := billingexpr.RunExpr(quotes[flat], billingexpr.TokenParams{P: 1e6, Len: 1e6})
	require.NoError(t, err)
	require.InDelta(t, 4e6, quotedMillion, 1e-8)
	zero, text, image, audio := 0.0, 100.0, 100.0, 100.0
	vectors := []struct {
		name, model string
		params      billingexpr.TokenParams
		usd         float64
		tier        string
	}{
		{"flat-1000", flat, billingexpr.TokenParams{P: 1000, Len: 1000}, 0.004, ""},
		{"flat-1M", flat, billingexpr.TokenParams{P: 1e6, Len: 1e6}, 4, ""},
	}
	for _, length := range []float64{1000, 200000, 200001, 272000, 272001, 1e6} {
		params := billingexpr.TokenParams{P: 1000, C: 500, Len: length, CR: 300, CC: 200, CC1h: 100, CRText: &text, CRImg: &image, CRAudio: &audio}
		usd, tier := (1000*4+500*20+100*0.25+100*0.5+100*0.75+300*4)/1e6, "short"
		if length > 272000 {
			usd, tier = (1000*8+500*30+100*0.5+100*1+100*1.5+300*8)/1e6, "long"
		}
		vectors = append(vectors, struct {
			name, model string
			params      billingexpr.TokenParams
			usd         float64
			tier        string
		}{"cache-boundary", tiered, params, usd, tier})
	}
	for _, p := range []string{"100000", "200000"} {
		require.NoError(t, db.Model(&model.Option{}).Where("key = ?", model.PublicCreditsPerUSDOptionKey).Update("value", p).Error)
		require.NoError(t, common.SetPublicCreditsPerUSD(decimal.NewFromInt(777777)), "another node may have a stale display cache")
		units, e := model.CreditDenominationSnapshot()
		require.NoError(t, e)
		require.Equal(t, p, units.PublicCreditsPerUSDExact)
		display, e := units.ProjectLedgerQuota(500000)
		require.NoError(t, e)
		require.Equal(t, p, display.String())
		for _, v := range vectors {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{OriginModelName: v.model, UsingGroup: "default", UserGroup: "default", BillingRequestInput: &billingexpr.RequestInput{Body: []byte(`{}`)}}
			price, e := relayhelper.ModelPriceHelper(ctx, info, int(v.params.P), &relaytypes.TokenCountMeta{MaxTokens: 1})
			require.NoError(t, e, v.name)
			require.NotNil(t, info.TieredBillingSnapshot)
			require.Equal(t, stored[v.model], info.TieredBillingSnapshot.ExprString)
			require.Equal(t, float64(500000), info.TieredBillingSnapshot.QuotaPerUnit)
			if v.model == flat {
				want, e := common.QuotaFromDecimalStrict(decimal.NewFromFloat(v.usd).Mul(decimal.NewFromInt(500000)))
				require.NoError(t, e)
				require.Equal(t, want, price.QuotaToPreConsume)
				if v.params.P == 1e6 {
					require.Equal(t, 2000000, price.QuotaToPreConsume)
				}
			}
			params := v.params
			if params.CRText == nil {
				params.CRText = &zero
				params.CRImg = &zero
				params.CRAudio = &zero
			}
			ok, actual, result, e := service.TryTieredSettleWithError(info, params)
			require.NoError(t, e, v.name)
			require.True(t, ok)
			require.NotNil(t, result)
			require.Nil(t, result.Clamp)
			expected, e := common.QuotaFromDecimalStrict(decimal.NewFromFloat(v.usd).Mul(decimal.NewFromInt(500000)))
			require.NoError(t, e)
			require.Equal(t, expected, actual, v.name)
			require.Equal(t, v.tier, result.MatchedTier)
		}
	}
	read := ratioSyncRunHandler(t, http.MethodGet, "/api/option/pricing", nil, GetUSDPriceOptions)
	require.Equal(t, http.StatusOK, read.Code)
	var current struct {
		Success bool
		Data    model.USDPriceConfig
	}
	require.NoError(t, json.Unmarshal(read.Body.Bytes(), &current))
	require.True(t, current.Success)
	require.Equal(t, saved.Revision, current.Data.Revision, "display denomination is absent from the billing revision")
	require.Equal(t, saved.Values, current.Data.Values)
	beforeNoOp := ratioSyncStoredOptions(t, db)
	repeat := write(model.USDPriceUpdate{SchemaVersion: 2, Currency: "USD", ExpectedRevision: current.Data.Revision, Values: current.Data.Values})
	require.Equal(t, current.Data.Revision, repeat.Revision)
	require.Equal(t, beforeNoOp, ratioSyncStoredOptions(t, db), "saving canonical readback again cannot multiply the raw expression a second time")
	require.Equal(t, rawBefore["billing_setting.billing_expr"], beforeNoOp["billing_setting.billing_expr"])
}
