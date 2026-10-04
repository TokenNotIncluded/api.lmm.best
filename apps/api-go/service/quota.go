package service

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/logger"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/pkg/billingexpr"
	perfmetrics "github.com/LIghtJUNction/api.lmm.best/pkg/perf_metrics"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/LIghtJUNction/api.lmm.best/types"

	"github.com/bytedance/gopkg/util/gopool"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

type TokenDetails struct {
	TextTokens  int
	AudioTokens int
}

type QuotaInfo struct {
	InputDetails  TokenDetails
	OutputDetails TokenDetails
	ModelName     string
	UsePrice      bool
	ModelPrice    float64
	ModelRatio    float64
	GroupRatio    float64
}

func hasCustomModelRatio(modelName string, currentRatio float64) bool {
	defaultRatio, exists := ratio_setting.GetDefaultModelRatioMap()[modelName]
	if !exists {
		return true
	}
	return currentRatio != defaultRatio
}

func calculateAudioQuota(info QuotaInfo) (int, *common.QuotaClamp) {
	if info.UsePrice {
		modelPrice := decimal.NewFromFloat(info.ModelPrice)
		quotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
		groupRatio := decimal.NewFromFloat(info.GroupRatio)

		quota := modelPrice.Mul(quotaPerUnit).Mul(groupRatio)
		return common.QuotaFromDecimalChecked(quota)
	}

	completionRatio := decimal.NewFromFloat(ratio_setting.GetCompletionRatio(info.ModelName))
	audioRatio := decimal.NewFromFloat(ratio_setting.GetAudioRatio(info.ModelName))
	audioCompletionRatio := decimal.NewFromFloat(ratio_setting.GetAudioCompletionRatio(info.ModelName))

	groupRatio := decimal.NewFromFloat(info.GroupRatio)
	modelRatio := decimal.NewFromFloat(info.ModelRatio)
	ratio := groupRatio.Mul(modelRatio)

	inputTextTokens := decimal.NewFromInt(int64(info.InputDetails.TextTokens))
	outputTextTokens := decimal.NewFromInt(int64(info.OutputDetails.TextTokens))
	inputAudioTokens := decimal.NewFromInt(int64(info.InputDetails.AudioTokens))
	outputAudioTokens := decimal.NewFromInt(int64(info.OutputDetails.AudioTokens))

	quota := decimal.Zero
	quota = quota.Add(inputTextTokens)
	quota = quota.Add(outputTextTokens.Mul(completionRatio))
	quota = quota.Add(inputAudioTokens.Mul(audioRatio))
	quota = quota.Add(outputAudioTokens.Mul(audioRatio).Mul(audioCompletionRatio))

	quota = quota.Mul(ratio)

	// If ratio is not zero and quota is less than or equal to zero, set quota to 1
	if !ratio.IsZero() && quota.LessThanOrEqual(decimal.Zero) {
		quota = decimal.NewFromInt(1)
	}

	return common.QuotaFromDecimalChecked(quota)
}

// realtimeUsageParts derives unclassified text from inclusive token totals,
// while rejecting impossible or overflowing upstream counters. Missing totals
// may be reconstructed from reported modality counts, never from audio bytes.
func realtimeUsageParts(usage *dto.RealtimeUsage) (inputText, outputText, inputTokens, outputTokens int, err error) {
	if usage == nil {
		return
	}
	input := usage.InputTokenDetails
	output := usage.OutputTokenDetails
	counts := []int{usage.TotalTokens, usage.InputTokens, usage.OutputTokens,
		input.TextTokens, input.AudioTokens, input.ImageTokens, input.CachedTokens,
		input.CachedCreationTokens, input.CacheWriteTokens,
		output.TextTokens, output.AudioTokens, output.ImageTokens, output.ReasoningTokens}
	if input.CachedTokensDetails != nil {
		counts = append(counts, input.CachedTokensDetails.TextTokens,
			input.CachedTokensDetails.AudioTokens, input.CachedTokensDetails.ImageTokens)
	}
	for _, count := range counts {
		if count < 0 {
			return 0, 0, 0, 0, errors.New("realtime usage contains negative token counts")
		}
	}
	parts := func(total, text, audio, image int) (int, int, error) {
		if total == 0 {
			total = text
			for _, count := range []int{audio, image} {
				if count > math.MaxInt-total {
					return 0, 0, errors.New("realtime token counts overflow")
				}
				total += count
			}
		}
		remaining := total
		for _, count := range []int{audio, image, text} {
			if count > remaining {
				return 0, 0, errors.New("realtime modality counts exceed token total")
			}
			remaining -= count
		}
		return text + remaining, total, nil
	}
	inputText, inputTokens, err = parts(usage.InputTokens, input.TextTokens, input.AudioTokens, input.ImageTokens)
	if err != nil {
		return
	}
	outputText, outputTokens, err = parts(usage.OutputTokens, output.TextTokens, output.AudioTokens, output.ImageTokens)
	if err != nil {
		return
	}
	if inputTokens > math.MaxInt-outputTokens {
		err = errors.New("realtime token total overflows")
	} else if input.CachedTokens > inputTokens || output.ReasoningTokens > outputTokens {
		err = errors.New("realtime token details exceed token total")
	} else if input.CachedTokensDetails != nil {
		_, status := input.ValidatedCachedTokenDetails(inputTokens)
		if status == dto.CacheReadDetailsInvalid {
			err = errors.New("realtime cached token details are invalid")
		}
	}
	return
}

