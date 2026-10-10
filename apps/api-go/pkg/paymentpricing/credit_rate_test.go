package paymentpricing

import (
	"math"
	"math/big"
	"strings"
	"sync"
	"testing"
)

func creditTestRat(t *testing.T, value string) *big.Rat {
	t.Helper()
	r, ok := new(big.Rat).SetString(value)
	if !ok {
		t.Fatalf("invalid test rate: %q", value)
	}
	return r
}

func TestCreditRateReconciliation(t *testing.T) {
	for _, tc := range []struct {
		name, units, basis, want string
		credits, minor           int64
	}{
		{"USD", "1", "500000", "1", 500000, 100},
		{"CNY", "6.8", "500000", "6.8", 500000, 680},
		{"CNY changed FX", "7.25", "500000", "7.25", 500000, 725},
		{"custom direct LDC", "10", "500000", "15", 750000, 1500},
		{"paired non-one basis", "1", "3400000", "1", 3400000, 100},
		{"repeating rate", "1", "1500000", "1", 1500000, 100},
		{"one credit", "1", "500000", "0.000002", 1, 1},
		{"large integer", "1", "500000", "18014398509.481982", 9007199254740991, 1801439850949},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rate, err := newCreditRate(creditTestRat(t, tc.units), creditTestRat(t, tc.basis))
			if err != nil {
				t.Fatal(err)
			}
			quote, err := rate.quote(tc.credits)
			if err != nil || quote.Cmp(creditTestRat(t, tc.want)) != 0 {
				t.Fatalf("quote = %v, want %s, err = %v", quote, tc.want, err)
			}
			minor, err := rate.ceilMinor(tc.credits, 100)
			if err != nil || minor != tc.minor {
				t.Fatalf("minor = %d, want %d, err = %v", minor, tc.minor, err)
			}
		})
	}
}

func TestCreditRatePreservesSubCentRemainders(t *testing.T) {
	for _, tc := range []struct {
		units, denominator string
		want               int64
	}{
		{"1", "100", 1},
		{"1.0000000000000000000000001", "100", 2},
		{"0.9999999999999999999999999", "100", 1},
		{"1." + strings.Repeat("0", 120) + "1", "100", 2},
		{"1", "3", 34},
	} {
		rate, err := newCreditRate(creditTestRat(t, tc.units), creditTestRat(t, tc.denominator))
		if err != nil {
			t.Fatal(err)
		}
		got, err := rate.ceilMinor(1, 100)
		if err != nil || got != tc.want {
			t.Fatalf("ceil(%s/%s) = %d, want %d, err = %v", tc.units, tc.denominator, got, tc.want, err)
		}
	}
}

func TestCreditRateOwnsItsInputsAndResults(t *testing.T) {
	numerator, denominator := big.NewRat(1, 1), big.NewRat(500000, 1)
	rate, err := newCreditRate(numerator, denominator)
	if err != nil {
		t.Fatal(err)
	}
	numerator.SetInt64(99)
	denominator.SetInt64(1)
	first, err := rate.quote(500000)
	if err != nil {
		t.Fatal(err)
	}
	first.SetInt64(999)
	second, err := rate.quote(500000)
	if err != nil || second.Cmp(big.NewRat(1, 1)) != 0 {
		t.Fatalf("rate was mutated through input or quote: %v, %v", second, err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := int64(1); n <= 100; n++ {
				got, err := rate.quote(n * 500000)
				if err != nil || got.Cmp(big.NewRat(n, 1)) != 0 {
					t.Errorf("concurrent quote = %v, err = %v", got, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestCreditRateRejectsInvalidAmountsAndOverflow(t *testing.T) {
	for _, pair := range [][2]*big.Rat{
		{nil, big.NewRat(1, 1)}, {big.NewRat(1, 1), nil},
		{big.NewRat(0, 1), big.NewRat(1, 1)}, {big.NewRat(1, 1), big.NewRat(0, 1)},
		{big.NewRat(-1, 1), big.NewRat(1, 1)}, {big.NewRat(1, 1), big.NewRat(-1, 1)},
	} {
		if _, err := newCreditRate(pair[0], pair[1]); err == nil {
			t.Fatal("invalid rate accepted")
		}
	}
	if _, err := (creditRate{}).quote(1); err == nil {
		t.Fatal("zero-value rate accepted")
	}
	rate, err := newCreditRate(big.NewRat(1, 1), big.NewRat(1, 1))
	if err != nil {
		t.Fatal(err)
	}
	for _, amount := range []int64{0, -1, math.MinInt64} {
		if _, err := rate.quote(amount); err == nil {
			t.Fatal("invalid credit amount accepted")
		}
	}
	for _, scale := range []int64{0, -1} {
		if _, err := rate.ceilMinor(1, scale); err == nil {
			t.Fatal("invalid minor scale accepted")
		}
	}
	got, err := rate.ceilMinor(math.MaxInt64, 1)
	if err != nil || got != math.MaxInt64 {
		t.Fatalf("int64 boundary = %d, err = %v", got, err)
	}
	if _, err := rate.ceilMinor(math.MaxInt64, 100); err == nil {
		t.Fatal("invoice overflow accepted")
	}
}

func FuzzCreditRateExactMinor(f *testing.F) {
	f.Add(uint32(1), uint32(1), uint32(3), uint16(100))
	f.Add(uint32(500000), uint32(1), uint32(500000), uint16(100))
	f.Fuzz(func(t *testing.T, rawQ, rawN, rawD uint32, rawScale uint16) {
		// Bound the oracle products within uint64 and keep every case positive.
		q, n, d := uint64(rawQ%1000000)+1, uint64(rawN%1000000)+1, uint64(rawD%1000000)+1
		scale := uint64(rawScale%1000) + 1
		rate, err := newCreditRate(big.NewRat(int64(n), 1), big.NewRat(int64(d), 1))
		if err != nil {
			t.Fatal(err)
		}
		want := (q*n*scale + d - 1) / d
		got, err := rate.ceilMinor(int64(q), int64(scale))
		if err != nil || uint64(got) != want {
			t.Fatalf("minor = %d, want %d, err = %v", got, want, err)
		}
	})
}
