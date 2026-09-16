package helper

import (
	"fmt"
	"math"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/logger"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/pkg/billingexpr"
	"github.com/LIghtJUNction/api.lmm.best/pkg/dynamic_pricing"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/setting/billing_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/dynamic_pricing_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	hosttypes "github.com/LIghtJUNction/api.lmm.best/types"

	"github.com/gin-gonic/gin"
)

func modelPriceNotConfiguredError(modelName string, userId int) error {
	if model.IsAdmin(userId) {
		return fmt.Errorf(
			"模型 %s 的价格未配置。请前往「系统设置 → 运营设置」开启自用模式，或在「系统设置 → 分组与模型定价设置」中为该模型配置价格；"+
				"Model %s price not configured. Go to System Settings → Operation Settings to enable self-use mode, or configure the model price in System Settings → Group & Model Pricing.",
			modelName, modelName,
		)
	}
	return fmt.Errorf(
		"模型 %s 的价格尚未由管理员配置，暂时无法使用，请联系站点管理员开启该模型；"+
			"Model %s has not been priced by the administrator yet. Please contact the site administrator to enable this model.",
		modelName, modelName,
	)
}

func requestDynamicPricingMultiplier(c *gin.Context, info *relaycommon.RelayInfo) (float64, error) {
	if !dynamic_pricing_setting.IsEnabled() {
		return 1.0, nil
	}
	if info == nil {
		return 0, fmt.Errorf("dynamic pricing requires relay information")
	}
	channelID := common.GetContextKeyInt(c, constant.ContextKeyChannelId)
	multiplier, _, err := dynamic_pricing.GetRequestMultiplier(info.OriginModelName, channelID)
	return multiplier, err
}

func validateDynamicPricingBillingBase(modelName string, groupRatio, billingBase float64) error {
	if !dynamic_pricing_setting.IsEnabled() {
		return nil
	}
	if groupRatio <= 0 || math.IsNaN(groupRatio) || math.IsInf(groupRatio, 0) ||
		billingBase <= 0 || math.IsNaN(billingBase) || math.IsInf(billingBase, 0) {
		return fmt.Errorf("dynamic pricing blocked model %s: the effective billing base must be positive", modelName)
	}
	return nil
}

// https://docs.claude.com/en/docs/build-with-claude/prompt-caching#1-hour-cache-duration
const claudeCacheCreation1hMultiplier = 6 / 3.75

// defaultTieredPreConsumeMaxTokens is the fallback completion-token estimate
// used for tiered expression pre-consume when the client omits max_tokens, so
// the pre-consumed quota still reflects a plausible output cost in paid groups.
const defaultTieredPreConsumeMaxTokens = 8192

// HandleGroupRatio checks for "auto_group" in the context and updates the group ratio and relayInfo.UsingGroup if present
func HandleGroupRatio(ctx *gin.Context, relayInfo *relaycommon.RelayInfo) hosttypes.GroupRatioInfo {
	groupRatioInfo := hosttypes.GroupRatioInfo{
		GroupRatio:        1.0, // default ratio
		GroupSpecialRatio: -1,
	}

	// check auto group
	autoGroup, exists := ctx.Get("auto_group")
	if exists {
		logger.LogDebug(ctx, "final group: %s", autoGroup)
		relayInfo.UsingGroup = autoGroup.(string)
	}

	// check user group special ratio
	userGroupRatio, ok := ratio_setting.GetGroupGroupRatio(relayInfo.UserGroup, relayInfo.UsingGroup)
	if ok {
		// user group special ratio
		groupRatioInfo.GroupSpecialRatio = userGroupRatio
		groupRatioInfo.GroupRatio = userGroupRatio
		groupRatioInfo.HasSpecialRatio = true
	} else {
		// normal group ratio
		groupRatioInfo.GroupRatio = ratio_setting.GetGroupRatio(relayInfo.UsingGroup)
	}
	groupRatioInfo.TrustDiscountRatio = 1
	if relayInfo.UserId > 0 {
		trustLevel, err := model.GetTrustLevelInfoByUserID(relayInfo.UserId)
		if err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("failed to calculate trust discount for user %d: %s", relayInfo.UserId, err.Error()))
		} else {
			groupRatioInfo.TrustLevel = trustLevel.Level
			groupRatioInfo.TrustDiscountRatio = trustLevel.DiscountRatio
			groupRatioInfo.GroupRatio *= trustLevel.DiscountRatio
		}
	}

	return groupRatioInfo
}

