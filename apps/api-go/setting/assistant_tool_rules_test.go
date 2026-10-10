// Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later
package setting

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAssistantToolRulesDefaultsAndBoundaries(t *testing.T) {
	_, policy, err := NormalizeAssistantToolPolicy("")
	require.NoError(t, err)
	require.False(t, policy.AllowedAtLevel("get_usage_summary", 0))
	for level := 1; level <= 6; level++ {
		require.True(t, policy.AllowedAtLevel("get_usage_summary", level))
	}
	require.False(t, policy.AllowedAtLevel("get_admin_channels", 4))
	require.True(t, policy.AllowedAtLevel("get_admin_channels", 5))
	require.False(t, policy.AllowedAtLevel("get_admin_server_config", 5))
	require.True(t, policy.AllowedAtLevel("get_admin_server_config", 6))
	require.False(t, policy.AllowedAtLevel("grant_l1_access", 1))
	require.False(t, policy.MarketServiceAllowed("unconfigured"))
	for level := 0; level < 5; level++ {
		require.Equal(t, 10, policy.WeeklyDiscountLimit(level))
	}
	require.Zero(t, policy.WeeklyDiscountLimit(5))
	require.Zero(t, policy.WeeklyDiscountLimit(6))
	raw := `{"version":1,"groups":{"market_connections":false},"rules":{"get_usage_summary":{"min_level":2,"max_level":4},"prepare_weekly_discount":{"min_level":0,"max_level":4,"discount_percent_by_level":{"0":0,"1":20,"2":25,"3":30,"4":40}},"call_market_tool":{"min_level":1,"max_level":6,"market_service_ids":["service-b","service-a"]}}}`
	canonical, policy, err := NormalizeAssistantToolPolicy(raw)
	require.NoError(t, err)
	require.Contains(t, canonical, `["service-a","service-b"]`)
	require.False(t, policy.AllowedAtLevel("get_usage_summary", 1))
	require.True(t, policy.AllowedAtLevel("get_usage_summary", 2))
	require.False(t, policy.AllowedAtLevel("get_usage_summary", 5))
	require.Equal(t, 25, policy.WeeklyDiscountLimit(2))
	require.Zero(t, policy.WeeklyDiscountLimit(0))
	require.False(t, policy.MarketServiceAllowed("service-a"))
}
func TestAssistantToolRulesRejectAmbiguousAndPrivilegeWidening(t *testing.T) {
	for _, rule := range []string{
		`"get_usage_summary":{"min_level":0,"max_level":6}`,
		`"get_admin_channels":{"min_level":4,"max_level":6}`,
		`"get_admin_server_config":{"min_level":5,"max_level":6}`,
		`"grant_l1_access":{"min_level":0,"max_level":1}`,
		`"search_web":{"min_level":0,"min_level":1,"max_level":6}`,
		`"search_web":{"min_level":null,"max_level":6}`,
		`"search_web":{"min_level":0,"max_level":7}`,
		`"search_web":{"min_level":4,"max_level":2}`,
		`"search_web":{"min_level":0,"max_level":6,"unknown":true}`,
		`"unknown":{"min_level":0,"max_level":6}`,
		`"prepare_weekly_discount":{"min_level":0,"max_level":6,"discount_percent_by_level":{"1":100}}`,
		`"prepare_weekly_discount":{"min_level":0,"max_level":6,"discount_percent_by_level":{"5":10}}`,
		`"prepare_weekly_discount":{"min_level":0,"max_level":6,"discount_percent_by_level":{"1":10,"1":20}}`,
		`"search_web":{"min_level":0,"max_level":6,"discount_percent_by_level":{"1":10}}`,
		`"call_market_tool":{"min_level":1,"max_level":6,"market_service_ids":["same","same"]}`,
		`"call_market_tool":{"min_level":1,"max_level":6,"market_service_ids":null}`,
		`"create_site_issue":{"min_level":0,"max_level":6,"default_visibility":"public"}`,
	} {
		_, _, err := NormalizeAssistantToolPolicy(`{"version":1,"rules":{` + rule + `}}`)
		require.Error(t, err, rule)
	}
}