func realtimeHasUsage(usage *dto.RealtimeUsage) bool {
	return usage != nil && (usage.TotalTokens > 0 || usage.InputTokens > 0 || usage.OutputTokens > 0 ||
		usage.InputTokenDetails.TextTokens > 0 || usage.InputTokenDetails.AudioTokens > 0 || usage.InputTokenDetails.ImageTokens > 0 ||
		usage.OutputTokenDetails.TextTokens > 0 || usage.OutputTokenDetails.AudioTokens > 0 || usage.OutputTokenDetails.ImageTokens > 0)
}

// ValidateRealtimeUsage validates one upstream measurement before a session
// collector adds it to the cumulative usage that already incurred a cost.
func ValidateRealtimeUsage(usage *dto.RealtimeUsage) error {
	_, _, _, _, err := realtimeUsageParts(usage)
	return err
}

// Every reservation and the final settlement use the same frozen request
// prices. The caller supplies cumulative session usage, not a single response.
func realtimeReservationQuota(info *relaycommon.RelayInfo, usage *dto.RealtimeUsage) (int, *billingexpr.TieredResult, error) {
	if info == nil {
		return 0, nil, errors.New("realtime relay info is nil")
	}
	inputText, outputText, inputTokens, outputTokens, err := realtimeUsageParts(usage)
	if err != nil {
		return 0, nil, err
	}
	if !realtimeHasUsage(usage) {
		return 0, nil, nil
	}
	price := info.PriceData
	if info.TieredBillingSnapshot != nil {
		converted := &dto.Usage{PromptTokens: inputTokens, CompletionTokens: outputTokens,
			PromptTokensDetails: usage.InputTokenDetails, CompletionTokenDetails: usage.OutputTokenDetails}
		vars := billingexpr.UsedVars(info.TieredBillingSnapshot.ExprString)
		if ok, quota, result := TryTieredSettle(info, BuildTieredTokenParams(converted, false, vars)); ok {
			if info.QuotaClamp != nil {
				return quota, result, info.QuotaClamp
			}
			return quota, result, nil
		}
	}
	for _, value := range []float64{price.ModelPrice, price.ModelRatio, price.CompletionRatio,
		price.AudioRatio, price.AudioCompletionRatio, price.ImageRatio, price.CacheRatio, price.GroupRatioInfo.GroupRatio} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return 0, nil, errors.New("realtime price snapshot is invalid")
		}
	}
	group := decimal.NewFromFloat(price.GroupRatioInfo.GroupRatio)
	var value decimal.Decimal
	if price.UsePrice {
		value = decimal.NewFromFloat(price.ModelPrice).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).Mul(group)
	} else {
		audio := decimal.NewFromFloat(price.AudioRatio)
		image := decimal.NewFromFloat(price.ImageRatio)
		input := decimal.NewFromInt(int64(inputText)).
			Add(decimal.NewFromInt(int64(usage.InputTokenDetails.AudioTokens)).Mul(audio)).
			Add(decimal.NewFromInt(int64(usage.InputTokenDetails.ImageTokens)).Mul(image))
		cached, status := usage.InputTokenDetails.ValidatedCachedTokenDetails(inputTokens)
		if status == dto.CacheReadDetailsReported {
			// Only upstream-classified caches receive a modality discount. A
			// bare aggregate cannot identify text versus audio cache tokens.
			// Audio cache has its own official rate, which PriceData does not
			// capture yet. Keep it at full audio price instead of reusing the
			// text discount for a different modality.
			cacheWeight := decimal.NewFromInt(int64(cached.TextTokens))
			input = input.Add(cacheWeight.Mul(decimal.NewFromFloat(price.CacheRatio).Sub(decimal.NewFromInt(1))))
		}
		output := decimal.NewFromInt(int64(outputText)).Mul(decimal.NewFromFloat(price.CompletionRatio)).
			Add(decimal.NewFromInt(int64(usage.OutputTokenDetails.AudioTokens)).Mul(audio).Mul(decimal.NewFromFloat(price.AudioCompletionRatio))).
			Add(decimal.NewFromInt(int64(usage.OutputTokenDetails.ImageTokens)).Mul(image).Mul(decimal.NewFromFloat(price.CompletionRatio)))
		value = input.Add(output).Mul(decimal.NewFromFloat(price.ModelRatio)).Mul(group)
		if value.IsPositive() && value.LessThan(decimal.NewFromInt(1)) {
			value = decimal.NewFromInt(1)
		}
	}
	value = price.ApplyOtherRatiosToDecimal(value)
	quota, clamp := common.QuotaFromDecimalChecked(value)
	noteQuotaClamp(info, clamp)
	if clamp != nil {
		return quota, nil, clamp
	}
	return quota, nil, nil
}

