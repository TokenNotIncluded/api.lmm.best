package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type profileShareRoundTripFunc func(*http.Request) (*http.Response, error)

func (f profileShareRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func profileShareCursorFixture(t *testing.T) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/cursor-public-stats.rsc.html")
	require.NoError(t, err)
	return body
}
func profileShareTestClient(t *testing.T, roundTrip profileShareRoundTripFunc) {
	t.Helper()
	old := profileShareHTTPClient
	profileShareHTTPClient = &http.Client{Transport: roundTrip, Timeout: time.Second, CheckRedirect: old.CheckRedirect}
	profileCursorCache.Lock()
	previous := profileCursorCache.Entries
	profileCursorCache.Entries = make(map[string]profileCursorCacheEntry)
	profileCursorCache.Unlock()
	t.Cleanup(func() {
		profileShareHTTPClient = old
		profileCursorCache.Lock()
		profileCursorCache.Entries = previous
		profileCursorCache.Unlock()
	})
}

func TestProfileShareCursorActualRSCFixtureAndDateWindow(t *testing.T) {
	body := profileShareCursorFixture(t)
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	row, err := parseProfileCursorHTML(body, "profile-fixture", now)
	require.NoError(t, err)
	require.EqualValues(t, 502839373, *row.Tokens)
	require.Equal(t, "2026-09-04", row.PeriodStart)
	require.Equal(t, "2026-10-03", row.PeriodEnd)
	require.Equal(t, "unspecified", row.PeriodTimezone)
	require.Equal(t, "public_ssr", row.Source)
	require.Nil(t, row.Requests)
	require.Nil(t, row.Agents)
	require.Empty(t, row.ObservedAt)
	for name, mutate := range map[string]func([]byte) []byte{
		"private":           func(v []byte) []byte { return bytes.ReplaceAll(v, []byte("PUBLIC"), []byte("PRIVATE")) },
		"wrong_identity":    func(v []byte) []byte { return bytes.ReplaceAll(v, []byte("profile-fixture"), []byte("somebody-else")) },
		"invalid_date":      func(v []byte) []byte { return bytes.ReplaceAll(v, []byte("2026-09-04"), []byte("2026-02-30")) },
		"duplicate_date":    func(v []byte) []byte { return bytes.ReplaceAll(v, []byte("2026-09-05"), []byte("2026-09-04")) },
		"negative_tokens":   func(v []byte) []byte { return bytes.ReplaceAll(v, []byte("115326150"), []byte("-1")) },
		"fractional_tokens": func(v []byte) []byte { return bytes.ReplaceAll(v, []byte("115326150"), []byte("1.5")) },
		"overflow":          func(v []byte) []byte { return bytes.ReplaceAll(v, []byte("115326150"), []byte("9007199254740992")) },
		"missing_tokens":    func(v []byte) []byte { return bytes.ReplaceAll(v, []byte("tokensOverTime"), []byte("unknownField")) },
		"malformed_push":    func(v []byte) []byte { return bytes.ReplaceAll(v, []byte("push([1,"), []byte("push([1,oops,")) },
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseProfileCursorHTML(mutate(body), "profile-fixture", now)
			require.Error(t, err)
		})
	}
	_, err = parseProfileCursorHTML([]byte(`<script>self.__next_f.push([1,"25:{}\n"]);alert(1)</script>`), "profile-fixture", now)
	require.Error(t, err)
	_, err = parseProfileCursorHTML(body, "profile-fixture", time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
	require.Error(t, err)
}

