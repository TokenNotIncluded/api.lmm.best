package toolmarket

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

type network struct {
	client *http.Client
	test   bool
}

func newNetwork() *network {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	tr := &http.Transport{Proxy: nil, MaxIdleConns: 16, MaxIdleConnsPerHost: 2, MaxConnsPerHost: 4, IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	tr.DialContext = func(ctx context.Context, kind, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, ErrDenied
		}
		ips, e := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if e != nil || len(ips) == 0 {
			return nil, ErrUpstream
		}
		// Validate every answer, then dial a checked IP directly. There is no second
		// DNS lookup and no proxy path that can bypass this check.
		for _, ip := range ips {
			if !publicIP(ip) {
				return nil, ErrDenied
			}
		}
		for _, ip := range ips {
			c, e := dialer.DialContext(ctx, kind, net.JoinHostPort(ip.String(), port))
			if e == nil {
				return c, nil
			}
		}
		return nil, ErrUpstream
	}
	return &network{client: &http.Client{Transport: tr, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("2001::/23"), netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"),
}

func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	if ip.Is6() && !netip.MustParsePrefix("2000::/3").Contains(ip) {
		return false
	}
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}
func (n *network) url(raw string) (*url.URL, error) {
	if len(raw) == 0 || len(raw) > 2048 {
		return nil, ErrInvalid
	}
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || strings.ContainsAny(raw, "\r\n\\") {
		return nil, ErrInvalid
	}
	if n.test && u.Scheme == "http" {
		return u, nil
	}
	if u.Scheme != "https" || u.Port() != "" && u.Port() != "443" {
		return nil, ErrDenied
	}
	h := strings.ToLower(u.Hostname())
	if h == "localhost" || strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".local") || strings.Contains(h, "%") {
		return nil, ErrDenied
	}
	if ip, e := netip.ParseAddr(h); e == nil && !publicIP(ip) {
		return nil, ErrDenied
	}
	return u, nil
}
func (n *network) request(ctx context.Context, method, endpoint string, body []byte, headers http.Header) (*http.Response, error) {
	if _, e := n.url(endpoint); e != nil {
		return nil, e
	}
	r, e := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if e != nil {
		return nil, ErrInvalid
	}
	r.Header = headers.Clone()
	resp, e := n.client.Do(r)
	if e != nil {
		return nil, ErrUpstream
	}
	return resp, nil
}
func readBody(r *http.Response) ([]byte, error) {
	defer r.Body.Close()
	b, e := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if e != nil || len(b) > maxBody {
		return nil, ErrUpstream
	}
	return b, nil
}
func (n *network) getJSON(ctx context.Context, endpoint string, v any) error {
	r, e := n.request(ctx, "GET", endpoint, nil, http.Header{"Accept": []string{"application/json"}, "Mcp-Protocol-Version": []string{ProtocolVersion}})
	if e != nil {
		return e
	}
	b, e := readBody(r)
	if e != nil {
		return e
	}
	if r.StatusCode == 404 {
		return ErrMissing
	}
	if r.StatusCode != 200 {
		return ErrUpstream
	}
	if uniqueJSON(b) != nil || json.Unmarshal(b, v) != nil {
		return ErrUpstream
	}
	return nil
}
func origin(u *url.URL) string { return u.Scheme + "://" + u.Host }
