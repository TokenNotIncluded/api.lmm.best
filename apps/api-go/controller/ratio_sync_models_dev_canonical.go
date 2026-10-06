package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/billing_setting"
)

const canonicalModelsDevMaxBytes = 10 << 20

type canonicalModelsDevRate struct {
	literal string
	exact   *big.Rat
}

type canonicalModelsDevTier struct {
	threshold int64
	rates     map[string]canonicalModelsDevRate
}

// convertModelsDevCanonicalData selects one explicitly named models.dev
// provider, never a cheaper same-name model from another provider. models.dev
// is third-party metadata; selecting a provider does not establish provenance.
//
// The returned sync fields are the USD editor's input contract: model_ratio
// is calibrated credits/token; the other ratios are dimensionless; expressions
// retain true USD/1M coefficients. The existing schema-2 CAS save performs the
// legacy expression storage conversion. Even flat prices use an expression so
// cache-write lanes and existing authoritative expressions cannot be lost.
// A rejected model contributes no fields, and its reason is returned separately.
func convertModelsDevCanonicalData(reader io.Reader, provider string, creditsPerUSD float64) (map[string]any, map[string]string, error) {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return nil, nil, fmt.Errorf("models.dev requires one explicit provider")
	}
	if reader == nil {
		return nil, nil, fmt.Errorf("models.dev response is unavailable")
	}
	if math.IsNaN(creditsPerUSD) || math.IsInf(creditsPerUSD, 0) || creditsPerUSD <= 0 || creditsPerUSD > common.MaxWalletQuota {
		return nil, nil, fmt.Errorf("models.dev requires a valid fixed credits-per-USD denomination")
	}
	data, err := io.ReadAll(io.LimitReader(reader, canonicalModelsDevMaxBytes+1))
	if err != nil {
		return nil, nil, fmt.Errorf("read models.dev response: %w", err)
	}
	if len(data) > canonicalModelsDevMaxBytes {
		return nil, nil, fmt.Errorf("models.dev response exceeds the size limit")
	}
	providers, err := canonicalModelsDevObject(data)
	if err != nil {
		return nil, nil, fmt.Errorf("decode models.dev response: %w", err)
	}
	selected, exists := providers[provider]
	if !exists {
		return nil, nil, fmt.Errorf("models.dev provider %q is not present", provider)
	}
	block, err := canonicalModelsDevObject(selected)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid models.dev provider block")
	}
	if rawID, exists := block["id"]; exists {
		var id string
		if json.Unmarshal(rawID, &id) != nil || id != provider {
			return nil, nil, fmt.Errorf("models.dev provider identity does not match the selected block")
		}
	}
	var adapter string
	if rawAdapter, exists := block["npm"]; exists {
		if json.Unmarshal(rawAdapter, &adapter) != nil {
			return nil, nil, fmt.Errorf("invalid models.dev provider adapter metadata")
		}
	}
	models, err := canonicalModelsDevObject(block["models"])
	if err != nil || len(models) == 0 {
		return nil, nil, fmt.Errorf("models.dev provider has no model entries")
	}
	result := map[string]any{}
	for _, field := range []string{
		"model_ratio", "completion_ratio", "cache_ratio", "create_cache_ratio",
		"image_ratio", "audio_ratio", "audio_completion_ratio", "model_price",
		billing_setting.BillingModeField, billing_setting.BillingExprField,
	} {
		result[field] = map[string]any{}
	}
	skipped := map[string]string{}
	denomination, _ := new(big.Rat).SetString(strconv.FormatFloat(creditsPerUSD, 'g', -1, 64))
	names := make([]string, 0, len(models))
	for name := range models {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		values, reason := canonicalModelsDevModel(name, models[name], adapter, denomination)
		if strings.TrimSpace(name) == "" || strings.TrimSpace(name) != name {
			reason = "invalid model identifier"
		}
		if reason != "" {
			skipped[name] = reason
			continue
		}
		for field, value := range values {
			result[field].(map[string]any)[name] = value
		}
	}
	return result, skipped, nil
}

