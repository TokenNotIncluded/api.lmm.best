package model

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func marketTestMetering(t *testing.T, f marketFixture, metrics []string) []byte {
	t.Helper()
	key := []byte("test-only-collector-key-32-bytes!!")
	require.NoError(t, f.db.Model(&ToolMarketToolVersion{}).Where("version_id = ? AND tool_id = ?", f.tool.VersionID, f.tool.ToolID).Update("remote_digest", "test-meter-definition").Error)
	var v ToolMarketVersion
	require.NoError(t, f.db.First(&v, "id = ?", f.tool.VersionID).Error)
	cfg := toolMarketMeteringConfig{Adapters: []toolMarketMeteringAdapter{{ID: "test-collector", ServiceID: f.service.ID, VersionID: f.tool.VersionID, Endpoint: v.Endpoint, ToolName: f.tool.Name, RemoteDigest: "test-meter-definition", Metrics: metrics, KeyBase64: base64.StdEncoding.EncodeToString(key)}}}
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "meters.json")
	require.NoError(t, os.WriteFile(path, data, 0600))
	t.Setenv("LMM_TOOL_MARKET_METERING_CONFIG", path)
	return key
}
func marketTestSignedReceipt(t *testing.T, c ToolMarketCall, key []byte, quantities map[string]int64) json.RawMessage {
	t.Helper()
	r := toolMarketMeteringReceipt{AdapterID: "test-collector", Context: ToolMarketMeteringContext(c), Quantities: quantities}
	r.ResultDigest, _ = ToolMarketMeteringResultDigest(json.RawMessage(`{"structuredContent":{"value":"completed"}}`))
	mac := hmac.New(sha256.New, key)
	mac.Write(toolMarketMeteringMessage(r))
	r.Signature = hex.EncodeToString(mac.Sum(nil))
	data, err := json.Marshal(map[string]any{"structuredContent": map[string]any{"value": "completed"}, "_meta": map[string]any{"lmm_metering": r}})
	require.NoError(t, err)
	return data
}
func TestToolMarketUntrustedUsageBlockedBeforeHold(t *testing.T) {
	f := newMarketFixture(t, 100)
	t.Setenv("LMM_TOOL_MARKET_METERING_CONFIG", "")
	require.NoError(t, f.db.Model(&ToolMarketToolVersion{}).Where("version_id = ?", f.tool.VersionID).Updates(map[string]any{"billing_mode": "input_tokens", "input_token_price_quota": 1000000, "max_input_tokens": 100}).Error)
	_, _, err := ReserveToolMarketCall(f.input("forged-usage"))
	require.ErrorIs(t, err, ErrToolMarketMetering)
	require.Equal(t, 1000, marketTestBalance(t, f.db, f.buyer.Id))
	draft := marketTestDraft(100)
	draft.Tools[0].BillingMode = "input_tokens"
	draft.Tools[0].InputTokenPriceQuota = 1000000
	draft.Tools[0].MaxInputTokens = 100
	_, err = SaveToolMarketDraft(f.author.Id, f.service.ID, draft)
	require.ErrorIs(t, err, ErrToolMarketMetering)
	// Even a root publisher cannot manufacture an authorized collector.
	_, err = SaveToolMarketDraft(f.root.Id, "", draft)
	require.ErrorIs(t, err, ErrToolMarketMetering)
}
func TestToolMarketComputeSettlementAndReceiptBinding(t *testing.T) {
	f := newMarketFixture(t, 300)
	key := marketTestMetering(t, f, []string{"cpu_core_milliseconds", "memory_mib_seconds"})
	rules := []ToolMarketBillingRule{{"cpu_core_milliseconds", 100, 1000}, {"memory_mib_seconds", 200, 1024}}
	tool := f.tool
	tool.BillingMode = "metered"
	tool.BillingRules = rules
	tool.RemoteDigest = "test-meter-definition"
	require.NoError(t, f.db.Save(&tool).Error)
	call, _, err := ReserveToolMarketCall(f.input("compute"))
	require.NoError(t, err)
	require.Equal(t, 300, call.PriceQuota)
	_, err = StartToolMarketCall(call.ID)
	require.NoError(t, err)
	quantities := map[string]int64{"cpu_core_milliseconds": 500, "memory_mib_seconds": 512}
	data := marketTestSignedReceipt(t, *call, key, quantities)
	for _, change := range []func(*toolMarketMeteringReceipt){
		func(r *toolMarketMeteringReceipt) { r.Context.CallID = "another-call" },
		func(r *toolMarketMeteringReceipt) { r.Context.VersionID = "another-version" },
		func(r *toolMarketMeteringReceipt) { r.Context.ToolID = "another-tool" },
		func(r *toolMarketMeteringReceipt) { r.Context.InputDigest = "other-arguments" },
		func(r *toolMarketMeteringReceipt) { r.Context.PricingDigest = "other-price" },
		func(r *toolMarketMeteringReceipt) { r.Quantities["cpu_core_milliseconds"] = 999 },
		func(r *toolMarketMeteringReceipt) { r.AdapterID = "publisher" },
	} {
		var envelope struct {
			StructuredContent json.RawMessage `json:"structuredContent"`
			Meta              struct {
				Receipt toolMarketMeteringReceipt `json:"lmm_metering"`
			} `json:"_meta"`
		}
		require.NoError(t, json.Unmarshal(data, &envelope))
		change(&envelope.Meta.Receipt)
		bad, _ := json.Marshal(envelope)
		require.Error(t, RecordToolMarketResult(call.ID, true, bad))
	}
	require.Error(t, RecordToolMarketResult(call.ID, true, json.RawMessage(`{"structuredContent":{"usage":{"cpu_core_milliseconds":500}}}`)))
	require.Error(t, RecordToolMarketResult(call.ID, true, marketTestSignedReceipt(t, *call, key, nil)))
	require.Error(t, RecordToolMarketResult(call.ID, true, marketTestSignedReceipt(t, *call, key, map[string]int64{"cpu_core_milliseconds": 1001, "memory_mib_seconds": 512})))
	require.NoError(t, RecordToolMarketResult(call.ID, true, data))
	require.NoError(t, FinishToolMarketCall(call.ID, true))
	require.NoError(t, FinishToolMarketCall(call.ID, true))
	require.NoError(t, f.db.First(call, "id = ?", call.ID).Error)
	require.Equal(t, 150, call.PriceQuota)
	require.Equal(t, quantities, call.UsageQuantities)
	require.Equal(t, 850, marketTestBalance(t, f.db, f.buyer.Id))
	require.Equal(t, 132, marketTestBalance(t, f.db, f.author.Id))
	require.Equal(t, 18, marketTestBalance(t, f.db, f.root.Id))
	require.NoError(t, f.db.First(f.grant, "id = ?", f.grant.ID).Error)
	require.Zero(t, f.grant.ReservedQuota)
	require.Equal(t, 150, f.grant.SpentQuota)
	// Removing the collector closes future execution; settled replay stays exactly once.
	t.Setenv("LMM_TOOL_MARKET_METERING_CONFIG", "")
	_, _, err = ReserveToolMarketCall(f.input("compute-new"))
	require.ErrorIs(t, err, ErrToolMarketMetering)
	replay, err := LookupToolMarketReplay(f.input("compute"))
	require.NoError(t, err)
	require.Equal(t, 150, replay.PriceQuota)
}
func TestToolMarketLegacyUnauthenticatedOutcomeCannotRecoverCharge(t *testing.T) {
	f := newMarketFixture(t, 100)
	_ = marketTestMetering(t, f, []string{"input_tokens"})
	require.NoError(t, f.db.Model(&ToolMarketToolVersion{}).Where("version_id = ?", f.tool.VersionID).Updates(map[string]any{"billing_mode": "input_tokens", "input_token_price_quota": 1000000, "max_input_tokens": 100}).Error)
	call, _, err := ReserveToolMarketCall(f.input("old-held"))
	require.NoError(t, err)
	_, err = StartToolMarketCall(call.ID)
	require.NoError(t, err)
	require.NoError(t, f.db.Create(&ToolMarketResult{CallID: call.ID, UserID: f.buyer.Id, Success: true, InputTokens: 40, UsageRecorded: true}).Error)
	require.Error(t, FinishToolMarketCall(call.ID, true))
	require.Equal(t, 900, marketTestBalance(t, f.db, f.buyer.Id))
	require.NoError(t, f.db.Model(&ToolMarketCall{}).Where("id = ?", call.ID).Update("resolve_by", 1).Error)
	require.NoError(t, ExpireToolMarketCall(call.ID))
	require.Equal(t, 1000, marketTestBalance(t, f.db, f.buyer.Id))
}
func TestToolMarketMetricBoundsAndIntegerArithmetic(t *testing.T) {
	for metric, scale := range toolMarketMetricScales {
		rules := []ToolMarketBillingRule{{metric, 7, scale * 2}}
		cap, err := toolMarketRulesQuota(rules, nil)
		require.NoError(t, err)
		require.Equal(t, 14, cap)
		cost, err := toolMarketRulesQuota(rules, map[string]int64{metric: scale})
		require.NoError(t, err)
		require.Equal(t, 7, cost)
		_, err = toolMarketRulesQuota(rules, map[string]int64{metric: scale*2 + 1})
		require.Error(t, err)
		_, err = toolMarketRulesQuota(rules, map[string]int64{metric: -1})
		require.Error(t, err)
	}
	_, err := toolMarketRulesQuota([]ToolMarketBillingRule{{"images", 1, 1}, {"images", 2, 2}}, nil)
	require.Error(t, err)
	_, err = toolMarketRulesQuota([]ToolMarketBillingRule{{"unknown", 1, 1}}, nil)
	require.Error(t, err)
	_, err = toolMarketRulesQuota([]ToolMarketBillingRule{{"images", 1000000000000, 1000000000000}}, nil)
	require.Error(t, err)
}

