package service

import (
	"fmt"
	"math"
	"net/http"

	"github.com/LIghtJUNction/api.lmm.best/pkg/billingexpr"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/gin-gonic/gin"
)

// TieredResultWrapper wraps billingexpr.TieredResult for use at the service layer.
type TieredResultWrapper = billingexpr.TieredResult

// BuildTieredTokenParams constructs billingexpr.TokenParams from a dto.Usage,
// normalizing P and C so they mean "tokens not separately priced by the
// expression". Sub-categories (cache, image, audio) are only subtracted
// when the expression references them via their own variable.
//
// GPT-format APIs report prompt_tokens / completion_tokens as totals that
// include all sub-categories (cache, image, audio). Claude-format APIs
// report them as text-only. This function normalizes to text-only when
// sub-categories are separately priced.
func BuildTieredTokenParams(usage *dto.Usage, isClaudeUsageSemantic bool, usedVars map[string]bool) billingexpr.TokenParams {
	if usage == nil {
		return billingexpr.TokenParams{CacheClassificationStatus: dto.CacheReadDetailsUnknown}
	}
	p := float64(usage.PromptTokens)
	c := float64(usage.CompletionTokens)
	cr := float64(usage.PromptTokensDetails.CachedTokens)
	cacheCreation5m := usage.PromptTokensDetails.CacheCreationTokensTotal()
	cacheCreation1h := 0

	if usage.UsageSemantic == "anthropic" {
		cacheCreation1h = usage.ClaudeCacheCreation1hTokens
		cacheCreation5m = usage.ClaudeCacheCreation5mTokens
	}
	cc5m, cc1h := float64(cacheCreation5m), float64(cacheCreation1h)

	img := float64(usage.PromptTokensDetails.ImageTokens)
	ai := float64(usage.PromptTokensDetails.AudioTokens)
	imgO := float64(usage.CompletionTokenDetails.ImageTokens)
	ao := float64(usage.CompletionTokenDetails.AudioTokens)

	// len = total input context length for tier condition evaluation.
	// Non-Claude: prompt_tokens already includes everything.
	// Claude: input_tokens is text-only, so add cache read + cache creation.
	inputLen := p
	if isClaudeUsageSemantic {
		inputLen = p + cr + cc5m + cc1h
	}

	if !isClaudeUsageSemantic {
		if usedVars["cr"] {
			p -= cr
		}
		if usedVars["cc"] {
			p -= cc5m
		}
		if usedVars["cc1h"] {
			p -= cc1h
		}
		if usedVars["img"] {
			p -= img
		}
		if usedVars["ai"] {
			p -= ai
		}
		if usedVars["img_o"] {
			c -= imgO
		}
		if usedVars["ao"] {
			c -= ao
		}
	}

	// OpenAI cache-write usage reports unadjusted prefix counts, so cr + cc can
	// exceed the prompt and drive the remainder negative. Clamp at zero.
	if p < 0 {
		p = 0
	}
	if c < 0 {
		c = 0
	}

	params := billingexpr.TokenParams{
		P:    p,
		C:    c,
		Len:  inputLen,
		CR:   cr,
		CC:   cc5m,
		CC1h: cc1h,
		Img:  img,
		ImgO: imgO,
		AI:   ai,
		AO:   ao,
	}
	if usage.AudioSeconds != nil {
		seconds := *usage.AudioSeconds
		params.AudioSeconds = &seconds
	}

	cacheInputLimit := usage.PromptTokens
	if isClaudeUsageSemantic {
		// Claude prompt tokens exclude cache reads and writes. Validate cache
		// classifications against the inclusive input total without overflow.
		for _, count := range []int{usage.PromptTokensDetails.CachedTokens, cacheCreation5m, cacheCreation1h} {
			if count < 0 || cacheInputLimit < 0 || count > math.MaxInt-cacheInputLimit {
				cacheInputLimit = -1
				break
			}
			cacheInputLimit += count
		}
	}
	cached, status := usage.PromptTokensDetails.ValidatedCachedTokenDetails(cacheInputLimit)
	params.CacheClassificationStatus = status
	if status == dto.CacheReadDetailsReported || (status == dto.CacheReadDetailsUnknown && usage.PromptTokensDetails.CachedTokens == 0 && cacheInputLimit >= 0) {
		// A reported aggregate zero proves each cache cost is zero, while the
		// upstream classification itself remains unknown for logging purposes.
		text, image, audio := float64(cached.TextTokens), float64(cached.ImageTokens), float64(cached.AudioTokens)
		params.CRText, params.CRImg, params.CRAudio = &text, &image, &audio
	}
	classified := usedVars["cr_text"] || usedVars["cr_img"] || usedVars["cr_audio"]
	if !classified {
		return params // Preserve all existing aggregate-cache expression semantics.
	}
	if params.CRText == nil || params.CRImg == nil || params.CRAudio == nil {
		params.MeasurementError = "cache classification is " + status
		return params
	}

	// Classified cache and media dimensions can overlap. Subtract their
	// union once from p, and let each media variable retain cached tokens only
	// when its cache modality is not priced separately by the expression.
	cacheExcluded := float64(0)
	if usedVars["cr"] {
		cacheExcluded = cr
	} else {
		if usedVars["cr_text"] {
			cacheExcluded += *params.CRText
		}
		if usedVars["cr_img"] {
			cacheExcluded += *params.CRImg
		}
		if usedVars["cr_audio"] {
			cacheExcluded += *params.CRAudio
		}
	}
	if usedVars["cr"] || usedVars["cr_img"] {
		params.Img -= *params.CRImg
	}
	if usedVars["cr"] || usedVars["cr_audio"] {
		params.AI -= *params.CRAudio
	}
	if !isClaudeUsageSemantic {
		params.P = float64(usage.PromptTokens) - cacheExcluded
		if usedVars["cc"] {
			params.P -= cc5m
		}
		if usedVars["cc1h"] {
			params.P -= cc1h
		}
		if usedVars["img"] {
			params.P -= params.Img
		}
		if usedVars["ai"] {
			params.P -= params.AI
		}
	}
	if params.P < 0 || params.Img < 0 || params.AI < 0 {
		params.MeasurementError = "classified input token dimensions exceed their inclusive total"
	}
	return params
}