func TestProfileShareAggregateURLSnapshotAndDestinationValidation(t *testing.T) {
	for _, tc := range []struct{ provider, url string }{
		{"cursor", "http://cursor.com/@user"}, {"cursor", "https://cursor.com.evil.org/@user"}, {"cursor", "https://user:secret@cursor.com/@user"},
		{"cursor", "https://cursor.com:443/@user"}, {"cursor", "https://cursor.com/@user?redirect=https://127.0.0.1"}, {"cursor", "https://cursor.com/@user#x"},
		{"cursor", "https://cursor.com/%40user"}, {"cursor", "https://cursor.com/@user/../../admin"}, {"cursor", "https://CURSOR.COM/@user"},
		{"chatgpt", "https://chatgpt.com/u/user/session"}, {"custom", "https://127.0.0.1/u/user"}, {"custom", "https://host.local/u/user"}, {"custom", "https://example.org/u/../admin"}, {"custom", "https://github.com/%252e%252e/user"},
	} {
		_, err := validateProfileLinkedURL(tc.provider, tc.url)
		require.Error(t, err, tc.url)
	}
	for _, tc := range []struct{ provider, url string }{{"cursor", "https://cursor.com/@lightjunction"}, {"chatgpt", "https://chatgpt.com/u/lightjunction.me"}, {"custom", "https://example.org/profile/owner"}} {
		_, err := validateProfileLinkedURL(tc.provider, tc.url)
		require.NoError(t, err)
	}
	for _, raw := range []string{"127.0.0.1", "::1", "::ffff:127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "192.0.2.1", "198.18.0.1", "2001:db8::1", "fe80::1", "fd00::1", "64:ff9b::7f00:1"} {
		require.False(t, profileSharePublicIP(netip.MustParseAddr(raw)), raw)
	}
	require.True(t, profileSharePublicIP(netip.MustParseAddr("1.1.1.1")))
	_, err := profileShareSafeDial(context.Background(), "tcp", "127.0.0.1:443")
	require.Error(t, err)
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	tokens := int64(117000000000)
	base := model.ProfileLinkedProfile{Provider: "chatgpt", URL: "https://chatgpt.com/u/owner", Snapshot: &model.ProfileUsageSnapshot{Tokens: &tokens, Period: "all", ObservedAt: "2026-10-03T18:46:00Z", Approximate: true, Source: "Codex profile lifetime tokens; displayed 117B; owner-observed snapshot"}}
	require.NoError(t, validateProfileLinkedProfiles([]model.ProfileLinkedProfile{base}, now))
	for _, change := range []func(*model.ProfileUsageSnapshot){
		func(s *model.ProfileUsageSnapshot) { s.Tokens = nil }, func(s *model.ProfileUsageSnapshot) { n := int64(-1); s.Tokens = &n }, func(s *model.ProfileUsageSnapshot) { s.ObservedAt = "2026-10-05T00:00:00Z" },
		func(s *model.ProfileUsageSnapshot) { s.Period = "custom" }, func(s *model.ProfileUsageSnapshot) { s.PeriodStart = "2026-01-01"; s.PeriodEnd = "2026-01-02" },
		func(s *model.ProfileUsageSnapshot) { s.Source = "cookie\nsecret" },
	} {
		snapshot := *base.Snapshot
		change(&snapshot)
		profile := base
		profile.Snapshot = &snapshot
		require.Error(t, validateProfileLinkedProfiles([]model.ProfileLinkedProfile{profile}, now))
	}
	require.Error(t, validateProfileLinkedProfiles([]model.ProfileLinkedProfile{base, base}, now))
}

func setupProfileAggregateController(t *testing.T) (*gin.Engine, *model.ProfileShare) {
	t.Helper()
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.ProfileShare{}, &model.QuotaData{}))
	owner := model.User{Username: "aggregate-owner", Password: "password", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&owner).Error)
	require.NoError(t, db.Create(&model.QuotaData{UserID: owner.Id, CreatedAt: time.Now().Add(-time.Hour).Unix(), TokenUsed: 42, Count: 2, ModelName: "PRIVATE_MODEL"}).Error)
	share, err := model.EnableProfileShare(owner.Id)
	require.NoError(t, err)
	engine := gin.New()
	engine.Use(func(c *gin.Context) { c.Set("id", owner.Id); c.Next() })
	engine.GET("/self", GetSelfProfileShare)
	engine.POST("/self", EnableSelfProfileShare)
	engine.DELETE("/self", DisableSelfProfileShare)
	engine.GET("/share/:token", GetPublicProfileShareSVG)
	return engine, share
}