func PreWssConsumeQuota(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.RealtimeUsage) error {
	quota, _, err := realtimeReservationQuota(relayInfo, usage)
	if err != nil {
		return err
	}
	if relayInfo.Billing == nil {
		if quota == 0 && relayInfo.PriceData.FreeModel {
			return nil
		}
		return errors.New("realtime billing session is missing")
	}
	if err := relayInfo.Billing.Reserve(quota); err != nil {
		return err
	}
	logger.LogInfo(ctx, fmt.Sprintf("realtime cumulative quota reserved: %d", quota))
	return nil
}

func PostWssConsumeQuota(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, modelName string,
	usage *dto.RealtimeUsage, extraContent string) {
	if relayInfo == nil || (relayInfo.Billing == nil && !relayInfo.PriceData.FreeModel) {
		logger.LogError(ctx, "realtime settlement requires a billing session")
		return
	}
	if usage == nil {
		usage = &dto.RealtimeUsage{}
	}
	quota, tieredResult, quotaErr := realtimeReservationQuota(relayInfo, usage)
	usageEstimated := false
	if quotaErr != nil {
		// The collector validates each measurement before accumulation. For
		// legacy callers with invalid final counters, preserve the existing
		// budget as an explicit estimate; it may include the startup reserve
		// and must never be described as reported consumption.
		var clamp *common.QuotaClamp
		if !errors.As(quotaErr, &clamp) {
			usageEstimated = true
			quota = 0
			if relayInfo.Billing != nil {
				quota = relayInfo.Billing.GetPreConsumedQuota()
			}
		}
		logger.LogError(ctx, "invalid realtime final usage: "+quotaErr.Error())
	}
	useTimeSeconds := time.Now().Unix() - relayInfo.StartTime.Unix()

	tokenName := ctx.GetString("token_name")
	completionRatio := relayInfo.PriceData.CompletionRatio
	audioRatio := relayInfo.PriceData.AudioRatio
	audioCompletionRatio := relayInfo.PriceData.AudioCompletionRatio

	modelRatio := relayInfo.PriceData.ModelRatio
	groupRatio := relayInfo.PriceData.GroupRatioInfo.GroupRatio
	modelPrice := relayInfo.PriceData.ModelPrice
	usePrice := relayInfo.PriceData.UsePrice

	hasUsage := realtimeHasUsage(usage)
	var logContent string
	if !usePrice {
		logContent = fmt.Sprintf("模型倍率 %.2f，补全倍率 %.2f，音频倍率 %.2f，音频补全倍率 %.2f，分组倍率 %.2f",
			modelRatio, completionRatio, audioRatio, audioCompletionRatio, groupRatio)
	} else {
		logContent = fmt.Sprintf("模型价格 %.2f，分组倍率 %.2f", modelPrice, groupRatio)
	}

	// record all the consume log even if quota is 0
	if !hasUsage && quotaErr == nil {
		// in this case, must be some error happened
		// we cannot just return, because we may have to return the pre-consumed quota
		quota = 0
		logContent += "（可能是上游超时）"
		logger.LogError(ctx, fmt.Sprintf("total tokens is 0, cannot consume quota, userId %d, channelId %d, "+
			"tokenId %d, model %s， pre-consumed quota %d", relayInfo.UserId, relayInfo.ChannelId, relayInfo.TokenId, modelName, relayInfo.FinalPreConsumedQuota))
	} else {
		model.UpdateUserUsedQuotaAndRequestCount(relayInfo.UserId, quota)
		model.UpdateChannelUsedQuota(relayInfo.ChannelId, quota)
		if err := model.RecordPublicRelayUsage(relayInfo.ChannelId, quota); err != nil {
			common.SysError("failed to record public relay usage: " + err.Error())
		}
	}

	if err := SettleBilling(ctx, relayInfo, quota); err != nil {
		logger.LogError(ctx, "error settling billing: "+err.Error())
	}

	logModel := modelName
	if extraContent != "" {
		logContent += ", " + extraContent
	}
	other := GenerateWssOtherInfo(ctx, relayInfo, usage, modelRatio, groupRatio,
		completionRatio, audioRatio, audioCompletionRatio, modelPrice, relayInfo.PriceData.GroupRatioInfo.GroupSpecialRatio)
	if !hasUsage && quotaErr == nil {
		other["upstream_empty_usage"] = true
	}
	if usageEstimated {
		other["usage_estimated"] = true
		other["usage_estimate_basis"] = "realtime_session_reserved_budget"
	}
	other["cache_ratio"] = relayInfo.PriceData.CacheRatio
	other["cache_tokens"] = usage.InputTokenDetails.CachedTokens
	if usage.InputTokenDetails.CachedTokensDetails != nil {
		other["cached_tokens_details"] = *usage.InputTokenDetails.CachedTokensDetails
		if usage.InputTokenDetails.CachedTokensDetails.AudioTokens > 0 {
			other["unsupported_audio_cache_pricing"] = true
		}
	} else if usage.InputTokenDetails.CachedTokens > 0 {
		other["unclassified_cache_pricing"] = true
	}
	if tieredResult != nil {
		InjectTieredBillingInfo(other, relayInfo, tieredResult)
	}
	attachQuotaSaturation(ctx, relayInfo, other)
	model.RecordConsumeLog(ctx, relayInfo.UserId, model.RecordConsumeLogParams{
		ChannelId:        relayInfo.ChannelId,
		PromptTokens:     usage.InputTokens,
		CompletionTokens: usage.OutputTokens,
		ModelName:        logModel,
		TokenName:        tokenName,
		Quota:            quota,
		Content:          logContent,
		TokenId:          relayInfo.TokenId,
		UseTimeSeconds:   int(useTimeSeconds),
		IsStream:         relayInfo.IsStream,
		Group:            relayInfo.UsingGroup,
		Other:            other,
	})
}

