package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseUpdateSourcesDefaultsAndValidation(t *testing.T) {
	sources, errs := parseUpdateSources("")
	if len(errs) != 0 || len(sources) != 4 {
		t.Fatalf("defaults = %#v, errors = %#v", sources, errs)
	}
	_, errs = parseUpdateSources(`{"aur":["lmm-api-go-bin","bad package"],"github_release":"bad/repo"}`)
	if len(errs) != 1 {
		t.Fatalf("validation errors = %#v, want one invalid package error", errs)
	}
}

func TestFetchGitHubReleaseSource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/project/releases" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"tag_name":"web-v2.0.0","html_url":"https://example.test/web","draft":false,"prerelease":false}]`))
	}))
	defer server.Close()
	previous := githubUpdateAPIBase
	githubUpdateAPIBase = server.URL
	defer func() { githubUpdateAPIBase = previous }()
	candidates, err := fetchGitHubSource(context.Background(), updateSource{Type: "github_release", Repository: "acme/project"})
	if err != nil || len(candidates) != 1 || candidates[0].Version != "web-v2.0.0" {
		t.Fatalf("candidates = %#v, error = %v", candidates, err)
	}
}

func TestIsUpdateVersionNewer(t *testing.T) {
	if !isUpdateVersionNewer("0.9.9", "0.10.0") || isUpdateVersionNewer("0.10.0", "0.9.9") {
		t.Fatal("version comparison failed")
	}
}
