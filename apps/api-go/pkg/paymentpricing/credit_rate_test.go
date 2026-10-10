package paymentpricing

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
)

func mustCreditRate(t *testing.T, numerator, denominator string) CreditSettlementRate {
	t.Helper()
	rate, err := NewCreditSettlementRate(numerator, denominator)
	if err != nil {
		t.Fatal(err)
	}
	return rate
}

func TestCreditSettlementRateReconciliation(t *testing.T) {
	for _, tc := range []struct {
		name, numerator, denominator, want string
		credits, minor, wantMinor          int64
		wantMinimum                        bool
	}{
		{"USD", "1", "500000", "1", 500000, 100, 100, true},
		{"CNY", "6.8", "500000", "6.8", 500000, 100, 680, true},
		{"CNY_new_quote", "7.25", "500000", "7.25", 500000, 100, 725, true},
		{"one_credit", "1", "500000", "0.000002", 1, 100, 1, false},
		{"below_cent", "1", "500000", "0.009998", 4999, 100, 1, false},
		{"one_cent", "1", "500000", "0.01", 5000, 100, 1, true},
		{"above_cent", "1", "500000", "0.010002", 5001, 100, 2, true},
		{"direct_LDC", "10", "500000", "10", 500000, 100, 1000, true},
		{"paired_LDC", "1", "3400000", "1", 3400000, 100, 100, true},
		{"historical_basis", "1", "3500000", "1", 3500000, 100, 100, true},
		{"zero_decimal_invoice", "150", "500000", "150", 500000, 1, 150, true},
		{"three_decimal_invoice", "0.307", "500000", "0.307", 500000, 1000, 307, true},
		{"fractional_denominator", "1.25", "2.5", "3.5", 7, 100, 350, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rate := mustCreditRate(t, tc.numerator, tc.denominator)
			quote, err := rate.QuoteDecimal(tc.credits)
			if err != nil {
				t.Fatal(err)
			}
			quote = strings.TrimRight(strings.TrimRight(quote, "0"), ".")
			if quote != tc.want {
				t.Fatalf("quote=%s want=%s", quote, tc.want)
			}
			minor, err := rate.CeilMinor(tc.credits, tc.minor)
			if err != nil || minor != tc.wantMinor {
				t.Fatalf("minor=%d error=%v want=%d", minor, err, tc.wantMinor)
			}
			minimum, err := rate.ReachesMinor(tc.credits, tc.minor)
			if err != nil || minimum != tc.wantMinimum {
				t.Fatalf("minimum=%v error=%v want=%v", minimum, err, tc.wantMinimum)
			}
			t.Logf("RECON %s credits=%d quote=%s minor=%d minimum=%v", tc.name, tc.credits, quote, minor, minimum)
		})
	}
}

func TestCreditSettlementRateRepeatingFractionsKeepWholeDenominator(t *testing.T) {
	rate := mustCreditRate(t, "1", "3")
	got, err := rate.QuoteDecimal(1)
	if err != nil || got != "0."+strings.Repeat("3", 96) {
		t.Fatalf("one third=%s error=%v", got, err)
	}
	got, err = rate.QuoteDecimal(2)
	if err != nil || got != "0."+strings.Repeat("6", 95)+"7" {
		t.Fatalf("two thirds=%s error=%v", got, err)
	}
	if value, err := rate.CeilMinor(3, 100); err != nil || value != 100 {
		t.Fatalf("exact amount must not gain a cent: %d %v", value, err)
	}
	// Final decimal quote is exactly at a 96-place rounding tie.
	tie := mustCreditRate(t, "1", "2"+strings.Repeat("0", 96))
	got, err = tie.QuoteDecimal(1)
	if err != nil || got != "0."+strings.Repeat("0", 95)+"1" {
		t.Fatalf("tie=%s error=%v", got, err)
	}
}

