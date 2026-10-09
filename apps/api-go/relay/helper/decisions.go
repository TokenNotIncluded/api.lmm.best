package helper

import (
	"errors"
	"fmt"
	"math"

	"github.com/LIghtJUNction/api.lmm.best/common"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/setting/billing_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	hosttypes "github.com/LIghtJUNction/api.lmm.best/types"
	"github.com/gin-gonic/gin"
)

const decisionsPriceGroupContextKey = "decisions.price_group"

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
// Regional/long-context uplifts must be included in the configured tariff.
// There is no fallback to a same-named chat model, per-call price or expression.
func ModelPriceDecisions(c *gin.Context, info *relaycommon.RelayInfo, promptTokens int) (hosttypes.PriceData, error) {
	key := DecisionsPriceKey(info.OriginModelName)
	if _, fixed := ratio_setting.GetModelPrice(key, false); fixed || billing_setting.GetBillingMode(key) == billing_setting.BillingModeTieredExpr {
		return hosttypes.PriceData{}, fmt.Errorf("%s requires an explicit input-token ratio in this Decisions implementation", key)
	}
	ratio, found, matched := ratio_setting.GetModelRatio(key)
	if !found || matched != key {
		return hosttypes.PriceData{}, modelPriceNotConfiguredError(key, info.UserId)
	}
	if math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio < 0 {
		return hosttypes.PriceData{}, errors.New("invalid Decisions input-token ratio")
	}
	group := HandleGroupRatio(c, info)
	if math.IsNaN(group.GroupRatio) || math.IsInf(group.GroupRatio, 0) || group.GroupRatio < 0 {
		return hosttypes.PriceData{}, errors.New("invalid Decisions group ratio")
	}
	// Keep the existing minimum input reservation even when counting is disabled.
	// No generated-output allowance is added to this input-only endpoint.
	reservedTokens := common.Max(promptTokens, common.PreConsumedQuota)
	reservedTokens = common.Max(reservedTokens, 1)
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
