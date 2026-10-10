package controller

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
)

const ProfileShareSettingsMaxBytes = 16 << 10
const profileShareMaxLinkedProfiles = 5
const profileShareMaxMetric int64 = 1<<53 - 1

var profileShareHandlePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,79}$`)
var profileShareHostPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+$`)

func validateProfileLinkedURL(provider, raw string) (*url.URL, error) {
	if len(raw) > 512 {
		return nil, errors.New("profile URL is too long")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || u.Port() != "" || u.Host != strings.ToLower(u.Hostname()) {
		return nil, errors.New("profile URL must use canonical HTTPS without credentials, ports or query")
	}
	host := u.Hostname()
	if !profileShareHostPattern.MatchString(host) || net.ParseIP(host) != nil || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".lan") || strings.HasSuffix(host, ".home") || strings.HasSuffix(host, ".arpa") || strings.HasSuffix(host, ".onion") || strings.HasSuffix(host, ".test") || strings.HasSuffix(host, ".invalid") || strings.HasSuffix(host, ".example") {
		return nil, errors.New("profile URL must use a public hostname")
	}
	var handle string
	switch provider {
	case "cursor":
		if host != "cursor.com" || !strings.HasPrefix(u.Path, "/@") {
			return nil, errors.New("Cursor URL must be https://cursor.com/@handle")
		}
		handle = strings.TrimPrefix(u.Path, "/@")
	case "chatgpt":
		if host != "chatgpt.com" || !strings.HasPrefix(u.Path, "/u/") {
			return nil, errors.New("ChatGPT URL must be https://chatgpt.com/u/handle")
		}
		handle = strings.TrimPrefix(u.Path, "/u/")
	case "custom":
		// Custom links are never fetched. GitHub and Hugging Face use /handle
		// paths, so preserve arbitrary canonical public profile paths.
		if u.Path == "" || u.Path == "/" || path.Clean(u.Path) != u.Path || strings.ContainsAny(u.Path, "%\\") {
			return nil, errors.New("invalid profile path")
		}
		if _, err := profileShareCleanText(u.Path, 512); err != nil {
			return nil, errors.New("invalid profile path")
		}
		if u.String() != raw {
			return nil, errors.New("profile URL must be canonical")
		}
		return u, nil
	default:
		return nil, errors.New("unsupported profile provider")
	}
	if !profileShareHandlePattern.MatchString(handle) || u.String() != raw {
		return nil, errors.New("invalid profile handle or path")
	}
	return u, nil
}

func validateProfileLinkedProfiles(profiles []model.ProfileLinkedProfile, now time.Time) error {
	if len(profiles) > profileShareMaxLinkedProfiles {
		return errors.New("at most five linked profiles are allowed")
	}
	seen := make(map[string]bool, len(profiles))
	for i := range profiles {
		profile := &profiles[i]
		if _, err := validateProfileLinkedURL(profile.Provider, profile.URL); err != nil {
			return err
		}
		if seen[profile.URL] {
			return errors.New("duplicate profile URL")
		}
		seen[profile.URL] = true
		var err error
		if profile.Label, err = profileShareCleanText(profile.Label, 48); err != nil {
			return errors.New("invalid profile label")
		}
		s := profile.Snapshot
		if s == nil {
			continue
		}
		if s.Tokens == nil && s.Requests == nil && s.Messages == nil {
			return errors.New("snapshot requires a known metric")
		}
		for _, value := range []*int64{s.Tokens, s.Requests, s.Messages} {
			if value != nil && (*value < 0 || *value > profileShareMaxMetric) {
				return errors.New("snapshot metrics must be non-negative safe integers")
			}
		}
		observed, err := time.Parse(time.RFC3339, s.ObservedAt)
		if err != nil || observed.Before(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)) || observed.After(now.Add(5*time.Minute)) {
			return errors.New("invalid snapshot observation time")
		}
		s.ObservedAt = observed.UTC().Format(time.RFC3339)
		if s.Source, err = profileShareCleanText(s.Source, 80); err != nil {
			return errors.New("invalid snapshot source")
		}
		switch s.Period {
		case "all", "7d", "30d", "365d", "custom":
		default:
			return errors.New("invalid snapshot period")
		}
		if (s.PeriodStart == "") != (s.PeriodEnd == "") || (s.Period == "custom" && s.PeriodStart == "") || (s.Period == "all" && s.PeriodStart != "") {
			return errors.New("invalid snapshot date range")
		}
		if s.PeriodStart != "" {
			start, startErr := time.Parse("2006-01-02", s.PeriodStart)
			end, endErr := time.Parse("2006-01-02", s.PeriodEnd)
			maxDays := 365
			if s.Period == "7d" {
				maxDays = 7
			} else if s.Period == "30d" {
				maxDays = 30
			}
			if startErr != nil || endErr != nil || end.Before(start) || end.Sub(start) > time.Duration(maxDays)*24*time.Hour || s.PeriodEnd > observed.UTC().Format("2006-01-02") {
				return errors.New("invalid snapshot date range")
			}
		}
	}
	return nil
}