func profileAggregateRequest(engine *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(method, path, strings.NewReader(body)))
	return response
}

func TestProfileShareAggregateIndependentConsentPersistenceAndUnknownSources(t *testing.T) {
	engine, share := setupProfileAggregateController(t)
	var calls atomic.Int32
	profileShareTestClient(t, func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: 404, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("private")), Request: r}, nil
	})
	path := "/share/" + share.Token + ".svg?layout=aggregate&format=full"
	require.Equal(t, 404, profileAggregateRequest(engine, http.MethodGet, path, "").Code)
	settings := `{"aggregate_usage_enabled":false,"linked_profiles":[{"provider":"cursor","url":"https://cursor.com/@profile-fixture"},{"provider":"chatgpt","url":"https://chatgpt.com/u/owner"},{"provider":"custom","url":"https://example.org/u/owner"}]}`
	response := profileAggregateRequest(engine, http.MethodPost, "/self", settings)
	require.Equal(t, 200, response.Code)
	require.Contains(t, response.Body.String(), `"status":"disabled"`)
	require.Zero(t, calls.Load())
	response = profileAggregateRequest(engine, http.MethodPost, "/self", `{"aggregate_usage_enabled":true}`)
	require.Equal(t, 200, response.Code)
	require.Contains(t, response.Body.String(), `"model_usage_enabled":false`)
	require.Contains(t, response.Body.String(), `"status":"unavailable"`)
	require.Contains(t, response.Body.String(), `"status":"login_required"`)
	require.Contains(t, response.Body.String(), `"status":"unsupported"`)
	var envelope struct {
		Data struct {
			Sources []profileAggregateSource `json:"aggregate_sources"`
		}
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.EqualValues(t, 42, *envelope.Data.Sources[0].Tokens)
	for _, row := range envelope.Data.Sources[1:] {
		require.Nil(t, row.Tokens)
		require.Nil(t, row.Requests)
		require.Nil(t, row.Messages)
	}
	response = profileAggregateRequest(engine, http.MethodGet, path, "")
	require.Equal(t, 200, response.Code)
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	require.Contains(t, response.Body.String(), "42 tokens")
	require.Contains(t, response.Body.String(), "Unavailable")
	require.NotContains(t, response.Body.String(), "PRIVATE_MODEL")
	require.Equal(t, 404, profileAggregateRequest(engine, http.MethodGet, "/share/"+share.Token+".svg?layout=models", "").Code)
	require.Equal(t, 200, profileAggregateRequest(engine, http.MethodPost, "/self", `{"model_usage_enabled":true}`).Code)
	stored, err := model.GetProfileShare(share.UserID)
	require.NoError(t, err)
	require.True(t, stored.AggregateUsageEnabled)
	require.True(t, stored.ModelUsageEnabled)
	require.Len(t, stored.LinkedProfiles, 3)
	require.Equal(t, share.Token, stored.Token)
	require.Equal(t, 200, profileAggregateRequest(engine, http.MethodPost, "/self", `{"aggregate_usage_enabled":false,"linked_profiles":[]}`).Code)
	stored, err = model.GetProfileShare(share.UserID)
	require.NoError(t, err)
	require.True(t, stored.ModelUsageEnabled)
	require.False(t, stored.AggregateUsageEnabled)
	require.Empty(t, stored.LinkedProfiles)
	require.Equal(t, 404, profileAggregateRequest(engine, http.MethodGet, path, "").Code)
	require.Equal(t, 200, profileAggregateRequest(engine, http.MethodDelete, "/self", "").Code)
	require.Equal(t, 404, profileAggregateRequest(engine, http.MethodGet, path, "").Code)
	for _, body := range []string{`{"linked_profiles":[{"provider":"cursor","url":"https://cursor.com/@user","cookies":"secret"}]}`, `{"linked_profiles":[{"provider":"cursor","url":"https://cursor.com/@user","snapshot":{"session":"secret"}}]}`, strings.Repeat(" ", ProfileShareSettingsMaxBytes+1) + "{}"} {
		require.Equal(t, 400, profileAggregateRequest(engine, http.MethodPost, "/self", body).Code)
	}
}

