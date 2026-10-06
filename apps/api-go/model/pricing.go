package model

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/setting/billing_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/LIghtJUNction/api.lmm.best/types"
	"gorm.io/gorm"
)

type Pricing struct {
	ModelName              string                  `json:"model_name"`
	Description            string                  `json:"description,omitempty"`
	Icon                   string                  `json:"icon,omitempty"`
	Tags                   string                  `json:"tags,omitempty"`
	VendorID               int                     `json:"vendor_id,omitempty"`
	QuotaType              int                     `json:"quota_type"`
	ModelRatio             float64                 `json:"model_ratio"`
	ModelPrice             float64                 `json:"model_price"`
	OwnerBy                string                  `json:"owner_by"`
	CompletionRatio        float64                 `json:"completion_ratio"`
	CacheRatio             *float64                `json:"cache_ratio,omitempty"`
	CreateCacheRatio       *float64                `json:"create_cache_ratio,omitempty"`
	ImageRatio             *float64                `json:"image_ratio,omitempty"`
	AudioRatio             *float64                `json:"audio_ratio,omitempty"`
	AudioCompletionRatio   *float64                `json:"audio_completion_ratio,omitempty"`
	EnableGroup            []string                `json:"enable_groups"`
	SupportedEndpointTypes []constant.EndpointType `json:"supported_endpoint_types"`
	BillingMode            string                  `json:"billing_mode,omitempty"`
	BillingExpr            string                  `json:"billing_expr,omitempty"`
	PricingVersion         string                  `json:"pricing_version,omitempty"`
	PricingSchemaVersion   int                     `json:"pricing_schema_version,omitempty"`
	PricingCurrency        string                  `json:"pricing_currency,omitempty"`
	InputPrice             *float64                `json:"input_price,omitempty"`
	OutputPrice            *float64                `json:"output_price,omitempty"`
	CacheReadPrice         *float64                `json:"cache_read_price,omitempty"`
	CacheWritePrice        *float64                `json:"cache_write_price,omitempty"`
	ImagePrice             *float64                `json:"image_price,omitempty"`
	AudioInputPrice        *float64                `json:"audio_input_price,omitempty"`
	AudioOutputPrice       *float64                `json:"audio_output_price,omitempty"`
}

type PricingVendor struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Icon        string `json:"icon,omitempty"`
}

const pricingRefreshTimeout = 10 * time.Second

type pricingSnapshot struct {
	pricing                   []Pricing
	vendors                   []PricingVendor
	supportedEndpoints        map[string]common.EndpointInfo
	modelEnableGroups         map[string][]string
	modelQuotaTypes           map[string]int
	modelSupportEndpointTypes map[string][]constant.EndpointType
	refreshedAt               time.Time
	generation                uint64
}

var (
	pricingCache        atomic.Pointer[pricingSnapshot]
	updatePricingLock   sync.Mutex
	pricingInvalidation atomic.Uint64
	pricingRefreshHook  func()
	pricingContextHook  func() (context.Context, context.CancelFunc)
	pricingVendorHook   func()
)

func cloneFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func clonePricing(pricing []Pricing) []Pricing {
	if pricing == nil {
		return nil
	}
	cloned := make([]Pricing, len(pricing))
	for i := range pricing {
		cloned[i] = pricing[i]
		cloned[i].CacheRatio = cloneFloat64(pricing[i].CacheRatio)
		cloned[i].InputPrice = cloneFloat64(pricing[i].InputPrice)
		cloned[i].OutputPrice = cloneFloat64(pricing[i].OutputPrice)
		cloned[i].CacheReadPrice = cloneFloat64(pricing[i].CacheReadPrice)
		cloned[i].CacheWritePrice = cloneFloat64(pricing[i].CacheWritePrice)
		cloned[i].ImagePrice = cloneFloat64(pricing[i].ImagePrice)
		cloned[i].AudioInputPrice = cloneFloat64(pricing[i].AudioInputPrice)
		cloned[i].AudioOutputPrice = cloneFloat64(pricing[i].AudioOutputPrice)
		cloned[i].CreateCacheRatio = cloneFloat64(pricing[i].CreateCacheRatio)
		cloned[i].ImageRatio = cloneFloat64(pricing[i].ImageRatio)
		cloned[i].AudioRatio = cloneFloat64(pricing[i].AudioRatio)
		cloned[i].AudioCompletionRatio = cloneFloat64(pricing[i].AudioCompletionRatio)
		cloned[i].EnableGroup = append([]string(nil), pricing[i].EnableGroup...)
		cloned[i].SupportedEndpointTypes = append([]constant.EndpointType(nil), pricing[i].SupportedEndpointTypes...)
	}
	return cloned
}

