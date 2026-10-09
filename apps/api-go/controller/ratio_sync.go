package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/logger"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/pkg/billingexpr"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/setting/billing_setting"
	"github.com/samber/lo"

	"github.com/gin-gonic/gin"
)

const (
	defaultTimeoutSeconds      = 10
	defaultEndpoint            = "/api/pricing"
	maxConcurrentFetches       = 8
	maxRatioConfigBytes        = 10 << 20 // 10MB
	officialRatioPresetID      = -100
	officialRatioPresetName    = "官方倍率预设"
	officialRatioPresetBaseURL = "https://basellm.github.io"
	modelsDevPresetID          = -101
	modelsDevPresetName        = "models.dev 价格预设"
	modelsDevPresetBaseURL     = "https://models.dev"
	modelsDevHost              = "models.dev"
	modelsDevPath              = "/api.json"
)

func nearlyEqual(a, b float64) bool {
	if a == b {
		return true
	}
	if a == 0 || b == 0 {
		return false
	}
	// Tolerate only float representation noise, not an absolute money cutoff.
	// Even the smallest positive rate differs from a free quote.
	scale := math.Max(math.Abs(a), math.Abs(b))
	return math.Abs(a-b) <= 4*(scale-math.Nextafter(scale, math.Inf(-1)))
}

func valuesEqual(a, b interface{}) bool {
	af, aok := a.(float64)
	bf, bok := b.(float64)
	if aok && bok {
		return nearlyEqual(af, bf)
	}
	return a == b
}

func pricingSyncValuesEqual(field string, a, b any) bool {
	if field == billing_setting.BillingExprField {
		left, lok := a.(string)
		right, rok := b.(string)
		if lok && rok {
			return billingexpr.PricingEquivalent(left, right)
		}
	}
	return valuesEqual(a, b)
}

var pricingSyncFields = []string{
	"model_ratio",
	"completion_ratio",
	"cache_ratio",
	"create_cache_ratio",
	"image_ratio",
	"audio_ratio",
	"audio_completion_ratio",
	"model_price",
	billing_setting.BillingModeField,
	billing_setting.BillingExprField,
}

var numericPricingSyncFields = map[string]bool{
	"model_ratio":            true,
	"completion_ratio":       true,
	"cache_ratio":            true,
	"create_cache_ratio":     true,
	"image_ratio":            true,
	"audio_ratio":            true,
	"audio_completion_ratio": true,
	"model_price":            true,
}

type upstreamResult struct {
	Name string         `json:"name"`
	Data map[string]any `json:"data,omitempty"`
	Err  string         `json:"err,omitempty"`
}

func valueMap(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return typed
	case map[string]float64:
		return lo.MapValues(typed, func(value float64, _ string) any { return value })
	case map[string]string:
		return lo.MapValues(typed, func(value string, _ string) any { return value })
	default:
		return nil
	}
}