func canonicalModelsDevModel(name string, raw json.RawMessage, adapter string, denomination *big.Rat) (map[string]any, string) {
	model, err := canonicalModelsDevObject(raw)
	if err != nil {
		return nil, "invalid model metadata"
	}
	if rawID, exists := model["id"]; exists {
		var id string
		if json.Unmarshal(rawID, &id) != nil || id != name {
			return nil, "model identity does not match its provider model key"
		}
	}
	if reason := canonicalModelsDevModalities(model["modalities"]); reason != "" {
		return nil, reason
	}
	var family string
	if rawFamily, exists := model["family"]; exists && json.Unmarshal(rawFamily, &family) != nil {
		return nil, "invalid model family metadata"
	}
	// Native Anthropic usage separates 5-minute and 1-hour cache creation from
	// p. models.dev's generic cache_write field cannot price both TTLs. Never
	// leave cc1h unpriced or invent its rate; this is adapter/family capability
	// metadata, independent of a production channel or a hard-coded model ID.
	if strings.TrimSpace(adapter) == "@ai-sdk/anthropic" || strings.HasPrefix(strings.ToLower(strings.TrimSpace(family)), "claude") {
		return nil, "native Anthropic cache prices require an explicit 1-hour TTL price; models.dev does not supply it"
	}
	cost, err := canonicalModelsDevObject(model["cost"])
	if err != nil {
		return nil, "missing or invalid token prices"
	}
	for _, key := range canonicalModelsDevKeys(cost) {
		switch key {
		case "input", "output", "cache_read", "cache_write", "tiers", "context_over_200k":
		default:
			return nil, "unsupported price field: " + key
		}
	}
	base, reason := canonicalModelsDevRates(cost)
	if reason != "" {
		return nil, reason
	}
	tiers, reason := canonicalModelsDevTiers(cost["tiers"], base)
	if reason != "" {
		return nil, reason
	}
	// context_over_200k is a legacy alias. Structured tiers carry the actual
	// threshold (for example 272000); an alias must never replace that number.
	if alias, exists := cost["context_over_200k"]; exists {
		object, err := canonicalModelsDevObject(alias)
		if err != nil || len(tiers) != 1 {
			return nil, "context price alias requires one matching explicit context tier"
		}
		for _, key := range canonicalModelsDevKeys(object) {
			if key != "input" && key != "output" && key != "cache_read" && key != "cache_write" {
				return nil, "unsupported context price alias field: " + key
			}
		}
		rates, aliasReason := canonicalModelsDevRates(object)
		if aliasReason != "" || !canonicalModelsDevSameRates(rates, tiers[0].rates) {
			return nil, "context price alias conflicts with the explicit context tier"
		}
	}
	input := base["input"].exact
	calibrated := new(big.Rat).Mul(input, denomination)
	calibrated.Quo(calibrated, big.NewRat(1_000_000, 1))
	ratio, ok := canonicalModelsDevFiniteFloat(calibrated)
	if !ok {
		return nil, "input price is outside the calibrated ratio range"
	}
	values := map[string]any{
		"model_ratio":                    ratio,
		billing_setting.BillingModeField: billing_setting.BillingModeTieredExpr,
		billing_setting.BillingExprField: canonicalModelsDevExpression(base, tiers),
	}
	// A zero input price with a paid output/cache lane is expressible, but has
	// no finite completion/cache ratio. The expression remains authoritative.
	if input.Sign() > 0 {
		for _, lane := range []struct{ price, field string }{
			{"output", "completion_ratio"}, {"cache_read", "cache_ratio"}, {"cache_write", "create_cache_ratio"},
		} {
			if rate, exists := base[lane.price]; exists {
				multiplier, ok := canonicalModelsDevFiniteFloat(new(big.Rat).Quo(rate.exact, input))
				if !ok {
					return nil, lane.price + " price is outside the dimensionless ratio range"
				}
				values[lane.field] = multiplier
			}
		}
	}
	return values, ""
}

func canonicalModelsDevModalities(raw json.RawMessage) string {
	modalities, err := canonicalModelsDevObject(raw)
	if err != nil {
		return "missing or invalid modality metadata"
	}
	var input, output []string
	if json.Unmarshal(modalities["input"], &input) != nil || len(input) == 0 || json.Unmarshal(modalities["output"], &output) != nil || len(output) != 1 || output[0] != "text" {
		return "only complete text-output token prices are supported"
	}
	for _, modality := range input {
		if modality != "text" && modality != "image" && modality != "pdf" {
			return "unsupported input modality: " + modality
		}
	}
	return ""
}

func canonicalModelsDevRates(object map[string]json.RawMessage) (map[string]canonicalModelsDevRate, string) {
	rates := map[string]canonicalModelsDevRate{}
	for _, key := range []string{"input", "output", "cache_read", "cache_write"} {
		raw, exists := object[key]
		if !exists {
			if key == "input" || key == "output" {
				return nil, "missing " + key + " token price"
			}
			continue
		}
		literal := string(bytes.TrimSpace(raw))
		// Validate the JSON number and float range before allocating an exact
		// rational. Bound literals so hostile zero exponents cannot allocate huge
		// integers. Prices that would underflow must not become a free quote.
		var number json.Number
		if len(literal) > 128 || json.Unmarshal(raw, &number) != nil || literal == "null" || len(literal) == 0 || literal[0] == '"' {
			return nil, "invalid " + key + " token price"
		}
		value, err := number.Float64()
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return nil, "invalid " + key + " token price"
		}
		if exponent := strings.IndexAny(literal, "eE"); exponent >= 0 {
			parsed, err := strconv.ParseInt(literal[exponent+1:], 10, 32)
			if err != nil || parsed < -1000 || parsed > 1000 {
				return nil, "invalid " + key + " token price"
			}
		}
		exact, ok := new(big.Rat).SetString(literal)
		if !ok || exact.Sign() < 0 || (exact.Sign() > 0 && value == 0) {
			return nil, key + " token price is outside the representable range"
		}
		// Keep the original numeric literal: no six-decimal rounding and no
		// conversion of a monetary coefficient into a dimensionless multiplier.
		// The DSL parses bare integers as int64. A finite JSON integer outside
		// that range must be a float literal without changing its numeric value.
		if !strings.ContainsAny(literal, ".eE") {
			if _, err := strconv.ParseInt(literal, 10, 64); err != nil {
				literal += ".0"
			}
		}
		rates[key] = canonicalModelsDevRate{literal: literal, exact: exact}
	}
	return rates, ""
}