func roundDerivedTokenCount(value float64) int {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return -1
	}
	rounded := math.Round(value)
	if rounded > float64(math.MaxInt) {
		return -1
	}
	if rounded == float64(math.MaxInt) {
		return math.MaxInt
	}
	return int(rounded)
}

func CalcOpenRouterCacheCreateTokens(usage dto.Usage, priceData types.PriceData) int {
	if priceData.CacheCreationRatio == 1 {
		return 0
	}
	quotaPrice := priceData.ModelRatio / common.QuotaPerUnit
	promptCacheCreatePrice := quotaPrice * priceData.CacheCreationRatio
	promptCacheReadPrice := quotaPrice * priceData.CacheRatio
	completionPrice := quotaPrice * priceData.CompletionRatio

	cost, _ := usage.Cost.(float64)
	totalPromptTokens := float64(usage.PromptTokens)
	completionTokens := float64(usage.CompletionTokens)
	promptCacheReadTokens := float64(usage.PromptTokensDetails.CachedTokens)

	value := (cost -
		totalPromptTokens*quotaPrice +
		promptCacheReadTokens*(quotaPrice-promptCacheReadPrice) -
		completionTokens*completionPrice) /
		(promptCacheCreatePrice - quotaPrice)
	return roundDerivedTokenCount(value)
}