func TestProfileShareAggregateRevocationDuringFetchAndCachePrivacy(t *testing.T) {
	engine, share := setupProfileAggregateController(t)
	body := profileShareCursorFixture(t)
	var calls atomic.Int32
	profileShareTestClient(t, func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		require.Empty(t, r.Header.Get("Authorization"))
		require.Empty(t, r.Header.Get("Cookie"))
		require.NoError(t, model.DisableProfileShare(share.UserID))
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/html"}}, Body: io.NopCloser(bytes.NewReader(body)), Request: r}, nil
	})
	enabled := true
	profiles := []model.ProfileLinkedProfile{{Provider: "cursor", URL: "https://cursor.com/@profile-fixture"}}
	_, err := model.SetProfileShareSettings(share, nil, &enabled, &profiles)
	require.NoError(t, err)
	path := "/share/" + share.Token + ".svg?layout=aggregate"
	require.Equal(t, 404, profileAggregateRequest(engine, http.MethodGet, path, "").Code)
	require.EqualValues(t, 1, calls.Load())
	require.Equal(t, 404, profileAggregateRequest(engine, http.MethodGet, path, "").Code)
	require.EqualValues(t, 1, calls.Load(), "revoked token must not access even a cached external source")
	// Expired positive entries must never survive a new private/error result.
	profileCursorCache.Lock()
	profileCursorCache.Entries[profiles[0].URL] = profileCursorCacheEntry{Row: profileAggregateSource{Status: "live", Tokens: new(int64)}, Expires: time.Now().Add(-time.Second)}
	profileCursorCache.Unlock()
	profileShareHTTPClient.Transport = profileShareRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 404, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("private")), Request: r}, nil
	})
	row := fetchProfileCursor(context.Background(), profiles[0].URL, time.Now())
	require.Equal(t, "unavailable", row.Status)
	require.Nil(t, row.Tokens)
}

func TestProfileShareAggregateCursorRedirectAndOversizedResponseAreUnavailable(t *testing.T) {
	var calls atomic.Int32
	profileShareTestClient(t, func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://127.0.0.1/private"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})
	row := fetchProfileCursor(context.Background(), "https://cursor.com/@profile-fixture", time.Now())
	require.Equal(t, "unavailable", row.Status)
	require.EqualValues(t, 1, calls.Load())
	require.Nil(t, row.Tokens)
	profileShareHTTPClient.Transport = profileShareRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/html"}}, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", profileShareCursorMaxBody+1))), Request: r}, nil
	})
	row = fetchProfileCursor(context.Background(), "https://cursor.com/@other", time.Now())
	require.Equal(t, "unavailable", row.Status)
	require.Nil(t, row.Tokens)
}

