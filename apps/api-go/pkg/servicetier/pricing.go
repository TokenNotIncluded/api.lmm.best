// Package servicetier owns the OpenAI accelerated-text tariff. Unknown prices
// are unavailable, not free. Each request retains a validated price snapshot.
package servicetier

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	PolicyOption       = "ServiceTierPricingPolicy"
	CatalogOption      = "ServiceTierPricingCatalog"
	PricingURL         = "https://developers.openai.com/api/docs/pricing.md"
	MaxCatalogAge      = 24 * time.Hour
	DefaultOutputLimit = 8192
)

type Policy struct {
	Enabled         bool     `json:"enabled"`
	FastMarkup      float64  `json:"fast_markup"`
	UltrafastMarkup float64  `json:"ultrafast_markup"`
	FastGroups      []string `json:"fast_groups"`
	UltrafastGroups []string `json:"ultrafast_groups"`
}

func DefaultPolicy() Policy {
	return Policy{FastMarkup: 1.2, UltrafastMarkup: 1.2, FastGroups: []string{}, UltrafastGroups: []string{}}
}
func (p Policy) Validate() error {
	for _, n := range []float64{p.FastMarkup, p.UltrafastMarkup} {
		if !finite(n) || n < 1 || n > 100 {
			return errors.New("service-tier sales multipliers must be between 1 and 100")
		}
	}
	for _, groups := range [][]string{p.FastGroups, p.UltrafastGroups} {
		seen := map[string]bool{}
		for _, g := range groups {
			if g == "" || strings.TrimSpace(g) != g || g == "auto" || strings.ContainsAny(g, "*\r\n") || seen[g] {
				return fmt.Errorf("invalid or duplicate service-tier group %q", g)
			}
			seen[g] = true
		}
	}
	return nil
}
func (p Policy) Allows(tier, group string) bool {
	if !p.Enabled {
		return false
	}
	switch tier {
	case "fast":
		return slices.Contains(p.FastGroups, group)
	case "ultrafast":
		return slices.Contains(p.UltrafastGroups, group)
	}
	return false
}
func (p Policy) Markup(tier string) float64 {
	if tier == "ultrafast" {
		return p.UltrafastMarkup
	}
	return p.FastMarkup
}

// A nil cache price means unavailable, not zero.
type Rates struct {
	Input       float64  `json:"input"`
	CachedInput *float64 `json:"cached_input"`
	CacheWrite  *float64 `json:"cache_write"`
	Output      float64  `json:"output"`
}
type TierRates struct {
	Short Rates  `json:"short"`
	Long  *Rates `json:"long,omitempty"`
}
type ModelRates struct {
	Standard  TierRates  `json:"standard"`
	Fast      *TierRates `json:"fast,omitempty"`
	Ultrafast *TierRates `json:"ultrafast,omitempty"`
}
type Catalog struct {
	Source            string                `json:"source"`
	SHA256            string                `json:"sha256"`
	FetchedAt         time.Time             `json:"fetched_at"`
	ShortContextLimit int                   `json:"short_context_limit"`
	Models            map[string]ModelRates `json:"models"`
}