func ModelPriceHelper(c *gin.Context, info *relaycommon.RelayInfo, promptTokens int, meta *types.TokenCountMeta) (hosttypes.PriceData, error) {
	modelPrice, usePrice := ratio_setting.GetModelPrice(info.OriginModelName, false)

	groupRatioInfo := HandleGroupRatio(c, info)

	// Check if this model uses tiered_expr billing
	if billing_setting.GetBillingMode(info.OriginModelName) == billing_setting.BillingModeTieredExpr {
		return modelPriceHelperTiered(c, info, promptTokens, meta, groupRatioInfo)
	}

	var modelRatio float64
	var completionRatio float64
	var cacheRatio float64
	var imageRatio float64
	var cacheCreationRatio float64
	var cacheCreationRatio5m float64
	var cacheCreationRatio1h float64
	var audioRatio float64
	var audioCompletionRatio float64
	var freeModel bool
	if !usePrice {
		var success bool
		var matchName string
		modelRatio, success, matchName = ratio_setting.GetModelRatio(info.OriginModelName)
		if !success {
			acceptUnsetRatio := false
			if info.UserSetting.AcceptUnsetRatioModel {
				acceptUnsetRatio = true
			}
			if !acceptUnsetRatio {
				return hosttypes.PriceData{}, modelPriceNotConfiguredError(matchName, info.UserId)
			}
		}
		completionRatio = ratio_setting.GetCompletionRatio(info.OriginModelName)
		cacheRatio, _ = ratio_setting.GetCacheRatio(info.OriginModelName)
		cacheCreationRatio, _ = ratio_setting.GetCreateCacheRatio(info.OriginModelName)
		cacheCreationRatio5m = cacheCreationRatio
		// 固定1h和5min缓存写入价格的比例
		cacheCreationRatio1h = cacheCreationRatio * claudeCacheCreation1hMultiplier
		imageRatio, _ = ratio_setting.GetImageRatio(info.OriginModelName)
		audioRatio = ratio_setting.GetAudioRatio(info.OriginModelName)
		audioCompletionRatio = ratio_setting.GetAudioCompletionRatio(info.OriginModelName)
	} else {
		if meta.ImagePriceRatio != 0 {
			modelPrice = modelPrice * meta.ImagePriceRatio
		}
	}

	// check if free model pre-consume is disabled
	if !operation_setting.GetQuotaSetting().EnableFreeModelPreConsume {
		// if model price or ratio is 0, do not pre-consume quota
		if groupRatioInfo.GroupRatio == 0 {
			freeModel = true
		} else if usePrice {
			if modelPrice == 0 {
				freeModel = true
			}
		} else {
			if modelRatio == 0 {
				freeModel = true
			}
		}
	}
	billingBase := modelRatio
	if usePrice {
		billingBase = modelPrice
	}
	if err := validateDynamicPricingBillingBase(info.OriginModelName, groupRatioInfo.GroupRatio, billingBase); err != nil {
		return hosttypes.PriceData{}, err
	}

	priceData := hosttypes.PriceData{
		FreeModel:            freeModel,
		ModelPrice:           modelPrice,
		ModelRatio:           modelRatio,
		CompletionRatio:      completionRatio,
		GroupRatioInfo:       groupRatioInfo,
		UsePrice:             usePrice,
		CacheRatio:           cacheRatio,
		ImageRatio:           imageRatio,
		AudioRatio:           audioRatio,
		AudioCompletionRatio: audioCompletionRatio,
		CacheCreationRatio:   cacheCreationRatio,
		CacheCreation5mRatio: cacheCreationRatio5m,
		CacheCreation1hRatio: cacheCreationRatio1h,
	}
	// 动态定价：在预扣与结算两个路径之前注入倍率，使 pre-consume 与 post-consume 一致。
	// 两条计费分支的预扣额度计算都必须发生在此注入之后，确保 ratio-billed
	// (非 usePrice) 模型的 pre-consume 同样包含动态倍率。
	if dynamic_pricing_setting.IsEnabled() {
		multiplier, err := requestDynamicPricingMultiplier(c, info)
		if err != nil {
			return hosttypes.PriceData{}, err
		}
		priceData.AddOtherRatio("dynamic_pricing", multiplier)
	}
	if !freeModel {
		if usePrice {
			for name, ratio := range meta.BillingRatios {
				priceData.AddOtherRatio(name, ratio)
			}
			quotaToPreConsume := priceData.ApplyOtherRatiosToFloat(modelPrice * common.QuotaPerUnit * groupRatioInfo.GroupRatio)
			quota, err := common.QuotaFromFloatStrict(quotaToPreConsume)
			if err != nil {
				return hosttypes.PriceData{}, err
			}
			priceData.QuotaToPreConsume = quota
		} else {
			preConsumedTokens := common.Max(promptTokens, common.PreConsumedQuota)
			if meta.MaxTokens != 0 {
				preConsumedTokens += meta.MaxTokens
			}
			ratio := modelRatio * groupRatioInfo.GroupRatio
			quota, err := common.QuotaFromFloatStrict(priceData.ApplyOtherRatiosToFloat(float64(preConsumedTokens) * ratio))
			if err != nil {
				return hosttypes.PriceData{}, err
			}
			priceData.QuotaToPreConsume = quota
		}
	}

	if common.DebugEnabled {
		logger.LogDebug(c, "model_price_helper result: %s", priceData.ToSetting())
	}
	info.PriceData = priceData
	return priceData, nil
}

