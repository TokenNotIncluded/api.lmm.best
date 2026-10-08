package servicetier

import (
	"math"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)

func testCatalog(t *testing.T) Catalog {
	t.Helper()
	data, err := os.ReadFile("testdata/official-prices.md")
	if err != nil {
		t.Fatal(err)
	}
	c, err := ParseCatalog(string(data), testNow)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func testPolicy() Policy {
	p := DefaultPolicy()
	p.Enabled = true
	p.FastGroups = []string{"paid"}
	p.UltrafastGroups = []string{"paid"}
	return p
}
func testQuote(t *testing.T, tier string) *Quote {
	t.Helper()
	q, err := NewQuote(testCatalog(t), testPolicy(), "gpt-6-astra", tier, "paid", .1, 1000, testNow)
	if err != nil {
		t.Fatal(err)
	}
	return q
}
func assertUSD(t *testing.T, q *Quote, u Usage, want string) {
	t.Helper()
	got, err := q.CostUSD(u)
	if err != nil {
		t.Fatal(err)
	}
	expected, ok := new(big.Rat).SetString(want)
	if !ok || got.Cmp(expected) != 0 {
		t.Fatalf("cost = %s, want %s", got, want)
	}
}
func TestOfficialPriceCatalog(t *testing.T) {
	c := testCatalog(t)
	if len(c.Models) != 22 || c.ShortContextLimit != 272000 {
		t.Fatalf("unexpected catalog: %d models, threshold %d", len(c.Models), c.ShortContextLimit)
	}
	for model, want := range map[string]float64{"gpt-5.5": 12.5, "gpt-4.1": 3.5, "gpt-6-astra": 20} {
		if c.Models[model].Fast.Short.Input != want {
			t.Fatal(model)
		}
	}
	if c.Models["gpt-6.1-sol"].Ultrafast.Short.Input != 12 {
		t.Fatal("ultrafast price")
	}
	if c.Models["gpt-4o-2024-05-13"].Fast.Short.CachedInput != nil {
		t.Fatal("unpriced cache became free")
	}
}
func TestDefaultOffAndGroupIsolation(t *testing.T) {
	c := testCatalog(t)
	for _, tc := range []struct {
		name  string
		p     Policy
		group string
	}{
		{"default off", DefaultPolicy(), "paid"}, {"wrong group", testPolicy(), "normal"}, {"auto not wildcard", testPolicy(), "auto"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewQuote(c, tc.p, "gpt-6-astra", "fast", tc.group, 1, 0, testNow); err == nil {
				t.Fatal("unauthorized acceleration")
			}
		})
	}
}
func TestUnsafePricesAndMultipliersAreRejected(t *testing.T) {
	for _, n := range []float64{0, .9, -1, math.NaN(), math.Inf(1), 101} {
		p := testPolicy()
		p.FastMarkup = n
		if p.Validate() == nil {
			t.Fatalf("accepted %v", n)
		}
	}
	for _, group := range []string{"*", "auto", "paid\nnormal", " paid"} {
		p := testPolicy()
		p.FastGroups = []string{group}
		if p.Validate() == nil {
			t.Fatal(group)
		}
	}
	c := testCatalog(t)
	for _, tc := range []struct {
		model, tier string
		ratio       float64
		now         time.Time
	}{
		{"unknown", "fast", 1, testNow}, {"gpt-4.1", "ultrafast", 1, testNow}, {"gpt-6-astra", "fast", 1, testNow.Add(MaxCatalogAge)}, {"gpt-6-astra", "fast", math.MaxFloat64, testNow},
	} {
		if _, err := NewQuote(c, testPolicy(), tc.model, tc.tier, "paid", tc.ratio, 0, tc.now); err == nil {
			t.Fatalf("accepted unsafe quote %+v", tc)
		}
	}
}
func TestActualTierCacheAndLongContext(t *testing.T) {
	q := testQuote(t, "ultrafast")
	assertUSD(t, q, Usage{Input: 4000, Cached: 2000, CacheWrite: 1000, Output: 1000}, "0.5364")
	q.Observe("default")
	assertUSD(t, q, Usage{Input: 4000, Cached: 2000, CacheWrite: 1000, Output: 1000}, "0.0894")
	q.Observe("priority")
	assertUSD(t, q, Usage{Input: 1000, Output: 1000}, "0.144")
	q.Observe("auto")
	if q.ActualTier != "fast" {
		t.Fatal(q.ActualTier)
	}
	q.Observe("ultrafast")
	assertUSD(t, q, Usage{Input: 272000, Output: 1000}, "19.944")
	assertUSD(t, q, Usage{Input: 272001, Output: 1000}, "39.708144")
	q.Observe("unpriced-future-tier")
	if _, err := q.CostUSD(Usage{Input: 1}); err == nil {
		t.Fatal("unknown actual tier silently priced")
	}
}
func TestQuoteSnapshotDoesNotChangeWithCatalog(t *testing.T) {
	c := testCatalog(t)
	q, err := NewQuote(c, testPolicy(), "gpt-6-astra", "fast", "paid", .01, 0, testNow)
	if err != nil {
		t.Fatal(err)
	}
	c.Models["gpt-6-astra"].Fast.Short.Input = 999
	*c.Models["gpt-6-astra"].Fast.Short.CachedInput = 999
	if q.OutputLimit != 8192 || q.SalesMultiplier != 1.2 {
		t.Fatalf("unsafe default %+v", q)
	}
	assertUSD(t, q, Usage{Input: 1000, Cached: 100}, "0.02184")
	reserve, err := q.ReserveUSD(1000)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := q.CostUSD(Usage{Input: 1000, Cached: 1000, CacheWrite: 1000, Output: q.OutputLimit})
	if err != nil {
		t.Fatal(err)
	}
	if reserve.Cmp(actual) < 0 {
		t.Fatal("cache overlap exceeded conservative reserve")
	}
}
func TestMissingLongOrCachePricesReject(t *testing.T) {
	q, err := NewQuote(testCatalog(t), testPolicy(), "gpt-5.5", "fast", "paid", 1, 1, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = q.ReserveUSD(272001); err == nil {
		t.Fatal("missing long-context price accepted")
	}
	q, err = NewQuote(testCatalog(t), testPolicy(), "gpt-4o-2024-05-13", "fast", "paid", 1, 1, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = q.CostUSD(Usage{Input: 10, Cached: 1}); err == nil {
		t.Fatal("missing cache price accepted")
	}
	if _, err = q.CostUSD(Usage{Input: 10, Cached: 11}); err == nil {
		t.Fatal("invalid usage accepted")
	}
}
func TestCreditsRoundOnceAndRejectOverflow(t *testing.T) {
	for _, tc := range []struct {
		value, scale string
		want         int
	}{{"0", "500000", 0}, {"0.0000001", "500000", 1}, {"0.144", "500000", 72000}} {
		value, _ := new(big.Rat).SetString(tc.value)
		got, err := Credits(value, tc.scale, 1000000)
		if err != nil || got != tc.want {
			t.Fatalf("%+v: %d %v", tc, got, err)
		}
	}
	if _, err := Credits(big.NewRat(10, 1), "100", 999); err == nil {
		t.Fatal("overflow accepted")
	}
}
func TestHeaderAndOriginProtection(t *testing.T) {
	for _, tc := range []struct {
		body    string
		headers []string
		want    string
		bad     bool
	}{
		{"priority", nil, "fast", false}, {"", nil, "default", false}, {"", []string{"ultrafast"}, "ultrafast", false},
		{"default", []string{"ultrafast"}, "", true}, {"fast", []string{"ultrafast"}, "", true}, {"", []string{"ultrafast", "ultrafast"}, "", true}, {"", []string{"ultrafast, ultrafast"}, "", true},
	} {
		got, err := Requested(tc.body, tc.headers)
		if (err != nil) != tc.bad || !tc.bad && got != tc.want {
			t.Fatalf("%+v: %s %v", tc, got, err)
		}
	}
	for _, raw := range []string{"https://api.openai.com.evil/v1/responses", "https://api.openai.com@evil/v1/responses", "http://api.openai.com/v1/responses", "https://eu.api.openai.com/v1/responses", "https://api.openai.com:444/v1/responses"} {
		if OfficialOrigin(raw, "") {
			t.Fatal(raw)
		}
	}
	if !OfficialOrigin("wss://api.openai.com/v1/responses", "api.openai.com") || OfficialOrigin("https://api.openai.com/v1/responses", "evil") {
		t.Fatal("origin handling")
	}
}
func TestSyncRejectsMalformedOrPartialPrices(t *testing.T) {
	data, err := os.ReadFile("testdata/official-prices.md")
	if err != nil {
		t.Fatal(err)
	}
	valid := string(data)
	for name, bad := range map[string]string{
		"html": "<html>pricing unavailable</html>", "changed columns": strings.Replace(valid, "Short context input", "Input", 1),
		"missing tier": strings.Split(valid, "### Ultrafast pricing data")[0], "missing currency": strings.Replace(valid, "$10.00", "10.00", 1),
		"negative": strings.Replace(valid, "$10.00", "$-10.00", 1), "nan": strings.Replace(valid, "$10.00", "$NaN", 1),
		"partial row": strings.Replace(valid, "$50.00 | $20.00", "- | $20.00", 1), "conflicting threshold": strings.Replace(valid, ">272K input tokens", ">300K input tokens", 1),
		"duplicate section": valid + "\n### Fast pricing data\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseCatalog(bad, testNow); err == nil {
				t.Fatal("unsafe price sync accepted")
			}
		})
	}
}