func GetPricing() []Pricing {
	snapshot := loadPricingSnapshot()
	if snapshot == nil {
		startPricingRefresh()
		return nil
	}
	if pricingCacheNeedsRefresh(snapshot) {
		startPricingRefresh()
	}
	if snapshot == nil {
		return nil
	}
	return clonePricing(snapshot.pricing)
}

func InvalidatePricingCache() {
	// Invalidating pricing is called from channel-cache publication. It must not
	// wait for a possibly slow pricing rebuild, otherwise an unrelated pricing
	// request can stall the channel synchronization loop indefinitely.
	pricingInvalidation.Add(1)
}

func loadPricingSnapshot() *pricingSnapshot {
	return pricingCache.Load()
}

func pricingCacheNeedsRefresh(snapshot *pricingSnapshot) bool {
	return snapshot == nil ||
		pricingInvalidation.Load() != snapshot.generation ||
		time.Since(snapshot.refreshedAt) > time.Minute
}

// startPricingRefresh serializes refresh attempts while allowing request paths
// to keep serving the immutable last-known-good snapshot during database I/O.
func startPricingRefresh() {
	if !updatePricingLock.TryLock() {
		return
	}
	db := DB
	go func(db *gorm.DB) {
		defer updatePricingLock.Unlock()
		if snapshot := pricingCache.Load(); !pricingCacheNeedsRefresh(snapshot) {
			return
		}
		if err := refreshPricingLockedSafely(db); err != nil {
			common.SysLog(fmt.Sprintf("refresh pricing cache failed: %v", err))
		}
	}(db)
}

// refreshPricingNow is the synchronous seam used by explicit administrative
// refreshes and focused tests. Failed builds never replace the current snapshot.
func refreshPricingNow() error {
	updatePricingLock.Lock()
	defer updatePricingLock.Unlock()
	return refreshPricingLockedSafely(DB)
}

func refreshPricingLockedSafely(db *gorm.DB) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("pricing cache refresh panic: %v", recovered)
		}
	}()
	return refreshPricingLocked(db)
}

// refreshPricingLocked records the generation it started for, builds every
// derived datum locally under one deadline, and publishes exactly once.
func refreshPricingLocked(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("pricing database is unavailable")
	}
	generation := pricingInvalidation.Load()
	ctx, cancel := context.WithTimeout(context.Background(), pricingRefreshTimeout)
	if pricingContextHook != nil {
		cancel()
		ctx, cancel = pricingContextHook()
	}
	defer cancel()

	snapshot, err := buildPricingSnapshot(ctx, db, generation)
	if err != nil {
		return err
	}
	if pricingRefreshHook != nil {
		pricingRefreshHook()
	}
	pricingCache.Store(snapshot)
	return nil
}

// GetVendors 返回当前定价接口使用到的供应商信息
func GetVendors() []PricingVendor {
	snapshot := loadPricingSnapshot()
	if snapshot == nil {
		GetPricing()
		snapshot = pricingCache.Load()
	} else if pricingCacheNeedsRefresh(snapshot) {
		startPricingRefresh()
	}
	if snapshot == nil {
		return nil
	}
	return append([]PricingVendor(nil), snapshot.vendors...)
}