func refreshTieredBillingGroup(relayInfo *relaycommon.RelayInfo) (*billingexpr.BillingSnapshot, error) {
	if relayInfo == nil {
		return nil, nil
	}
	snap := relayInfo.TieredBillingSnapshot
	if snap == nil || snap.BillingMode != "tiered_expr" {
		return nil, nil
	}

	groupRatio := relayInfo.PriceData.GroupRatioInfo.GroupRatio
	// A retry may select a different fixed group; retain the frozen expression base.
	estimatedQuotaAfterGroup := snap.EstimatedQuotaBeforeGroup * groupRatio
	estimatedQuota, err := billingexpr.QuotaRoundStrict(estimatedQuotaAfterGroup)
	if err != nil {
		return nil, err
	}
	snap.GroupRatio = groupRatio
	snap.EstimatedQuotaAfterGroup = estimatedQuota
	return snap, nil
}

// PrepareTieredBillingForSelectedGroup refreshes routing-dependent billing
// state before an upstream attempt. An existing session reserves any higher
// estimate before sending. If the initial group was free and skipped
// pre-consume, switching to a paid group creates the session at that point.
func PrepareTieredBillingForSelectedGroup(c *gin.Context, relayInfo *relaycommon.RelayInfo) *types.NewAPIError {
	snap, err := refreshTieredBillingGroup(relayInfo)
	if err != nil {
		return types.NewErrorWithStatusCode(
			err,
			types.ErrorCodeModelPriceError,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}
	if snap == nil {
		return nil
	}
	if snap.GroupRatio == 0 {
		// Paid-to-free keeps FreeModel as-is: FreeModel means "pre-consume was
		// skipped", which is not true once a session exists, and settlement
		// already yields 0 for a zero group ratio.
		return nil
	}

	// The selected group is paid; clear a FreeModel flag frozen when the
	// initial group was free so downstream state stays consistent.
	relayInfo.PriceData.FreeModel = false

	if relayInfo.Billing == nil {
		return PreConsumeBilling(c, snap.EstimatedQuotaAfterGroup, relayInfo)
	}
	if err := relayInfo.Billing.Reserve(snap.EstimatedQuotaAfterGroup); err != nil {
		return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
	}
	relayInfo.FinalPreConsumedQuota = relayInfo.Billing.GetPreConsumedQuota()
	return nil
}

// The funding reservation may use only the remaining subscription grant.
// Missing-usage fallback still needs the original request-side estimate.
func estimatedBillingQuotaCap(info *relaycommon.RelayInfo) int {
	if info == nil {
		return 0
	}
	cap := info.FinalPreConsumedQuota
	if snap := info.TieredBillingSnapshot; snap != nil && snap.BillingMode == "tiered_expr" {
		if snap.EstimatedQuotaAfterGroup > cap {
			cap = snap.EstimatedQuotaAfterGroup
		}
		return cap
	}
	if info.BillingSource == BillingSourceSubscription && info.PriceData.QuotaToPreConsume > cap {
		cap = info.PriceData.QuotaToPreConsume
	}
	return cap
}

// TryTieredSettle checks if the request uses tiered_expr billing and, if so,
// computes the actual quota using the captured BillingSnapshot. Returns:
//   - ok=true, quota, result  when tiered billing applies
//   - ok=false, 0, nil        when it doesn't (caller should fall through to existing logic)
func TryTieredSettle(relayInfo *relaycommon.RelayInfo, params billingexpr.TokenParams) (ok bool, quota int, result *billingexpr.TieredResult) {
	ok, quota, result, err := TryTieredSettleWithError(relayInfo, params)
	if err != nil {
		// Legacy callers retain their existing reservation fallback. New
		// measured-dimension callers can use the error-returning variant to
		// distinguish unavailable usage from an actual zero or a matched tier.
		return true, estimatedBillingQuotaCap(relayInfo), nil
	}
	return ok, quota, result
}

// TryTieredSettleWithError exposes measurement/expression failures instead of
// silently pricing a known duration or malformed cache breakdown at the frozen
// pre-consume estimate. The caller owns the explicit failure/fallback policy.
func TryTieredSettleWithError(relayInfo *relaycommon.RelayInfo, params billingexpr.TokenParams) (ok bool, quota int, result *billingexpr.TieredResult, err error) {
	if relayInfo == nil {
		return false, 0, nil, nil
	}
	snap := relayInfo.TieredBillingSnapshot
	if snap == nil || snap.BillingMode != "tiered_expr" {
		return false, 0, nil, nil
	}

	requestInput := billingexpr.RequestInput{}
	if relayInfo.BillingRequestInput != nil {
		requestInput = *relayInfo.BillingRequestInput
	}

	tr, err := billingexpr.ComputeTieredQuotaWithRequest(snap, params, requestInput)
	if err != nil {
		return true, 0, nil, fmt.Errorf("tiered usage settlement failed: %w", err)
	}

	// Surface any single-request saturation from settlement onto RelayInfo so the
	// consume log records it under admin_info, regardless of which caller
	// (text, audio, WSS) consumes the returned quota. First non-nil wins.
	noteQuotaClamp(relayInfo, tr.Clamp)

	quota = tr.ActualQuotaAfterGroup
	quota = enforceTieredMinimumQuota(quota, &tr, snap.GroupRatio)

	return true, quota, &tr, nil
}

// enforceTieredMinimumQuota keeps a successful, positive-price tiered request
// from becoming free solely because quota rounding discarded a sub-quota unit.
// Free groups and zero/failed settlements intentionally retain their zero value.
func enforceTieredMinimumQuota(quota int, result *billingexpr.TieredResult, groupRatio float64) int {
	if quota == 0 && result != nil && result.ActualQuotaBeforeGroup > 0 && groupRatio > 0 {
		return 1
	}
	return quota
}