func canonicalModelsDevTiers(raw json.RawMessage, base map[string]canonicalModelsDevRate) ([]canonicalModelsDevTier, string) {
	if len(raw) == 0 {
		return nil, ""
	}
	var entries []json.RawMessage
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &entries) != nil || len(entries) == 0 || len(entries) > 32 {
		return nil, "invalid context price tiers"
	}
	tiers := make([]canonicalModelsDevTier, 0, len(entries))
	for _, rawEntry := range entries {
		entry, err := canonicalModelsDevObject(rawEntry)
		if err != nil {
			return nil, "invalid context price tier"
		}
		for _, key := range canonicalModelsDevKeys(entry) {
			if key != "input" && key != "output" && key != "cache_read" && key != "cache_write" && key != "tier" {
				return nil, "unsupported tier price field: " + key
			}
		}
		rates, reason := canonicalModelsDevRates(entry)
		if reason != "" {
			return nil, "context tier: " + reason
		}
		if len(rates) != len(base) {
			return nil, "context tier must preserve every base price lane"
		}
		for key := range base {
			if _, exists := rates[key]; !exists {
				return nil, "context tier must preserve every base price lane"
			}
		}
		condition, err := canonicalModelsDevObject(entry["tier"])
		if err != nil || len(condition) != 2 {
			return nil, "unsupported context tier condition"
		}
		var kind string
		var size int64
		if json.Unmarshal(condition["type"], &kind) != nil || kind != "context" || json.Unmarshal(condition["size"], &size) != nil || size <= 0 || size > common.MaxWalletQuota {
			return nil, "unsupported context tier condition"
		}
		tiers = append(tiers, canonicalModelsDevTier{threshold: size, rates: rates})
	}
	sort.Slice(tiers, func(i, j int) bool { return tiers[i].threshold < tiers[j].threshold })
	for i := 1; i < len(tiers); i++ {
		if tiers[i-1].threshold == tiers[i].threshold {
			return nil, "duplicate context tier threshold"
		}
	}
	return tiers, ""
}

func canonicalModelsDevExpression(base map[string]canonicalModelsDevRate, tiers []canonicalModelsDevTier) string {
	terms := func(rates map[string]canonicalModelsDevRate) string {
		parts := []string{"p * " + rates["input"].literal, "c * " + rates["output"].literal}
		for _, lane := range []struct{ key, variable string }{{"cache_read", "cr"}, {"cache_write", "cc"}} {
			if rate, exists := rates[lane.key]; exists {
				parts = append(parts, lane.variable+" * "+rate.literal)
			}
		}
		return strings.Join(parts, " + ")
	}
	if len(tiers) == 0 {
		return `tier("base", ` + terms(base) + ")"
	}
	expression := ""
	previous := base
	for i, tier := range tiers {
		name := "base"
		if i > 0 {
			name = "context_over_" + strconv.FormatInt(tiers[i-1].threshold, 10)
		}
		expression += fmt.Sprintf(`len <= %d ? tier(%q, %s) : `, tier.threshold, name, terms(previous))
		previous = tier.rates
	}
	lastName := "context_over_" + strconv.FormatInt(tiers[len(tiers)-1].threshold, 10)
	return expression + fmt.Sprintf(`tier(%q, %s)`, lastName, terms(previous))
}

func canonicalModelsDevFiniteFloat(exact *big.Rat) (float64, bool) {
	value, _ := exact.Float64()
	return value, !math.IsNaN(value) && !math.IsInf(value, 0) && (exact.Sign() == 0 || value > 0)
}

func canonicalModelsDevSameRates(left, right map[string]canonicalModelsDevRate) bool {
	if len(left) != len(right) {
		return false
	}
	for key, rate := range left {
		other, exists := right[key]
		if !exists || rate.exact.Cmp(other.exact) != 0 {
			return false
		}
	}
	return true
}

func canonicalModelsDevKeys(object map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func canonicalModelsDevObject(raw []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return nil, fmt.Errorf("expected a JSON object")
	}
	object := map[string]json.RawMessage{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("invalid JSON object")
		}
		key, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("invalid JSON object key")
		}
		if _, exists := object[key]; exists {
			return nil, fmt.Errorf("duplicate JSON object key")
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return nil, fmt.Errorf("invalid JSON object value")
		}
		object[key] = value
	}
	if end, err := decoder.Token(); err != nil || end != json.Delim('}') {
		return nil, fmt.Errorf("incomplete JSON object")
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return nil, fmt.Errorf("unexpected trailing JSON data")
	}
	return object, nil
}