func GetModelSupportEndpointTypes(model string) []constant.EndpointType {
	if model == "" {
		return make([]constant.EndpointType, 0)
	}
	snapshot := loadPricingSnapshot()
	if snapshot == nil {
		GetPricing()
		snapshot = pricingCache.Load()
	} else if pricingCacheNeedsRefresh(snapshot) {
		startPricingRefresh()
	}
	if snapshot != nil {
		if endpoints, ok := snapshot.modelSupportEndpointTypes[model]; ok {
			return append([]constant.EndpointType(nil), endpoints...)
		}
	}
	return make([]constant.EndpointType, 0)
}

func getPricingEndpointTypesForAbility(ability AbilityWithChannel, advancedCustomConfigs map[int]*dto.AdvancedCustomConfig) []constant.EndpointType {
	if ability.ChannelType == constant.ChannelTypeNewAPI || ability.ChannelType == constant.ChannelTypeOpenAI {
		if upstreamModel, err := ResolveChannelModelName(ability.Model, ability.ChannelModelMapping); err == nil {
			return common.GetEndpointTypesByChannelType(ability.ChannelType, upstreamModel)
		}
	}
	if ability.ChannelType != constant.ChannelTypeAdvancedCustom {
		return common.GetEndpointTypesByChannelType(ability.ChannelType, ability.Model)
	}
	if config := advancedCustomConfigs[ability.ChannelId]; config != nil {
		return config.SupportedEndpointTypesForModel(ability.Model)
	}
	return common.GetEndpointTypesByChannelType(ability.ChannelType, ability.Model)
}

// loadPricingAdvancedCustomConfigs runs while a pricing refresh is serialized
// and briefly reads the published channel cache. Channel publication never
// waits on the pricing refresh lock, so this lock order cannot deadlock.
// The returned configs are pointers shared with the channel cache; they are
// replaced wholesale on update and never mutated in place, so reading them after
// RUnlock is safe.
func loadPricingAdvancedCustomConfigs(ctx context.Context, db *gorm.DB, enableAbilities []AbilityWithChannel) (map[int]*dto.AdvancedCustomConfig, error) {
	channelIDs := make([]int, 0)
	seen := make(map[int]struct{})
	for _, ability := range enableAbilities {
		if ability.ChannelType != constant.ChannelTypeAdvancedCustom {
			continue
		}
		if _, exists := seen[ability.ChannelId]; exists {
			continue
		}
		seen[ability.ChannelId] = struct{}{}
		channelIDs = append(channelIDs, ability.ChannelId)
	}
	if len(channelIDs) == 0 {
		return nil, nil
	}

	configs := make(map[int]*dto.AdvancedCustomConfig, len(channelIDs))
	if common.MemoryCacheEnabled {
		channelSyncLock.RLock()
		defer channelSyncLock.RUnlock()
		if !channelCacheReady {
			return nil, channelCacheNotReadyErrorLocked()
		}
		for _, channelID := range channelIDs {
			if config := channel2advancedCustomConfig[channelID]; config != nil {
				configs[channelID] = config
			}
		}
		return configs, nil
	}

	for _, channelID := range channelIDs {
		channel := &Channel{Id: channelID}
		err := db.WithContext(ctx).First(channel, "id = ?", channelID).Error
		if err != nil {
			return nil, fmt.Errorf("load advanced custom channel settings for channel %d: %w", channelID, err)
		}
		if channel.Type != constant.ChannelTypeAdvancedCustom {
			continue
		}
		if config := channel.GetOtherSettings().AdvancedCustom; config != nil {
			configs[channelID] = config
		}
	}
	return configs, nil
}

func appendPricingEndpoint(endpoints []string, endpoint string) []string {
	if endpoint == "" || common.StringsContains(endpoints, endpoint) {
		return endpoints
	}
	return append(endpoints, endpoint)
}

func isFixedNativePricingEndpoint(endpoint string) bool {
	switch constant.EndpointType(endpoint) {
	case constant.EndpointTypeSystemOne, constant.EndpointTypeModeration, constant.EndpointTypeLive,
		constant.EndpointTypeRealtimeTranscription, constant.EndpointTypeRealtimeTranslation:
		return true
	default:
		return false
	}
}