type profileAggregateSource struct {
	Provider       string `json:"provider"`
	URL            string `json:"url"`
	Label          string `json:"label"`
	Status         string `json:"status"`
	Tokens         *int64 `json:"tokens,omitempty"`
	Requests       *int64 `json:"requests,omitempty"`
	Messages       *int64 `json:"messages,omitempty"`
	Agents         *int64 `json:"agents,omitempty"`
	Period         string `json:"period"`
	PeriodStart    string `json:"period_start,omitempty"`
	PeriodEnd      string `json:"period_end,omitempty"`
	PeriodTimezone string `json:"period_timezone,omitempty"`
	ObservedAt     string `json:"observed_at,omitempty"`
	FetchedAt      string `json:"fetched_at,omitempty"`
	Approximate    bool   `json:"approximate"`
	Source         string `json:"source"`
	SnapshotSource string `json:"snapshot_source,omitempty"`
	Reason         string `json:"reason,omitempty"`
}

func profileAggregateBase(profile model.ProfileLinkedProfile) profileAggregateSource {
	label := profile.Label
	if label == "" {
		switch profile.Provider {
		case "cursor":
			label = "Cursor"
		case "chatgpt":
			label = "ChatGPT"
		default:
			label = "Custom profile"
		}
	}
	source := "owner_snapshot"
	if profile.Provider == "cursor" && profile.Snapshot == nil {
		source = "public_ssr"
	}
	return profileAggregateSource{Provider: profile.Provider, URL: profile.URL, Label: label, Status: "disabled", Period: "unknown", Source: source}
}

func resolveProfileAggregateSources(ctx context.Context, share *model.ProfileShare, period string, now time.Time) []profileAggregateSource {
	rows := []profileAggregateSource{{Provider: "lmm", URL: profileShareDestination, Label: "LMM Best", Status: "disabled", Period: period, Source: "native", PeriodTimezone: "UTC"}}
	for _, profile := range share.LinkedProfiles {
		rows = append(rows, profileAggregateBase(profile))
	}
	if !share.AggregateUsageEnabled {
		return rows
	}
	start := profileSharePeriodStart(period, now)
	rows[0].PeriodStart, rows[0].PeriodEnd = time.Unix(start, 0).UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339)
	if start == 0 {
		rows[0].PeriodStart = ""
	}
	// Native aggregate intentionally excludes model names and spend, whose
	// publication remains governed by the independent model_usage_enabled gate.
	usage, err := model.GetProfileShareAggregateUsage(share.UserID, start, now.Unix())
	if err == nil {
		rows[0].Status = "live"
		rows[0].Tokens, rows[0].Requests = &usage.Tokens, &usage.Requests
		rows[0].FetchedAt = now.UTC().Format(time.RFC3339)
	} else {
		rows[0].Status, rows[0].Reason = "unavailable", "usage_unavailable"
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i, profile := range share.LinkedProfiles {
		if profile.Snapshot != nil {
			s := profile.Snapshot
			rows[i+1].Status, rows[i+1].Period = "snapshot", s.Period
			rows[i+1].Tokens, rows[i+1].Requests, rows[i+1].Messages = s.Tokens, s.Requests, s.Messages
			rows[i+1].PeriodStart, rows[i+1].PeriodEnd, rows[i+1].ObservedAt = s.PeriodStart, s.PeriodEnd, s.ObservedAt
			rows[i+1].Approximate, rows[i+1].SnapshotSource = s.Approximate, s.Source
			continue
		}
		switch profile.Provider {
		case "chatgpt":
			rows[i+1].Status, rows[i+1].Reason = "login_required", "anonymous_metrics_unavailable"
		case "custom":
			rows[i+1].Status, rows[i+1].Reason = "unsupported", "snapshot_required"
		case "cursor":
			wg.Add(1)
			go func(index int, profile model.ProfileLinkedProfile) {
				defer wg.Done()
				resolved := fetchProfileCursor(ctx, profile.URL, now)
				resolved.Label = rows[index].Label
				rows[index] = resolved
			}(i+1, profile)
		}
	}
	wg.Wait()
	return rows
}

// Reject special-purpose address ranges, including IPv4 mapped IPv6. Validate
// all DNS answers then dial a pinned address, so a second DNS lookup cannot
// turn a previously public hostname into a private destination.
func profileSharePublicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return false
	}
	for _, raw := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "2001::/23", "64:ff9b::/96", "64:ff9b:1::/48"} {
		if netip.MustParsePrefix(raw).Contains(ip) {
			return false
		}
	}
	return true
}

func profileShareSafeDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host != "cursor.com" || port != "443" {
		return nil, errors.New("unsupported profile destination")
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return nil, errors.New("profile DNS unavailable")
	}
	for _, ip := range ips {
		if !profileSharePublicIP(ip) {
			return nil, errors.New("unsafe profile destination")
		}
	}
	var lastErr error
	for _, ip := range ips {
		conn, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

var profileShareHTTPClient = &http.Client{
	Timeout:       4 * time.Second,
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	Transport:     &http.Transport{Proxy: nil, DialContext: profileShareSafeDial, TLSHandshakeTimeout: 2 * time.Second, ResponseHeaderTimeout: 3 * time.Second, MaxConnsPerHost: 2, MaxIdleConns: 4, MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second, MaxResponseHeaderBytes: 32 << 10},
}
