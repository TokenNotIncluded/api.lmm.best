package model

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"sort"
)

var ErrToolMarketMetering = errors.New("tool market usage report is missing or invalid")

// Rates are quota per metric scale. Tool-reported counts use integer base units;
// optional independently collected counts are distinguished by their source.
type ToolMarketBillingRule struct {
	Metric      string `json:"metric"`
	RateQuota   int    `json:"rate_quota"`
	MaxQuantity int64  `json:"max_quantity"`
}

var toolMarketMetricScales = map[string]int64{
	"input_tokens": 1000000, "output_tokens": 1000000, "input_characters": 1000, "output_characters": 1000, "images": 1,
	"audio_milliseconds": 1000, "video_milliseconds": 1000, "cpu_core_milliseconds": 1000,
	"memory_mib_seconds": 1024, "gpu_milliseconds": 1000, "vm_milliseconds": 1000, "storage_mib_seconds": 3686400,
}

func toolMarketRulesQuota(rules []ToolMarketBillingRule, quantities map[string]int64) (int, error) {
	if len(rules) == 0 || len(rules) > len(toolMarketMetricScales) {
		return 0, ErrToolMarketInput
	}
	total := big.NewInt(0)
	seen := map[string]bool{}
	if quantities != nil && len(quantities) != len(rules) {
		return 0, ErrToolMarketInput
	}
	for _, r := range rules {
		scale, ok := toolMarketMetricScales[r.Metric]
		if !ok || seen[r.Metric] || r.RateQuota <= 0 || !marketQuotaValid(r.RateQuota) || r.MaxQuantity <= 0 || r.MaxQuantity > 1000000000000 {
			return 0, ErrToolMarketInput
		}
		seen[r.Metric] = true
		quantity := r.MaxQuantity
		if quantities != nil {
			var exists bool
			quantity, exists = quantities[r.Metric]
			if !exists || quantity < 0 || quantity > r.MaxQuantity {
				return 0, ErrToolMarketInput
			}
		}
		amount := new(big.Int).Mul(big.NewInt(int64(r.RateQuota)), big.NewInt(quantity))
		amount.Add(amount, big.NewInt(scale-1)).Div(amount, big.NewInt(scale))
		total.Add(total, amount)
	}
	if !total.IsInt64() || int64(int(total.Int64())) != total.Int64() || !marketQuotaValid(int(total.Int64())) {
		return 0, ErrToolMarketInput
	}
	return int(total.Int64()), nil
}
func toolMarketToolRules(t ToolMarketToolVersion) []ToolMarketBillingRule {
	if t.BillingMode == "input_tokens" {
		return []ToolMarketBillingRule{{Metric: "input_tokens", RateQuota: t.InputTokenPriceQuota, MaxQuantity: int64(t.MaxInputTokens)}}
	}
	return t.BillingRules
}
func toolMarketCallRules(c ToolMarketCall) []ToolMarketBillingRule {
	return toolMarketToolRules(ToolMarketToolVersion{BillingMode: c.BillingMode, InputTokenPriceQuota: c.InputTokenPriceQuota, MaxInputTokens: c.MaxInputTokens, BillingRules: c.BillingRules})
}

// Operator-only configuration. Each adapter pins an exact version/definition.
// Signing untrusted self-reported usage is prohibited: the collector must own
// the upstream request or get independent provider/hypervisor telemetry.
type toolMarketMeteringAdapter struct {
	ID           string   `json:"id"`
	ServiceID    string   `json:"service_id"`
	VersionID    string   `json:"version_id"`
	Endpoint     string   `json:"endpoint"`
	ToolName     string   `json:"tool_name"`
	RemoteDigest string   `json:"remote_digest"`
	Metrics      []string `json:"metrics"`
	KeyBase64    string   `json:"key_base64"`
}
type toolMarketMeteringConfig struct {
	Adapters []toolMarketMeteringAdapter `json:"adapters"`
}

