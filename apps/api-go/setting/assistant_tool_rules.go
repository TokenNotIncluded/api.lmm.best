// Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later
package setting

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Rules only narrow an existing capability. They never replace live role,
// ownership, confirmation, payment, registration or service-grant checks.
type AssistantToolRule struct {
	Description *string `json:"description,omitempty"`
	ParameterDescriptions map[string]string `json:"parameter_descriptions,omitempty"`
	MinLevel               int            `json:"min_level"`
	MaxLevel               int            `json:"max_level"`
	DiscountPercentByLevel map[string]int `json:"discount_percent_by_level,omitempty"`
	MarketServiceIDs       []string       `json:"market_service_ids,omitempty"`
	DefaultVisibility      string         `json:"default_visibility,omitempty"`
}

func DefaultAssistantToolRule(name string) AssistantToolRule {
	rule := AssistantToolRule{MaxLevel: 6}
	for _, group := range assistantToolCatalogue {
		for _, tool := range group.Tools {
			if tool.Name != name {
				continue
			}
			switch tool.Access {
			case "l0":
				rule.MaxLevel = 0
			case "l1":
				rule.MinLevel = 1
			case "admin":
				rule.MinLevel = 5
			case "root":
				rule.MinLevel = 6
			}
			return rule
		}
	}
	return rule
}

func (policy AssistantToolPolicy) Rule(name string) AssistantToolRule {
	if rule, ok := policy.Rules[name]; ok {
		return rule
	}
	return DefaultAssistantToolRule(name)
}

func (policy AssistantToolPolicy) AllowedAtLevel(name string, level int) bool {
	rule := policy.Rule(name)
	return policy.Enabled(name) && level >= rule.MinLevel && level <= rule.MaxLevel
}

func AssistantToolAllowedAtLevel(name string, level int) bool {
	assistantSettingsMutex.RLock()
	defer assistantSettingsMutex.RUnlock()
	return assistantToolPolicy.AllowedAtLevel(name, level)
}

// Preserve the old ten-percent ceiling until an operator explicitly changes it.
// Administrators do not receive conversation rewards by default.
func (policy AssistantToolPolicy) WeeklyDiscountLimit(level int) int {
	if level < 0 || level > 6 {
		return 0
	}
	if limit, ok := policy.Rule("prepare_weekly_discount").DiscountPercentByLevel[strconv.Itoa(level)]; ok {
		return limit
	}
	if level >= 5 {
		return 0
	}
	return 10
}

func AssistantWeeklyDiscountLimit(level int) int {
	assistantSettingsMutex.RLock()
	defer assistantSettingsMutex.RUnlock()
	return assistantToolPolicy.WeeklyDiscountLimit(level)
}

func (policy AssistantToolPolicy) MarketServiceAllowed(id string) bool {
	return id != "" && policy.Enabled("call_market_tool") && slices.Contains(policy.Rule("call_market_tool").MarketServiceIDs, id)
}

func decodeAssistantToolRules(decoder *json.Decoder) (map[string]AssistantToolRule, error) {
	invalid := errors.New("invalid assistant tool rules")
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, invalid
	}
	rules := make(map[string]AssistantToolRule)
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		if _, duplicate := rules[name]; err != nil || !ok || duplicate || !AssistantToolKnown(name) {
			return nil, invalid
		}
		if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
			return nil, invalid
		}
		rule := AssistantToolRule{}
		seen := map[string]bool{}
		for decoder.More() {
			token, err := decoder.Token()
			key, ok := token.(string)
			if err != nil || !ok || seen[key] {
				return nil, invalid
			}
			seen[key] = true
			switch key {
			case "description":
				if err := decoder.Decode(&rule.Description); err != nil || rule.Description == nil {
					return nil, invalid
				}
				if err := ValidateAssistantToolDescription(*rule.Description, AssistantToolDescriptionMaxBytes); err != nil {
					return nil, err
				}
			case "parameter_descriptions":
				descriptions, err := decodeAssistantToolParameterDescriptions(decoder)
				if err != nil {
					return nil, err
				}
				rule.ParameterDescriptions = descriptions
			case "min_level", "max_level":
				var value *int
				if err := decoder.Decode(&value); err != nil || value == nil {
					return nil, invalid
				}
				if key == "min_level" {
					rule.MinLevel = *value
				} else {
					rule.MaxLevel = *value
				}
			case "discount_percent_by_level":
				if name != "prepare_weekly_discount" {
					return nil, invalid
				}
				if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
					return nil, invalid
				}
				rule.DiscountPercentByLevel = make(map[string]int)
				for decoder.More() {
					token, err := decoder.Token()
					level, ok := token.(string)
					if _, duplicate := rule.DiscountPercentByLevel[level]; err != nil || !ok || len(level) != 1 || level < "0" || level > "6" || duplicate {
						return nil, invalid
					}
					var value *int
					if err := decoder.Decode(&value); err != nil || value == nil || *value < 0 || *value > 99 || (level >= "5" && *value != 0) {
						return nil, invalid
					}
					rule.DiscountPercentByLevel[level] = *value
				}
				if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
					return nil, invalid
				}
			case "market_service_ids":
				if name != "call_market_tool" {
					return nil, invalid
				}
				if err := decoder.Decode(&rule.MarketServiceIDs); err != nil || rule.MarketServiceIDs == nil || len(rule.MarketServiceIDs) > 64 {
					return nil, invalid
				}
				ids := make(map[string]bool)
				for _, id := range rule.MarketServiceIDs {
					if len(id) > 128 || strings.TrimSpace(id) != id || id == "" || ids[id] {
						return nil, invalid
					}
					ids[id] = true
				}
				slices.Sort(rule.MarketServiceIDs)
			case "default_visibility":
				if name != "create_site_issue" {
					return nil, invalid
				}
				if err := decoder.Decode(&rule.DefaultVisibility); err != nil || (rule.DefaultVisibility != "user" && rule.DefaultVisibility != "admin") {
					return nil, invalid
				}
			default:
				return nil, invalid
			}
		}
		if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
			return nil, invalid
		}
		defaults := DefaultAssistantToolRule(name)
		if !seen["min_level"] || !seen["max_level"] || rule.MinLevel < defaults.MinLevel || rule.MaxLevel > defaults.MaxLevel || rule.MinLevel > rule.MaxLevel {
			return nil, fmt.Errorf("invalid level range for %s", name)
		}
		rules[name] = rule
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, invalid
	}
	return rules, nil
}
