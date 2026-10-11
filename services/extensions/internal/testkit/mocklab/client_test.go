package mocklab

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testManifest(base string) []byte {
	m := Manifest{Marker: Marker, RunID: strings.Repeat("a", 32), BaseURL: base, ControlToken: strings.Repeat("b", 40), Epoch: 1791633600,
		Instances: map[string]Instance{"stripe": {Protocol: "stripe-v1", URL: base + "/r/" + strings.Repeat("a", 32) + "/stripe"}}}
	b, _ := json.Marshal(m)
	return b
}
func TestTransportMapsOnlyDeclaredHost(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path != "/r/"+strings.Repeat("a", 32)+"/stripe/v1/account" || r.Header.Get("X-Mock-Run") != strings.Repeat("a", 32) {
			t.Error("wrong run mapping")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"acct_mocklab"}`)
	}))
	defer server.Close()
	c, err := Parse(testManifest(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	client, err := c.HTTP("stripe", "api.stripe.com")
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Get("https://api.stripe.com/v1/account")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	for _, url := range []string{"https://undeclared.invalid/v1/account", "http://api.stripe.com/v1/account", "https://api.stripe.com:443/v1/account"} {
		if _, err := client.Get(url); !errors.Is(err, ErrBoundary) {
			t.Fatalf("undeclared target accepted: %v", err)
		}
	}
	if hits != 1 {
		t.Fatal("unexpected socket request", hits)
	}
}
func TestTransportDoesNotFollowRedirect(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Redirect(w, r, "https://undeclared.invalid/", http.StatusFound)
	}))
	defer server.Close()
	c, err := Parse(testManifest(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	client, err := c.HTTP("stripe", "api.stripe.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Get("https://api.stripe.com/v1/account"); !errors.Is(err, ErrBoundary) {
		t.Fatal("redirect followed", err)
	}
	if hits != 1 {
		t.Fatal(hits)
	}
}
func TestManifestRejectsNonLoopbackAndCrossRun(t *testing.T) {
	for _, base := range []string{"http://example.invalid:80", "https://127.0.0.1:80", "http://169.254.1.1:80", "http://user:pass@127.0.0.1:80"} {
		if _, err := Parse(testManifest(base)); err == nil {
			t.Fatal("invalid manifest accepted")
		}
	}
	raw := strings.Replace(string(testManifest("http://127.0.0.1:9876")), "/stripe\"", "/other\"", 1)
	if _, err := Parse([]byte(raw)); err == nil {
		t.Fatal("cross-run instance accepted")
	}
}
