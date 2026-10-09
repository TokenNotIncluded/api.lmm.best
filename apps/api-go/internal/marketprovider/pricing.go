// Package marketprovider defines reviewed upstream presets and exact price quotes.
// It contains no credentials, HTTP clients, or wallet mutations.
package marketprovider

import (
	"encoding/json"
	"errors"
	"math/big"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

var (
	ErrPrice         = errors.New("upstream price is missing or unsupported")
	ErrVariablePrice = errors.New("variable upstream price requires a verified spending bound")
	decimalPattern   = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]{1,2})?$`)
)

type Preset struct {
	ReadTools     []string `json:"read_tools"`
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Endpoint      string   `json:"endpoint"`
	ExecuteTool   string   `json:"execute_tool"`
	InspectTool   string   `json:"inspect_tool"`
	Documentation string   `json:"documentation"`
	OAuth         bool     `json:"oauth"`
}

// Return values, not a mutable global registry. Credentials may only be sent to
// the exact endpoint selected by the author and saved with the draft version.
func Presets() []Preset {
	return []Preset{
		{ReadTools: []string{"monid_discover", "monid_inspect"}, ID: "monid", Name: "Monid", Endpoint: "https://mcp.monid.ai/v1", ExecuteTool: "monid_run", InspectTool: "monid_inspect", Documentation: "https://docs.monid.ai", OAuth: true},
		{ReadTools: []string{"find_tools", "list_tools", "describe_tool"}, ID: "agentkey", Name: "AgentKey", Endpoint: "https://api.agentkey.app/v1/mcp", ExecuteTool: "execute_tool", InspectTool: "describe_tool", Documentation: "https://docs.agentkey.app", OAuth: true},
	}
}

func Find(id string) (Preset, bool) {
	for _, preset := range Presets() {
		if preset.ID == id {
			return preset, true
		}
	}
	return Preset{}, false
}

// Pricing is part of the immutable, reviewed publication. It is not a secret.
// PriceQuota on the tool is a maximum per-call charge, not a fixed charge.
type Pricing struct {
	Provider   string `json:"provider"`
	Multiplier string `json:"multiplier"`
}

func (p Pricing) Validate(endpoint, tool string) error {
	preset, ok := Find(p.Provider)
	if !ok || endpoint != preset.Endpoint || tool != preset.ExecuteTool {
		return ErrPrice
	}
	m, err := decimal(p.Multiplier)
	if err != nil || m.Cmp(big.NewRat(1, 1)) < 0 || m.Cmp(big.NewRat(100, 1)) > 0 {
		return ErrPrice
	}
	return nil
}

type Quote struct {
	TargetProvider string `json:"target_provider,omitempty"`
	TargetEndpoint string `json:"target_endpoint,omitempty"`
	Provider       string `json:"provider"`
	Unit           string `json:"unit"`
	AmountUSD      string `json:"amount_usd"`
	FlatFeeUSD     string `json:"flat_fee_usd,omitempty"`
	// A quote is the provider's published tariff, not proof of the merchant's
	// subscription invoice or of a completed execution.
}

// parseDecimal bounds input size and exponent, not the site's ledger scale.
func parseDecimal(value string) (*big.Rat, error) {
	if len(value) == 0 || len(value) > 64 || !decimalPattern.MatchString(value) {
		return nil, ErrPrice
	}
	n, ok := new(big.Rat).SetString(value)
	if !ok || n.Sign() < 0 {
		return nil, ErrPrice
	}
	return n, nil
}

// Upstream prices retain their own amount limit. Ledger units are not prices.
func decimal(value string) (*big.Rat, error) {
	n, err := parseDecimal(value)
	if err != nil || n.Cmp(big.NewRat(1_000_000, 1)) > 0 {
		return nil, ErrPrice
	}
	return n, nil
}

func number(raw json.RawMessage) (string, error) {
	var s string
	if len(raw) > 0 && raw[0] == '"' {
		if json.Unmarshal(raw, &s) != nil {
			return "", ErrPrice
		}
	} else {
		s = string(raw)
	}
	if _, err := decimal(s); err != nil {
		return "", err
	}
	return s, nil
}

func money(raw json.RawMessage, currency string) (string, error) {
	if len(raw) == 0 {
		return "", ErrPrice
	}
	if raw[0] == '{' {
		var amount struct {
			Value    json.RawMessage `json:"value"`
			Currency string          `json:"currency"`
		}
		if json.Unmarshal(raw, &amount) != nil || amount.Currency != "USD" || (currency != "" && currency != amount.Currency) {
			return "", ErrPrice
		}
		return number(amount.Value)
	}
	if currency != "USD" {
		return "", ErrPrice
	}
	return number(raw)
}

func MonidQuote(raw json.RawMessage) (Quote, error) {
	var price struct {
		Type     string          `json:"type"`
		Amount   json.RawMessage `json:"amount"`
		Currency string          `json:"currency"`
		FlatFee  json.RawMessage `json:"flatFee"`
	}
	q := Quote{Provider: "monid"}
	if json.Unmarshal(raw, &price) != nil {
		return q, ErrPrice
	}
	switch price.Type {
	case "PER_CALL":
		q.Unit = "call"
	case "PER_RESULT":
		q.Unit = "result"
	default:
		return q, ErrPrice
	}
	var err error
	q.AmountUSD, err = money(price.Amount, price.Currency)
	if err != nil {
		return q, err
	}
	if len(price.FlatFee) > 0 && string(price.FlatFee) != "null" {
		// A nested USD amount already establishes the currency of a numeric fee.
		q.FlatFeeUSD, err = money(price.FlatFee, "USD")
		if err != nil {
			return q, err
		}
	}
	return q, nil
}

func AgentKeyQuote(raw json.RawMessage) (Quote, error) {
	var cost struct {
		USD json.RawMessage `json:"usd_per_call"`
	}
	q := Quote{Provider: "agentkey", Unit: "call"}
	if json.Unmarshal(raw, &cost) != nil {
		return q, ErrPrice
	}
	var err error
	q.AmountUSD, err = number(cost.USD)
	return q, err
}

// Quota computes one final ceiling after multiplying exact decimal values. A
// nonzero sub-quota upstream price must never become an unintended free call.
func (q Quote) Quota(multiplier, creditsPerUSD string) (int, error) {
	if q.Unit != "call" {
		return 0, ErrVariablePrice
	}
	if _, ok := Find(q.Provider); !ok {
		return 0, ErrPrice
	}
	cost, err := decimal(q.AmountUSD)
	if err != nil {
		return 0, err
	}
	if q.FlatFeeUSD != "" {
		fee, err := decimal(q.FlatFeeUSD)
		if err != nil {
			return 0, err
		}
		cost.Add(cost, fee)
	}
	m, err := decimal(multiplier)
	if err != nil || m.Cmp(big.NewRat(1, 1)) < 0 || m.Cmp(big.NewRat(100, 1)) > 0 {
		return 0, ErrPrice
	}
	units, err := parseDecimal(creditsPerUSD)
	if err != nil || units.Sign() <= 0 {
		return 0, ErrPrice
	}
	cost.Mul(cost, m).Mul(cost, units)
	n := new(big.Int).Quo(new(big.Int).Add(cost.Num(), new(big.Int).Sub(cost.Denom(), big.NewInt(1))), cost.Denom())
	if !n.IsInt64() || n.Sign() < 0 || n.Int64() > 9_007_199_254_740_991 {
		return 0, ErrPrice
	}
	quota := int(n.Int64())
	if int64(quota) != n.Int64() {
		return 0, ErrPrice
	}
	return quota, nil
}

// QuoteArguments excludes arbitrary account-level operations from a merchant's
// shared key. Only execution identifiers are forwarded to read-only inspection.
func QuoteArguments(provider string, arguments map[string]any) (map[string]any, error) {
	text := func(key string) (string, error) {
		s, ok := arguments[key].(string)
		if !ok || len(s) == 0 || len(s) > 256 || strings.TrimSpace(s) != s || strings.ContainsAny(s, "\r\n\x00") {
			return "", ErrPrice
		}
		return s, nil
	}
	switch provider {
	case "agentkey":
		name, err := text("name")
		if err != nil || !strings.Contains(name, "/") || strings.EqualFold(name, "agentkey_account") || strings.HasPrefix(strings.ToLower(name), "agentkey/") {
			return nil, ErrPrice
		}
		return map[string]any{"name": name}, nil
	case "monid":
		provider, err := text("provider")
		if err != nil {
			return nil, err
		}
		endpoint, err := text("endpoint")
		if err != nil || !strings.HasPrefix(endpoint, "/") || strings.HasPrefix(endpoint, "//") {
			return nil, ErrPrice
		}
		return map[string]any{"provider": provider, "endpoint": endpoint}, nil
	default:
		return nil, ErrPrice
	}
}

// ValidateTool also applies to manually submitted drafts and old publications.
// A known provider cannot bypass its pricing or account boundary by deselecting
// the preset, adding :443, changing case, or choosing another path on its host.
func ValidateTool(endpoint, name string, pricing *Pricing) error {
	u, err := url.Parse(endpoint)
	if err != nil {
		return ErrPrice
	}
	for _, preset := range Presets() {
		official, _ := url.Parse(preset.Endpoint)
		if !strings.EqualFold(strings.TrimSuffix(u.Hostname(), "."), official.Hostname()) {
			continue
		}
		if endpoint != preset.Endpoint {
			return ErrPrice
		}
		if name == preset.ExecuteTool {
			if pricing == nil {
				return ErrPrice
			}
			return pricing.Validate(endpoint, name)
		}
		if pricing != nil || !slices.Contains(preset.ReadTools, name) {
			return ErrPrice
		}
		return nil
	}
	if pricing != nil {
		return pricing.Validate(endpoint, name)
	}
	return nil
}

// Run IDs are opaque path segments, never URLs or caller-provided paths.
func ValidRunID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