func asFloat64(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func normalizeSyncValue(field string, value any) any {
	if numericPricingSyncFields[field] {
		if parsed, ok := asFloat64(value); ok {
			return parsed
		}
	}
	return value
}

func FetchUpstreamRatios(c *gin.Context) {
	var req dto.UpstreamRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.SysError("failed to bind upstream request: " + err.Error())
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求参数格式错误"})
		return
	}

	pricingConfig, err := model.GetUSDPriceConfig()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "pricing currency units are unavailable"})
		return
	}
	localData, err := pricingSyncDataFromConfig(pricingConfig)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": err.Error()})
		return
	}

	if req.Timeout <= 0 {
		req.Timeout = defaultTimeoutSeconds
	}

	var upstreams []dto.UpstreamDTO

	if len(req.Upstreams) > 0 {
		for _, u := range req.Upstreams {
			if strings.HasPrefix(u.BaseURL, "http") {
				if u.Endpoint == "" {
					u.Endpoint = defaultEndpoint
				}
				u.BaseURL = strings.TrimRight(u.BaseURL, "/")
				upstreams = append(upstreams, u)
			}
		}
	} else if len(req.ChannelIDs) > 0 {
		intIds := make([]int, 0, len(req.ChannelIDs))
		for _, id64 := range req.ChannelIDs {
			intIds = append(intIds, int(id64))
		}
		dbChannels, err := model.GetChannelsByIds(intIds)
		if err != nil {
			logger.LogError(c.Request.Context(), "failed to query channels: "+err.Error())
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "查询渠道失败"})
			return
		}
		for _, ch := range dbChannels {
			if base := ch.GetBaseURL(); strings.HasPrefix(base, "http") {
				upstreams = append(upstreams, dto.UpstreamDTO{
					ID:       ch.Id,
					Name:     ch.Name,
					BaseURL:  strings.TrimRight(base, "/"),
					Endpoint: "",
				})
			}
		}
	}

	if len(upstreams) == 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "无有效上游渠道"})
		return
	}

	var wg sync.WaitGroup
	ch := make(chan upstreamResult, len(upstreams))

	sem := make(chan struct{}, maxConcurrentFetches)

	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport := &http.Transport{MaxIdleConns: 100, IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 10 * time.Second, ExpectContinueTimeout: 1 * time.Second, ResponseHeaderTimeout: 10 * time.Second}
	if common.TLSInsecureSkipVerify {
		transport.TLSClientConfig = common.InsecureTLSConfig.Clone()
	}
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			host = addr
		}
		// 对 github.io 优先尝试 IPv4，失败则回退 IPv6
		if strings.HasSuffix(host, "github.io") {
			if conn, err := dialer.DialContext(ctx, "tcp4", addr); err == nil {
				return conn, nil
			}
			return dialer.DialContext(ctx, "tcp6", addr)
		}
		return dialer.DialContext(ctx, network, addr)
	}
	client := &http.Client{Transport: transport}
	defer transport.CloseIdleConnections()

	for _, chn := range upstreams {
		wg.Add(1)
		go func(chItem dto.UpstreamDTO) {
			defer wg.Done()

			uniqueName := chItem.Name
			if chItem.ID != 0 {
				uniqueName = fmt.Sprintf("%s(%d)", chItem.Name, chItem.ID)
			}
			parent := c.Request.Context()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-parent.Done():
				ch <- upstreamResult{Name: uniqueName, Err: "request cancelled"}
				return
			}
			if parent.Err() != nil {
				ch <- upstreamResult{Name: uniqueName, Err: "request cancelled"}
				return
			}
			// Queueing is governed by the caller; each acquired slot gets its own request budget.
			ctx, cancel := context.WithTimeout(parent, time.Duration(req.Timeout)*time.Second)
			defer cancel()

			isOpenRouter := chItem.Endpoint == "openrouter"

			endpoint := chItem.Endpoint
			var fullURL string
			if isOpenRouter {
				fullURL = chItem.BaseURL + "/v1/models"
			} else if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
				fullURL = endpoint
			} else {
				if endpoint == "" {
					endpoint = defaultEndpoint
				} else if !strings.HasPrefix(endpoint, "/") {
					endpoint = "/" + endpoint
				}
				fullURL = chItem.BaseURL + endpoint
			}
			isModelsDev := isModelsDevAPIEndpoint(fullURL)
			var sourceProvider string
			if isModelsDev {
				parsed, err := url.Parse(fullURL)
				if err != nil || len(parsed.Query()["provider"]) != 1 || strings.TrimSpace(parsed.Query().Get("provider")) == "" {
					ch <- upstreamResult{Name: uniqueName, Err: "models.dev requires an explicit provider query parameter"}
					return
				}
				sourceProvider = strings.TrimSpace(parsed.Query().Get("provider"))
			}

			httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
			if err != nil {
				logger.LogWarn(c.Request.Context(), "build request failed: "+err.Error())
				ch <- upstreamResult{Name: uniqueName, Err: err.Error()}
				return
			}

			// OpenRouter requires Bearer token auth
			if isOpenRouter && chItem.ID != 0 {
				dbCh, err := model.GetChannelById(chItem.ID, true)
				if err != nil {
					ch <- upstreamResult{Name: uniqueName, Err: "failed to get channel key: " + err.Error()}
					return
				}
				key, _, apiErr := dbCh.GetNextEnabledKey()
				if apiErr != nil {
					ch <- upstreamResult{Name: uniqueName, Err: "failed to get enabled channel key: " + apiErr.Error()}
					return
				}
				if strings.TrimSpace(key) == "" {
					ch <- upstreamResult{Name: uniqueName, Err: "no API key configured for this channel"}
					return
				}
				httpReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(key))
			} else if isOpenRouter {
				ch <- upstreamResult{Name: uniqueName, Err: "OpenRouter requires a valid channel with API key"}
				return
			}

			// 简单重试：最多 3 次，指数退避
			var resp *http.Response
			var lastErr error
			for attempt := 0; attempt < 3; attempt++ {
				// Check if request context was cancelled before retry
				if ctx.Err() != nil {
					logger.LogWarn(c.Request.Context(), fmt.Sprintf("request cancelled before attempt %d on %s", attempt+1, chItem.Name))
					ch <- upstreamResult{Name: uniqueName, Err: "request cancelled"}
					return
				}

				resp, lastErr = client.Do(httpReq)
				if lastErr == nil {
					break
				}

				// Don't retry on cancellation
				if ctx.Err() != nil {
					logger.LogWarn(c.Request.Context(), "request cancelled during retry on "+chItem.Name)
					ch <- upstreamResult{Name: uniqueName, Err: "request cancelled"}
					return
				}

				if attempt == 2 {
					break
				}
				backoff := time.NewTimer(time.Duration(200*(1<<attempt)) * time.Millisecond)
				select {
				case <-ctx.Done():
					backoff.Stop()
					logger.LogWarn(c.Request.Context(), "request cancelled during retry backoff on "+chItem.Name)
					ch <- upstreamResult{Name: uniqueName, Err: "request cancelled"}
					return
				case <-backoff.C:
				}
			}
			if lastErr != nil {
				logger.LogWarn(c.Request.Context(), "http error on "+chItem.Name+": "+lastErr.Error())
				ch <- upstreamResult{Name: uniqueName, Err: lastErr.Error()}
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				logger.LogWarn(c.Request.Context(), "non-200 from "+chItem.Name+": "+resp.Status)
				ch <- upstreamResult{Name: uniqueName, Err: resp.Status}
				return
			}

			// Content-Type 和响应体大小校验
			if ct := resp.Header.Get("Content-Type"); ct != "" && !strings.Contains(strings.ToLower(ct), "application/json") {
				logger.LogWarn(c.Request.Context(), "unexpected content-type from "+chItem.Name+": "+ct)
			}
			bodyBytes, err := common.ReadAllLimit(resp.Body, maxRatioConfigBytes)
			if err != nil {
				logger.LogWarn(c.Request.Context(), "read response failed from "+chItem.Name+": "+err.Error())
				ch <- upstreamResult{Name: uniqueName, Err: err.Error()}
				return
			}

			// type3: OpenRouter /v1/models -> convert per-token pricing to ratios
			if isOpenRouter {
				converted, err := convertOpenRouterToRatioDataWithAnchor(bytes.NewReader(bodyBytes), pricingConfig.CreditsPerUSD)
				if err != nil {
					logger.LogWarn(c.Request.Context(), "OpenRouter parse failed from "+chItem.Name+": "+err.Error())
					ch <- upstreamResult{Name: uniqueName, Err: err.Error()}
					return
				}
				ch <- upstreamResult{Name: uniqueName, Data: converted}
				return
			}

			// type4: select one reported provider and retain complete USD billing shapes.
			if isModelsDev {
				converted, skipped, err := convertModelsDevCanonicalData(bytes.NewReader(bodyBytes), sourceProvider, pricingConfig.CreditsPerUSD)
				if err != nil {
					logger.LogWarn(c.Request.Context(), "models.dev parse failed from "+chItem.Name+": "+err.Error())
					ch <- upstreamResult{Name: uniqueName, Err: err.Error()}
					return
				}
				converted[syncSkippedModels] = valueMap(skipped)
				providers := map[string]any{}
				for _, field := range pricingSyncFields {
					for name := range valueMap(converted[field]) {
						providers[name] = sourceProvider
					}
				}
				converted[syncSourceProviders] = providers
				ch <- upstreamResult{Name: uniqueName, Data: converted}
				return
			}

			converted, err := decodeUpstreamPricingData(bodyBytes, pricingConfig.CreditsPerUSD)
			if err != nil {
				ch <- upstreamResult{Name: uniqueName, Err: err.Error()}
				return
			}

			// Final check before sending result - don't queue work if the request was cancelled
			if ctx.Err() != nil {
				logger.LogWarn(c.Request.Context(), "request cancelled before sending result for "+chItem.Name)
				return
			}

			ch <- upstreamResult{Name: uniqueName, Data: converted}
		}(chn)
	}

	wg.Wait()
	close(ch)

	var testResults []dto.TestResult
	var successfulChannels []struct {
		name string
		data map[string]any
	}

	for r := range ch {
		if r.Err != "" {
			testResults = append(testResults, dto.TestResult{
				Name:   r.Name,
				Status: "error",
				Error:  r.Err,
			})
		} else {
			protectPricingSyncShapes(localData, r.Data)
			sourceProviders, skippedModels := make(map[string]string), make(map[string]string)
			for name, value := range valueMap(r.Data[syncSourceProviders]) {
				if text, ok := value.(string); ok {
					sourceProviders[name] = text
				}
			}
			for name, value := range valueMap(r.Data[syncSkippedModels]) {
				if text, ok := value.(string); ok {
					skippedModels[name] = text
				}
			}
			testResults = append(testResults, dto.TestResult{
				Name:            r.Name,
				Status:          "success",
				SourceProviders: sourceProviders,
				SkippedModels:   skippedModels,
			})
			successfulChannels = append(successfulChannels, struct {
				name string
				data map[string]any
			}{name: r.Name, data: r.Data})
		}
	}

	differences := buildDifferences(localData, successfulChannels)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"pricing_config": pricingConfig,
			"differences":    differences,
			"test_results":   testResults,
		},
	})
}

