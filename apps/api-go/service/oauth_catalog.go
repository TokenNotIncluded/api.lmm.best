package service

import (
	"context"
	"encoding/base64"
	"math"
	"slices"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/oauthserver"
	"github.com/LIghtJUNction/api.lmm.best/pkg/paymentpricing"
	"github.com/LIghtJUNction/api.lmm.best/setting/billing_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/shopspring/decimal"
)

type OAuthCatalog struct {
	SchemaVersion int                 `json:"schema_version"`
	Resource      string              `json:"resource"`
	UpdatedAt     int64               `json:"updated_at"`
	Groups        []OAuthCatalogGroup `json:"groups"`
	Models        []OAuthCatalogModel `json:"models"`
}
type OAuthCatalogGroup struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Scope      string   `json:"scope"`
	Multiplier *float64 `json:"multiplier"`
}
type OAuthCatalogModel struct {
	ID            string                  `json:"id"`
	GroupID       string                  `json:"group_id"`
	Group         string                  `json:"group"`
	UpstreamModel string                  `json:"upstream_model"`
	Name          string                  `json:"name"`
	APIs          []string                `json:"apis"`
	Pricing       OAuthCatalogPricing     `json:"pricing"`
	NativeCost    *OAuthCatalogNativeCost `json:"native_cost"`
}
type OAuthCatalogPricing struct {
	Currency                string                  `json:"currency"`
	Unit                    string                  `json:"unit"`
	PriceBasis              string                  `json:"price_basis"`
	GroupMultiplier         *float64                `json:"group_multiplier"`
	TrustMultiplier         *float64                `json:"trust_multiplier"`
	Input                   *float64                `json:"input"`
	Output                  *float64                `json:"output"`
	CacheRead               *float64                `json:"cache_read"`
	CacheWrite              *float64                `json:"cache_write"`
	Request                 *float64                `json:"request"`
	FinalCostDependsOnUsage bool                    `json:"final_cost_depends_on_usage"`
	UpdatedAt               int64                   `json:"updated_at"`
	NativeCost              *OAuthCatalogNativeCost `json:"-"`
}

type OAuthCatalogNativeCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

type oauthAbility struct {
	Group       string
	Model       string
	ChannelType int
	ChannelID   int
}

func (s *OAuthIntegration) liveAbilities(ctx context.Context, groups []string, name string) ([]oauthAbility, error) {
	var rows []oauthAbility
	if len(groups) == 0 {
		return rows, nil
	}
	query := s.DB.WithContext(ctx).Table("abilities").Select(`DISTINCT abilities."group", abilities.model, channels.type AS channel_type, channels.id AS channel_id`).
		Joins("JOIN channels ON channels.id = abilities.channel_id").Where(`abilities."group" IN ? AND abilities.enabled = ? AND channels.status = ?`, groups, true, common.ChannelStatusEnabled)
	if name != "" {
		query = query.Where("abilities.model = ?", name)
	}
	err := query.Limit(10001).Scan(&rows).Error
	if len(rows) > 10000 {
		return nil, ErrOAuthDenied
	}
	return rows, err
}

func oauthAPIs(channelType int, name string) []string {
	// Image routes also advertise a generic OpenAI endpoint, which must not
	// turn an image generator into a language model in the Pi catalog.
	if common.IsImageGenerationModel(name) {
		return nil
	}
	// Advanced custom endpoint configuration needs its own audited capability
	// mapping. Do not invent capabilities or return an unusable model for it.
	if channelType == constant.ChannelTypeAdvancedCustom {
		return nil
	}
	var apis []string
	for _, endpoint := range common.GetEndpointTypesByChannelType(channelType, name) {
		switch endpoint {
		case constant.EndpointTypeOpenAI:
			apis = append(apis, "openai-completions")
		case constant.EndpointTypeOpenAIResponse:
			apis = append(apis, "openai-responses")
		case constant.EndpointTypeAnthropic:
			apis = append(apis, "anthropic-messages")
		}
	}
	return apis
}

func OAuthAPIForPath(path string) string {
	switch path {
	case "/v1/chat/completions":
		return "openai-completions"
	case "/v1/responses":
		return "openai-responses"
	case "/v1/messages":
		return "anthropic-messages"
	default:
		return ""
	}
}

func (s *OAuthIntegration) ValidateModel(ctx context.Context, user *model.User, grant oauthserver.Grant, group, name, path string) error {
	if name == "" || len(name) > 512 || !slices.Contains(s.GrantedGroups(user, grant), group) {
		return ErrOAuthDenied
	}
	api := OAuthAPIForPath(path)
	if api == "" {
		return ErrOAuthDenied
	}
	rows, err := s.liveAbilities(ctx, []string{group}, name)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if slices.Contains(oauthAPIs(row.ChannelType, row.Model), api) {
			return nil
		}
	}
	return ErrOAuthDenied
}

func oauthNumber(value float64) *float64 {
	if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	return &value
}

func oauthGroupRatio(user *model.User, group string) *float64 {
	if ratio, ok := ratio_setting.GetGroupGroupRatio(user.Group, group); ok {
		return oauthNumber(ratio)
	}
	ratio, exists := ratio_setting.GetGroupRatioCopy()[group]
	if !exists {
		return nil
	}
	return oauthNumber(ratio)
}

