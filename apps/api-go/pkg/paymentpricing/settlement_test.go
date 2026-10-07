package paymentpricing

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

func TestParseSettlementPricingKeepsExplicitLDCContract(t *testing.T) {
	method := map[string]string{
		"type": "epay", "settlement_unit": " ldc ", "unit_price": "10.00",
		"settlement_units_per_platform_unit": "10", "topup_ratio": "0.14",
	}
	original := make(map[string]string, len(method))
	for key, value := range method {
		original[key] = value
	}
	pricing, err := ParseSettlementPricing("epay", method, decimal.Zero, decimal.Zero)
	if err != nil {
		t.Fatal(err)
	}
	if pricing.SettlementCurrency != "LDC" || !pricing.UsesSettlementUnitsPerPlatformUnit || pricing.UsesFixedCreditDenomination {
		t.Fatalf("unexpected LDC pricing: %+v", pricing)
	}
	requireDecimal(t, pricing.SettlementUnitsPerPlatformUnit, "10")
	if !reflect.DeepEqual(method, original) {
		t.Fatal("parser mutated the configured payment method")
	}
	quote, err := QuoteSettlementForCredits(750000, pricing, decimal.Zero, decimal.NewFromInt(500000))
	if err != nil {
		t.Fatal(err)
	}
	requireDecimal(t, quote, "15") // topup_ratio is deliberately outside the base quote.
	minor, err := CeilSettlementMinorForCredits(750000, pricing, decimal.Zero, decimal.NewFromInt(500000), 100)
	if err != nil || minor != 1500 {
		t.Fatalf("minor = %d, err = %v", minor, err)
	}
}

func TestParseSettlementPricingPairedLDCDoesNotRequireCNYFX(t *testing.T) {
	for _, explicitPlatform := range []bool{false, true} {
		method := map[string]string{"settlement_currency": "LDC", "settlement_units_per_usd": "1"}
		basis := decimal.RequireFromString("6.8")
		if explicitPlatform {
			method["platform_units_per_usd"] = "6.8"
			basis = decimal.Zero
		}
		pricing, err := ParseSettlementPricing("epay", method, basis, decimal.Zero)
		if err != nil {
			t.Fatal(err)
		}
		quote, err := QuoteSettlementForCredits(3400000, pricing, decimal.Zero, decimal.NewFromInt(500000))
		if err != nil {
			t.Fatal(err)
		}
		requireDecimal(t, quote, "1")
	}
}

func TestParseSettlementPricingRejectsAmbiguousOrMissingContracts(t *testing.T) {
	for name, method := range map[string]map[string]string{
		"missing currency":      {"unit_price": "1"},
		"invalid currency":      {"settlement_unit": "L DC", "unit_price": "1"},
		"LDC without rate":      {"settlement_unit": "LDC"},
		"orphan platform rate":  {"settlement_unit": "LDC", "platform_units_per_usd": "1"},
		"mixed rates":           {"settlement_unit": "LDC", "settlement_units_per_usd": "1", "unit_price": "1"},
		"conflicting aliases":   {"settlement_unit": "LDC", "unit_price": "1", "settlement_units_per_platform_unit": "2"},
		"zero rate":             {"settlement_unit": "LDC", "unit_price": "0"},
		"negative rate":         {"settlement_unit": "LDC", "unit_price": "-1"},
		"exponent rate":         {"settlement_unit": "LDC", "unit_price": "1e2"},
		"blank configured rate": {"settlement_unit": "LDC", "unit_price": ""},
		"zero platform rate":    {"settlement_unit": "LDC", "platform_units_per_usd": "0", "settlement_units_per_usd": "1"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseSettlementPricing("epay", method, decimal.NewFromInt(1), decimal.RequireFromString("6.8")); err == nil {
				t.Fatal("expected invalid pricing to fail")
			}
		})
	}
	if _, err := ParseSettlementPricing("epay", map[string]string{
		"settlement_unit": "LDC", "settlement_units_per_usd": "1",
	}, decimal.Zero, decimal.RequireFromString("6.8")); err == nil {
		t.Fatal("missing immutable platform basis must not fall back to CNY FX")
	}
}

func TestParseSettlementPricingRetainsStandardFiatContracts(t *testing.T) {
	for _, method := range []string{"alipay", "wxpay"} {
		pricing, err := ParseSettlementPricing(method, map[string]string{
			"settlement_currency": "USD", "unit_price": "invalid stale value",
		}, decimal.NewFromInt(1), decimal.RequireFromString("6.8"))
		if err != nil {
			t.Fatal(err)
		}
		if pricing.SettlementCurrency != CurrencyCNY || !pricing.UsesFixedCreditDenomination {
			t.Fatalf("unexpected built-in contract: %+v", pricing)
		}
		quote, err := QuoteSettlementForCredits(500000, pricing, decimal.NewFromInt(500000), decimal.Zero)
		if err != nil {
			t.Fatal(err)
		}
		requireDecimal(t, quote, "6.8")
	}
	usd, err := ParseSettlementPricing("usd", map[string]string{"settlement_unit": "USD"}, decimal.NewFromInt(1), decimal.Zero)
	if err != nil {
		t.Fatal(err)
	}
	requireDecimal(t, usd.SettlementUnitsPerUSD, "1")
}