func readToolMarketMeteringAdapters() ([]toolMarketMeteringAdapter, error) {
	path := os.Getenv("LMM_TOOL_MARKET_METERING_CONFIG")
	if path == "" {
		return nil, nil
	}
	if !filepath.IsAbs(path) {
		return nil, ErrToolMarketMetering
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 65536 || !toolMarketMeteringFileOwned(info) {
		return nil, ErrToolMarketMetering
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, ErrToolMarketMetering
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) || opened.Mode().Perm()&0077 != 0 || !toolMarketMeteringFileOwned(opened) {
		return nil, ErrToolMarketMetering
	}
	var cfg toolMarketMeteringConfig
	dec := json.NewDecoder(io.LimitReader(f, 65537))
	dec.DisallowUnknownFields()
	if dec.Decode(&cfg) != nil || dec.Decode(new(any)) != io.EOF || len(cfg.Adapters) > 100 {
		return nil, ErrToolMarketMetering
	}
	ids := map[string]bool{}
	scopes := map[string]bool{}
	for _, a := range cfg.Adapters {
		key, err := base64.StdEncoding.DecodeString(a.KeyBase64)
		scope := a.ServiceID + ":" + a.VersionID + ":" + a.ToolName
		if err != nil || len(key) < 32 || a.ID == "" || ids[a.ID] || scopes[scope] || a.ServiceID == "" || a.VersionID == "" || a.Endpoint == "" || a.ToolName == "" || a.RemoteDigest == "" || len(a.Metrics) == 0 {
			return nil, ErrToolMarketMetering
		}
		ids[a.ID] = true
		scopes[scope] = true
		seen := map[string]bool{}
		for _, m := range a.Metrics {
			if _, ok := toolMarketMetricScales[m]; !ok || seen[m] {
				return nil, ErrToolMarketMetering
			}
			seen[m] = true
		}
	}
	return cfg.Adapters, nil
}
func ToolMarketMeteringMetrics(serviceID, endpoint, toolName string) []string {
	metrics := make([]string, 0, len(toolMarketMetricScales))
	for metric := range toolMarketMetricScales {
		metrics = append(metrics, metric)
	}
	sort.Strings(metrics)
	return metrics
}
func marketDraftMeteringAllowed(serviceID, endpoint string, t ToolMarketToolInput) bool {
	allowed := ToolMarketMeteringMetrics(serviceID, endpoint, t.Name)
	set := map[string]bool{}
	for _, m := range allowed {
		set[m] = true
	}
	rules := toolMarketToolRules(ToolMarketToolVersion{BillingMode: t.BillingMode, InputTokenPriceQuota: t.InputTokenPriceQuota, MaxInputTokens: t.MaxInputTokens, BillingRules: t.BillingRules})
	for _, r := range rules {
		if !set[r.Metric] {
			return false
		}
	}
	return len(rules) > 0
}
func toolMarketAuthorizedAdapter(serviceID string, v ToolMarketVersion, t ToolMarketToolVersion) (*toolMarketMeteringAdapter, error) {
	adapters, err := readToolMarketMeteringAdapters()
	if err != nil {
		return nil, err
	}
	for _, a := range adapters {
		if a.ServiceID != serviceID || a.VersionID != v.ID || a.Endpoint != v.Endpoint || a.ToolName != t.Name || a.RemoteDigest != t.RemoteDigest {
			continue
		}
		allowed := map[string]bool{}
		for _, m := range a.Metrics {
			allowed[m] = true
		}
		for _, r := range toolMarketToolRules(t) {
			if !allowed[r.Metric] {
				return nil, ErrToolMarketMetering
			}
		}
		return &a, nil
	}
	return nil, ErrToolMarketMetering
}
func ValidateToolMarketMetering(serviceID string, v ToolMarketVersion, t ToolMarketToolVersion) error {
	if t.BillingMode == "" {
		return nil
	}
	if t.BillingMode != "input_tokens" && t.BillingMode != "metered" {
		return ErrToolMarketMetering
	}
	if _, err := toolMarketRulesQuota(toolMarketToolRules(t), nil); err != nil {
		return err
	}
	return nil
}

// Sent in MCP _meta; user arguments cannot replace it. No key is sent.
type ToolMarketMeteringRequest struct {
	CallID        string `json:"call_id"`
	VersionID     string `json:"version_id"`
	ToolID        string `json:"tool_id"`
	InputDigest   string `json:"input_digest"`
	PricingDigest string `json:"pricing_digest"`
}

func ToolMarketMeteringContext(c ToolMarketCall) ToolMarketMeteringRequest {
	return ToolMarketMeteringRequest{c.ID, c.VersionID, c.ToolID, c.InputDigest, marketDigest(toolMarketCallRules(c))}
}

type toolMarketMeteringReceipt struct {
	AdapterID    string                    `json:"adapter_id"`
	Context      ToolMarketMeteringRequest `json:"context"`
	Quantities   map[string]int64          `json:"quantities"`
	Signature    string                    `json:"signature"`
	ResultDigest string                    `json:"result_digest"`
}

// Domain separator + compact encoding/json tuple; map keys sorted by json.
func ToolMarketMeteringResultDigest(data json.RawMessage) (string, error) {
	var envelope map[string]any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if dec.Decode(&envelope) != nil || envelope == nil || dec.Decode(new(any)) != io.EOF {
		return "", ErrToolMarketMetering
	}
	if meta, ok := envelope["_meta"].(map[string]any); ok {
		delete(meta, "lmm_metering")
		if len(meta) == 0 {
			delete(envelope, "_meta")
		}
	}
	return marketDigest(envelope), nil
}
func toolMarketMeteringMessage(r toolMarketMeteringReceipt) []byte {
	data, _ := json.Marshal(struct {
		AdapterID    string                    `json:"adapter_id"`
		Context      ToolMarketMeteringRequest `json:"context"`
		Quantities   map[string]int64          `json:"quantities"`
		ResultDigest string                    `json:"result_digest"`
	}{r.AdapterID, r.Context, r.Quantities, r.ResultDigest})
	return append([]byte("lmm-tool-market-metering-v1\n"), data...)
}
func VerifyToolMarketMeteringResult(db *gorm.DB, c ToolMarketCall, data json.RawMessage) (map[string]int64, error) {
	var v ToolMarketVersion
	var t ToolMarketToolVersion
	if db.First(&v, "id = ? AND service_id = ?", c.VersionID, c.ServiceID).Error != nil || db.First(&t, "version_id = ? AND tool_id = ?", c.VersionID, c.ToolID).Error != nil {
		return nil, ErrToolMarketMetering
	}
	a, err := toolMarketAuthorizedAdapter(c.ServiceID, v, t)
	if err != nil {
		return nil, err
	}
	var result struct {
		Meta struct {
			Receipt json.RawMessage `json:"lmm_metering"`
		} `json:"_meta"`
	}
	if json.Unmarshal(data, &result) != nil || len(result.Meta.Receipt) == 0 {
		return nil, ErrToolMarketMetering
	}
	var r toolMarketMeteringReceipt
	dec := json.NewDecoder(bytes.NewReader(result.Meta.Receipt))
	dec.DisallowUnknownFields()
	if dec.Decode(&r) != nil || dec.Decode(new(any)) != io.EOF || r.AdapterID != a.ID || r.Context != ToolMarketMeteringContext(c) || r.Quantities == nil {
		return nil, ErrToolMarketMetering
	}
	digest, digestErr := ToolMarketMeteringResultDigest(data)
	if digestErr != nil || r.ResultDigest != digest {
		return nil, ErrToolMarketMetering
	}
	signature, err := hex.DecodeString(r.Signature)
	if err != nil || len(signature) != sha256.Size {
		return nil, ErrToolMarketMetering
	}
	key, _ := base64.StdEncoding.DecodeString(a.KeyBase64)
	mac := hmac.New(sha256.New, key)
	mac.Write(toolMarketMeteringMessage(r))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return nil, ErrToolMarketMetering
	}
	if _, err := toolMarketRulesQuota(toolMarketCallRules(c), r.Quantities); err != nil {
		return nil, ErrToolMarketMetering
	}
	return r.Quantities, nil
}
