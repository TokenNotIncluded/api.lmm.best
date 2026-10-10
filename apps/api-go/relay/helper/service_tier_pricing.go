package helper

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/pkg/servicetier"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/reasoning"
	hosttypes "github.com/LIghtJUNction/api.lmm.best/types"
	"github.com/gin-gonic/gin"
)

func ServiceTierHeaderValues(header http.Header) []string {
	var values []string
	for key, v := range header {
		if strings.EqualFold(key, "OpenAI-Service-Tier") {
			values = append(values, v...)
		}
	}
	return values
}
func requestedServiceTier(c *gin.Context, info *relaycommon.RelayInfo) (string, error) {
	var bodyTier string
	switch req := info.Request.(type) {
	case *dto.OpenAIResponsesRequest:
		if req != nil {
			bodyTier = req.ServiceTier
		}
	case *dto.OpenAIResponsesCompactionRequest:
		if req != nil {
			bodyTier = req.ServiceTier
		}
	case *dto.GeneralOpenAIRequest:
		if req != nil && len(req.ServiceTier) > 0 {
			if err := json.Unmarshal(req.ServiceTier, &bodyTier); err != nil {
				return "", errors.New("service_tier must be a string")
			}
		}
	default:
		return "default", nil
	}
	var headers []string
	if c != nil && c.Request != nil {
		headers = ServiceTierHeaderValues(c.Request.Header)
	}
	return servicetier.Requested(bodyTier, headers)
}
func modelPriceHelperServiceTier(c *gin.Context, info *relaycommon.RelayInfo, promptTokens int, meta *types.TokenCountMeta) (hosttypes.PriceData, bool, error) {
	tier, err := requestedServiceTier(c, info)
	if err != nil {
		return hosttypes.PriceData{}, true, err
	}
	if !servicetier.Accelerated(tier) {
		return hosttypes.PriceData{}, false, nil
	}
	fail := func(err error) (hosttypes.PriceData, bool, error) { return hosttypes.PriceData{}, true, err }
	if info.IsAssistant || info.IsPlayground {
		return fail(errors.New("accelerated pricing requires a normal API token and wallet billing"))
	}
	info.InitChannelMeta(c)
	if info.ChannelType != constant.ChannelTypeOpenAI {
		return fail(errors.New("accelerated pricing currently supports official OpenAI channels only"))
	}
	if !servicetier.OfficialOrigin(strings.TrimRight(info.ChannelBaseUrl, "/")+"/v1/responses", "") {
		return fail(errors.New("accelerated pricing requires the global api.openai.com endpoint"))
	}
	switch info.Request.(type) {
	case *dto.OpenAIResponsesRequest, *dto.GeneralOpenAIRequest:
	default:
		return fail(errors.New("accelerated pricing supports Responses and Chat Completions only"))
	}
	if err := ModelMappedHelper(c, info, nil); err != nil {
		return fail(err)
	}
	_, modelName := reasoning.ParseOpenAIReasoningEffortFromModelSuffix(info.UpstreamModelName)
	policy, catalog, err := setting.ServiceTierPricing()
	if err != nil {
		return fail(err)
	}
	groupInfo := HandleGroupRatio(c, info)
	outputLimit := 0
	if meta != nil {
		outputLimit = meta.MaxTokens
	}
	quote, err := servicetier.NewQuote(catalog, policy, modelName, tier, info.UsingGroup, groupInfo.GroupRatio, outputLimit, time.Now())
	if err != nil {
		return fail(err)
	}
	// Disabling ordinary estimation must not create a zero-input premium reserve.
	// This local byte estimate does not fetch remote media.
	if promptTokens <= 0 && info.Request != nil {
		if full := info.Request.GetTokenCountMeta(); full != nil {
			promptTokens = len(full.CombineText) + full.ToolsCount*8 + full.MessagesCount*3 + 3
		}
	}
	credits, err := common.CreditsPerUSD()
	if err != nil {
		return fail(err)
	}
	usd, err := quote.ReserveUSD(max(promptTokens, 1))
	if err != nil {
		return fail(err)
	}
	reserved, err := servicetier.Credits(usd, credits.String(), common.MaxQuota)
	if err != nil {
		return fail(err)
	}
	info.ServiceTierQuote = quote
	info.ServiceTierCreditsPerUSD = credits.String()
	info.ForcePreConsume = true
	info.TieredBillingSnapshot = nil
	info.BillingRequestInput = nil
	price := hosttypes.PriceData{QuotaToPreConsume: reserved, GroupRatioInfo: groupInfo}
	// Settlement uses the frozen USD tariff, not per-model expressions/discounts.
	info.PriceData = price
	return price, true, nil
}