func oauthPricing(name string, groupRatio, trustRatio *float64, channelIDs []int, updated int64) OAuthCatalogPricing {
	p := OAuthCatalogPricing{Currency: "USD", Unit: "unknown", PriceBasis: "unknown", GroupMultiplier: groupRatio, TrustMultiplier: trustRatio, FinalCostDependsOnUsage: true, UpdatedAt: updated}
	if billing_setting.GetBillingMode(name) == billing_setting.BillingModeTieredExpr {
		p.Unit, p.PriceBasis = "expression", "tiered_expression"
		return p
	}

	if groupRatio == nil || trustRatio == nil || common.QuotaPerUnit <= 0 {
		return p
	}
	factor := *groupRatio * *trustRatio

	rates, err := paymentpricing.CurrentRates()
	if err != nil {
		return p
	}
	toUSD := func(platformUnits float64) *float64 {
		if platformUnits < 0 || math.IsNaN(platformUnits) || math.IsInf(platformUnits, 0) {
			return nil
		}
		amount, err := rates.FiatForPlatformUnits(decimal.NewFromFloat(platformUnits), paymentpricing.CurrencyUSD)
		if err != nil {
			return nil
		}
		return oauthNumber(amount.InexactFloat64())
	}
	if price, exists := ratio_setting.GetModelPrice(name, false); exists {
		p.Unit, p.PriceBasis = "request", "configured_base_rates"

		p.Request = toUSD(price * factor)

		return p
	}
	inputRatio, exists, _ := ratio_setting.GetModelRatio(name)
	if !exists {
		return p
	}
	input := inputRatio * factor * 1_000_000 / common.QuotaPerUnit
	p.Unit = "million_tokens"
	p.PriceBasis = "configured_base_rates"
	p.Input, p.Output = toUSD(input), toUSD(input*ratio_setting.GetCompletionRatio(name))
	// The bool reports whether an override exists; the returned defaults are
	// also the values used by relay/helper/price.go during settlement.
	cacheRead, _ := ratio_setting.GetCacheRatio(name)
	p.CacheRead = toUSD(input * cacheRead)
	cacheWrite, _ := ratio_setting.GetCreateCacheRatio(name)
	p.CacheWrite = toUSD(input * cacheWrite)
	if p.Input != nil && p.Output != nil && p.CacheRead != nil && p.CacheWrite != nil {
		p.NativeCost = &OAuthCatalogNativeCost{Input: *p.Input, Output: *p.Output, CacheRead: *p.CacheRead, CacheWrite: *p.CacheWrite}
	}
	return p
}

func (s *OAuthIntegration) Catalog(ctx context.Context, user *model.User, grant oauthserver.Grant) (*OAuthCatalog, error) {
	now := time.Now().Unix()
	result := &OAuthCatalog{SchemaVersion: 1, Resource: s.Resource, UpdatedAt: now, Groups: []OAuthCatalogGroup{}, Models: []OAuthCatalogModel{}}
	groups := s.GrantedGroups(user, grant)
	rows, err := s.liveAbilities(ctx, groups, "")
	if err != nil {
		return nil, err
	}
	trust, err := model.GetTrustLevelInfoForUser(user)
	if err != nil {
		return nil, err
	}
	multiplier := oauthNumber(trust.DiscountRatio)
	for _, group := range groups {
		result.Groups = append(result.Groups, OAuthCatalogGroup{ID: OAuthGroupID(group), Name: group, Scope: OAuthGroupScope(group), Multiplier: oauthGroupRatio(user, group)})
	}
	entries := make(map[string]*OAuthCatalogModel)
	channelIDs := make(map[string]map[int]struct{})
	for _, row := range rows {
		apis := oauthAPIs(row.ChannelType, row.Model)
		if len(apis) == 0 || row.Model == "" || len(row.Model) > 512 {
			continue
		}
		id := "lmm:" + OAuthGroupID(row.Group) + ":" + base64.RawURLEncoding.EncodeToString([]byte(row.Model))
		entry := entries[id]
		if entry == nil {
			entry = &OAuthCatalogModel{ID: id, GroupID: OAuthGroupID(row.Group), Group: row.Group, UpstreamModel: row.Model, Name: row.Group + " / " + row.Model, APIs: []string{}}
			entries[id] = entry
			channelIDs[id] = make(map[int]struct{})
		}
		channelIDs[id][row.ChannelID] = struct{}{}
		for _, api := range apis {
			if !slices.Contains(entry.APIs, api) {
				entry.APIs = append(entry.APIs, api)
			}
		}
	}
	for _, entry := range entries {
		slices.Sort(entry.APIs)
		ids := make([]int, 0, len(channelIDs[entry.ID]))
		for channelID := range channelIDs[entry.ID] {
			ids = append(ids, channelID)
		}
		entry.Pricing = oauthPricing(entry.UpstreamModel, oauthGroupRatio(user, entry.Group), multiplier, ids, now)
		entry.NativeCost = entry.Pricing.NativeCost
		result.Models = append(result.Models, *entry)
	}
	usedGroups := make(map[string]struct{}, len(result.Models))
	for _, entry := range result.Models {
		usedGroups[entry.Group] = struct{}{}
	}
	result.Groups = slices.DeleteFunc(result.Groups, func(group OAuthCatalogGroup) bool {
		_, used := usedGroups[group.Name]
		return !used
	})
	slices.SortFunc(result.Models, func(a, b OAuthCatalogModel) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return result, nil
}
