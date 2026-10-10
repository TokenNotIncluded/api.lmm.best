package service

import (
	"regexp"
	"testing"
)

func TestWaffoPancakeRequestKey(t *testing.T) {
	key := waffoPancakeRequestKey("checkout", "merchant-a", "order-1")
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`).MatchString(key) {
		t.Fatalf("invalid provider idempotency key: %q", key)
	}
	if retry := waffoPancakeRequestKey("checkout", "merchant-a", "order-1"); retry != key {
		t.Fatal("a retry changed the operation key")
	}
	for _, other := range []string{
		waffoPancakeRequestKey("publish", "merchant-a", "order-1"),
		waffoPancakeRequestKey("checkout", "merchant-b", "order-1"),
		waffoPancakeRequestKey("checkout", "merchant-a", "order-2"),
	} {
		if other == key {
			t.Fatal("different operations share an idempotency key")
		}
	}
	if waffoPancakeRequestKey("create", "ab", "c") == waffoPancakeRequestKey("create", "a", "bc") {
		t.Fatal("field boundaries were lost")
	}
	if waffoPancakeRequestKey("create", "a:b", "c") == waffoPancakeRequestKey("create", "a", "b:c") {
		t.Fatal("separator-bearing values share an idempotency key")
	}
}

func TestWaffoPancakeReportedAmount(t *testing.T) {
	zero, prorated, blank := "0.00", "4.50", ""
	for _, tc := range []struct {
		name     string
		reported *string
		legacy   string
		snapshot bool
		want     string
	}{
		{"legacy", nil, "10.00", false, "10.00"},
		{"explicit zero", &zero, "10.00", true, "0.00"},
		{"prorated charge", &prorated, "10.00", true, "4.50"},
		{"reported without snapshot", &prorated, "10.00", false, "4.50"},
		{"unknown with list price", nil, "10.00", true, ""},
		{"blank is not a legacy fallback", &blank, "10.00", true, ""},
		{"missing legacy amount", nil, "", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := waffoPancakeReportedAmount(tc.reported, tc.legacy, tc.snapshot); got != tc.want {
				t.Fatalf("amount = %q, want %q", got, tc.want)
			}
		})
	}
}
