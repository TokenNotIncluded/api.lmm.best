package store

import (
	"encoding/json"
	"testing"
)

func TestPricePreservesTypedIntegerUnits(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want Price
	}{
		{`{"amount_minor":125,"currency":"USD"}`, Price{Amount: 125, Currency: "USD"}},
		{`{"amount_credits":9007199254740991,"unit":"credits"}`, Price{Amount: 9007199254740991, Unit: "credits"}},
	} {
		var got Price
		if err := json.Unmarshal([]byte(tc.raw), &got); err != nil || got != tc.want {
			t.Fatalf("decode %s: %v %#v", tc.raw, err, got)
		}
		raw, err := json.Marshal(got)
		if err != nil || string(raw) != tc.raw {
			t.Fatalf("roundtrip: %s %v", raw, err)
		}
	}
	for _, raw := range []string{
		`null`, `{}`, `{"amount_credits":1,"unit":"credits","currency":null}`,
		`{"amount_credits":1,"unit":"credits","amount_minor":null}`,
		`{"amount_credits":1,"amount_credits":2,"unit":"credits"}`,
		`{"amount_credits":1,"Unit":"credits"}`, `{"amount_credits":1.0,"unit":"credits"}`,
		`{"amount_credits":1e2,"unit":"credits"}`, `{"amount_credits":-1,"unit":"credits"}`,
		`{"amount_credits":9223372036854775808,"unit":"credits"}`,
		`{"amount_minor":1,"currency":"USD","unit":"credits"}`,
	} {
		var got Price
		if json.Unmarshal([]byte(raw), &got) == nil {
			t.Errorf("accepted malformed price %s", raw)
		}
	}
	if ID(Price{Amount: 1, Unit: "credits"}) == ID(Price{Amount: 1, Currency: "USD"}) {
		t.Fatal("credits and currency minor have the same command identity")
	}
}
