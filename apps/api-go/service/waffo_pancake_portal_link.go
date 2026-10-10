package service

import (
	"errors"
	"net/url"
	"strings"
	"time"
	"unicode"
)

// WaffoPancakeCustomerPortalLink contains a short-lived bearer URL. Never log or
// persist it, and do not include it in an assistant tool response.
type WaffoPancakeCustomerPortalLink struct {
	PortalURL string `json:"portal_url"`
	ExpiresAt string `json:"expires_at"`
}

// The SDK uses the hosted Pancake portal. Fail closed if the provider returns
// another origin, store, or token location instead of following an arbitrary URL.
func validateWaffoPancakeCustomerPortalLink(link *WaffoPancakeCustomerPortalLink, storeID string, now time.Time) error {
	invalid := errors.New("invalid Waffo Pancake customer portal response")
	if link == nil || storeID == "" || strings.TrimSpace(storeID) != storeID || strings.ContainsAny(storeID, "/?#\\") {
		return invalid
	}
	if strings.IndexFunc(link.PortalURL, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return invalid
	}
	u, err := url.Parse(link.PortalURL)
	if err != nil || u.Scheme != "https" || u.Host != "pancake.waffo.ai" || u.User != nil ||
		u.Opaque != "" || u.RawQuery != "" || u.ForceQuery ||
		u.EscapedPath() != "/consumer/portal/store/"+storeID {
		return invalid
	}
	fragment, err := url.ParseQuery(u.Fragment)
	if err != nil || len(fragment) != 1 || len(fragment["token"]) != 1 || fragment.Get("token") == "" ||
		strings.IndexFunc(fragment.Get("token"), func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return invalid
	}
	expiresAt, err := time.Parse(time.RFC3339, link.ExpiresAt)
	if err != nil || !expiresAt.After(now) {
		return invalid
	}
	return nil
}
