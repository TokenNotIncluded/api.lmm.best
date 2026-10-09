package marketprovider

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestMonidPriceFormats(t *testing.T) {
	for _, raw := range []string{
		`{"type":"PER_CALL","amount":{"value":0.006,"currency":"USD"}}`,
		`{"type":"PER_CALL","amount":0.006,"currency":"USD"}`,
	} {
		q, err := MonidQuote(json.RawMessage(raw))
		if err != nil {
			t.Fatal(err)
		}
		got, err := q.Quota("1.2", "500000")
		if err != nil || got != 3600 {
			t.Fatalf("quota=%d err=%v", got, err)
		}
	}
}

func TestPriceRejectsMissingAndUnsupported(t *testing.T) {
	for _, raw := range []string{
		`{}`, `null`, `{"type":"PER_CALL","amount":0}`, `{"type":"PER_CALL","amount":null,"currency":"USD"}`,
		`{"type":"PER_CALL","amount":-1,"currency":"USD"}`,
		`{"type":"PER_CALL","amount":{"value":1,"currency":"EUR"}}`,
		`{"type":"PER_CALL","amount":{"value":1,"currency":"USD"},"currency":"EUR"}`,
		`{"type":"DYNAMIC","amount":1,"currency":"USD"}`,
		`{"type":"PER_CALL","amount":1,"currency":"USD","flatFee":null}`,
	} {
		if _, err := MonidQuote(json.RawMessage(raw)); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	if _, err := AgentKeyQuote(json.RawMessage(`{"credits_per_call":3}`)); err == nil {
		t.Fatal("credits incorrectly treated as USD")
	}
}

func TestExactCeilingAndMultiplierBounds(t *testing.T) {
	q, err := AgentKeyQuote(json.RawMessage(`{"usd_per_call":0.00000000001,"credits_per_call":0.2}`))
	if err != nil {
		t.Fatal(err)
	}
	n, err := q.Quota("1.2", "500000")
	if err != nil || n != 1 {
		t.Fatalf("tiny price became %d: %v", n, err)
	}
	for _, bad := range []string{"0", "0.99", "100.1", "NaN", "1/2", " 1", "-1", "1e99"} {
		if _, err := q.Quota(bad, "500000"); err == nil {
			t.Errorf("accepted multiplier %q", bad)
		}
	}
	q.AmountUSD = "0"
	if n, err := q.Quota("1.2", "500000"); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}

func TestResultPriceIsNotPerCall(t *testing.T) {
	q, err := MonidQuote(json.RawMessage(`{"type":"PER_RESULT","amount":0.000253,"currency":"USD","flatFee":0.002}`))
	if err != nil || q.Unit != "result" || q.FlatFeeUSD != "0.002" {
		t.Fatal(q, err)
	}
	if _, err := q.Quota("1.2", "500000"); !errors.Is(err, ErrVariablePrice) {
		t.Fatalf("variable price silently billed per call: %v", err)
	}
}

func TestPresetIdentityAndAccountBoundary(t *testing.T) {
	p := Pricing{Provider: "agentkey", Multiplier: "1.2"}
	if p.Validate("https://api.agentkey.app/v1/mcp", "execute_tool") != nil {
		t.Fatal("valid preset rejected")
	}
	if p.Validate("https://attacker.example/mcp", "execute_tool") == nil {
		t.Fatal("credential origin not bound")
	}
	for _, name := range []string{"agentkey_account", "AgentKey/account", "something", " Firecrawl/scrape"} {
		if _, err := QuoteArguments("agentkey", map[string]any{"name": name}); err == nil {
			t.Errorf("accepted %s", name)
		}
	}
	if _, err := QuoteArguments("agentkey", map[string]any{"name": "Firecrawl/scrape"}); err != nil {
		t.Fatal(err)
	}
}
