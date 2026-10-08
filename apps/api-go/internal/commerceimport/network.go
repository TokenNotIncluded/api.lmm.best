package commerceimport

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

// Mirrors the project's immutable public-network protection rather than its
// configurable fetch settings: an administrator's allow-private-IP setting
// must never make an OAuth secret eligible for an internal destination.
var deniedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("168.63.129.16/32"), netip.MustParsePrefix("fec0::/10"), netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2001::/23"), netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("64:ff9b::/96"), netip.MustParsePrefix("64:ff9b:1::/48"),
}

var publicIPv6Allocation = netip.MustParsePrefix("2000::/3")

func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	if ip.Is6() && !publicIPv6Allocation.Contains(ip) {
		return false
	}
	for _, prefix := range deniedPrefixes {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

type lookupFunc func(context.Context, string, string) ([]netip.Addr, error)
type dialFunc func(context.Context, string, string) (net.Conn, error)

// newHTTPClient's dependency parameters are an internal test seam. Production
// construction always uses the OS resolver, direct dialing, and platform TLS
// trust; callers cannot inject an arbitrary transport or disable TLS checks.
func newHTTPClient(lookup lookupFunc, dial dialFunc, tlsConfig *tls.Config) *http.Client {
	transport := &http.Transport{
		Proxy: nil, ForceAttemptHTTP2: true, TLSClientConfig: tlsConfig,
		TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 20 * time.Second,
		MaxResponseHeaderBytes: 32 << 10, MaxIdleConns: 32, MaxIdleConnsPerHost: 4,
		MaxConnsPerHost: 8, IdleConnTimeout: 30 * time.Second,
	}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		host = strings.ToLower(host)
		if err != nil || !publicHostname(host) {
			return nil, ErrInvalidOrigin
		}
		ips, err := lookup(ctx, "ip", host)
		if err != nil || len(ips) == 0 {
			return nil, ErrNetwork
		}
		// Validate every result before making any connection, even if the first
		// result was public. Dial a literal IP so DNS cannot change in between.
		for _, ip := range ips {
			if !publicIP(ip) {
				return nil, ErrInvalidOrigin
			}
		}
		for _, ip := range ips {
			conn, err := dial(ctx, network, net.JoinHostPort(ip.Unmap().String(), port))
			if err == nil {
				return conn, nil
			}
		}
		return nil, ErrNetwork
	}
	return &http.Client{Transport: transport, Timeout: 45 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

type Client struct {
	http *http.Client
	now  func() time.Time
}

// NewClient returns a dedicated direct HTTPS client with no cookie jar, proxy
// environment support, redirects, shared cache, or automatic protocol retry.
func NewClient() *Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	return &Client{http: newHTTPClient(net.DefaultResolver.LookupNetIP, dialer.DialContext, &tls.Config{MinVersion: tls.VersionTLS12}), now: time.Now}
}

func (c *Client) CloseIdleConnections() { c.http.CloseIdleConnections() }

var defaultClient = NewClient()
