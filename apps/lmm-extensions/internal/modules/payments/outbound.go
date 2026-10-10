package payments

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

func outboundClient() *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	t.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	t.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, ErrInvalid
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, ErrUnavailable
		}
		if len(ips) == 0 {
			return nil, ErrUnavailable
		}
		for _, ip := range ips {
			if !publicIP(ip) {
				return nil, ErrDenied
			}
		}
		d := net.Dialer{Timeout: 5 * time.Second}
		return d.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
	}
	return &http.Client{Timeout: 8 * time.Second, Transport: t, CheckRedirect: func(*http.Request, []*http.Request) error { return ErrDenied }}
}
func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return false
	}
	for _, s := range []string{"100.64.0.0/10", "192.0.0.0/24", "198.18.0.0/15", "64:ff9b::/96"} {
		if netip.MustParsePrefix(s).Contains(ip) {
			return false
		}
	}
	return true
}
func secureURL(s string) (*url.URL, error) {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" {
		return nil, ErrInvalid
	}
	if u.Port() != "" && u.Port() != "443" {
		return nil, ErrInvalid
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && !publicIP(ip) {
		return nil, ErrInvalid
	}
	if strings.EqualFold(u.Hostname(), "localhost") {
		return nil, ErrInvalid
	}
	return u, nil
}
func boundedBytes(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, MaxEvidence+1))
	if err != nil {
		return nil, ErrInvalid
	}
	if len(b) > MaxEvidence {
		return nil, ErrInvalid
	}
	return b, nil
}
func jsonObject(b []byte, dst any) error {
	// Reject duplicate keys at every depth to avoid different Go/Rust parsers
	// assigning different meanings to the same signed bytes.
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err := jsonValue(d, 0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrInvalid
	}
	if json.Unmarshal(b, dst) != nil {
		return ErrInvalid
	}
	return nil
}
func jsonValue(d *json.Decoder, depth int) error {
	if depth > 32 {
		return ErrInvalid
	}
	t, err := d.Token()
	if err != nil {
		return ErrInvalid
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			k, err := d.Token()
			if err != nil {
				return ErrInvalid
			}
			s, ok := k.(string)
			if !ok || seen[s] {
				return ErrInvalid
			}
			seen[s] = true
			if err = jsonValue(d, depth+1); err != nil {
				return err
			}
		}
		t, err = d.Token()
		if err != nil || t != json.Delim('}') {
			return ErrInvalid
		}
	case '[':
		for d.More() {
			if err = jsonValue(d, depth+1); err != nil {
				return err
			}
		}
		t, err = d.Token()
		if err != nil || t != json.Delim(']') {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}
func requestJSON(ctx context.Context, client *http.Client, method, endpoint string, form url.Values, headers http.Header) ([]byte, error) {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	r, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, ErrInvalid
	}
	r.Header = headers.Clone()
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	res, err := client.Do(r)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer res.Body.Close()
	b, err := boundedBytes(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, ErrUnavailable
	} // No provider error body or secret URL escapes.
	return b, nil
}
