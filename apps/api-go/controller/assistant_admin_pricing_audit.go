package controller

import (
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service/authz"
	"github.com/LIghtJUNction/api.lmm.best/setting/billing_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/dynamic_pricing_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
)

const assistantAdminPricingAuditPageSize = 100

func assistantAdminPricingAuditTools() []assistantOpenAIToolDefinition {
	return []assistantOpenAIToolDefinition{{Type: "function", Function: assistantOpenAIToolFunction{
		Name:        "audit_admin_model_pricing",
		Description: "For administrators, audit live enabled model pricing, detect missing/invalid/free rates and conflicting modes, inspect group multipliers and dynamic-pricing cost coverage. Read-only. Paginate until next_offset is absent before claiming the entire catalog was checked. Rates are base prices before group, trust, subscription and dynamic adjustments; the audit cannot establish profitability without upstream invoices and traffic mix.",
		Parameters: objectSchema(map[string]any{
			"model_ids": map[string]any{"type": "array", "maxItems": assistantAdminPricingAuditPageSize, "items": map[string]any{"type": "string", "maxLength": 200}},
			"offset":    map[string]any{"type": "integer", "minimum": 0},
			"limit":     map[string]any{"type": "integer", "minimum": 1, "maximum": assistantAdminPricingAuditPageSize},
		}, nil),
	}}}
}

func assistantAuditNonNegative(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

// Build each row from the live pricing snapshot, while checking configuration
// presence separately: a fallback ratio must never be reported as configured.
func assistantAdminPricingAuditRow(pricing model.Pricing, ratios, prices, groups map[string]float64) map[string]any {
	matched := ratio_setting.FormatMatchingModelName(pricing.ModelName)
	_, hasRatio := ratios[matched]
	_, hasPrice := prices[matched]
	issues := make([]string, 0)
	row := map[string]any{"model_id": pricing.ModelName, "matched_pricing_key": matched}
	if hasRatio && hasPrice {
		issues = append(issues, "both_fixed_and_token_rates_configured")
	}
	if billing_setting.GetBillingMode(pricing.ModelName) == billing_setting.BillingModeTieredExpr || pricing.BillingMode == billing_setting.BillingModeTieredExpr {
		row["mode"] = billing_setting.BillingModeTieredExpr
		expr, configured := billing_setting.GetBillingExpr(pricing.ModelName)
		if !configured || strings.TrimSpace(expr) == "" {
			issues = append(issues, "missing_tiered_expression")
		}
		row["requires_usage_scenario"] = true
	} else if pricing.QuotaType == 1 {
		row["mode"] = "fixed_request"
		if !hasPrice {
			issues = append(issues, "missing_configured_fixed_price")
		}
		if !assistantAuditNonNegative(pricing.ModelPrice) {
			issues = append(issues, "invalid_fixed_price")
		} else {
			row["base_usd_per_request"] = pricing.ModelPrice
			if pricing.ModelPrice == 0 {
				issues = append(issues, "zero_price_review_intent")
			}
		}
	} else {
		row["mode"] = "ratio"
		if !hasRatio {
			issues = append(issues, "missing_configured_token_ratio")
		}
		inputRate := pricing.ModelRatio * 2
		outputRate := inputRate * pricing.CompletionRatio
		if !assistantAuditNonNegative(pricing.ModelRatio) || !assistantAuditNonNegative(pricing.CompletionRatio) || !assistantAuditNonNegative(inputRate) || !assistantAuditNonNegative(outputRate) {
			issues = append(issues, "invalid_token_ratio")
		} else {
			row["base_input_usd_per_million"] = inputRate
			row["base_output_usd_per_million"] = outputRate
			if pricing.ModelRatio == 0 || pricing.CompletionRatio == 0 {
				issues = append(issues, "zero_price_review_intent")
			}
		}
	}
	for _, optional := range []struct {
		name  string
		value *float64
	}{
		{"cache_ratio", pricing.CacheRatio}, {"create_cache_ratio", pricing.CreateCacheRatio},
		{"image_ratio", pricing.ImageRatio}, {"audio_ratio", pricing.AudioRatio}, {"audio_completion_ratio", pricing.AudioCompletionRatio},
	} {
		if optional.value != nil && !assistantAuditNonNegative(*optional.value) {
			issues = append(issues, "invalid_"+optional.name)
		}
	}
	groupViews := make([]map[string]any, 0, min(len(pricing.EnableGroup), assistantAdminPricingAuditPageSize))
	for i, group := range pricing.EnableGroup {
		if i >= assistantAdminPricingAuditPageSize {
			break
		}
		view := map[string]any{"group": group}
		value, exists := groups[group]
		if !exists {
			view["issue"] = "missing_group_ratio_uses_default_one"
			view["multiplier"] = 1
		} else if !assistantAuditNonNegative(value) {
			view["issue"] = "invalid_group_ratio"
		} else {
			view["multiplier"] = value
			if value == 0 {
				view["issue"] = "zero_group_price_review_intent"
			}
		}
		groupViews = append(groupViews, view)
	}
	row["groups"] = groupViews
	row["groups_truncated"] = len(pricing.EnableGroup) > len(groupViews)
	if len(pricing.EnableGroup) == 0 {
		issues = append(issues, "no_enabled_routing_group")
	}
	row["issues"] = issues
	return row
}

func executeAssistantAdminPricingAuditTool(userID int, input map[string]any) map[string]any {
	user, err := assistantAdminUser(userID)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	requested, valid := stringList(input, "model_ids", assistantAdminPricingAuditPageSize)
	if !valid {
		return map[string]any{"ok": false, "error": "model_ids must contain at most 100 exact model IDs"}
	}
	selected := make(map[string]bool, len(requested))
	for _, id := range requested {
		if strings.TrimSpace(id) == "" || len([]rune(id)) > 200 {
			return map[string]any{"ok": false, "error": "model_ids must contain nonempty IDs of at most 200 characters"}
		}
		selected[id] = true
	}
	offset, limit := 0, assistantAdminPricingAuditPageSize
	for _, field := range []string{"offset", "limit"} {
		if _, present := input[field]; !present {
			continue
		}
		number, ok := inputNumber(input, field)
		maximum, minimum := float64(2_147_483_647), float64(0)
		if field == "limit" {
			maximum, minimum = assistantAdminPricingAuditPageSize, 1
		}
		if !ok || math.Trunc(number) != number || number < minimum || number > maximum {
			return map[string]any{"ok": false, "error": field + " is outside the supported integer range"}
		}
		if field == "offset" {
			offset = int(number)
		} else {
			limit = int(number)
		}
	}
	pricing := getPricingCache()
	if pricing == nil {
		return map[string]any{"ok": false, "status": "pricing_cache_unready", "error": "pricing cache is unavailable; retry after refresh"}
	}
	filtered := make([]model.Pricing, 0, len(pricing))
	found := make(map[string]bool)
	for _, row := range pricing {
		if strings.TrimSpace(row.ModelName) != "" && !found[row.ModelName] && (len(selected) == 0 || selected[row.ModelName]) {
			filtered = append(filtered, row)
			found[row.ModelName] = true
		}
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].ModelName < filtered[j].ModelName })
	missing := make([]string, 0)
	for id := range selected {
		if !found[id] {
			missing = append(missing, id)
		}
	}
	sort.Strings(missing)
	start, end := min(offset, len(filtered)), min(min(offset, len(filtered))+limit, len(filtered))
	rows := make([]map[string]any, 0, end-start)
	ratios, prices, groups := ratio_setting.GetModelRatioCopy(), ratio_setting.GetModelPriceCopy(), ratio_setting.GetGroupRatioCopy()
	for _, row := range filtered[start:end] {
		rows = append(rows, assistantAdminPricingAuditRow(row, ratios, prices, groups))
	}
	result := map[string]any{
		"ok": true, "read_only": true, "models": rows, "total_models": len(filtered), "offset": offset,
		"missing_requested_models": missing, "truncated": end < len(filtered),
		"rate_scope": "Base USD accounting rates before group overrides, trust discounts, subscriptions and dynamic multipliers; no profitability guarantee without upstream costs and measured traffic mix.",
	}
	if authz.Can(userID, user.Role, authz.ChannelRead) {
		result["dynamic_pricing"] = assistantAdminPricingCostCoverage()
	} else {
		result["dynamic_pricing"] = map[string]any{"coverage_available": false, "reason": "channel read permission is required"}
	}
	if end < len(filtered) {
		result["next_offset"] = end
	}
	return result
}