func PostAudioConsumeQuota(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.Usage, extraContent string) {

	var tieredUsedVars map[string]bool
	if snap := relayInfo.TieredBillingSnapshot; snap != nil {
		tieredUsedVars = billingexpr.UsedVars(snap.ExprString)
	}
	var tieredResult *billingexpr.TieredResult
	tieredOk, tieredQuota, tieredRes := TryTieredSettle(relayInfo, BuildTieredTokenParams(usage, false, tieredUsedVars))
	if tieredOk {
		tieredResult = tieredRes
	}

	useTimeSeconds := time.Now().Unix() - relayInfo.StartTime.Unix()
	textInputTokens := usage.PromptTokensDetails.TextTokens
	textOutTokens := usage.CompletionTokenDetails.TextTokens

	audioInputTokens := usage.PromptTokensDetails.AudioTokens
	audioOutTokens := usage.CompletionTokenDetails.AudioTokens

	tokenName := ctx.GetString("token_name")
	completionRatio := decimal.NewFromFloat(ratio_setting.GetCompletionRatio(relayInfo.OriginModelName))
	audioRatio := decimal.NewFromFloat(ratio_setting.GetAudioRatio(relayInfo.OriginModelName))
	audioCompletionRatio := decimal.NewFromFloat(ratio_setting.GetAudioCompletionRatio(relayInfo.OriginModelName))

	modelRatio := relayInfo.PriceData.ModelRatio
	groupRatio := relayInfo.PriceData.GroupRatioInfo.GroupRatio
	modelPrice := relayInfo.PriceData.ModelPrice
	usePrice := relayInfo.PriceData.UsePrice

	quotaInfo := QuotaInfo{
		InputDetails: TokenDetails{
			TextTokens:  textInputTokens,
			AudioTokens: audioInputTokens,
		},
		OutputDetails: TokenDetails{
			TextTokens:  textOutTokens,
			AudioTokens: audioOutTokens,
		},
		ModelName:  relayInfo.OriginModelName,
		UsePrice:   usePrice,
		ModelRatio: modelRatio,
		GroupRatio: groupRatio,
	}

	quota, clamp := calculateAudioQuota(quotaInfo)
	noteQuotaClamp(relayInfo, clamp)
	if tieredOk {
		quota = tieredQuota
	}

	totalTokens := usage.TotalTokens
	var logContent string
	if !usePrice {
		logContent = fmt.Sprintf("模型倍率 %.2f，补全倍率 %.2f，音频倍率 %.2f，音频补全倍率 %.2f，分组倍率 %.2f",
			modelRatio, completionRatio.InexactFloat64(), audioRatio.InexactFloat64(), audioCompletionRatio.InexactFloat64(), groupRatio)
	} else {
		logContent = fmt.Sprintf("模型价格 %.2f，分组倍率 %.2f", modelPrice, groupRatio)
	}

	// record all the consume log even if quota is 0
	if totalTokens == 0 {
		// in this case, must be some error happened
		// we cannot just return, because we may have to return the pre-consumed quota
		quota = 0
		logContent += "（可能是上游超时）"
		logger.LogError(ctx, fmt.Sprintf("total tokens is 0, cannot consume quota, userId %d, channelId %d, "+
			"tokenId %d, model %s， pre-consumed quota %d", relayInfo.UserId, relayInfo.ChannelId, relayInfo.TokenId, relayInfo.OriginModelName, relayInfo.FinalPreConsumedQuota))
	} else {
		model.UpdateUserUsedQuotaAndRequestCount(relayInfo.UserId, quota)
		model.UpdateChannelUsedQuota(relayInfo.ChannelId, quota)
		if err := model.RecordPublicRelayUsage(relayInfo.ChannelId, quota); err != nil {
			common.SysError("failed to record public relay usage: " + err.Error())
		}
	}

	if err := SettleBilling(ctx, relayInfo, quota); err != nil {
		logger.LogError(ctx, "error settling billing: "+err.Error())
	}

	logModel := relayInfo.OriginModelName
	if extraContent != "" {
		logContent += ", " + extraContent
	}
	other := GenerateAudioOtherInfo(ctx, relayInfo, usage, modelRatio, groupRatio,
		completionRatio.InexactFloat64(), audioRatio.InexactFloat64(), audioCompletionRatio.InexactFloat64(), modelPrice, relayInfo.PriceData.GroupRatioInfo.GroupSpecialRatio)
	if totalTokens == 0 {
		other["upstream_empty_usage"] = true
	}
	if tieredResult != nil {
		InjectTieredBillingInfo(other, relayInfo, tieredResult)
	}
	attachQuotaSaturation(ctx, relayInfo, other)
	model.RecordConsumeLog(ctx, relayInfo.UserId, model.RecordConsumeLogParams{
		ChannelId:        relayInfo.ChannelId,
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		ModelName:        logModel,
		TokenName:        tokenName,
		Quota:            quota,
		Content:          logContent,
		TokenId:          relayInfo.TokenId,
		UseTimeSeconds:   int(useTimeSeconds),
		IsStream:         relayInfo.IsStream,
		Group:            relayInfo.UsingGroup,
		Other:            other,
	})
	gopool.Go(func() {
		perfmetrics.RecordRelaySample(relayInfo, true, int64(usage.CompletionTokens))
	})
}

