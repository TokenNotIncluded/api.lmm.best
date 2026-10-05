package model

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestToolMarketUsagePriceRoundingAndNormalization(t *testing.T) {
	// $0.042 per million input tokens * 70 * 500,000 quota/$.
	price, err := toolMarketTokenQuota(1470000, 394)
	require.NoError(t, err)
	require.Equal(t, 580, price)
	tool := ToolMarketToolInput{BillingMode: "input_tokens", InputTokenPriceQuota: 1470000, MaxInputTokens: 200000}
	require.NoError(t, normalizeToolMarketPricing(&tool))
	require.Equal(t, 294000, tool.PriceQuota)
	tool.BillingMode = "unknown"
	require.Error(t, normalizeToolMarketPricing(&tool))
}

func TestToolMarketUsageSettlementRefundsUnusedHoldOnce(t *testing.T) {
	f := newMarketFixture(t, 100)
	key := marketTestMetering(t, f, []string{"input_tokens"})
	require.NoError(t, f.db.Model(&ToolMarketToolVersion{}).Where("tool_id = ? AND version_id = ?", f.tool.ToolID, f.tool.VersionID).Updates(map[string]any{"billing_mode": "input_tokens", "input_token_price_quota": 1000000, "max_input_tokens": 100}).Error)
	require.NoError(t, SetToolMarketBudget(f.buyer.Id, "account", "", 1000))
	call, created, err := ReserveToolMarketCall(f.input("metered"))
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, 900, marketTestBalance(t, f.db, f.buyer.Id))
	started, err := StartToolMarketCall(call.ID)
	require.NoError(t, err)
	require.True(t, started)
	// Mutable service settings do not change the reserved immutable rate.
	require.NoError(t, f.db.Model(&ToolMarketToolVersion{}).Where("tool_id = ? AND version_id = ?", f.tool.ToolID, f.tool.VersionID).Update("input_token_price_quota", 9000000).Error)
	require.NoError(t, RecordToolMarketResult(call.ID, true, marketTestSignedReceipt(t, *call, key, map[string]int64{"input_tokens": 40})))
	require.NoError(t, FinishToolMarketCall(call.ID, true))
	require.NoError(t, FinishToolMarketCall(call.ID, true))
	require.Equal(t, 960, marketTestBalance(t, f.db, f.buyer.Id))
	require.Equal(t, 35, marketTestBalance(t, f.db, f.author.Id))
	require.Equal(t, 5, marketTestBalance(t, f.db, f.root.Id))
	require.NoError(t, f.db.First(call, "id = ?", call.ID).Error)
	require.Equal(t, 40, call.PriceQuota)
	require.Equal(t, 40, call.InputTokens)
	require.NoError(t, f.db.First(f.grant, "id = ?", f.grant.ID).Error)
	require.Zero(t, f.grant.ReservedQuota)
	require.Equal(t, 40, f.grant.SpentQuota)
	var budget ToolMarketBudget
	require.NoError(t, f.db.Where("user_id = ?", f.buyer.Id).First(&budget).Error)
	require.Zero(t, budget.ReservedQuota)
	require.Equal(t, 40, budget.SpentQuota)
	replay, err := LookupToolMarketReplay(f.input("metered"))
	require.NoError(t, err)
	require.Equal(t, 40, replay.PriceQuota)
}

func TestToolMarketUsageMissingOrOverCapCannotCharge(t *testing.T) {
	for _, payload := range []string{`{"content":[]}`, `{"structuredContent":{"usage":{"input_tokens":101}}}`} {
		t.Run(payload, func(t *testing.T) {
			f := newMarketFixture(t, 100)
			_ = marketTestMetering(t, f, []string{"input_tokens"})
			require.NoError(t, f.db.Model(&ToolMarketToolVersion{}).Where("tool_id = ? AND version_id = ?", f.tool.ToolID, f.tool.VersionID).Updates(map[string]any{"billing_mode": "input_tokens", "input_token_price_quota": 1000000, "max_input_tokens": 100}).Error)
			call, _, err := ReserveToolMarketCall(f.input("invalid"))
			require.NoError(t, err)
			_, err = StartToolMarketCall(call.ID)
			require.NoError(t, err)
			require.Error(t, RecordToolMarketResult(call.ID, true, json.RawMessage(payload)))
			require.Error(t, FinishToolMarketCall(call.ID, true))
			require.NoError(t, FinishToolMarketCall(call.ID, false))
			require.Equal(t, 1000, marketTestBalance(t, f.db, f.buyer.Id))
		})
	}
}
