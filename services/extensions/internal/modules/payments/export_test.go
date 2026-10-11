package payments

import "net/http"

// ReplaceStripeTransportForTest exists only in the Go test binary. Production
// still has a fixed Stripe endpoint and its outbound network policy.
func ReplaceStripeTransportForTest(s *Stripe, transport http.RoundTripper) {
	s.client = &http.Client{Transport: transport}
}