func TestToolMarketMeteringConfigFailsClosed(t *testing.T) {
	f := newMarketFixture(t, 100)
	key := marketTestMetering(t, f, []string{"input_tokens"})
	_ = key
	path := os.Getenv("LMM_TOOL_MARKET_METERING_CONFIG")

	// Read the actual endpoint from the immutable version to avoid fixture assumptions.
	var v ToolMarketVersion
	require.NoError(t, f.db.First(&v, "id = ?", f.tool.VersionID).Error)
	require.NotEmpty(t, ToolMarketMeteringMetrics(f.service.ID, v.Endpoint, f.tool.Name))
	linked := filepath.Join(t.TempDir(), "hardlink.json")
	require.NoError(t, os.Link(path, linked))
	require.Empty(t, ToolMarketMeteringMetrics(f.service.ID, v.Endpoint, f.tool.Name))
	require.NoError(t, os.Remove(linked))
	require.NoError(t, os.Chmod(path, 0644))
	require.Empty(t, ToolMarketMeteringMetrics(f.service.ID, v.Endpoint, f.tool.Name))
	require.NoError(t, os.Chmod(path, 0600))
	require.NoError(t, os.WriteFile(path, []byte(`{"adapters":[{"id":"weak","key_base64":"YQ=="}]}`), 0600))
	require.Empty(t, ToolMarketMeteringMetrics(f.service.ID, v.Endpoint, f.tool.Name))
	require.NoError(t, os.WriteFile(path, []byte(`{"adapters":[],"trust_all":true}`), 0600))
	_, err := readToolMarketMeteringAdapters()
	require.ErrorIs(t, err, ErrToolMarketMetering)
}