// ApplyServiceTierToJSON runs after mapping, conversion, field filters and
// parameter/header overrides at the shared final HTTP/WS body boundary.
func ApplyServiceTierToJSON(c *gin.Context, info *relaycommon.RelayInfo, target, host string, headers http.Header, body []byte) ([]byte, error) {
	// A cross-channel retry retains its original quote. Do not let an unsupported
	// retry channel bypass the destination, model and output-budget checks below.
	if info != nil && info.ServiceTierQuote != nil && (info.ChannelMeta == nil || info.ChannelType != constant.ChannelTypeOpenAI) {
		return nil, errors.New("accelerated quote requires an official OpenAI channel")
	}
	if info == nil || info.ChannelMeta == nil || info.ChannelType != constant.ChannelTypeOpenAI {
		return body, nil
	}
	quote := info.ServiceTierQuote
	official := servicetier.OfficialOrigin(target, host)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return nil, errors.New("service-tier guard requires a JSON object")
	}
	var bodyTier string
	if raw, ok := fields["service_tier"]; ok {
		if err := json.Unmarshal(raw, &bodyTier); err != nil {
			return nil, errors.New("service_tier must be a string")
		}
	}
	tier, err := servicetier.Requested(bodyTier, ServiceTierHeaderValues(headers))
	if err != nil {
		return nil, err
	}
	if quote == nil {
		if servicetier.Accelerated(tier) {
			return nil, errors.New("accelerated request has no authorized pricing reservation")
		}
		if official && tier == "default" {
			fields["service_tier"] = json.RawMessage(`"default"`)
		} else {
			return body, nil
		}
	} else {
		if !official {
			return nil, errors.New("accelerated quote cannot be sent to an unverified endpoint")
		}
		policy, _, err := setting.ServiceTierPricing()
		if err != nil {
			return nil, err
		}
		if info.UsingGroup != quote.Group || !policy.Allows(quote.RequestedTier, info.UsingGroup) {
			return nil, errors.New("accelerated group authorization changed before send")
		}
		var modelName string
		if err := json.Unmarshal(fields["model"], &modelName); err != nil {
			return nil, errors.New("missing outbound model")
		}
		_, modelName = reasoning.ParseOpenAIReasoningEffortFromModelSuffix(modelName)
		if modelName != quote.Model {
			return nil, errors.New("outbound model differs from the accelerated price quote")
		}
		// Restore a filtered tier only when it was already authorized and reserved.
		if bodyTier != "" && tier != quote.RequestedTier {
			return nil, errors.New("outbound service tier differs from the price quote")
		}
		if len(ServiceTierHeaderValues(headers)) > 0 && tier != quote.RequestedTier {
			return nil, errors.New("outbound header changes the service tier")
		}
		fields["service_tier"], _ = json.Marshal(quote.RequestedTier)
		if raw, ok := fields["background"]; ok && string(raw) != "false" && string(raw) != "null" {
			return nil, errors.New("background mode has no synchronous accelerated settlement")
		}
		for _, name := range []string{"audio", "modalities"} {
			if raw := fields[name]; len(raw) > 0 && string(raw) != "null" {
				return nil, errors.New("accelerated audio/modalities pricing is not configured")
			}
		}
		// Hosted tools have separate fees. Admit only client-executed tools until
		// their provider cost dimensions are represented by a verified tariff.
		var tools []struct {
			Type string `json:"type"`
		}
		if raw, ok := fields["tools"]; ok && string(raw) != "null" {
			if err := json.Unmarshal(raw, &tools); err != nil {
				return nil, err
			}
		}
		for _, tool := range tools {
			if tool.Type != "function" && tool.Type != "custom" {
				return nil, fmt.Errorf("accelerated price for hosted tool %q is not configured", tool.Type)
			}
		}
		if raw, ok := fields["n"]; ok && string(raw) != "1" {
			return nil, errors.New("accelerated requests require n=1")
		}
		outputField := "max_output_tokens"
		if strings.Contains(target, "/chat/completions") {
			outputField = "max_completion_tokens"
		}
		for _, key := range []string{"max_output_tokens", "max_completion_tokens", "max_tokens"} {
			if raw, ok := fields[key]; ok && string(raw) != "null" {
				var n int
				if err := json.Unmarshal(raw, &n); err != nil || n <= 0 || n > quote.OutputLimit {
					return nil, errors.New("outbound output limit exceeds the reserved accelerated budget")
				}
				outputField = key
			}
		}
		// Enforce the existing 8192-token fallback as an actual output cap.
		if raw := fields[outputField]; len(raw) == 0 || string(raw) == "null" {
			fields[outputField], _ = json.Marshal(quote.OutputLimit)
		}
	}
	// Collapse duplicate keys so two decoders cannot see different tier/model values.
	return json.Marshal(fields)
}
func ApplyServiceTierToRequest(c *gin.Context, req *http.Request, info *relaycommon.RelayInfo) error {
	if info != nil && info.ServiceTierQuote != nil && (info.ChannelMeta == nil || info.ChannelType != constant.ChannelTypeOpenAI) {
		return errors.New("accelerated quote requires an official OpenAI channel")
	}
	if req == nil || req.URL == nil || req.Body == nil || req.Method != http.MethodPost || info == nil || info.ChannelMeta == nil || info.ChannelType != constant.ChannelTypeOpenAI {
		return nil
	}
	path := req.URL.EscapedPath()
	if path != "/v1/responses" && path != "/v1/chat/completions" {
		if info.ServiceTierQuote != nil || len(ServiceTierHeaderValues(req.Header)) > 0 {
			return errors.New("accelerated pricing does not support this endpoint")
		}
		return nil
	}
	oldBody := req.Body
	body, err := io.ReadAll(oldBody)
	if err != nil {
		_ = oldBody.Close()
		return err
	}
	patched, err := ApplyServiceTierToJSON(c, info, req.URL.String(), req.Host, req.Header, body)
	_ = oldBody.Close()
	if err != nil {
		return err
	}
	req.Body = io.NopCloser(bytes.NewReader(patched))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(patched)), nil }
	req.ContentLength = int64(len(patched))
	req.Header.Del("Content-Length")
	return nil
}
