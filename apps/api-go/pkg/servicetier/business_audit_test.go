// Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later
package servicetier

import (
	"encoding/json"
	"math/big"
	"testing"
)

// A supplied long-context tariff cannot silently be replaced with a cheaper
// short-context tariff merely because another tier has no long-context price.
func TestBusinessAuditTierLocalLongContextPrice(t *testing.T) {
	catalog := testCatalog(t)
	rates := catalog.Models["gpt-6-astra"]
	rates.Standard.Long = nil
	long := rates.Fast.Short
	long.Input = rates.Fast.Short.Input * 2
	rates.Fast.Long = &long
	catalog.Models["gpt-6-astra"] = rates
	encoded, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err = DecodeCatalog(string(encoded))
	if err != nil {
		return
	} // Rejecting an unsupported catalog is safe.
	quote, err := NewQuote(catalog, testPolicy(), "gpt-6-astra", "fast", "paid", 1, 1000, testNow)
	if err != nil {
		// Explicitly rejecting an unsupported combination is safe; undercharging is not.
		return
	}
	input := catalog.ShortContextLimit + 1
	actual, err := quote.CostUSD(Usage{Input: input})
	if err != nil {
		t.Fatal(err)
	}
	expected := new(big.Rat).Mul(big.NewRat(int64(input), 1_000_000), rat(long.Input))
	expected.Mul(expected, rat(quote.SalesMultiplier))
	if actual.Cmp(expected) != 0 {
		t.Fatalf("AUDIT-PRICE-01: accepted fast.Long=%v but charged %s USD, expected %s USD for %d input tokens", long.Input, actual.FloatString(8), expected.FloatString(8), input)
	}
}