func buildDifferences(localData map[string]any, successfulChannels []struct {
	name string
	data map[string]any
}) map[string]map[string]dto.DifferenceItem {
	differences := make(map[string]map[string]dto.DifferenceItem)

	allModels := make(map[string]struct{})

	for _, field := range pricingSyncFields {
		for modelName := range valueMap(localData[field]) {
			allModels[modelName] = struct{}{}
		}
	}

	for _, channel := range successfulChannels {
		for _, field := range pricingSyncFields {
			for modelName := range valueMap(channel.data[field]) {
				allModels[modelName] = struct{}{}
			}
		}
	}

	confidenceMap := make(map[string]map[string]bool)
	expressionsEquivalent := make(map[string]map[string]bool)

	// Legacy fallback detection was made before currency conversion, so the
	// sentinel remains untrusted at every target credit anchor.
	for _, channel := range successfulChannels {
		confidenceMap[channel.name] = make(map[string]bool)
		expressionsEquivalent[channel.name] = make(map[string]bool)
		for name := range allModels {
			confidenceMap[channel.name][name] = valueMap(channel.data[syncUntrustedModels])[name] != true
			localExpr, _ := valueMap(localData[billing_setting.BillingExprField])[name].(string)
			upstreamExpr, _ := valueMap(channel.data[billing_setting.BillingExprField])[name].(string)
			if strings.TrimSpace(localExpr) != "" && strings.TrimSpace(upstreamExpr) != "" {
				expressionsEquivalent[channel.name][name] = billingexpr.PricingEquivalent(localExpr, upstreamExpr)
			}
		}
	}

	for modelName := range allModels {
		for _, ratioType := range pricingSyncFields {
			var localValue interface{} = nil
			if val, exists := valueMap(localData[ratioType])[modelName]; exists {
				localValue = normalizeSyncValue(ratioType, val)
			}

			upstreamValues := make(map[string]interface{})
			confidenceValues := make(map[string]bool)
			hasUpstreamValue := false
			hasDifference := false

			for _, channel := range successfulChannels {
				var upstreamValue interface{} = nil
				if numericPricingSyncFields[ratioType] && expressionsEquivalent[channel.name][modelName] &&
					valueMap(localData[billing_setting.BillingModeField])[modelName] == billing_setting.BillingModeTieredExpr &&
					valueMap(channel.data[billing_setting.BillingModeField])[modelName] == billing_setting.BillingModeTieredExpr {
					// These fallback ratios/per-call prices are not used by either
					// model's equivalent authoritative expression.
					upstreamValues[channel.name] = "same"
					confidenceValues[channel.name] = confidenceMap[channel.name][modelName]
					continue
				}

				pairedExpressionChange := false
				if ratioType == billing_setting.BillingModeField || ratioType == billing_setting.BillingExprField {
					mode := valueMap(channel.data[billing_setting.BillingModeField])[modelName]
					expr, _ := valueMap(channel.data[billing_setting.BillingExprField])[modelName].(string)
					pairedExpressionChange = mode == billing_setting.BillingModeTieredExpr && strings.TrimSpace(expr) != "" &&
						(!valuesEqual(valueMap(localData[billing_setting.BillingModeField])[modelName], mode) || !expressionsEquivalent[channel.name][modelName])
				}
				if val, exists := valueMap(channel.data[ratioType])[modelName]; exists {
					upstreamValue = normalizeSyncValue(ratioType, val)
					hasUpstreamValue = true

					if pairedExpressionChange || localValue != nil && !pricingSyncValuesEqual(ratioType, localValue, upstreamValue) {
						hasDifference = true
					} else if pricingSyncValuesEqual(ratioType, localValue, upstreamValue) {
						upstreamValue = "same"
					}
				}
				if upstreamValue == nil && localValue == nil {
					upstreamValue = "same"
				}

				if localValue == nil && upstreamValue != nil && upstreamValue != "same" {
					hasDifference = true
				}

				upstreamValues[channel.name] = upstreamValue

				confidenceValues[channel.name] = confidenceMap[channel.name][modelName]
			}

			shouldInclude := false

			if localValue != nil {
				if hasDifference {
					shouldInclude = true
				}
			} else {
				if hasUpstreamValue {
					shouldInclude = true
				}
			}

			if shouldInclude {
				if differences[modelName] == nil {
					differences[modelName] = make(map[string]dto.DifferenceItem)
				}
				differences[modelName][ratioType] = dto.DifferenceItem{
					Current:    localValue,
					Upstreams:  upstreamValues,
					Confidence: confidenceValues,
				}
			}
		}
	}

	channelHasDiff := make(map[string]bool)
	for _, ratioMap := range differences {
		for _, item := range ratioMap {
			for chName, val := range item.Upstreams {
				if val != nil && val != "same" {
					channelHasDiff[chName] = true
				}
			}
		}
	}

	for modelName, ratioMap := range differences {
		for ratioType, item := range ratioMap {
			for chName := range item.Upstreams {
				if !channelHasDiff[chName] {
					delete(item.Upstreams, chName)
					delete(item.Confidence, chName)
				}
			}

			allSame := true
			for _, v := range item.Upstreams {
				if v != "same" {
					allSame = false
					break
				}
			}
			if len(item.Upstreams) == 0 || allSame {
				delete(ratioMap, ratioType)
			} else {
				differences[modelName][ratioType] = item
			}
		}

		if len(ratioMap) == 0 {
			delete(differences, modelName)
		}
	}

	return differences
}

