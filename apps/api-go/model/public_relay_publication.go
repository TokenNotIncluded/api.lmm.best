/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/

package model

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"gorm.io/gorm"
)

// Management responses retain review notes and IDs, without credentials. The
// available amount must match withdrawal even after a wallet-unit migration.
type PublicRelayManagementView struct {
	PublicRelayContribution
	AvailableTipQuota int64 `json:"available_tip_quota"`
}

func publicRelayManagementViews(items []PublicRelayContribution) ([]PublicRelayManagementView, error) {
	views := make([]PublicRelayManagementView, 0, len(items))
	for _, item := range items {
		available, err := publicRelayAvailableCreditTx(DB, &item)
		if err != nil {
			return nil, err
		}
		views = append(views, PublicRelayManagementView{PublicRelayContribution: item, AvailableTipQuota: available})
	}
	return views, nil
}

// A surviving contribution ID alone does not prove that a channel can route.
func publicRelayVisibleQuery(db *gorm.DB, group string) *gorm.DB {
	channels := db.Session(&gorm.Session{NewDB: true}).Model(&Channel{}).
		Select("channels.id").
		Joins("JOIN abilities ON abilities.channel_id = channels.id").
		Where("channels.status = ? AND channels.type <> ? AND abilities.enabled = ? AND abilities."+commonGroupCol+" = ?", common.ChannelStatusEnabled, constant.ChannelTypeOpenHuman, true, group)
	return db.Where(map[string]interface{}{"status": PublicRelayApproved, "group": group}).Where("channel_id IN (?)", channels)
}

func preparePublicRelayChannel(item *PublicRelayContribution) (*Channel, error) {
	var input ChannelCreationInput
	if json.Unmarshal([]byte(item.ChannelConfig), &input) != nil || input.Channel == nil {
		return nil, ErrPublicRelayInvalidInput
	}
	source := input.Channel
	if _, ok := constant.ChannelTypeNames[source.Type]; !ok || source.Type == constant.ChannelTypeUnknown || source.Type == constant.ChannelTypeOpenHuman {
		return nil, ErrPublicRelayInvalidInput
	}
	if strings.TrimSpace(source.Name) != item.Name || strings.TrimSpace(source.Models) != item.Models || strings.TrimSpace(source.Key) == "" || strings.TrimSpace(item.Models) == "" {
		return nil, ErrPublicRelayInvalidInput
	}
	baseURL := ""
	if source.BaseURL != nil {
		baseURL = *source.BaseURL
	}
	normalized, err := normalizePublicRelayURL(baseURL)
	if err != nil || normalized != item.BaseURL {
		return nil, ErrPublicRelayInvalidURL
	}
	// Empty provider defaults such as local Ollama must not reach a private
	// service merely because the contributor left the address blank.
	if normalized == "" && source.Type < len(constant.ChannelBaseURLs) && constant.ChannelBaseURLs[source.Type] != "" {
		if _, err := normalizePublicRelayURL(constant.ChannelBaseURLs[source.Type]); err != nil {
			return nil, err
		}
	}
	if err := ValidateChannel(source, true); err != nil {
		return nil, err
	}
	if strings.TrimSpace(source.GetSetting().Proxy) != "" {
		return nil, fmt.Errorf("shared channels cannot configure a server proxy")
	}
	// Copy configuration explicitly: contributors cannot choose an existing
	// channel ID, another group's balance policy, priority, or runtime counters.
	channel := &Channel{
		Type: source.Type, Key: source.Key, Name: item.Name, Models: item.Models,
		BaseURL: &normalized, Group: operation_setting.GetPublicRelayGroup(),
		Status: common.ChannelStatusEnabled, PublicRelayContributionId: item.Id,
		OpenAIOrganization: source.OpenAIOrganization, TestModel: source.TestModel,
		ModelMapping: source.ModelMapping, StatusCodeMapping: source.StatusCodeMapping,
		AutoBan: source.AutoBan, Other: source.Other, Setting: source.Setting,
		ParamOverride: source.ParamOverride, HeaderOverride: source.HeaderOverride,
		OtherSettings: source.OtherSettings, Remark: source.Remark,
	}
	input.Channel = channel
	// One submission owns one channel. Batch credentials become a multi-key
	// pool; never expose a secret-key prefix in the public channel name.
	if input.Mode == "batch" {
		input.Mode = "multi_to_single"
	}
	input.BatchAddSetKeyPrefix2Name = false
	prepared, err := PrepareChannelCreation(input)
	if err != nil {
		return nil, err
	}
	if len(prepared) != 1 {
		return nil, ErrPublicRelayInvalidInput
	}
	return &prepared[0], nil
}