// ModelPriceHelperPerCall 按次/按量计费的 PriceHelper (MJ、Task)
func ModelPriceHelperPerCall(c *gin.Context, info *relaycommon.RelayInfo) (hosttypes.PriceData, error) {
	groupRatioInfo := HandleGroupRatio(c, info)

	modelPrice, success := ratio_setting.GetModelPrice(info.OriginModelName, true)
	usePrice := success
	var modelRatio float64

	if !success {
		defaultPrice, ok := ratio_setting.GetDefaultModelPriceMap()[info.OriginModelName]
		if ok {
			modelPrice = defaultPrice
			usePrice = true
		} else {
			var ratioSuccess bool
			var matchName string
			modelRatio, ratioSuccess, matchName = ratio_setting.GetModelRatio(info.OriginModelName)
			acceptUnsetRatio := false
			if info.UserSetting.AcceptUnsetRatioModel {
				acceptUnsetRatio = true
			}
			if !ratioSuccess && !acceptUnsetRatio {
				return hosttypes.PriceData{}, modelPriceNotConfiguredError(matchName, info.UserId)
			}
		}
	}

	freeModel := false

	if usePrice {
		if !operation_setting.GetQuotaSetting().EnableFreeModelPreConsume {
			if groupRatioInfo.GroupRatio == 0 || modelPrice == 0 {
				freeModel = true
			}
		}
	} else {
		// 按量计费：以模型倍率的一半作为预扣额度
		modelPrice = -1
		if !operation_setting.GetQuotaSetting().EnableFreeModelPreConsume {
			if groupRatioInfo.GroupRatio == 0 || modelRatio == 0 {
				freeModel = true
			}
		}
	}
	billingBase := modelRatio
	if usePrice {
		billingBase = modelPrice
	}
	if err := validateDynamicPricingBillingBase(info.OriginModelName, groupRatioInfo.GroupRatio, billingBase); err != nil {
		return hosttypes.PriceData{}, err
	}

	priceData := hosttypes.PriceData{
		FreeModel:      freeModel,
		ModelPrice:     modelPrice,
		ModelRatio:     modelRatio,
		UsePrice:       usePrice,
		GroupRatioInfo: groupRatioInfo,
	}
	// 动态定价：按次计费同样应用模型倍率。注入必须位于 Quota 写入之前，
	// 这样 priceData 额度的所有下游使用（relay_task 的预扣按 OtherRatios 折算、
	// task_billing 的差额结算）都能看到该倍率。
	if dynamic_pricing_setting.IsEnabled() {
		multiplier, err := requestDynamicPricingMultiplier(c, info)
		if err != nil {
			return hosttypes.PriceData{}, err
		}
		priceData.AddOtherRatio("dynamic_pricing", multiplier)
	}

	// 预扣额度（基础额度，不含 OtherRatios；下游统一按 OtherRatios 折算，
	// 此处不可把动态倍率直接折入 Quota，否则 relay_task 会重复应用）。
	if !freeModel {
		var quotaBase float64
		if usePrice {
			quotaBase = modelPrice * common.QuotaPerUnit * groupRatioInfo.GroupRatio
		} else {
			// 按量计费：以模型倍率的一半作为预扣额度
			quotaBase = modelRatio / 2 * common.QuotaPerUnit * groupRatioInfo.GroupRatio
		}
		quota, err := common.QuotaFromFloatStrict(quotaBase)
		if err != nil {
			return hosttypes.PriceData{}, err
		}
		priceData.Quota = quota
	}

	return priceData, nil
}

