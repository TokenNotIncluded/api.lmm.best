package paymentpricing

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestCreditRateAdapterPreservesAllPricingModes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		pricing SettlementPricing
		k, qpu  string
		credits int64
		want    string
	}{
		{"standard_USD", SettlementPricing{SettlementCurrency: "USD", UsesFixedCreditDenomination: true, SettlementUnitsPerUSD: decimal.NewFromInt(1)}, "500000", "0", 500000, "1"},
		{"standard_CNY", SettlementPricing{SettlementCurrency: "CNY", UsesFixedCreditDenomination: true, SettlementUnitsPerUSD: decimal.RequireFromString("6.8")}, "500000", "0", 500000, "6.8"},
		{"historical_standard", SettlementPricing{UsesFixedCreditDenomination: true, SettlementUnitsPerUSD: decimal.NewFromInt(1)}, "3400000", "500000", 3400000, "1"},
		{"direct_LDC", SettlementPricing{SettlementCurrency: "LDC", UsesSettlementUnitsPerPlatformUnit: true, SettlementUnitsPerPlatformUnit: decimal.NewFromInt(10)}, "0", "500000", 500000, "10"},
		{"paired_LDC", SettlementPricing{SettlementCurrency: "LDC", SettlementUnitsPerUSD: decimal.NewFromInt(1), PlatformUnitsPerUSD: decimal.RequireFromString("6.8")}, "0", "500000", 3400000, "1"},
		{"paired_repeating", SettlementPricing{SettlementUnitsPerUSD: decimal.NewFromInt(1), PlatformUnitsPerUSD: decimal.NewFromInt(3)}, "0", "500000", 1500000, "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, qpu := decimal.RequireFromString(tc.k), decimal.RequireFromString(tc.qpu)
			rate, err := NormalizeCreditSettlementRate(tc.pricing, k, qpu)
			if err != nil {
				t.Fatal(err)
			}
			quote, err := QuoteSettlementForCredits(tc.credits, tc.pricing, k, qpu)
			if err != nil || !quote.Equal(decimal.RequireFromString(tc.want)) {
				t.Fatalf("quote=%s want=%s error=%v", quote, tc.want, err)
			}
			got, err := rate.CeilMinor(tc.credits, 100)
			want, wrapperErr := CeilSettlementMinorForCredits(tc.credits, tc.pricing, k, qpu, 100)
			if err != nil || wrapperErr != nil || got != want {
				t.Fatalf("normalized and compatibility wrappers disagree: %d/%d %v/%v", got, want, err, wrapperErr)
			}
		})
	}
}

func TestCreditRateAdapterFreezesPriceWithoutMutatingGatewaySnapshot(t *testing.T) {
	pricing := SettlementPricing{SettlementCurrency: "LDC", SettlementUnitsPerUSD: decimal.NewFromInt(1), PlatformUnitsPerUSD: decimal.NewFromInt(3)}
	qpu := decimal.NewFromInt(500000)
	rate, err := NormalizeCreditSettlementRate(pricing, decimal.Zero, qpu)
	if err != nil {
		t.Fatal(err)
	}
	if pricing.PlatformUnitsPerUSD.String() != "3" || qpu.String() != "500000" {
		t.Fatal("normalization modified its inputs")
	}
	pricing.PlatformUnitsPerUSD = decimal.NewFromInt(9)
	qpu = decimal.NewFromInt(1000000)
	if got, err := rate.CeilMinor(1500000, 100); err != nil || got != 100 {
		t.Fatalf("later settings changed normalized rate: %d %v", got, err)
	}
}

func TestCreditRateAdapterRejectsConflictingOrMissingBasis(t *testing.T) {
	for _, pricing := range []SettlementPricing{
		{},
		{UsesFixedCreditDenomination: true, UsesSettlementUnitsPerPlatformUnit: true, SettlementUnitsPerUSD: decimal.NewFromInt(1)},
		{UsesFixedCreditDenomination: true, SettlementUnitsPerUSD: decimal.NewFromInt(1)},
		{UsesSettlementUnitsPerPlatformUnit: true, SettlementUnitsPerPlatformUnit: decimal.NewFromInt(1)},
	} {
		if _, err := NormalizeCreditSettlementRate(pricing, decimal.Zero, decimal.Zero); err == nil {
			t.Fatal("invalid pricing was accepted")
		}
	}
}
