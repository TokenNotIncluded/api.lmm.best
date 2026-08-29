package helper

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/pkg/billingexpr"
	"github.com/LIghtJUNction/api.lmm.best/pkg/dynamic_pricing"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/setting/billing_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/config"
	"github.com/LIghtJUNction/api.lmm.best/setting/dynamic_pricing_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestHandleGroupRatioAppliesTrustLevelDiscount(t *testing.T) {
	previousDB := model.DB
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}))

	savedGroupRatios := ratio_setting.GroupRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"trust-test":1.25}`))
	t.Cleanup(func() {
		model.DB = previousDB
		common.RedisEnabled = previousRedis
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(savedGroupRatios))
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	user := model.User{
		Username: "trust-price-user", Password: "password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "trust-test",
	}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&model.TopUp{
		UserId: user.Id, TradeNo: "trust-price-paid", Amount: 100,
		CreditedQuota: int64(common.QuotaPerUnit) * 100, Money: 100.0,
		Status: common.TopUpStatusSuccess, PaymentProvider: model.PaymentProviderStripe,
		CompleteTime: time.Now().Unix(),
	}).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		UserId: user.Id, UserGroup: "trust-test", UsingGroup: "trust-test",
	}

	groupRatio := HandleGroupRatio(ctx, info)
	require.InDelta(t, 1.2125, groupRatio.GroupRatio, 0.000001)
	require.Equal(t, 2, groupRatio.TrustLevel)
	require.InDelta(t, 0.97, groupRatio.TrustDiscountRatio, 0.000001)
}

func TestModelPriceHelperTieredUsesPreloadedRequestInput(t *testing.T) {
	gin.SetMode(gin.TestMode)

	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})

	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode": `{"tiered-test-model":"tiered_expr"}`,
		"billing_setting.billing_expr": `{"tiered-test-model":"param(\"stream\") == true ? tier(\"stream\", p * 3) : tier(\"base\", p * 2)"}`,
	}))

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/channel/test/1", nil)
	req.Body = nil
	req.ContentLength = 0
	req.Header.Set("Content-Type", "application/json")
	ctx.Request = req
	ctx.Set("group", "default")

	info := &relaycommon.RelayInfo{
		OriginModelName: "tiered-test-model",
		UserGroup:       "default",
		UsingGroup:      "default",
		RequestHeaders:  map[string]string{"Content-Type": "application/json"},
		BillingRequestInput: &billingexpr.RequestInput{
			Headers: map[string]string{"Content-Type": "application/json"},
			Body:    []byte(`{"stream":true}`),
		},
	}

	priceData, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{
		BillingRatios: map[string]float64{"n": 3},
	})
	require.NoError(t, err)
	require.Equal(t, 1500, priceData.QuotaToPreConsume)
	require.NotNil(t, info.TieredBillingSnapshot)
	require.Equal(t, "stream", info.TieredBillingSnapshot.EstimatedTier)
	require.Equal(t, billing_setting.BillingModeTieredExpr, info.TieredBillingSnapshot.BillingMode)
	require.Equal(t, common.QuotaPerUnit, info.TieredBillingSnapshot.QuotaPerUnit)
}

func TestModelPriceHelperTieredIncludesDynamicMultiplier(t *testing.T) {
	gin.SetMode(gin.TestMode)

	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	dpCfg := config.GlobalConfig.Get("dynamic_pricing_setting").(*dynamic_pricing_setting.DynamicPricingSetting)
	oldDP := dynamic_pricing_setting.GetSetting()
	t.Cleanup(func() {
		dynamic_pricing.SetState("tiered-dynamic-model", &dynamic_pricing.ModelState{Factor: 1})
		*dpCfg = oldDP
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})

	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":    `{"tiered-dynamic-model":"tiered_expr"}`,
		"billing_setting.billing_expr":    `{"tiered-dynamic-model":"tier(\"base\", p * 2)"}`,
		"group_ratio_setting.group_ratio": `{"default":1}`,
	}))
	dpCfg.Enabled = true
	dpCfg.MinFactor = 1
	dpCfg.RequireChannelCost = true
	dpCfg.BasePriceUSDPerMillion = 1
	dpCfg.ChannelCosts = map[string]float64{"1": 1}
	dynamic_pricing.SetState("tiered-dynamic-model", &dynamic_pricing.ModelState{Factor: 2})

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("group", "default")
	ctx.Set("channel_id", 1)
	info := &relaycommon.RelayInfo{
		OriginModelName: "tiered-dynamic-model",
		UserGroup:       "default",
		UsingGroup:      "default",
		BillingRequestInput: &billingexpr.RequestInput{
			Body: []byte(`{}`),
		},
	}

	priceData, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)
	// p*2 = 2000; 2000 / 1e6 * 500000 = 1000, then dynamic 2x = 2000.
	require.Equal(t, 2000, priceData.QuotaToPreConsume)
	require.Equal(t, 2.0, priceData.OtherRatios()["dynamic_pricing"])
	require.Equal(t, 2000, info.TieredBillingSnapshot.EstimatedQuotaAfterGroup)
}

func TestModelPriceHelperTieredPreConsumeMaxTokensFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)

	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})

	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":    `{"tiered-fallback-model":"tiered_expr"}`,
		"billing_setting.billing_expr":    `{"tiered-fallback-model":"tier(\"base\", p * 3 + c * 15)"}`,
		"group_ratio_setting.group_ratio": `{"default":1,"free":0}`,
	}))

	const promptTokens = 1000

	cases := []struct {
		name      string
		group     string
		maxTokens int
		expected  int
	}{
		{
			// max_tokens omitted in a paid group -> fall back to 8192 completion tokens.
			// p*3 + c*15 = 1000*3 + 8192*15 = 125880 -> /1e6 * 500000 = 62940
			name:      "non-free group falls back to 8192 completion tokens",
			group:     "default",
			maxTokens: 0,
			expected:  62940,
		},
		{
			// explicit max_tokens is used verbatim, no fallback.
			// 1000*3 + 100*15 = 4500 -> /1e6 * 500000 = 2250
			name:      "explicit max_tokens is used verbatim",
			group:     "default",
			maxTokens: 100,
			expected:  2250,
		},
		{
			// free group (ratio 0) stays zero; fallback is gated on non-zero group ratio.
			name:      "free group stays zero without fallback",
			group:     "free",
			maxTokens: 0,
			expected:  0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			req.Header.Set("Content-Type", "application/json")
			ctx.Request = req
			ctx.Set("group", tc.group)

			info := &relaycommon.RelayInfo{
				OriginModelName: "tiered-fallback-model",
				UserGroup:       tc.group,
				UsingGroup:      tc.group,
				RequestHeaders:  map[string]string{"Content-Type": "application/json"},
				BillingRequestInput: &billingexpr.RequestInput{
					Headers: map[string]string{"Content-Type": "application/json"},
					Body:    []byte(`{}`),
				},
			}

			priceData, err := ModelPriceHelper(ctx, info, promptTokens, &types.TokenCountMeta{MaxTokens: tc.maxTokens})
			require.NoError(t, err)
			require.Equal(t, tc.expected, priceData.QuotaToPreConsume)
		})
	}
}

func TestModelPriceHelperTieredRejectsPreConsumeOverflow(t *testing.T) {
	gin.SetMode(gin.TestMode)

	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})

	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":    `{"tiered-overflow-model":"tiered_expr"}`,
		"billing_setting.billing_expr":    `{"tiered-overflow-model":"tier(\"overflow\", p * 100000000000000000)"}`,
		"group_ratio_setting.group_ratio": `{"default":1}`,
	}))

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Set("group", "default")
	info := &relaycommon.RelayInfo{
		OriginModelName: "tiered-overflow-model",
		UserGroup:       "default",
		UsingGroup:      "default",
		BillingRequestInput: &billingexpr.RequestInput{
			Body: []byte(`{}`),
		},
	}

	_, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})

	var clamp *common.QuotaClamp
	require.ErrorAs(t, err, &clamp)
	require.Equal(t, "QuotaRound", clamp.Op)
	require.Equal(t, common.QuotaClampOverflow, clamp.Kind)
}

func TestModelPriceHelperRequestBillingRatiosOnlyApplyToFixedPrice(t *testing.T) {
	gin.SetMode(gin.TestMode)
	savedModelPrices := ratio_setting.ModelPrice2JSONString()
	savedModelRatios := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(savedModelPrices))
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(savedModelRatios))
	})

	modelPrices, err := common.Marshal(map[string]float64{
		"fixed-image-price":      0.04,
		"fractional-image-price": 0.0000012,
		"overflow-image-price":   float64(common.MaxQuota) / common.QuotaPerUnit / 2,
	})
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(string(modelPrices)))
	modelRatios, err := common.Marshal(map[string]float64{"ratio-image-price": 15})
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(string(modelRatios)))

	tests := []struct {
		name           string
		model          string
		wantQuota      int
		wantUsePrice   bool
		wantImageCount bool
	}{
		{
			name:           "fixed price applies image count",
			model:          "fixed-image-price",
			wantQuota:      180000,
			wantUsePrice:   true,
			wantImageCount: true,
		},
		{
			name:         "ratio price ignores request billing ratios",
			model:        "ratio-image-price",
			wantQuota:    15000,
			wantUsePrice: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Set("group", "default")
			info := &relaycommon.RelayInfo{
				OriginModelName: tt.model,
				UserGroup:       "default",
				UsingGroup:      "default",
			}
			meta := &types.TokenCountMeta{
				ImagePriceRatio: 3,
				BillingRatios:   map[string]float64{"n": 3},
			}

			priceData, err := ModelPriceHelper(ctx, info, 1000, meta)

			require.NoError(t, err)
			require.Equal(t, tt.wantQuota, priceData.QuotaToPreConsume)
			require.Equal(t, tt.wantUsePrice, priceData.UsePrice)
			require.Equal(t, tt.wantImageCount, priceData.HasOtherRatio("n"))
			require.Equal(t, priceData.OtherRatios(), info.PriceData.OtherRatios())
		})
	}

	newInfo := func(model string) (*gin.Context, *relaycommon.RelayInfo) {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Set("group", "default")
		return ctx, &relaycommon.RelayInfo{
			OriginModelName: model,
			UserGroup:       "default",
			UsingGroup:      "default",
		}
	}
	meta := &types.TokenCountMeta{BillingRatios: map[string]float64{"n": 3}}

	ctx, info := newInfo("fractional-image-price")
	priceData, err := ModelPriceHelper(ctx, info, 0, meta)
	require.NoError(t, err)
	// 0.0000012 * 500000 * 3 = 1.8, then truncate once to 1.
	require.Equal(t, 1, priceData.QuotaToPreConsume)

	ctx, info = newInfo("overflow-image-price")
	_, err = ModelPriceHelper(ctx, info, 0, meta)
	var clamp *common.QuotaClamp
	require.ErrorAs(t, err, &clamp)
	require.Equal(t, "QuotaFromFloat", clamp.Op)
	require.Equal(t, common.QuotaClampOverflow, clamp.Kind)
	require.Nil(t, info.Billing)
}

// TestModelPriceHelperPreConsumeIncludesDynamicMultiplier verifies that the
// dynamic pricing ratio is injected before the pre-consume quota computation
// in BOTH billing branches: the fixed-price (usePrice) branch and the
// ratio-billed (non-usePrice) branch. The latter previously computed
// QuotaToPreConsume before the injection, so the pre-consume under-covered
// the final charge (tokens × ratio × dynamic multiplier).
func TestModelPriceHelperPreConsumeIncludesDynamicMultiplier(t *testing.T) {
	gin.SetMode(gin.TestMode)
	savedModelPrices := ratio_setting.ModelPrice2JSONString()
	savedModelRatios := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(savedModelPrices))
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(savedModelRatios))
	})

	modelPrices, err := common.Marshal(map[string]float64{"dyn-fixed-price": 0.04})
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(string(modelPrices)))
	modelRatios, err := common.Marshal(map[string]float64{"dyn-ratio-price": 10})
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(string(modelRatios)))

	// GetMultiplier gates on dynamic_pricing_setting.enabled, so enable the
	// feature (and restore the full previous setting on cleanup).
	dpCfg := config.GlobalConfig.Get("dynamic_pricing_setting").(*dynamic_pricing_setting.DynamicPricingSetting)
	oldDpCfg := dynamic_pricing_setting.GetSetting()
	dpCfg.Enabled = true
	dpCfg.MinFactor = 1
	dpCfg.RequireChannelCost = true
	dpCfg.BasePriceUSDPerMillion = 1
	dpCfg.ChannelCosts = map[string]float64{"1": 1}
	t.Cleanup(func() { *dpCfg = oldDpCfg })

	// Seed dynamic pricing state: GetMultiplier returns 2.0 for both models.
	dynamic_pricing.SetState("dyn-fixed-price", &dynamic_pricing.ModelState{Factor: 2.0})
	dynamic_pricing.SetState("dyn-ratio-price", &dynamic_pricing.ModelState{Factor: 2.0})
	t.Cleanup(func() {
		// No delete API; neutralise so other tests in this package are
		// unaffected by the seeded multipliers.
		dynamic_pricing.SetState("dyn-fixed-price", &dynamic_pricing.ModelState{Factor: 1.0})
		dynamic_pricing.SetState("dyn-ratio-price", &dynamic_pricing.ModelState{Factor: 1.0})
	})

	newInfo := func(model string) (*gin.Context, *relaycommon.RelayInfo) {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Set("group", "default")
		ctx.Set("channel_id", 1)
		return ctx, &relaycommon.RelayInfo{
			OriginModelName: model,
			UserGroup:       "default",
			UsingGroup:      "default",
		}
	}
	meta := &types.TokenCountMeta{}

	// Fixed-price (usePrice): pre-consume = modelPrice × quotaPerUnit ×
	// groupRatio × dynamic multiplier = 0.04 × 500000 × 1 × 2.0 = 40000.
	ctx, info := newInfo("dyn-fixed-price")
	priceData, err := ModelPriceHelper(ctx, info, 1000, meta)
	require.NoError(t, err)
	require.True(t, priceData.UsePrice)
	require.Equal(t, 40000, priceData.QuotaToPreConsume)
	require.InDelta(t, 2.0, priceData.OtherRatioMultiplier(), 1e-9)

	// Ratio-billed (non-usePrice): preConsumedTokens = max(1000, 500) = 1000,
	// ratio = modelRatio × groupRatio = 10; base = 10000, and the dynamic
	// multiplier must be applied so pre-consume matches the final charge
	// (tokens × ratio × dynamic = 20000).
	ctx, info = newInfo("dyn-ratio-price")
	priceData, err = ModelPriceHelper(ctx, info, 1000, meta)
	require.NoError(t, err)
	require.False(t, priceData.UsePrice)
	require.Equal(t, 20000, priceData.QuotaToPreConsume)
	require.InDelta(t, 2.0, priceData.OtherRatioMultiplier(), 1e-9)
}

// TestModelPriceHelperPerCallIncludesDynamicMultiplier verifies the per-call
// (按次计费, MJ/Task) billing path: the dynamic pricing multiplier is injected
// into OtherRatios BEFORE the Quota write, but the base Quota itself stays
// unmultiplied — downstream task and Midjourney integrations apply
// OtherRatios, so folding the multiplier into Quota here would double-charge
// it.
func TestModelPriceHelperPerCallIncludesDynamicMultiplier(t *testing.T) {
	gin.SetMode(gin.TestMode)
	savedModelPrices := ratio_setting.ModelPrice2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(savedModelPrices))
	})

	modelPrices, err := common.Marshal(map[string]float64{"dyn-percall-price": 0.04})
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(string(modelPrices)))

	// GetMultiplier gates on dynamic_pricing_setting.enabled, so enable the
	// feature (and restore the full previous setting on cleanup).
	dpCfg := config.GlobalConfig.Get("dynamic_pricing_setting").(*dynamic_pricing_setting.DynamicPricingSetting)
	oldDpCfg := dynamic_pricing_setting.GetSetting()
	dpCfg.Enabled = true
	dpCfg.MinFactor = 1
	dpCfg.RequireChannelCost = true
	dpCfg.BasePriceUSDPerMillion = 1
	dpCfg.ChannelCosts = map[string]float64{"1": 1}
	t.Cleanup(func() { *dpCfg = oldDpCfg })

	// Seed dynamic pricing state: GetMultiplier returns 2.0 for the model.
	dynamic_pricing.SetState("dyn-percall-price", &dynamic_pricing.ModelState{Factor: 2.0})
	t.Cleanup(func() {
		// No delete API; neutralise so other tests in this package are
		// unaffected by the seeded multiplier.
		dynamic_pricing.SetState("dyn-percall-price", &dynamic_pricing.ModelState{Factor: 1.0})
	})

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("group", "default")
	ctx.Set("channel_id", 1)
	info := &relaycommon.RelayInfo{
		OriginModelName: "dyn-percall-price",
		UserGroup:       "default",
		UsingGroup:      "default",
	}

	priceData, err := ModelPriceHelperPerCall(ctx, info)
	require.NoError(t, err)

	// modelPrice 0.04 with groupRatio 1 is not free, and the fixed-price
	// (usePrice) branch is taken.
	require.False(t, priceData.FreeModel)
	require.True(t, priceData.UsePrice)

	// The multiplier is exposed via OtherRatios for downstream settlement...
	require.InDelta(t, 2.0, priceData.OtherRatioMultiplier(), 1e-9)
	require.Contains(t, priceData.OtherRatios(), "dynamic_pricing")
	require.InDelta(t, 2.0, priceData.OtherRatios()["dynamic_pricing"], 1e-9)

	// ...but the base quota is NOT multiplied: 0.04 × 500000 × 1 = 20000.
	// Per-call integrations apply OtherRatios afterwards; multiplying here
	// would double-charge the dynamic multiplier.
	require.Equal(t, 20000, priceData.Quota)
}

func TestDynamicPricingRejectsNonBillableBase(t *testing.T) {
	dpCfg := config.GlobalConfig.Get("dynamic_pricing_setting").(*dynamic_pricing_setting.DynamicPricingSetting)
	oldDP := dynamic_pricing_setting.GetSetting()
	dpCfg.Enabled = true
	dpCfg.RequireChannelCost = true
	t.Cleanup(func() { *dpCfg = oldDP })

	require.Error(t, validateDynamicPricingBillingBase("free-group-model", 0, 1))
	require.Error(t, validateDynamicPricingBillingBase("free-model", 1, 0))
	require.NoError(t, validateDynamicPricingBillingBase("paid-model", 0.8, 2))
}