func isModelsDevAPIEndpoint(rawURL string) bool {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	if strings.ToLower(parsedURL.Hostname()) != modelsDevHost {
		return false
	}
	path := strings.TrimSuffix(parsedURL.Path, "/")
	if path == "" {
		path = "/"
	}
	return path == modelsDevPath
}

// convertOpenRouterToRatioData parses OpenRouter's /v1/models response and converts
// per-token USD pricing into the local ratio format.
// model_ratio = prompt_price_per_token * target_credits_per_usd
//
// completion_ratio = completion_price / prompt_price (output/input multiplier)
func convertOpenRouterToRatioData(reader io.Reader) (map[string]any, error) {
	anchor, err := common.CreditsPerUSD()
	if err != nil {
		return nil, err
	}
	return convertOpenRouterToRatioDataWithAnchor(reader, anchor.InexactFloat64())
}

func convertOpenRouterToRatioDataWithAnchor(reader io.Reader, targetK float64) (map[string]any, error) {
	if !validSyncUnit(targetK) {
		return nil, fmt.Errorf("invalid pricing currency units")
	}
	var orResp struct {
		Data []struct {
			ID      string            `json:"id"`
			Pricing map[string]string `json:"pricing"`
		} `json:"data"`
	}

	if err := common.DecodeJson(reader, &orResp); err != nil {
		return nil, fmt.Errorf("failed to decode OpenRouter response: %w", err)
	}

	modelRatioMap := make(map[string]any)
	completionRatioMap := make(map[string]any)
	cacheRatioMap := make(map[string]any)
	skippedModels := make(map[string]any)

	for _, m := range orResp.Data {
		if strings.TrimSpace(m.ID) == "" {
			continue
		}
		unsupported := ""
		for field, raw := range m.Pricing {
			if field == "prompt" || field == "completion" || field == "input_cache_read" {
				continue
			}
			value, err := syncFloatLiteral(raw)
			if err != nil || value != 0 {
				unsupported = "OpenRouter quote has an unsupported billing dimension " + field
				break
			}
		}
		if unsupported != "" {
			skippedModels[m.ID] = unsupported
			continue
		}
		promptPrice, promptErr := syncFloatLiteral(m.Pricing["prompt"])
		completionPrice, compErr := syncFloatLiteral(m.Pricing["completion"])

		// Reject models where both prices are missing or invalid
		if promptErr != nil && compErr != nil {
			continue
		}

		// Missing prices: reject the model instead of treating as 0
		// Only explicit "0" or "0.0" in the JSON should be treated as free
		if promptErr != nil || compErr != nil {
			continue
		}

		// Validate parsed values are finite and non-negative
		if !isValidNonNegativeCost(promptPrice) || !isValidNonNegativeCost(completionPrice) {
			continue
		}

		// Negative values are sentinel values (e.g., -1 for dynamic/variable pricing) — skip
		if promptPrice < 0 || completionPrice < 0 {
			continue
		}

		if promptPrice == 0 && completionPrice == 0 {
			if m.Pricing["input_cache_read"] != "" {
				cache, err := syncFloatLiteral(m.Pricing["input_cache_read"])
				if err != nil || cache != 0 {
					continue
				}
			}
			// Free model
			modelRatioMap[m.ID] = 0.0
			continue
		}
		if promptPrice <= 0 {
			// No meaningful prompt baseline, cannot derive ratios safely.
			continue
		}

		// Normal case: promptPrice > 0
		ratio, err := scaleSyncPrice(promptPrice, targetK, 1)
		if err != nil {
			continue
		}

		// Validate computed ratio is finite
		if !isValidNonNegativeCost(ratio) {
			continue
		}
		compRatio, err := scaleSyncPrice(completionPrice, 1, promptPrice)
		if err != nil {
			continue
		}

		// Validate computed completion ratio is finite
		if !isValidNonNegativeCost(compRatio) {
			continue
		}
		// Convert input_cache_read to cache_ratio (= cache_read_price / prompt_price)
		if m.Pricing["input_cache_read"] != "" {
			cachePrice, err := syncFloatLiteral(m.Pricing["input_cache_read"])
			if err != nil {
				continue
			}
			cacheRatio, err := scaleSyncPrice(cachePrice, 1, promptPrice)
			if err != nil {
				continue
			}
			cacheRatioMap[m.ID] = cacheRatio
		}
		modelRatioMap[m.ID] = ratio
		completionRatioMap[m.ID] = compRatio
	}

	converted := make(map[string]any)
	if len(modelRatioMap) > 0 {
		converted["model_ratio"] = modelRatioMap
	}
	if len(completionRatioMap) > 0 {
		converted["completion_ratio"] = completionRatioMap
	}
	if len(cacheRatioMap) > 0 {
		converted["cache_ratio"] = cacheRatioMap
	}
	if len(skippedModels) > 0 {
		converted[syncSkippedModels] = skippedModels
	}

	return converted, nil
}

func GetSyncableChannels(c *gin.Context) {
	channels, err := model.GetAllChannels(0, 0, true, false)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	var syncableChannels []dto.SyncableChannel
	for _, channel := range channels {
		if channel.GetBaseURL() != "" {
			syncableChannels = append(syncableChannels, dto.SyncableChannel{
				ID:      channel.Id,
				Name:    channel.Name,
				BaseURL: channel.GetBaseURL(),
				Status:  channel.Status,
				Type:    channel.Type,
			})
		}
	}

	syncableChannels = append(syncableChannels, dto.SyncableChannel{
		ID:      officialRatioPresetID,
		Name:    officialRatioPresetName,
		BaseURL: officialRatioPresetBaseURL,
		Status:  1,
	})

	syncableChannels = append(syncableChannels, dto.SyncableChannel{
		ID:      modelsDevPresetID,
		Name:    modelsDevPresetName,
		BaseURL: modelsDevPresetBaseURL,
		Status:  1,
	})

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    syncableChannels,
	})
}