func PreConsumeTokenQuota(relayInfo *relaycommon.RelayInfo, quota int) error {
	if quota < 0 {
		return errors.New("quota 不能为负数！")
	}
	if relayInfo.IsPlayground || relayInfo.IsAssistant {
		return nil
	}
	// Atomically check and reserve the token quota so concurrent requests
	// cannot all pass a separate balance check before deducting.
	token, err := model.GetRelayBillingToken(relayInfo.TokenId, relayInfo.TokenKey)
	if err != nil {
		return err
	}
	unlimited := relayInfo.TokenUnlimited || token.UnlimitedQuota
	reserved, err := model.TryReserveTokenQuota(relayInfo.TokenId, relayInfo.TokenKey, quota, unlimited)
	if err != nil {
		return err
	}
	if !reserved {
		return fmt.Errorf("token quota is not enough, token remain quota: %s, need quota: %s", logger.FormatQuota(token.RemainQuota), logger.FormatQuota(quota))
	}
	return nil
}

type postConsumeQuotaResult struct {
	FundingApplied bool
	TokenApplied   bool
}

func PostConsumeQuota(relayInfo *relaycommon.RelayInfo, quota int, preConsumedQuota int, sendEmail bool) error {
	_, err := postConsumeQuotaWithResult(relayInfo, quota, preConsumedQuota, sendEmail)
	return err
}

func postConsumeQuotaWithResult(relayInfo *relaycommon.RelayInfo, quota int, preConsumedQuota int, sendEmail bool) (result postConsumeQuotaResult, err error) {

	// 1) Consume from wallet quota OR subscription item
	if relayInfo != nil && relayInfo.BillingSource == BillingSourceSubscription {
		if relayInfo.SubscriptionId == 0 {
			return result, errors.New("subscription id is missing")
		}
		delta := int64(quota)
		if delta != 0 {
			if err := model.PostConsumeUserSubscriptionDelta(relayInfo.SubscriptionId, delta); err != nil {
				return result, err
			}
			relayInfo.SubscriptionPostDelta += delta
		}
	} else {
		// Wallet
		if quota > 0 {
			err = model.DecreaseUserQuota(relayInfo.UserId, quota, false)
		} else {
			err = model.IncreaseUserQuota(relayInfo.UserId, -quota, false)
		}
		if err != nil {
			return result, err
		}
	}
	result.FundingApplied = true

	if !relayInfo.IsPlayground && !relayInfo.IsAssistant {
		if quota > 0 {
			err = model.DecreaseTokenQuota(relayInfo.TokenId, relayInfo.TokenKey, quota)
		} else {
			err = model.IncreaseTokenQuota(relayInfo.TokenId, relayInfo.TokenKey, -quota)
		}
		if err != nil {
			return result, err
		}
		result.TokenApplied = true
	}

	if sendEmail {
		if (quota + preConsumedQuota) != 0 {
			checkAndSendQuotaNotify(relayInfo, quota, preConsumedQuota)
		}
	}

	return result, nil
}

