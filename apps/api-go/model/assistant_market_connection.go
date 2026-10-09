// Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later
package model

import (
	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
	"time"
)

const AssistantToolMarketClient = "builtin-assistant"

type AssistantMarketConnection struct {
	ServiceID string `json:"service_id"`
	ToolID    string `json:"tool_id"`
	VersionID string `json:"version_id"`
}
type AssistantMarketLimits struct {
	MaxPriceQuota   int `json:"max_price_quota"`
	MaxTotalQuota   int `json:"max_total_quota"`
	MaxCalls        int `json:"max_calls"`
	LifetimeSeconds int `json:"lifetime_seconds"`
}

// Loading, the separately confirmed grant, and the one-time browser preview
// commit together. Reuse market checks, billing units and immutable versions.
func ConnectAssistantMarketToolTX(tx *gorm.DB, userID int, input AssistantMarketConnection, limits AssistantMarketLimits) (*ToolMarketGrant, error) {
	if limits.MaxPriceQuota < 0 || limits.MaxTotalQuota < limits.MaxPriceQuota || limits.MaxCalls < 1 || limits.MaxCalls > 100 || limits.LifetimeSeconds < 60 || limits.LifetimeSeconds > 86400 {
		return nil, ErrToolMarketInput
	}
	_, policy, err := ReadAssistantToolPolicyDB(tx)
	if err != nil {
		return nil, err
	}
	if err := RequireAssistantMarketAccessDB(tx, userID, input.ServiceID); err != nil {
		return nil, err
	}
	level, err := AssistantToolLevelDB(tx, userID)
	if err != nil {
		return nil, err
	}
	if !policy.MarketServiceAllowed(input.ServiceID) || !policy.AllowedAtLevel("connect_market_tool", level) {
		return nil, ErrToolMarketDenied
	}
	service, _, err := marketLiveTool(tx, userID, input.ToolID, input.VersionID)
	if err != nil {
		return nil, err
	}
	if service.ID != input.ServiceID {
		return nil, ErrToolMarketDenied
	}
	var version ToolMarketVersion
	if err := tx.Select("execution_type").First(&version, "id = ?", input.VersionID).Error; err != nil {
		return nil, err
	}
	// This bridge never dispatches image generation or another built-in service.
	if version.ExecutionType != "remote" {
		return nil, ErrToolMarketDenied
	}
	if err := setToolMarketInstallationDB(tx, userID, AssistantToolMarketClient, input.ToolID, input.VersionID, true); err != nil {
		return nil, err
	}
	return createToolMarketGrantDB(tx, userID, ToolMarketGrant{ClientID: AssistantToolMarketClient, ToolID: input.ToolID, VersionID: input.VersionID, MaxPriceQuota: limits.MaxPriceQuota, MaxTotalQuota: limits.MaxTotalQuota, MaxCalls: limits.MaxCalls, ExpiresAt: time.Now().Unix() + int64(limits.LifetimeSeconds)})
}

func AssistantMarketGrants(userID int, toolID, versionID string) ([]ToolMarketGrant, error) {
	rows := []ToolMarketGrant{}
	err := DB.Where("user_id = ? AND tool_id = ? AND version_id = ? AND revoked_at = 0 AND expires_at > ?", userID, toolID, versionID, common.GetTimestamp()).Scopes(marketExactTextScope("client_id", AssistantToolMarketClient)).Order("created_at DESC").Limit(10).Find(&rows).Error
	return rows, err
}

// Keep the policy row locked through reservation/dispatch. An old grant is not
// permission to invoke a service after the operator removes it from the assistant.
func RequireAssistantMarketAccessDB(tx *gorm.DB, userID int, serviceID string) error {
	_, policy, err := ReadAssistantToolPolicyDB(tx)
	if err != nil {
		return err
	}
	level, err := AssistantToolLevelDB(tx, userID)
	if err != nil {
		return err
	}
	if !policy.MarketServiceAllowed(serviceID) || !policy.AllowedAtLevel("call_market_tool", level) {
		return ErrToolMarketDenied
	}
	return nil
}