func TestCreditSettlementRateRejectsInvalidInputs(t *testing.T) {
	invalid := []string{"", "0", "0.0", "-1", "+1", "NaN", "Inf", "1e2", "1/2", ".5", "1.", "1..5", " 1", "1 ", "１２", "1_000"}
	for _, value := range invalid {
		if _, err := NewCreditSettlementRate(value, "1"); err == nil {
			t.Errorf("accepted numerator %q", value)
		}
		if _, err := NewCreditSettlementRate("1", value); err == nil {
			t.Errorf("accepted denominator %q", value)
		}
	}
	rate := mustCreditRate(t, "1", "500000")
	for _, credits := range []int64{-1, 0} {
		if _, err := rate.QuoteDecimal(credits); err == nil {
			t.Errorf("accepted credit amount %d", credits)
		}
		if _, err := rate.CeilMinor(credits, 100); err == nil {
			t.Errorf("accepted credit amount %d in invoice", credits)
		}
	}
	for _, minor := range []int64{-1, 0} {
		if _, err := rate.CeilMinor(1, minor); err == nil {
			t.Errorf("accepted minor-unit scale %d", minor)
		}
		if _, err := rate.ReachesMinor(1, minor); err == nil {
			t.Errorf("accepted minimum scale %d", minor)
		}
	}
	var missing CreditSettlementRate
	if _, err := missing.QuoteDecimal(1); err == nil {
		t.Fatal("zero-value rate accepted")
	}
	if _, err := missing.CeilMinor(1, 100); err == nil {
		t.Fatal("zero-value invoice rate accepted")
	}
	if _, err := missing.ReachesMinor(1, 100); err == nil {
		t.Fatal("zero-value minimum rate accepted")
	}
}

func TestCreditSettlementRateInt64Boundary(t *testing.T) {
	rate := mustCreditRate(t, "1", "1")
	got, err := rate.CeilMinor(math.MaxInt64, 1)
	if err != nil || got != math.MaxInt64 {
		t.Fatalf("int64 boundary=%d error=%v", got, err)
	}
	if _, err := rate.CeilMinor(math.MaxInt64, 100); err == nil {
		t.Fatal("overflow must fail")
	}
	// Multiplication may exceed int64 before division, while the invoice fits.
	rate = mustCreditRate(t, "1", "100")
	got, err = rate.CeilMinor(math.MaxInt64, 100)
	if err != nil || got != math.MaxInt64 {
		t.Fatalf("intermediate overflow changed valid result: %d %v", got, err)
	}
}

func TestCreditSettlementRateIntegerReference(t *testing.T) {
	// Independent int64 arithmetic, with deliberately bounded intermediates.
	// Test the result and both sides of each upward-rounded interval.
	for denominator := int64(1); denominator <= 97; denominator++ {
		for numerator := int64(1); numerator <= 19; numerator++ {
			rate := mustCreditRate(t, fmt.Sprint(numerator), fmt.Sprint(denominator))
			for _, credits := range []int64{1, 2, 7, 31, 97, 5000} {
				scaled := credits * numerator * 100
				want := scaled / denominator
				if scaled%denominator != 0 {
					want++
				}
				got, err := rate.CeilMinor(credits, 100)
				if err != nil || got != want {
					t.Fatalf("%d*%d/%d got=%d want=%d error=%v", credits, numerator, denominator, got, want, err)
				}
				minimum, err := rate.ReachesMinor(credits, 100)
				if err != nil || minimum != (scaled >= denominator) {
					t.Fatalf("minimum check disagrees with integer reference")
				}
			}
		}
	}
}

func TestCreditSettlementRateConcurrentImmutableQuotes(t *testing.T) {
	rate := mustCreditRate(t, "6.8", "500000")
	copyOfRate := rate
	var wg sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for iteration := 0; iteration < 100; iteration++ {
				got, err := copyOfRate.CeilMinor(500000, 100)
				if err != nil || got != 680 {
					t.Errorf("shared rate changed: %d %v", got, err)
				}
				if _, err := rate.QuoteDecimal(1); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	if got, err := rate.CeilMinor(500000, 100); err != nil || got != 680 {
		t.Fatalf("rate mutated after concurrent use: %d %v", got, err)
	}
}