func hasOnlyFixedNativePricingEndpoints(endpoints []string) bool {
	if len(endpoints) == 0 {
		return false
	}
	for _, endpoint := range endpoints {
		if !isFixedNativePricingEndpoint(endpoint) {
			return false
		}
	}
	return true
}

func loadEnabledPricingAbilities(ctx context.Context, db *gorm.DB) ([]AbilityWithChannel, error) {
	var abilities []AbilityWithChannel
	err := db.WithContext(ctx).Table("abilities").
		Select("abilities.*, channels.type as channel_type, channels.model_mapping as channel_model_mapping").
		Joins("left join channels on abilities.channel_id = channels.id").
		Where("abilities.enabled = ?", true).
		Scan(&abilities).Error
	return abilities, err
}

func buildPricingSnapshot(ctx context.Context, db *gorm.DB, generation uint64) (*pricingSnapshot, error) {
	//modelRatios := common.GetModelRatios()
	enableAbilities, err := loadEnabledPricingAbilities(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("load enabled abilities: %w", err)
	}
	// 预加载模型元数据与供应商一次，避免循环查询
	var allMeta []Model
	if err := db.WithContext(ctx).Find(&allMeta).Error; err != nil {
		return nil, fmt.Errorf("load model metadata: %w", err)
	}
	metaMap := make(map[string]*Model)
	prefixList := make([]*Model, 0)
	suffixList := make([]*Model, 0)
	containsList := make([]*Model, 0)
	for i := range allMeta {
		m := &allMeta[i]
		if m.NameRule == NameRuleExact {
			metaMap[m.ModelName] = m
		} else {
			switch m.NameRule {
			case NameRulePrefix:
				prefixList = append(prefixList, m)
			case NameRuleSuffix:
				suffixList = append(suffixList, m)
			case NameRuleContains:
				containsList = append(containsList, m)
			}
		}
	}

	// 将非精确规则模型匹配到 metaMap
	for _, m := range prefixList {
		for _, pricingModel := range enableAbilities {
			if strings.HasPrefix(pricingModel.Model, m.ModelName) {
				if _, exists := metaMap[pricingModel.Model]; !exists {
					metaMap[pricingModel.Model] = m
				}
			}
		}
	}
	for _, m := range suffixList {
		for _, pricingModel := range enableAbilities {
			if strings.HasSuffix(pricingModel.Model, m.ModelName) {
				if _, exists := metaMap[pricingModel.Model]; !exists {
					metaMap[pricingModel.Model] = m
				}
			}
		}
	}
	for _, m := range containsList {
		for _, pricingModel := range enableAbilities {
			if strings.Contains(pricingModel.Model, m.ModelName) {
				if _, exists := metaMap[pricingModel.Model]; !exists {
					metaMap[pricingModel.Model] = m
				}
			}
		}
	}

	// 预加载供应商
	var vendors []Vendor
	if err := db.WithContext(ctx).Find(&vendors).Error; err != nil {
		return nil, fmt.Errorf("load vendors: %w", err)
	}
	vendorMap := make(map[int]*Vendor)
	for i := range vendors {
		vendorMap[vendors[i].Id] = &vendors[i]
	}

	// 初始化默认供应商映射
	if err := initDefaultVendorMapping(ctx, db, metaMap, vendorMap, enableAbilities); err != nil {
		return nil, fmt.Errorf("initialize default vendor mapping: %w", err)
	}

	// 构建对前端友好的供应商列表
	vendorsList := make([]PricingVendor, 0, len(vendorMap))
	for _, v := range vendorMap {
		vendorsList = append(vendorsList, PricingVendor{
			ID:          v.Id,
			Name:        v.Name,
			Description: v.Description,
			Icon:        v.Icon,
		})
	}

	modelGroupsMap := make(map[string]*types.Set[string])

	for _, ability := range enableAbilities {
		groups, ok := modelGroupsMap[ability.Model]
		if !ok {
			groups = types.NewSet[string]()
			modelGroupsMap[ability.Model] = groups
		}
		groups.Add(ability.Group)
	}

	//这里使用切片而不是Set，因为一个模型可能支持多个端点类型，并且第一个端点是优先使用端点
	modelSupportEndpointsStr := make(map[string][]string)
	advancedCustomConfigs, err := loadPricingAdvancedCustomConfigs(ctx, db, enableAbilities)
	if err != nil {
		return nil, err
	}

	// 先根据已有能力填充原生端点
	for _, ability := range enableAbilities {
		endpoints := modelSupportEndpointsStr[ability.Model]
		channelTypes := getPricingEndpointTypesForAbility(ability, advancedCustomConfigs)
		for _, channelType := range channelTypes {
			if !common.StringsContains(endpoints, string(channelType)) {
				endpoints = append(endpoints, string(channelType))
			}
		}
		modelSupportEndpointsStr[ability.Model] = endpoints
	}

	// 再补充模型自定义端点：若配置有效则追加到已有推断，不再裁剪渠道真实能力
	for modelName, meta := range metaMap {
		if strings.TrimSpace(meta.Endpoints) == "" {
			continue
		}
		var raw map[string]interface{}
		if err := common.Unmarshal([]byte(meta.Endpoints), &raw); err == nil {
			endpoints := modelSupportEndpointsStr[modelName]
			_, knownNativeVoice := common.NativeVoiceEndpointType(modelName)
			fixedNativeCapabilities := knownNativeVoice || hasOnlyFixedNativePricingEndpoints(endpoints)
			for k, v := range raw {
				// Model annotations cannot invent a chat surface for a native-only
				// capability. Ordinary and custom-route metadata still merge.
				if fixedNativeCapabilities && !common.StringsContains(endpoints, k) {
					continue
				}
				switch v.(type) {
				case string, map[string]interface{}:
					endpoints = appendPricingEndpoint(endpoints, k)
				}
			}
			if len(endpoints) > 0 {
				modelSupportEndpointsStr[modelName] = endpoints
			}
		}
	}

	modelSupportEndpointTypes := make(map[string][]constant.EndpointType)
	for model, endpoints := range modelSupportEndpointsStr {
		supportedEndpoints := make([]constant.EndpointType, 0)
		for _, endpointStr := range endpoints {
			endpointType := constant.EndpointType(endpointStr)
			supportedEndpoints = append(supportedEndpoints, endpointType)
		}
		modelSupportEndpointTypes[model] = supportedEndpoints
	}

	// 构建全局 supportedEndpointMap（默认 + 自定义覆盖）
	supportedEndpointMap := make(map[string]common.EndpointInfo)
	// 1. 默认端点
	for _, endpoints := range modelSupportEndpointTypes {
		for _, et := range endpoints {
			if info, ok := common.GetDefaultEndpointInfo(et); ok {
				if _, exists := supportedEndpointMap[string(et)]; !exists {
					supportedEndpointMap[string(et)] = info
				}
			}
		}
	}
	// 2. 自定义端点（models 表）覆盖默认
	for _, meta := range metaMap {
		if strings.TrimSpace(meta.Endpoints) == "" {
			continue
		}
		var raw map[string]interface{}
		if err := common.Unmarshal([]byte(meta.Endpoints), &raw); err == nil {
			for k, v := range raw {
				// These gateway entry points have fixed protocol paths and methods.
				// A stale model annotation must not replace WS GET with chat POST.
				if isFixedNativePricingEndpoint(k) {
					continue
				}
				switch val := v.(type) {
				case string:
					supportedEndpointMap[k] = common.EndpointInfo{Path: val, Method: "POST"}
				case map[string]interface{}:
					ep := common.EndpointInfo{Method: "POST"}
					if p, ok := val["path"].(string); ok {
						ep.Path = p
					}
					if m, ok := val["method"].(string); ok {
						ep.Method = strings.ToUpper(m)
					}
					supportedEndpointMap[k] = ep
				default:
					// ignore unsupported types
				}
			}
		}
	}

	pricingMap := make([]Pricing, 0)
	for model, groups := range modelGroupsMap {
		pricing := Pricing{
			ModelName:              model,
			EnableGroup:            groups.Items(),
			SupportedEndpointTypes: modelSupportEndpointTypes[model],
		}

		// 补充模型元数据（描述、标签、供应商、状态）
		if meta, ok := metaMap[model]; ok {
			// 若模型被禁用(status!=1)，则直接跳过，不返回给前端
			if meta.Status != 1 {
				continue
			}
			pricing.Description = meta.Description
			pricing.Icon = meta.Icon
			pricing.Tags = meta.Tags
			pricing.VendorID = meta.VendorID
		}
		modelPrice, findPrice := ratio_setting.GetModelPrice(model, false)
		if findPrice {
			pricing.ModelPrice = modelPrice
			pricing.QuotaType = 1
		} else {
			modelRatio, _, _ := ratio_setting.GetModelRatio(model)
			pricing.ModelRatio = modelRatio
			pricing.CompletionRatio = ratio_setting.GetCompletionRatio(model)
			pricing.QuotaType = 0
		}
		if cacheRatio, ok := ratio_setting.GetCacheRatio(model); ok {
			pricing.CacheRatio = &cacheRatio
		}
		if createCacheRatio, ok := ratio_setting.GetCreateCacheRatio(model); ok {
			pricing.CreateCacheRatio = &createCacheRatio
		}
		if imageRatio, ok := ratio_setting.GetImageRatio(model); ok {
			pricing.ImageRatio = &imageRatio
		}
		if ratio_setting.ContainsAudioRatio(model) {
			audioRatio := ratio_setting.GetAudioRatio(model)
			pricing.AudioRatio = &audioRatio
		}
		if ratio_setting.ContainsAudioCompletionRatio(model) {
			audioCompletionRatio := ratio_setting.GetAudioCompletionRatio(model)
			pricing.AudioCompletionRatio = &audioCompletionRatio
		}
		if billingMode := billing_setting.GetBillingMode(model); billingMode == "tiered_expr" {
			if expr, ok := billing_setting.GetBillingExpr(model); ok && strings.TrimSpace(expr) != "" {
				pricing.BillingMode = billingMode
				pricing.BillingExpr = expr
			}
		}
		pricingMap = append(pricingMap, pricing)
	}

	// 防止大更新后数据不通用
	if len(pricingMap) > 0 {
		pricingMap[0].PricingVersion = "5a90f2b86c08bd983a9a2e6d66c255f4eaef9c4bc934386d2b6ae84ef0ff1f1f"
	}

	// 刷新缓存映射，供高并发快速查询
	modelEnableGroups := make(map[string][]string)
	modelQuotaTypeMap := make(map[string]int)
	for _, p := range pricingMap {
		modelEnableGroups[p.ModelName] = p.EnableGroup
		modelQuotaTypeMap[p.ModelName] = p.QuotaType
	}

	return &pricingSnapshot{
		pricing:                   pricingMap,
		vendors:                   vendorsList,
		supportedEndpoints:        supportedEndpointMap,
		modelEnableGroups:         modelEnableGroups,
		modelQuotaTypes:           modelQuotaTypeMap,
		modelSupportEndpointTypes: modelSupportEndpointTypes,
		refreshedAt:               time.Now(),
		generation:                generation,
	}, nil
}

// GetSupportedEndpointMap 返回全局端点到路径的映射
func GetSupportedEndpointMap() map[string]common.EndpointInfo {
	snapshot := loadPricingSnapshot()
	if snapshot == nil {
		GetPricing()
		snapshot = pricingCache.Load()
	} else if pricingCacheNeedsRefresh(snapshot) {
		startPricingRefresh()
	}
	if snapshot == nil {
		return nil
	}
	result := make(map[string]common.EndpointInfo, len(snapshot.supportedEndpoints))
	for endpoint, info := range snapshot.supportedEndpoints {
		result[endpoint] = info
	}
	return result
}
