// Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later
package servicetier

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strings"
	"testing"
	"time"
)

func bi01Rates(input, output, read, write float64) Rates {
	return Rates{Input: input, Output: output, CachedInput: &read, CacheWrite: &write}
}

// Prices are synthetic USD per million tokens. The mask selects independently
// published long tariffs: standard=1, fast=2, ultrafast=4.
func bi01Catalog(mask int) Catalog {
	standard := TierRates{Short: bi01Rates(2, 3, .25, 2.5)}
	fast := TierRates{Short: bi01Rates(5, 7, .75, 6)}
	ultra := TierRates{Short: bi01Rates(17, 19, 2.25, 21)}
	long := []Rates{bi01Rates(4, 6, .5, 5), bi01Rates(11, 13, 1.25, 14), bi01Rates(23, 29, 3.25, 31.5)}
	for i, tier := range []*TierRates{&standard, &fast, &ultra} {
		if mask&(1<<i) != 0 {
			tier.Long = &long[i]
		}
	}
	return Catalog{Source: PricingURL, SHA256: strings.Repeat("a", 64), FetchedAt: testNow,
		ShortContextLimit: 10, Models: map[string]ModelRates{"bi-01": {Standard: standard, Fast: &fast, Ultrafast: &ultra}}}
}

func bi01Quote(t *testing.T, c Catalog, requested string) *Quote {
	t.Helper()
	encoded, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeCatalog(string(encoded))
	if err != nil {
		t.Fatal(err) // A valid tier-local long price must remain usable.
	}
	q, err := NewQuote(decoded, testPolicy(), "bi-01", requested, "paid", 1, 4, testNow)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

// Independent decimal oracle: do not call production rates, rat or Credits.
func bi01Decimal(t *testing.T, value string) *big.Rat {
	t.Helper()
	r, ok := new(big.Rat).SetString(value)
	if !ok {
		t.Fatal(value)
	}
	return r
}
func bi01USD(t *testing.T, counts []int, prices []string) *big.Rat {
	t.Helper()
	sum := new(big.Rat)
	for i, count := range counts {
		sum.Add(sum, new(big.Rat).Mul(big.NewRat(int64(count), 1), bi01Decimal(t, prices[i])))
	}
	return sum.Mul(sum, big.NewRat(12, 10_000_000)) // Existing 1.2 markup, once.
}

func TestBI01TierLocalLongContextMatrix(t *testing.T) {
	short := [][]string{{"2", "3", "0.25", "2.5"}, {"5", "7", "0.75", "6"}, {"17", "19", "2.25", "21"}}
	long := [][]string{{"4", "6", "0.5", "5"}, {"11", "13", "1.25", "14"}, {"23", "29", "3.25", "31.5"}}
	for mask := 0; mask < 8; mask++ {
		for _, requested := range []string{"fast", "ultrafast"} {
			for _, observed := range []string{"", "default", "priority", "ultrafast"} {
				for _, input := range []int{9, 10, 11} {
					t.Run(fmt.Sprintf("long=%d/request=%s/actual=%s/input=%d", mask, requested, observed, input), func(t *testing.T) {
						q := bi01Quote(t, bi01Catalog(mask), requested)
						q.Observe(observed)
						selected := observed
						if selected == "" {
							selected = requested
						}
						index := map[string]int{"default": 0, "priority": 1, "fast": 1, "ultrafast": 2}[selected]
						usage := Usage{Input: input, Output: 4, Cached: 2, CacheWrite: 3}
						cost, costErr := q.CostUSD(usage)
						reserve, reserveErr := q.ReserveUSD(input)
						if input > 10 && mask != 0 && mask&(1<<index) == 0 {
							if costErr == nil || reserveErr == nil {
								t.Fatal("missing selected long tariff silently used a short tariff")
							}
							return
						}
						if costErr != nil || reserveErr != nil {
							t.Fatalf("cost: %v; reserve: %v", costErr, reserveErr)
						}
						prices := short[index]
						if input > 10 && mask != 0 {
							prices = long[index]
						}
						want := bi01USD(t, []int{input - 5, 4, 2, 3}, prices)
						if cost.Cmp(want) != 0 {
							t.Fatalf("cost=%s want=%s", cost, want)
						}
						// Fixture cache-write prices exceed normal input prices.
						wantReserve := bi01USD(t, []int{0, 4, input, input}, prices)
						if reserve.Cmp(wantReserve) != 0 || reserve.Cmp(cost) < 0 {
							t.Fatalf("reserve=%s want=%s cost=%s", reserve, wantReserve, cost)
						}
					})
				}
			}
		}
	}
}

func TestBI01LongCachePricesDoNotFallBackToShort(t *testing.T) {
	for _, requested := range []string{"fast", "ultrafast"} {
		for _, dimension := range []string{"read", "write"} {
			t.Run(requested+"/"+dimension, func(t *testing.T) {
				c := bi01Catalog(6)
				rates := c.Models["bi-01"].Fast
				if requested == "ultrafast" {
					rates = c.Models["bi-01"].Ultrafast
				}
				usage := Usage{Input: 11}
				if dimension == "read" {
					rates.Long.CachedInput = nil
					usage.Cached = 1
				} else {
					rates.Long.CacheWrite = nil
					usage.CacheWrite = 1
				}
				q := bi01Quote(t, c, requested)
				if _, err := q.CostUSD(usage); err == nil {
					t.Fatal("missing long cache price accepted")
				}
				usage.Input = 10
				if _, err := q.CostUSD(usage); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestBI01InvalidPricesUsageAndFreshness(t *testing.T) {
	for _, bad := range []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		for _, which := range []string{"input", "output", "read", "write"} {
			t.Run(fmt.Sprintf("price/%s/%v", which, bad), func(t *testing.T) {
				c := bi01Catalog(2)
				r := c.Models["bi-01"].Fast.Long
				switch which {
				case "input":
					r.Input = bad
				case "output":
					r.Output = bad
				case "read":
					r.CachedInput = &bad
				case "write":
					r.CacheWrite = &bad
				}
				if _, err := NewQuote(c, testPolicy(), "bi-01", "fast", "paid", 1, 4, testNow); err == nil {
					t.Fatal("invalid long price accepted")
				}
			})
		}
	}
	q := bi01Quote(t, bi01Catalog(2), "fast")
	for _, usage := range []Usage{{Input: -1}, {Output: -1}, {Cached: -1}, {CacheWrite: -1}, {Input: 11, Cached: 12}, {Input: 11, CacheWrite: 12}} {
		if _, err := q.CostUSD(usage); err == nil {
			t.Errorf("invalid usage accepted: %+v", usage)
		}
	}
	if _, err := q.ReserveUSD(-1); err == nil {
		t.Error("negative reservation input accepted")
	}
	for _, tc := range []struct {
		age time.Duration
		ok  bool
	}{{-5 * time.Minute, true}, {-5*time.Minute - time.Nanosecond, false}, {MaxCatalogAge - time.Nanosecond, true}, {MaxCatalogAge, false}} {
		_, err := NewQuote(bi01Catalog(2), testPolicy(), "bi-01", "fast", "paid", 1, 4, testNow.Add(tc.age))
		if (err == nil) != tc.ok {
			t.Errorf("age=%s error=%v", tc.age, err)
		}
	}
	for _, observed := range []string{"flex", "unknown-tier"} {
		q.Observe(observed)
		if _, err := q.CostUSD(Usage{Input: 11}); err == nil {
			t.Errorf("unpriced actual tier accepted: %s", observed)
		}
	}
}

func TestBI01LongPriceSnapshotSurvivesUpdates(t *testing.T) {
	c := bi01Catalog(2)
	q, err := NewQuote(c, testPolicy(), "bi-01", "fast", "paid", 1, 4, testNow)
	if err != nil {
		t.Fatal(err)
	}
	q.Observe("priority")
	usage := Usage{Input: 11, Cached: 2, CacheWrite: 3, Output: 4}
	before, err := q.CostUSD(usage)
	if err != nil {
		t.Fatal(err)
	}
	r := c.Models["bi-01"].Fast.Long
	r.Input, r.Output, *r.CachedInput, *r.CacheWrite = 999, 999, 999, 999
	c.ShortContextLimit = 1000
	c.FetchedAt = testNow.Add(-2 * MaxCatalogAge)
	if _, err := NewQuote(c, testPolicy(), "bi-01", "fast", "paid", 1, 4, testNow); err == nil {
		t.Fatal("expired catalog admitted a new request")
	}
	encoded, err := json.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}
	var restored Quote
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	// Expiry blocks new requests, not settlement of an existing frozen quote.
	restored.FetchedAt = c.FetchedAt
	after, err := restored.CostUSD(usage)
	if err != nil || after.Cmp(before) != 0 || restored.ContextLimit != 10 || restored.ActualTier != "fast" {
		t.Fatalf("frozen quote changed: cost=%v error=%v", after, err)
	}
}

func TestBI01ReserveUsesExactArithmeticAndRejectsCreditOverflow(t *testing.T) {
	defer func() {
		if failure := recover(); failure != nil {
			t.Errorf("finite prices caused a reservation panic: %v", failure)
		}
	}()
	c := bi01Catalog(3)
	c.Models["bi-01"].Fast.Long.Input = math.MaxFloat64
	*c.Models["bi-01"].Fast.Long.CachedInput = math.MaxFloat64
	q := bi01Quote(t, c, "fast")
	usd, err := q.ReserveUSD(11)
	if err != nil || usd.Sign() <= 0 {
		t.Fatalf("exact reservation failed: %v %v", usd, err)
	}
	if _, err := Credits(usd, "500000", math.MaxInt32); err == nil {
		t.Fatal("overflow reached the integer quota")
	}
}

func TestBI01FixedCreditsAndSingleFinalRounding(t *testing.T) {
	c := bi01Catalog(2)
	*c.Models["bi-01"].Fast.Long = bi01Rates(.1, .1, .1, .1)
	p := testPolicy()
	p.FastMarkup = 1
	q, err := NewQuote(c, p, "bi-01", "fast", "paid", 1, 1, testNow)
	if err != nil {
		t.Fatal(err)
	}
	// Eleven input tokens (one read, one write) plus one output token cost
	// 0.6 point in total. Rounding four dimensions first would cost 4 points.
	usd, err := q.CostUSD(Usage{Input: 11, Cached: 1, CacheWrite: 1, Output: 1})
	if err != nil || usd.Cmp(big.NewRat(12, 10_000_000)) != 0 {
		t.Fatalf("cost=%v error=%v", usd, err)
	}
	points, err := Credits(usd, "500000", math.MaxInt32)
	if err != nil || points != 1 {
		t.Fatalf("points=%d error=%v", points, err)
	}
	for _, tc := range []struct {
		usd  string
		max  int
		want int
		bad  bool
	}{{"0", 1, 0, false}, {"1", 500000, 500000, false}, {"4294.967294", math.MaxInt32, math.MaxInt32, false},
		{"4294.967294000001", math.MaxInt32, 0, true}, {"-0.000001", math.MaxInt32, 0, true}} {
		value := bi01Decimal(t, tc.usd)
		copy := new(big.Rat).Set(value)
		got, err := Credits(value, "500000", tc.max)
		if (err != nil) != tc.bad || err == nil && got != tc.want || value.Cmp(copy) != 0 {
			t.Errorf("%+v: got=%d error=%v", tc, got, err)
		}
	}
}

func TestBI01ReserveAddsCachePricesExactly(t *testing.T) {
	c := bi01Catalog(3)
	*c.Models["bi-01"].Fast.Long = bi01Rates(.1, .1, .2, .1)
	q := bi01Quote(t, c, "fast")
	usd, err := q.ReserveUSD(11)
	if err != nil || usd.Cmp(big.NewRat(111, 25_000_000)) != 0 {
		t.Fatalf("reservation added prices as floats: cost=%v error=%v", usd, err)
	}
}