func TestSettlementQuoteAndExactMinorRetainPairedRemainders(t *testing.T) {
	for _, tc := range []struct {
		name, platformRate, settlementRate, boundary string
		credits, wantMinor                           int64
		comparison                                   int
	}{
		{"above cent", "999999999999.999999999999", "10000000000", "0.01", 500000, 2, 1},
		{"below cent", "999999999999.999999999999", "9999999999.999999999999", "0.01", 500000, 1, -1},
		{"large quota above cent", "999999999999.999999999999", "10000000000", "180000000", 9000000000000000, 18000000001, 1},
		{"exact cent", "100", "1", "0.01", 500000, 1, 0},
		{"cancel repeating rate", "3", "1", "1", 1500000, 100, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pricing, err := ParseSettlementPricing("epay", map[string]string{
				"settlement_unit": "LDC", "platform_units_per_usd": tc.platformRate, "settlement_units_per_usd": tc.settlementRate,
			}, decimal.Zero, decimal.Zero)
			if err != nil {
				t.Fatal(err)
			}
			quote, err := QuoteSettlementForCredits(tc.credits, pricing, decimal.Zero, decimal.NewFromInt(500000))
			if err != nil {
				t.Fatal(err)
			}
			if quote.Cmp(decimal.RequireFromString(tc.boundary)) != tc.comparison {
				t.Fatalf("quote %s lost its relationship to boundary %s", quote, tc.boundary)
			}
			minor, err := CeilSettlementMinorForCredits(tc.credits, pricing, decimal.Zero, decimal.NewFromInt(500000), 100)
			if err != nil || minor != tc.wantMinor {
				t.Fatalf("minor = %d, want %d, err = %v", minor, tc.wantMinor, err)
			}
		})
	}
}

func TestExactSettlementMinorRetainsRemainderBeyondQuotePrecision(t *testing.T) {
	// This operator rate has more than 96 fractional digits. A finite decimal
	// quote becomes exactly one cent; the rational amount remains above it.
	pricing, err := ParseSettlementPricing("epay", map[string]string{
		"settlement_unit": "LDC", "platform_units_per_usd": "100",
		"settlement_units_per_usd": "1." + strings.Repeat("0", 120) + "1",
	}, decimal.Zero, decimal.Zero)
	if err != nil {
		t.Fatal(err)
	}
	quote, err := QuoteSettlementForCredits(500000, pricing, decimal.Zero, decimal.NewFromInt(500000))
	if err != nil {
		t.Fatal(err)
	}
	requireDecimal(t, quote, "0.01")
	minor, err := CeilSettlementMinorForCredits(500000, pricing, decimal.Zero, decimal.NewFromInt(500000), 100)
	if err != nil || minor != 2 {
		t.Fatalf("minor = %d, want 2, err = %v", minor, err)
	}
}

func TestExactSettlementMinorSupportsFixedAndDirectModes(t *testing.T) {
	for _, pricing := range []SettlementPricing{
		{UsesFixedCreditDenomination: true, SettlementUnitsPerUSD: decimal.NewFromInt(1)},
		{UsesSettlementUnitsPerPlatformUnit: true, SettlementUnitsPerPlatformUnit: decimal.NewFromInt(1)},
	} {
		minor, err := CeilSettlementMinorForCredits(1, pricing, decimal.NewFromInt(500000), decimal.NewFromInt(500000), 100)
		if err != nil || minor != 1 {
			t.Fatalf("minor = %d, want 1, err = %v", minor, err)
		}
	}
}

func TestSettlementQuoteRejectsInvalidBasisAndInvoiceOverflow(t *testing.T) {
	direct := SettlementPricing{UsesSettlementUnitsPerPlatformUnit: true, SettlementUnitsPerPlatformUnit: decimal.NewFromInt(1)}
	for _, tc := range []struct {
		credits int64
		pricing SettlementPricing
		k, qpu  decimal.Decimal
	}{
		{0, direct, decimal.Zero, decimal.NewFromInt(500000)},
		{-1, direct, decimal.Zero, decimal.NewFromInt(500000)},
		{1, direct, decimal.Zero, decimal.Zero},
		{1, SettlementPricing{UsesSettlementUnitsPerPlatformUnit: true}, decimal.Zero, decimal.NewFromInt(500000)},
		{1, SettlementPricing{UsesFixedCreditDenomination: true, SettlementUnitsPerUSD: decimal.NewFromInt(1)}, decimal.Zero, decimal.NewFromInt(500000)},
		{1, SettlementPricing{UsesFixedCreditDenomination: true, UsesSettlementUnitsPerPlatformUnit: true}, decimal.NewFromInt(500000), decimal.NewFromInt(500000)},
		{1, SettlementPricing{PlatformUnitsPerUSD: decimal.NewFromInt(1)}, decimal.Zero, decimal.NewFromInt(500000)},
	} {
		if _, err := QuoteSettlementForCredits(tc.credits, tc.pricing, tc.k, tc.qpu); err == nil {
			t.Fatal("expected invalid credit pricing to fail")
		}
		if _, err := CeilSettlementMinorForCredits(tc.credits, tc.pricing, tc.k, tc.qpu, 100); err == nil {
			t.Fatal("expected invalid minor pricing to fail")
		}
	}
	if _, err := CeilSettlementMinorForCredits(1, direct, decimal.Zero, decimal.NewFromInt(1), 0); err == nil {
		t.Fatal("zero minor scale must fail")
	}
	minor, err := CeilSettlementMinorForCredits(math.MaxInt64, direct, decimal.Zero, decimal.NewFromInt(1), 1)
	if err != nil || minor != math.MaxInt64 {
		t.Fatalf("max int64 quote = %d, err = %v", minor, err)
	}
	if _, err := CeilSettlementMinorForCredits(math.MaxInt64, direct, decimal.Zero, decimal.NewFromInt(1), 100); err == nil {
		t.Fatal("overflowing invoice minor amount must fail")
	}
}