func TestProfileShareAggregateSVGSnapshotScopeEscapingAndBounds(t *testing.T) {
	options, err := parseProfileShareAggregateSVGOptions(url.Values{"layout": {"aggregate"}, "lang": {"en"}, "title": {"<script>bad</script>"}})
	require.NoError(t, err)
	tokens := int64(117000000000)
	rows := []profileAggregateSource{{Provider: "chatgpt", URL: "https://chatgpt.com/u/owner", Label: `<image href="https://evil.org/x"/>`, Status: "snapshot", Tokens: &tokens, Period: "all", ObservedAt: "2026-10-03T18:46:00Z", Approximate: true, Source: "owner_snapshot", SnapshotSource: "Codex profile lifetime tokens; displayed 117B; owner-observed snapshot"}, {Label: "Private", Status: "unavailable", Period: "unknown"}}
	svg := renderProfileShareAggregateSVG(options, rows)
	require.Contains(t, svg, "≈117B tokens")
	require.Contains(t, svg, "Lifetime")
	require.Contains(t, svg, "Owner snapshot")
	require.Contains(t, svg, "2026-10-03T18:46:00Z")
	require.Contains(t, svg, "Codex profile lifetime tokens")
	require.Contains(t, svg, "&lt;image")
	require.NotContains(t, svg, "<image")
	require.NotContains(t, svg, "<script")
	require.NotContains(t, svg, "foreignObject")
	require.NotContains(t, svg, "0 tokens")
	require.Contains(t, svg, "— tokens")
	var parsed struct{}
	require.NoError(t, xml.Unmarshal([]byte(svg), &parsed))
	_, err = parseProfileShareAggregateSVGOptions(url.Values{"layout": {"aggregate"}, "height": {"1201"}})
	require.Error(t, err)
	_, err = parseProfileShareAggregateSVGOptions(url.Values{"layout": {"aggregate"}, "bg": {"url(https://evil.org/x)"}})
	require.Error(t, err)
}

func TestProfileShareAggregateCursorNoStoreNeverKeepsPrivateMetrics(t *testing.T) {
	body := profileShareCursorFixture(t)
	var calls atomic.Int32
	profileShareTestClient(t, func(r *http.Request) (*http.Response, error) {
		code := 200
		content := string(body)
		if calls.Add(1) > 1 {
			code = 404
			content = "private"
		}
		return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": {"text/html"}, "Cache-Control": {"public, max-age=60", "private, no-cache, no-store, max-age=0"}}, Body: io.NopCloser(strings.NewReader(content)), Request: r}, nil
	})
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	raw := "https://cursor.com/@profile-fixture"
	first := fetchProfileCursor(context.Background(), raw, now)
	require.Equal(t, "live", first.Status)
	require.EqualValues(t, 502839373, *first.Tokens)
	profileCursorCache.Lock()
	_, cached := profileCursorCache.Entries[raw]
	profileCursorCache.Unlock()
	require.False(t, cached)
	second := fetchProfileCursor(context.Background(), raw, now.Add(time.Second))
	require.Equal(t, "unavailable", second.Status)
	require.Nil(t, second.Tokens)
	require.EqualValues(t, 2, calls.Load())
}

func TestProfileShareAggregateSnapshotPeriodAndIndependentMetricVisibility(t *testing.T) {
	options, err := parseProfileShareAggregateSVGOptions(url.Values{"layout": {"aggregate"}})
	require.NoError(t, err)
	messages, requests := int64(7), int64(42)
	row := profileAggregateSource{Label: "Snapshot", Status: "snapshot", Messages: &messages, Requests: &requests, Period: "30d", ObservedAt: "2026-01-01T00:00:00Z", Source: "owner_snapshot"}
	svg := renderProfileShareAggregateSVG(options, []profileAggregateSource{row})
	require.Contains(t, svg, "30d @ 2026-01-01")
	require.NotContains(t, svg, "Last 30 days")
	require.Contains(t, svg, "7 messages")
	require.Contains(t, svg, "42 requests")
	row.Messages = nil
	options.ShowRequests = false
	svg = renderProfileShareAggregateSVG(options, []profileAggregateSource{row})
	require.NotContains(t, svg, "42 requests")
	profile := model.ProfileLinkedProfile{Provider: "custom", URL: "https://github.com/lightjunction", Snapshot: &model.ProfileUsageSnapshot{Requests: &requests, Period: "7d", PeriodStart: "2026-01-01", PeriodEnd: "2026-10-01", ObservedAt: "2026-10-03T18:46:00Z"}}
	require.Error(t, validateProfileLinkedProfiles([]model.ProfileLinkedProfile{profile}, time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)))
	_, err = validateProfileLinkedURL("custom", "https://huggingface.co/lightjunction")
	require.NoError(t, err)
}

