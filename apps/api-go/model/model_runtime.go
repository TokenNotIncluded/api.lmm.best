// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package model

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
)

type ModelRuntimeState struct {
	Status     string `json:"status"`
	Source     string `json:"source"`
	ObservedAt int64  `json:"observed_at"`
	Notice     string `json:"notice,omitempty"`
	ExpiresAt  int64  `json:"expires_at,omitempty"`
}

func ValidateModelOperationalNotice(m *Model, now int64) error {
	switch m.OperationalStatus {
	case "", "auto":
		m.OperationalStatus = "auto"
		m.OperationalNotice = ""
		m.OperationalUntil = 0
		return nil
	case "congested", "maintenance", "unavailable":
	default:
		return errors.New("invalid model operational status")
	}
	if !utf8.ValidString(m.OperationalNotice) || utf8.RuneCountInString(m.OperationalNotice) > 280 {
		return errors.New("public operational notice must not exceed 280 characters")
	}
	if m.OperationalUntil <= now || m.OperationalUntil > now+30*24*60*60 {
		return errors.New("operational notice must expire within the next 30 days")
	}
	return nil
}

// ModelRuntimeStates reads configuration only. It never sends upstream requests or
// equates recent success with health. No channel identifiers/credentials are exposed.
func ModelRuntimeStates(ctx context.Context, names []string, groups map[string]string, authenticated, access bool, now int64) (map[string]ModelRuntimeState, error) {
	var metadata []Model
	if err := DB.WithContext(ctx).Select("model_name", "status", "name_rule", "operational_status", "operational_notice", "operational_until").Order("id").Find(&metadata).Error; err != nil {
		return nil, err
	}
	type route struct {
		Model         string
		Group         string
		Enabled       bool
		ChannelStatus int
		ChannelInfo   ChannelInfo `gorm:"column:channel_info"`
	}
	var routes []route
	groupColumn := commonGroupCol
	if groupColumn == "" {
		groupColumn = `"group"`
	}
	if err := DB.WithContext(ctx).Table("abilities").Select("abilities.model, abilities."+groupColumn+" as "+groupColumn+", abilities.enabled, channels.status as channel_status, channels.channel_info").Joins("JOIN channels ON channels.id = abilities.channel_id").Where("abilities.model IN ?", names).Scan(&routes).Error; err != nil {
		return nil, err
	}
	result := make(map[string]ModelRuntimeState, len(names))
	for _, name := range names {
		state := ModelRuntimeState{Status: "not_listed", Source: "routing_configuration", ObservedAt: now}
		var meta *Model
		for i := range metadata {
			m := &metadata[i]
			if m.NameRule == NameRuleExact && m.ModelName == name {
				meta = m
				break
			}
		}
		if meta == nil {
			for _, rule := range []int{NameRulePrefix, NameRuleSuffix, NameRuleContains} {
				for i := range metadata {
					m := &metadata[i]
					if m.NameRule == rule && ((rule == NameRulePrefix && strings.HasPrefix(name, m.ModelName)) || (rule == NameRuleSuffix && strings.HasSuffix(name, m.ModelName)) || (rule == NameRuleContains && strings.Contains(name, m.ModelName))) {
						meta = m
						break
					}
				}
				if meta != nil {
					break
				}
			}
		}
		known, eligible, enabled, hasRoutes := meta != nil && meta.NameRule == NameRuleExact, false, false, false
		for _, r := range routes {
			if r.Model != name {
				continue
			}
			known = true
			hasRoutes = true
			_, allowed := groups[r.Group]
			if !authenticated || allowed {
				eligible = true
				if r.Enabled && r.ChannelStatus == common.ChannelStatusEnabled {
					enabled = enabled || runtimeChannelHasEnabledKey(r.ChannelInfo)
				}
			}
		}
		if !known || (meta != nil && meta.Status != 1) {
			result[name] = state
			continue
		}
		if authenticated && (!access || (hasRoutes && !eligible)) {
			state.Status = "no_access"
			state.Source = "account_permissions"
			result[name] = state
			continue
		}
		state.Status = "unavailable"
		if enabled {
			state.Status = "available"
		}
		if meta != nil && meta.OperationalUntil > now {
			switch meta.OperationalStatus {
			case "maintenance", "congested", "unavailable":
				state.Status = meta.OperationalStatus
				state.Source = "administrator_notice"
				state.Notice = meta.OperationalNotice
				state.ExpiresAt = meta.OperationalUntil
			}
		}
		result[name] = state
	}
	return result, nil
}

func runtimeChannelHasEnabledKey(info ChannelInfo) bool {
	if !info.IsMultiKey {
		return true
	}
	if info.MultiKeySize <= 0 {
		return false
	}
	disabled := 0
	for index, status := range info.MultiKeyStatusList {
		if index >= 0 && index < info.MultiKeySize && status != common.ChannelStatusEnabled {
			disabled++
		}
	}
	return disabled < info.MultiKeySize
}
