package marketprovider

import "testing"

// Site currency anchors can exceed the separate upstream price limit.
func TestLedgerScaleIsNotAnUpstreamPrice(t *testing.T) {
	q := Quote{Provider: "agentkey", Unit: "call", AmountUSD: "0.004"}
	for units, want := range map[string]int{"500000": 2400, "3500000": 16800, "2.5e7": 120000} {
		got, err := q.Quota("1.2", units)
		if err != nil || got != want {
			t.Fatalf("scale %s: quota=%d, want=%d, err=%v", units, got, want, err)
		}
	}
	for _, invalid := range []string{"0", "-1", "NaN", "1/2", "1e999", "1e99"} {
		if _, err := q.Quota("1.2", invalid); err == nil {
			t.Errorf("accepted invalid or overflowing ledger scale %q", invalid)
		}
	}
	q.AmountUSD = "1000001"
	if _, err := q.Quota("1.2", "3500000"); err == nil {
		t.Fatal("ledger fix removed the upstream price limit")
	}
}