func HasModelBillingConfig(modelName string) bool {
	if _, ok := ratio_setting.GetModelPrice(modelName, false); ok {
		return true
	}
	if _, ok, _ := ratio_setting.GetModelRatio(modelName); ok {
		return true
	}
	if billing_setting.GetBillingMode(modelName) != billing_setting.BillingModeTieredExpr {
		return false
	}
	expr, ok := billing_setting.GetBillingExpr(modelName)
	return ok && strings.TrimSpace(expr) != ""
}

func modelPriceHelperTiered(c *gin.Context, info *relaycommon.RelayInfo, promptTokens int, meta *types.TokenCountMeta, groupRatioInfo hosttypes.GroupRatioInfo) (hosttypes.PriceData, error) {
	exprStr, ok := billing_setting.GetBillingExpr(info.OriginModelName)
	if !ok {
		return hosttypes.PriceData{}, fmt.Errorf("model %s is configured as tiered_expr but has no billing expression", info.OriginModelName)
	}

	estimatedCompletionTokens := meta.MaxTokens
	if estimatedCompletionTokens == 0 && groupRatioInfo.GroupRatio != 0 {
		estimatedCompletionTokens = defaultTieredPreConsumeMaxTokens
	}

	requestInput, err := ResolveIncomingBillingExprRequestInputForExpr(c, info, exprStr)
	if err != nil {
		return hosttypes.PriceData{}, err
	}

	rawCost, trace, err := billingexpr.RunExprWithRequest(exprStr, billingexpr.TokenParams{
		P:   float64(promptTokens),
		C:   float64(estimatedCompletionTokens),
		Len: float64(promptTokens),
	}, requestInput)
	if err != nil {
		return hosttypes.PriceData{}, fmt.Errorf("model %s tiered expr run failed: %w", info.OriginModelName, err)
	}
	if err := validateDynamicPricingBillingBase(info.OriginModelName, groupRatioInfo.GroupRatio, rawCost); err != nil {
		return hosttypes.PriceData{}, err
	}

	// Expression coefficients are $/1M tokens prices; convert to quota the same way per-call billing does.
	quotaBeforeGroup := rawCost / 1_000_000 * common.QuotaPerUnit
	preConsumedQuota, err := billingexpr.QuotaRoundStrict(quotaBeforeGroup * groupRatioInfo.GroupRatio)
	if err != nil {
		return hosttypes.PriceData{}, err
	}

	freeModel := false
	if !operation_setting.GetQuotaSetting().EnableFreeModelPreConsume {
		if groupRatioInfo.GroupRatio == 0 {
			preConsumedQuota = 0
			freeModel = true
		}
	}

	exprHash := billingexpr.ExprHashString(exprStr)
	snapshot := &billingexpr.BillingSnapshot{
		BillingMode:               billing_setting.BillingModeTieredExpr,
		ModelName:                 info.OriginModelName,
		ExprString:                exprStr,
		ExprHash:                  exprHash,
		GroupRatio:                groupRatioInfo.GroupRatio,
		EstimatedPromptTokens:     promptTokens,
		EstimatedCompletionTokens: estimatedCompletionTokens,
		EstimatedQuotaBeforeGroup: quotaBeforeGroup,
		EstimatedQuotaAfterGroup:  preConsumedQuota,
		EstimatedTier:             trace.MatchedTier,
		QuotaPerUnit:              common.QuotaPerUnit,
		ExprVersion:               billingexpr.ExprVersion(exprStr),
	}
	info.TieredBillingSnapshot = snapshot
	info.BillingRequestInput = &requestInput

	priceData := hosttypes.PriceData{
		FreeModel:         freeModel,
		GroupRatioInfo:    groupRatioInfo,
		QuotaToPreConsume: preConsumedQuota,
	}
	if dynamic_pricing_setting.IsEnabled() {
		mult, err := requestDynamicPricingMultiplier(c, info)
		if err != nil {
			return hosttypes.PriceData{}, err
		}
		priceData.AddOtherRatio("dynamic_pricing", mult)
		quota, err := common.QuotaFromFloatStrict(float64(preConsumedQuota) * mult)
		if err != nil {
			return hosttypes.PriceData{}, err
		}
		priceData.QuotaToPreConsume = quota
		snapshot.EstimatedQuotaAfterGroup = quota
	}

	logger.LogDebug(c, "model_price_helper_tiered result: model=%s preConsume=%d quotaBeforeGroup=%.2f groupRatio=%.2f tier=%s", info.OriginModelName, preConsumedQuota, quotaBeforeGroup, groupRatioInfo.GroupRatio, trace.MatchedTier)

	info.PriceData = priceData
	return priceData, nil
}