func TestProfileShareAggregateVisualFixtures(t *testing.T) {
	dir := os.Getenv("PROFILE_SHARE_REVIEW_OUTPUT")
	if dir == "" {
		t.Skip("visual artifact output not requested")
	}
	require.NoError(t, os.MkdirAll(dir, 0755))
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	cursor, err := parseProfileCursorHTML(profileShareCursorFixture(t), "profile-fixture", now)
	require.NoError(t, err)
	cursor.Label = "Cursor"
	nativeTokens, nativeRequests, snapshotTokens := int64(899140697), int64(13815), int64(117000000000)
	rows := []profileAggregateSource{{Provider: "lmm", URL: profileShareDestination, Label: "LMM Best", Status: "live", Tokens: &nativeTokens, Requests: &nativeRequests, Period: "30d", PeriodStart: "2026-09-04T00:00:00Z", PeriodEnd: "2026-10-04T00:00:00Z", PeriodTimezone: "UTC", FetchedAt: now.Format(time.RFC3339), Source: "native"}, cursor, {Provider: "chatgpt", Label: "ChatGPT / Codex", Status: "snapshot", Tokens: &snapshotTokens, Period: "all", ObservedAt: "2026-10-03T18:46:00Z", Approximate: true, Source: "owner_snapshot", SnapshotSource: "Codex lifetime tokens; displayed 117B; owner-observed snapshot"}, {Provider: "custom", Label: "Unavailable source", Status: "unsupported", Period: "unknown", Source: "owner_snapshot"}}
	for _, theme := range []string{"dark", "paper", "transparent"} {
		options, err := parseProfileShareAggregateSVGOptions(url.Values{"layout": {"aggregate"}, "theme": {theme}, "lang": {"zh"}, "animation": {"none"}})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "aggregate-"+theme+".svg"), []byte(renderProfileShareAggregateSVG(options, rows)), 0644))
	}
}

func TestProfileShareAggregateOwnerSnapshotAndNativeQueryFailureRemainIndependent(t *testing.T) {
	engine, share := setupProfileAggregateController(t)
	body := `{"aggregate_usage_enabled":true,"linked_profiles":[{"provider":"chatgpt","url":"https://chatgpt.com/u/lightjunction.me","snapshot":{"tokens":117000000000,"requests":null,"messages":null,"period":"all","observed_at":"2026-10-03T18:46:00Z","approximate":true,"source":"Codex lifetime tokens; displayed 117B; owner-observed snapshot"}}]}`
	response := profileAggregateRequest(engine, http.MethodPost, "/self", body)
	require.Equal(t, 200, response.Code)
	var envelope struct {
		Data struct {
			Sources []profileAggregateSource `json:"aggregate_sources"`
		}
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.Len(t, envelope.Data.Sources, 2)
	snapshot := envelope.Data.Sources[1]
	require.Equal(t, "snapshot", snapshot.Status)
	require.Equal(t, "owner_snapshot", snapshot.Source)
	require.True(t, snapshot.Approximate)
	require.EqualValues(t, 117000000000, *snapshot.Tokens)
	require.Nil(t, snapshot.Requests)
	require.Nil(t, snapshot.Messages)
	require.NoError(t, model.DB.Migrator().DropTable(&model.QuotaData{}))
	response = profileAggregateRequest(engine, http.MethodGet, "/share/"+share.Token+".svg?layout=aggregate", "")
	require.Equal(t, 200, response.Code)
	require.Contains(t, response.Body.String(), "Unavailable")
	require.Contains(t, response.Body.String(), "≈117B tokens")
	require.NotContains(t, response.Body.String(), "0 tokens")
}