func assistantAdminPricingCostCoverage() map[string]any {
	settings := dynamic_pricing_setting.GetSetting()
	result := map[string]any{"enabled": settings.Enabled, "configured_cost_count": len(settings.ChannelCosts)}
	if err := settings.Validate(); err != nil {
		result["configuration_valid"] = false
	} else {
		result["configuration_valid"] = true
	}
	// Fetch identifiers only. Channel credentials never enter the tool process.
	var ids []int
	if err := model.DB.Model(&model.Channel{}).Where("status = ?", common.ChannelStatusEnabled).Order("id").Limit(assistantAdminMaxChannelRows+1).Pluck("id", &ids).Error; err != nil {
		result["coverage_available"] = false
		return result
	}
	result["coverage_available"] = true
	result["coverage_truncated"] = len(ids) > assistantAdminMaxChannelRows
	ids = ids[:min(len(ids), assistantAdminMaxChannelRows)]
	missing := make([]int, 0)
	for _, id := range ids {
		cost, exists := settings.ChannelCosts[strconv.Itoa(id)]
		if !exists || !assistantAuditNonNegative(cost) || cost == 0 {
			missing = append(missing, id)
		}
	}
	result["checked_active_channels"] = len(ids)
	result["missing_cost_count_in_checked_channels"] = len(missing)
	result["missing_cost_channel_ids"] = missing[:min(len(missing), assistantAdminPricingAuditPageSize)]
	result["missing_cost_ids_truncated"] = len(missing) > assistantAdminPricingAuditPageSize
	return result
}