func TestToolMarketReviewCannotAuthorizeCollector(t *testing.T) {
	f := newMarketFixture(t, 100)
	t.Setenv("LMM_TOOL_MARKET_METERING_CONFIG", "")
	draft, err := SaveToolMarketDraft(f.author.Id, f.service.ID, marketTestDraft(100))
	require.NoError(t, err)
	// Simulate an older version stored before this upgrade, not a publisher API.
	require.NoError(t, f.db.Model(&ToolMarketToolVersion{}).Where("version_id = ?", draft.DraftVersionID).Updates(map[string]any{"billing_mode": "input_tokens", "input_token_price_quota": 1000000, "max_input_tokens": 100}).Error)
	require.NoError(t, SubmitToolMarketDraft(f.author.Id, draft.ID, draft.DraftVersionID))
	var v ToolMarketVersion
	require.NoError(t, f.db.First(&v, "id = ?", draft.DraftVersionID).Error)
	require.NoError(t, f.db.Model(&v).Update("validation_digest", v.Digest).Error)
	require.ErrorIs(t, ReviewToolMarketVersion(f.root.Id, draft.ID, draft.DraftVersionID, true, "Independently reviewed definition"), ErrToolMarketMetering)
	require.NoError(t, f.db.First(draft, "id = ?", draft.ID).Error)
	require.Equal(t, f.tool.VersionID, draft.LiveVersionID)
}

func TestToolMarketVerifiedZeroUsageReleasesEntireHold(t *testing.T) {
	f := newMarketFixture(t, 100)
	key := marketTestMetering(t, f, []string{"input_tokens"})
	require.NoError(t, f.db.Model(&ToolMarketToolVersion{}).Where("version_id = ?", f.tool.VersionID).Updates(map[string]any{"billing_mode": "input_tokens", "input_token_price_quota": 1000000, "max_input_tokens": 100}).Error)
	call, _, err := ReserveToolMarketCall(f.input("verified-zero"))
	require.NoError(t, err)
	_, err = StartToolMarketCall(call.ID)
	require.NoError(t, err)
	require.NoError(t, RecordToolMarketResult(call.ID, true, marketTestSignedReceipt(t, *call, key, map[string]int64{"input_tokens": 0})))
	require.NoError(t, FinishToolMarketCall(call.ID, true))
	require.Equal(t, 1000, marketTestBalance(t, f.db, f.buyer.Id))
	require.Zero(t, marketTestBalance(t, f.db, f.author.Id))
	require.Zero(t, marketTestBalance(t, f.db, f.root.Id))
}
