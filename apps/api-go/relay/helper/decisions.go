package helper

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/pkg/billingexpr"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/setting/billing_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	hosttypes "github.com/LIghtJUNction/api.lmm.best/types"
	"github.com/gin-gonic/gin"
)

const decisionsPriceGroupContextKey = "decisions.price_group"

// This is synthetic billing metadata, never a client-controlled routing header.
const DecisionsBillingHostHeader = "x-lmm-billing-upstream-host"

// DecisionsPriceKey never changes the upstream model or its authorization name.
func DecisionsPriceKey(model string) string { return model + "@decisions" }

func GetAndValidateDecisionsRequest(c *gin.Context) (*dto.DecisionsRequest, error) {
	request := &dto.DecisionsRequest{}
	if err := common.UnmarshalBodyReusable(c, request); err != nil {
		return nil, err
	}
	return request, nil
}

// ModelPriceDecisions freezes a separate, explicitly configured input tariff.
// Conditional tariffs use the existing expression configuration and settlement
// snapshot. No vendor rates, thresholds or regional multipliers live in code.
// There is no fallback to a same-named chat model or per-call price.
func ModelPriceDecisions(c *gin.Context, info *relaycommon.RelayInfo, promptTokens int) (hosttypes.PriceData, error) {
	key := DecisionsPriceKey(info.OriginModelName)
	group := HandleGroupRatio(c, info)
	if math.IsNaN(group.GroupRatio) || math.IsInf(group.GroupRatio, 0) || group.GroupRatio < 0 {
		return hosttypes.PriceData{}, errors.New("invalid Decisions group ratio")
	}
	reservedTokens := common.Max(common.Max(promptTokens, common.PreConsumedQuota), 1)
	if billing_setting.GetBillingMode(key) == billing_setting.BillingModeTieredExpr {
		price, err := modelPriceHelperTieredForKey(c, info, reservedTokens, &types.TokenCountMeta{}, group, key)
		if err != nil {
			return hosttypes.PriceData{}, err
		}
		if info.TieredBillingSnapshot.EstimatedQuotaBeforeGroup > 0 && group.GroupRatio > 0 && price.QuotaToPreConsume < 1 {
			price.QuotaToPreConsume = 1
			info.TieredBillingSnapshot.EstimatedQuotaAfterGroup = 1
		}
		info.PriceData = price
		info.ForcePreConsume = true
		c.Set(decisionsPriceGroupContextKey, info.UsingGroup)
		return price, nil
	}
	if _, fixed := ratio_setting.GetModelPrice(key, false); fixed {
		return hosttypes.PriceData{}, fmt.Errorf("%s requires input-token pricing, not a per-call price", key)
	}
	ratio, found := ratio_setting.GetConfiguredModelRatio(key)
	if !found {
		return hosttypes.PriceData{}, modelPriceNotConfiguredError(key, info.UserId)
	}
	if math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio < 0 {
		return hosttypes.PriceData{}, errors.New("invalid Decisions input-token ratio")
	}
	// Keep the existing minimum input reservation even when counting is disabled.
	// No generated-output allowance is added to this input-only endpoint.
	quota, err := common.QuotaFromFloatStrict(float64(reservedTokens) * ratio * group.GroupRatio)
	if err != nil {
		return hosttypes.PriceData{}, err
	}
	free := ratio == 0 || group.GroupRatio == 0
	if !free && quota < 1 {
		quota = 1
	}
	price := hosttypes.PriceData{
		ModelRatio: ratio, CompletionRatio: 0, GroupRatioInfo: group,
		CacheRatio: 0, CacheCreationRatio: 0, CacheCreation5mRatio: 0,
		CacheCreation1hRatio: 0, ImageRatio: 1,
		FreeModel: free, QuotaToPreConsume: quota,
	}
	info.PriceData = price
	info.ForcePreConsume = true
	info.TieredBillingSnapshot = nil
	info.BillingRequestInput = nil
	c.Set(decisionsPriceGroupContextKey, info.UsingGroup)
	return price, nil
}

func setDecisionsBillingHost(input *billingexpr.RequestInput, baseURL string) error {
	host := "api.openai.com" // The OpenAI adaptor's default endpoint, not a tariff.
	if strings.TrimSpace(baseURL) != "" {
		endpoint, err := url.Parse(baseURL)
		if err != nil || endpoint.Hostname() == "" || (endpoint.Scheme != "https" && endpoint.Scheme != "http") {
			return errors.New("invalid Decisions channel base URL for billing")
		}
		host = strings.ToLower(endpoint.Hostname())
	}
	if input.Headers == nil {
		input.Headers = make(map[string]string)
	}
	// Header names are case insensitive. Remove every spoofed alias first.
	for key := range input.Headers {
		if strings.EqualFold(strings.TrimSpace(key), DecisionsBillingHostHeader) {
			delete(input.Headers, key)
		}
	}
	input.Headers[DecisionsBillingHostHeader] = host
	return nil
}

// RefreshDecisionsChannelPrice handles a retry selecting another processing
// host. Keep the expression frozen, but reserve the newly selected host's
// configured estimate before its request is sent.
func RefreshDecisionsChannelPrice(info *relaycommon.RelayInfo) error {
	snap := info.TieredBillingSnapshot
	if snap == nil {
		return nil
	}
	input := billingexpr.RequestInput{}
	if info.BillingRequestInput != nil {
		input = cloneRequestInput(*info.BillingRequestInput, true)
	}
	if err := setDecisionsBillingHost(&input, info.ChannelBaseUrl); err != nil {
		return err
	}
	zero := float64(0)
	params := billingexpr.TokenParams{P: float64(snap.EstimatedPromptTokens), Len: float64(snap.EstimatedPromptTokens), CRText: &zero, CRImg: &zero, CRAudio: &zero}
	cost, trace, err := billingexpr.RunExprWithRequest(snap.ExprString, params, input)
	if err != nil {
		return err
	}
	before := cost / 1_000_000 * snap.QuotaPerUnit
	quota, err := billingexpr.QuotaRoundStrict(before * snap.GroupRatio)
	if err != nil {
		return err
	}
	if before > 0 && snap.GroupRatio > 0 && quota < 1 {
		quota = 1
	}
	snap.EstimatedQuotaBeforeGroup, snap.EstimatedQuotaAfterGroup, snap.EstimatedTier = before, quota, trace.MatchedTier
	info.PriceData.QuotaToPreConsume = quota
	info.BillingRequestInput = &input
	return nil
}

// A group change cannot reuse a price frozen for another group.
func ValidateDecisionsPriceGroup(c *gin.Context, info *relaycommon.RelayInfo) error {
	value, exists := c.Get(decisionsPriceGroupContextKey)
	group, ok := value.(string)
	if !exists || !ok || group != info.UsingGroup {
		return errors.New("Decisions billing group changed; issue a new request for the selected group")
	}

	if value, exists := c.Get("auto_group"); exists {
		selected, ok := value.(string)
		if !ok || selected != group {
			return errors.New("Decisions automatic group changed after price reservation")
		}
	}
	return nil
}
