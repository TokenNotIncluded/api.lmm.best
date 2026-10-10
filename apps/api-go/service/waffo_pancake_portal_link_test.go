package service

import (
	"strings"
	"testing"
	"time"
)

func TestWaffoPancakeCustomerPortalLink(t *testing.T) {
	now := time.Date(2026, 10, 10, 19, 0, 0, 0, time.UTC)
	store := "STO_AbCdEfGhIjKlMnOpQrStUv"
	base := "https://pancake.waffo.ai/consumer/portal/store/" + store
	valid := func() *WaffoPancakeCustomerPortalLink {
		return &WaffoPancakeCustomerPortalLink{PortalURL: base + "#token=test-only-token", ExpiresAt: now.Add(15 * time.Minute).Format(time.RFC3339)}
	}
	t.Run("accept SDK portal link", func(t *testing.T) {
		if err := validateWaffoPancakeCustomerPortalLink(valid(), store, now); err != nil {
			t.Fatal(err)
		}
	})
	cases := map[string]string{
		"encoded control":    base + "#token=t%0A",
		"token whitespace":   base + "#token=a%20b",
		"empty URL":          "",
		"HTTP":               strings.Replace(base, "https:", "http:", 1) + "#token=t",
		"different origin":   "https://example.invalid/consumer/portal/store/" + store + "#token=t",
		"host suffix":        "https://pancake.waffo.ai.example.invalid/consumer/portal/store/" + store + "#token=t",
		"port override":      "https://pancake.waffo.ai:444/consumer/portal/store/" + store + "#token=t",
		"userinfo":           "https://user@pancake.waffo.ai/consumer/portal/store/" + store + "#token=t",
		"different store":    strings.Replace(base, store, "STO_other", 1) + "#token=t",
		"wrong path":         "https://pancake.waffo.ai/store/" + store + "#token=t",
		"escaped path":       strings.Replace(base, "/consumer/", "/%63onsumer/", 1) + "#token=t",
		"query token":        base + "?token=test-only-token",
		"extra query":        base + "?return_url=https://example.invalid#token=t",
		"empty query":        base + "?#token=t",
		"no fragment":        base,
		"empty token":        base + "#token=",
		"blank token":        base + "#token=%20",
		"duplicate token":    base + "#token=a&token=b",
		"extra fragment key": base + "#token=a&other=b",
		"invalid escape":     base + "#token=%zz",
		"control character":  base + "#token=t\n",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			link := valid()
			link.PortalURL = raw
			err := validateWaffoPancakeCustomerPortalLink(link, store, now)
			if err == nil {
				t.Fatal("accepted invalid portal link")
			}
			if strings.Contains(err.Error(), "test-only-token") || strings.Contains(err.Error(), raw) && raw != "" {
				t.Fatal("validation error disclosed a URL or token")
			}
		})
	}
	for name, expiry := range map[string]string{
		"missing expiry": "", "invalid expiry": "tomorrow", "expired": now.Add(-time.Second).Format(time.RFC3339), "expires now": now.Format(time.RFC3339),
	} {
		t.Run(name, func(t *testing.T) {
			link := valid()
			link.ExpiresAt = expiry
			if validateWaffoPancakeCustomerPortalLink(link, store, now) == nil {
				t.Fatal("accepted missing or expired validity")
			}
		})
	}
	t.Run("offset expiry", func(t *testing.T) {
		link := valid()
		link.ExpiresAt = now.Add(time.Minute).In(time.FixedZone("test", 3600)).Format(time.RFC3339)
		if err := validateWaffoPancakeCustomerPortalLink(link, store, now); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("nil response", func(t *testing.T) {
		if validateWaffoPancakeCustomerPortalLink(nil, store, now) == nil {
			t.Fatal("accepted nil response")
		}
	})
	for _, storeID := range []string{"", " " + store, store + "/other", store + "?x", store + "#x", store + "\\other"} {
		t.Run("invalid store "+storeID, func(t *testing.T) {
			if validateWaffoPancakeCustomerPortalLink(valid(), storeID, now) == nil {
				t.Fatal("accepted invalid store binding")
			}
		})
	}
}