func finite(n float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) }
func (r Rates) validate() error {
	if !finite(r.Input) || r.Input <= 0 || !finite(r.Output) || r.Output <= 0 {
		return errors.New("missing or invalid input/output prices")
	}
	for _, p := range []*float64{r.CachedInput, r.CacheWrite} {
		if p != nil && (!finite(*p) || *p < 0) {
			return errors.New("invalid cache price")
		}
	}
	return nil
}
func (c Catalog) Validate() error {
	if c.Source != PricingURL || len(c.SHA256) != 64 || c.FetchedAt.IsZero() || c.ShortContextLimit <= 0 || len(c.Models) == 0 || len(c.Models) > 256 {
		return errors.New("invalid service-tier catalog metadata")
	}
	if _, err := hex.DecodeString(c.SHA256); err != nil {
		return errors.New("invalid service-tier catalog digest")
	}
	for name, m := range c.Models {
		if name == "" || strings.ContainsAny(name, " /\\\r\n") || m.Fast == nil && m.Ultrafast == nil {
			return fmt.Errorf("invalid catalog model %q", name)
		}
		for _, tr := range []*TierRates{&m.Standard, m.Fast, m.Ultrafast} {
			if tr == nil {
				continue
			}
			if err := tr.Short.validate(); err != nil {
				return err
			}
			if tr.Long != nil {
				if err := tr.Long.validate(); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func (c Catalog) Fresh(now time.Time) bool {
	age := now.Sub(c.FetchedAt)
	return age >= -5*time.Minute && age < MaxCatalogAge
}
func DecodePolicy(value string) (Policy, error) {
	p := DefaultPolicy()
	if err := json.Unmarshal([]byte(value), &p); err != nil {
		return p, err
	}
	return p, p.Validate()
}
func DecodeCatalog(value string) (Catalog, error) {
	var c Catalog
	if err := json.Unmarshal([]byte(value), &c); err != nil {
		return c, err
	}
	return c, c.Validate()
}

// Auto is deliberately pinned to default at the outbound request boundary.
func Normalize(value string) (string, error) {
	switch value {
	case "", "auto", "default":
		return "default", nil
	case "fast", "priority":
		return "fast", nil
	case "ultrafast", "flex":
		return value, nil
	default:
		return "", fmt.Errorf("unsupported OpenAI service_tier %q", value)
	}
}
func Requested(body string, headers []string) (string, error) {
	tier, err := Normalize(body)
	if err != nil {
		return "", err
	}
	if len(headers) > 1 {
		return "", errors.New("multiple OpenAI-Service-Tier headers are not allowed")
	}
	if len(headers) == 1 {
		if headers[0] != "ultrafast" {
			return "", errors.New("invalid OpenAI-Service-Tier header")
		}
		if body != "" && tier != "ultrafast" {
			return "", errors.New("conflicting service-tier body and header")
		}
		tier = "ultrafast"
	}
	return tier, nil
}
func Accelerated(tier string) bool { return tier == "fast" || tier == "ultrafast" }

// Regional, Azure and third-party prices cannot reuse the global OpenAI tariff.
func OfficialOrigin(rawURL, host string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.User != nil || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "wss") || !strings.EqualFold(u.Hostname(), "api.openai.com") || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	return host == "" || strings.EqualFold(host, "api.openai.com") || strings.EqualFold(host, "api.openai.com:443")
}

type Quote struct {
	Model                  string     `json:"model"`
	RequestedTier          string     `json:"requested_tier"`
	ActualTier             string     `json:"actual_tier,omitempty"`
	ReconciliationRequired bool       `json:"reconciliation_required,omitempty"`
	Group                  string     `json:"group"`
	SalesMultiplier        float64    `json:"sales_multiplier"`
	CatalogSHA256          string     `json:"catalog_sha256"`
	FetchedAt              time.Time  `json:"fetched_at"`
	OutputLimit            int        `json:"output_limit"`
	ContextLimit           int        `json:"-"`
	Prices                 ModelRates `json:"-"`
}

func NewQuote(c Catalog, p Policy, model, tier, group string, groupRatio float64, outputLimit int, now time.Time) (*Quote, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if !p.Allows(tier, group) {
		return nil, errors.New("accelerated service tier is disabled or not allowed for this group")
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if !c.Fresh(now) {
		return nil, errors.New("service-tier prices are stale; synchronize official prices first")
	}
	rates, ok := c.Models[model]
	if !ok {
		return nil, fmt.Errorf("no verified service-tier price for model %s", model)
	}
	if tier == "fast" && rates.Fast == nil || tier == "ultrafast" && rates.Ultrafast == nil {
		return nil, fmt.Errorf("no verified %s price for model %s", tier, model)
	}
	if !finite(groupRatio) || groupRatio < 0 {
		return nil, errors.New("invalid billing group ratio")
	}
	if outputLimit == 0 {
		outputLimit = DefaultOutputLimit
	}
	if outputLimit < 1 || outputLimit > 1_000_000 {
		return nil, errors.New("invalid accelerated output budget")
	}
	multiplier := p.Markup(tier) * math.Max(1, groupRatio)
	if !finite(multiplier) {
		return nil, errors.New("service-tier multiplier overflow")
	}
	// Copy price values, including pointers, rather than retaining mutable state.
	encoded, err := json.Marshal(rates)
	if err != nil {
		return nil, err
	}
	var snapshot ModelRates
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		return nil, err
	}
	return &Quote{Model: model, RequestedTier: tier, Group: group, SalesMultiplier: multiplier, CatalogSHA256: c.SHA256, FetchedAt: c.FetchedAt, OutputLimit: outputLimit, ContextLimit: c.ShortContextLimit, Prices: snapshot}, nil
}
func (q *Quote) Observe(tier string) {
	if q == nil || tier == "" || tier == "auto" {
		return
	}
	normalized, err := Normalize(tier)
	if err != nil {
		q.ActualTier = tier
		return
	}
	q.ActualTier = normalized
}
func (q *Quote) rates(input int) (Rates, error) {
	tier := q.ActualTier
	if tier == "" {
		tier = q.RequestedTier
	}
	var tr *TierRates
	switch tier {
	case "default":
		tr = &q.Prices.Standard
	case "fast":
		tr = q.Prices.Fast
	case "ultrafast":
		tr = q.Prices.Ultrafast
	}
	if tr == nil {
		return Rates{}, errors.New("provider returned an unpriced service tier")
	}
	if input > q.ContextLimit && q.Prices.Standard.Long != nil {
		if tr.Long == nil {
			return Rates{}, errors.New("long-context service-tier price is unavailable")
		}
		return *tr.Long, nil
	}
	return tr.Short, nil
}

type Usage struct{ Input, Output, Cached, CacheWrite int }

func rat(n float64) *big.Rat {
	r, _ := new(big.Rat).SetString(strconv.FormatFloat(n, 'f', -1, 64))
	return r
}
func addTokens(sum *big.Rat, tokens int, price float64) {
	sum.Add(sum, new(big.Rat).Mul(new(big.Rat).SetInt64(int64(tokens)), rat(price)))
}
func (q *Quote) CostUSD(u Usage) (*big.Rat, error) {
	if u.Input < 0 || u.Output < 0 || u.Cached < 0 || u.CacheWrite < 0 || u.Cached > u.Input || u.CacheWrite > u.Input {
		return nil, errors.New("invalid service-tier token usage")
	}
	r, err := q.rates(u.Input)
	if err != nil {
		return nil, err
	}
	if u.Cached > 0 && r.CachedInput == nil || u.CacheWrite > 0 && r.CacheWrite == nil {
		return nil, errors.New("provider reported an unpriced cache operation")
	}
	total := new(big.Rat)
	addTokens(total, max(0, u.Input-u.Cached-u.CacheWrite), r.Input)
	addTokens(total, u.Output, r.Output)
	if u.Cached > 0 {
		addTokens(total, u.Cached, *r.CachedInput)
	}
	if u.CacheWrite > 0 {
		addTokens(total, u.CacheWrite, *r.CacheWrite)
	}
	total.Mul(total, rat(q.SalesMultiplier))
	return total.Quo(total, big.NewRat(1_000_000, 1)), nil
}
func (q *Quote) ReserveUSD(input int) (*big.Rat, error) {
	r, err := q.rates(input)
	if err != nil {
		return nil, err
	}
	// Cache-write counters can overlap reads. Reserve their upper input cost.
	rate := r.Input
	if r.CacheWrite != nil {
		rate = math.Max(rate, *r.CacheWrite)
	}
	if r.CachedInput != nil {
		rate += *r.CachedInput
	}
	total := new(big.Rat)
	addTokens(total, max(input, 0), rate)
	addTokens(total, q.OutputLimit, r.Output)
	total.Mul(total, rat(q.SalesMultiplier))
	return total.Quo(total, big.NewRat(1_000_000, 1)), nil
}

// Credits rounds upward only once, after exact decimal multiplication.
func Credits(usd *big.Rat, creditsPerUSD string, maxQuota int) (int, error) {
	scale, ok := new(big.Rat).SetString(creditsPerUSD)
	if !ok || scale.Sign() <= 0 || usd == nil || usd.Sign() < 0 {
		return 0, errors.New("invalid credit currency basis")
	}
	value := new(big.Rat).Mul(usd, scale)
	whole, rem := new(big.Int).QuoRem(value.Num(), value.Denom(), new(big.Int))
	if rem.Sign() > 0 {
		whole.Add(whole, big.NewInt(1))
	}
	if !whole.IsInt64() || whole.Int64() > int64(maxQuota) {
		return 0, errors.New("service-tier charge exceeds supported request quota")
	}
	return int(whole.Int64()), nil
}