func checkAndSendQuotaNotify(relayInfo *relaycommon.RelayInfo, quota int, preConsumedQuota int) {
	gopool.Go(func() {
		userSetting := relayInfo.UserSetting
		threshold := common.QuotaRemindThreshold
		if userSetting.QuotaWarningThreshold != 0 {
			threshold = int(userSetting.QuotaWarningThreshold)
		}

		//noMoreQuota := userCache.Quota-(quota+preConsumedQuota) <= 0
		quotaTooLow := false
		consumeQuota := quota + preConsumedQuota
		if relayInfo.UserQuota-consumeQuota < threshold {
			quotaTooLow = true
		}
		if quotaTooLow {
			prompt := "您的额度即将用尽"
			topUpLink := PaymentReturnURL("/wallet")

			// 根据通知方式生成不同的内容格式
			var content string
			var values []interface{}

			notifyType := userSetting.NotifyType
			if notifyType == "" {
				notifyType = dto.NotifyTypeEmail
			}

			if notifyType == dto.NotifyTypeBark {
				// Bark推送使用简短文本，不支持HTML
				content = "{{value}}，剩余额度：{{value}}，请及时充值"
				values = []interface{}{prompt, logger.FormatQuota(relayInfo.UserQuota)}
			} else if notifyType == dto.NotifyTypeGotify {
				content = "{{value}}，当前剩余额度为 {{value}}，请及时充值。"
				values = []interface{}{prompt, logger.FormatQuota(relayInfo.UserQuota)}
			} else {
				// 默认内容格式，适用于Email和Webhook（支持HTML）
				content = "{{value}}，当前剩余额度为 {{value}}，为了不影响您的使用，请及时充值。<br/>充值链接：<a href='{{value}}'>{{value}}</a>"
				values = []interface{}{prompt, logger.FormatQuota(relayInfo.UserQuota), topUpLink, topUpLink}
			}

			err := NotifyUser(relayInfo.UserId, relayInfo.UserEmail, relayInfo.UserSetting, dto.NewNotify(dto.NotifyTypeQuotaExceed, prompt, content, values))
			if err != nil {
				common.SysError(fmt.Sprintf("failed to send quota notify to user %d: %s", relayInfo.UserId, err.Error()))
			}
		}
	})
}

func checkAndSendSubscriptionQuotaNotify(relayInfo *relaycommon.RelayInfo) {
	gopool.Go(func() {
		if relayInfo == nil {
			return
		}
		if relayInfo.SubscriptionId == 0 || relayInfo.SubscriptionAmountTotal <= 0 {
			return
		}

		userSetting := relayInfo.UserSetting
		threshold := common.QuotaRemindThreshold
		if userSetting.QuotaWarningThreshold != 0 {
			threshold = int(userSetting.QuotaWarningThreshold)
		}

		usedAfter := relayInfo.SubscriptionAmountUsedAfterPreConsume + relayInfo.SubscriptionPostDelta
		remaining := relayInfo.SubscriptionAmountTotal - usedAfter
		if remaining >= int64(threshold) {
			return
		}

		prompt := "您的订阅额度即将用尽"
		topUpLink := PaymentReturnURL("/wallet")

		var content string
		var values []interface{}
		notifyType := userSetting.NotifyType
		if notifyType == "" {
			notifyType = dto.NotifyTypeEmail
		}

		if notifyType == dto.NotifyTypeBark {
			content = "{{value}}，剩余额度：{{value}}，请及时充值"
			values = []interface{}{prompt, logger.FormatQuota(int(remaining))}
		} else if notifyType == dto.NotifyTypeGotify {
			content = "{{value}}，当前剩余额度为 {{value}}，请及时充值。"
			values = []interface{}{prompt, logger.FormatQuota(int(remaining))}
		} else {
			content = "{{value}}，当前剩余额度为 {{value}}，为了不影响您的使用，请及时充值。<br/>充值链接：<a href='{{value}}'>{{value}}</a>"
			values = []interface{}{prompt, logger.FormatQuota(int(remaining)), topUpLink, topUpLink}
		}

		if err := NotifyUser(relayInfo.UserId, relayInfo.UserEmail, relayInfo.UserSetting, dto.NewNotify(dto.NotifyTypeQuotaExceed, prompt, content, values)); err != nil {
			common.SysError(fmt.Sprintf("failed to send subscription quota notify to user %d: %s", relayInfo.UserId, err.Error()))
		}
	})
}
