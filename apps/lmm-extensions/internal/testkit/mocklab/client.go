// Package mocklab is a test-only client for the standalone provider process.
// Only _test.go files may import this package. It must not enter a runtime host.
package mocklab

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const Marker = "LMM-MOCK-ONLY-v1"
const StripeKey = "sk_test_mocklab_only"
const StripeSecret = "whsec_mocklab_only_not_a_real_secret"
const StripeVersion = "mocklab-v1-fixture"
const StripeMerchant = "acct_mocklab"
const EPayKey = "mocklab_epay_secret_only"
const EPayMerchant = "1001"

var ErrBoundary = errors.New("mocklab: undeclared destination or invalid manifest")
var runPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,47}$`)

type Instance struct {
	Protocol string `json:"protocol"`
	Model    string `json:"model"`
	URL      string `json:"url"`
}
type Manifest struct {
	Marker       string              `json:"marker"`
	RunID        string              `json:"run_id"`
	BaseURL      string              `json:"base_url"`
	ControlToken string              `json:"control_token"`
	Epoch        int64               `json:"epoch"`
	Instances    map[string]Instance `json:"instances"`
}
type Client struct {
	Manifest Manifest
	base     *url.URL
	local    *http.Client
}

func Open(path string) (*Client, error) {
	st, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 || st.Size() > 65536 {
		return nil, ErrBoundary
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

func Parse(b []byte) (*Client, error) {
	var m Manifest
	if len(b) > 65536 || json.Unmarshal(b, &m) != nil || m.Marker != Marker || !runPattern.MatchString(m.RunID) ||
		len(m.ControlToken) < 32 || len(m.ControlToken) > 128 || m.Epoch < 1 || len(m.Instances) == 0 || len(m.Instances) > 16 {
		return nil, ErrBoundary
	}
	u, err := url.Parse(m.BaseURL)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || u.Port() == "" {
		return nil, ErrBoundary
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return nil, ErrBoundary
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || ip.To4() == nil || !ip.IsLoopback() {
		return nil, ErrBoundary
	}
	for name, item := range m.Instances {
		if !namePattern.MatchString(name) || item.URL != m.BaseURL+"/r/"+m.RunID+"/"+name {
			return nil, ErrBoundary
		}
	}
	// No environment proxy and no production transport fallback. A second DNS
	// lookup cannot change the destination because only an IP literal is used.
	tr := &http.Transport{Proxy: nil, DisableKeepAlives: true, MaxConnsPerHost: 4,
		ResponseHeaderTimeout: 3 * time.Second}
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != u.Host {
			return nil, ErrBoundary
		}
		return dialer.DialContext(ctx, network, address)
	}
	c := &http.Client{Transport: tr, Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return ErrBoundary }}
	return &Client{Manifest: m, base: u, local: c}, nil
}

// HTTP maps one declared original host to one mock instance. Production code
// still constructs and parses its real provider protocol. No validation flag is
// added to production configuration, and all other network targets fail closed.
func (c *Client) HTTP(instance, originalHost string) (*http.Client, error) {
	item, ok := c.Manifest.Instances[instance]
	if !ok || originalHost == "" || strings.ContainsAny(originalHost, ":/@?#\\") {
		return nil, ErrBoundary
	}
	target, err := url.Parse(item.URL)
	if err != nil {
		return nil, ErrBoundary
	}
	return &http.Client{Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return ErrBoundary },
		Transport:     mapped{client: c, target: target, originalHost: originalHost}}, nil
}

type mapped struct {
	client       *Client
	target       *url.URL
	originalHost string
}

func (m mapped) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL == nil || r.URL.Scheme != "https" || r.URL.Host != m.originalHost || r.URL.User != nil ||
		r.URL.Fragment != "" || r.URL.RawPath != "" || !strings.HasPrefix(r.URL.Path, "/") || strings.Contains(r.URL.Path, "..") {
		return nil, ErrBoundary
	}
	clone := r.Clone(r.Context())
	u := *r.URL
	u.Scheme, u.Host = m.target.Scheme, m.target.Host
	u.Path = m.target.Path + r.URL.Path
	clone.URL, clone.Host = &u, ""
	clone.Header = r.Header.Clone()
	clone.Header.Set("X-Mock-Run", m.client.Manifest.RunID)
	// The original host, path and credentials cannot select a different socket.
	response, err := m.client.local.Do(clone)
	if err != nil {
		if response != nil {
			response.Body.Close()
		}
		return nil, err
	}
	return response, nil
}

func (c *Client) Control(ctx context.Context, fields map[string]any, result any) error {
	raw, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	r, err := http.NewRequestWithContext(ctx, "POST", c.Manifest.BaseURL+"/control/"+c.Manifest.RunID, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Mock-Control", c.Manifest.ControlToken)
	response, err := c.local.Do(r)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1048577))
	if err != nil || len(data) > 1048576 || response.StatusCode != 200 {
		return ErrBoundary
	}
	if result == nil {
		return nil
	}
	return json.Unmarshal(data, result)
}

func (c *Client) Transition(ctx context.Context, instance, reference, state string) (string, error) {
	var result struct {
		Event string `json:"event"`
	}
	err := c.Control(ctx, map[string]any{"op": "transition", "instance": instance, "reference": reference, "state": state}, &result)
	if err == nil && result.Event == "" {
		return "", ErrBoundary
	}
	return result.Event, err
}

func (c *Client) Delivery(ctx context.Context, instance, event string) (*http.Request, error) {
	var result struct {
		Method  string            `json:"method"`
		Body    string            `json:"body_base64"`
		Headers map[string]string `json:"headers"`
	}
	err := c.Control(ctx, map[string]any{"op": "delivery", "instance": instance, "event": event}, &result)
	if err != nil {
		return nil, err
	}
	body, err := base64.StdEncoding.DecodeString(result.Body)
	if err != nil || len(body) > 32768 || result.Method != "POST" {
		return nil, ErrBoundary
	}
	r, err := http.NewRequestWithContext(ctx, "POST", "https://callback.mock.invalid/callbacks/1", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, v := range result.Headers {
		r.Header.Set(k, v)
	}
	return r, nil
}
