package legal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) (string, configuration) {
	t.Helper()
	root := t.TempDir()
	for file, content := range map[string]string{"zh.md": "# {{service_name}}\nChinese policy", "en.md": "# {{service_name}}\nEnglish policy"} {
		if err := os.WriteFile(filepath.Join(root, file), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	c := configuration{Version: 1, Published: true, DefaultLanguage: "zh-CN", Variables: map[string]string{"service_name": "Example"}, Documents: map[string]map[string]string{"user-agreement": {"zh-CN": "zh.md", "en": "en.md"}}}
	return root, c
}

func writeConfig(t *testing.T, root string, c configuration) string {
	t.Helper()
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "site.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPublishedLanguageAndRevision(t *testing.T) {
	root, c := fixture(t)
	h, err := Load(writeConfig(t, root, c))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ lang, want string }{{"", "Chinese"}, {"en", "English"}, {"en-US", "English"}, {"zh_CN", "Chinese"}, {"fr", "Chinese"}} {
		t.Run(tc.lang, func(t *testing.T) {
			response := httptest.NewRecorder()
			h.ServeHTTP(response, httptest.NewRequest("GET", "/api/user-agreement?lang="+tc.lang, nil))
			var body map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if response.Code != 200 || body["success"] != true || !strings.Contains(body["data"].(string), tc.want) || len(body["revision"].(string)) != 64 {
				t.Fatalf("wrong response: %s", response.Body.String())
			}
			if strings.Contains(response.Body.String(), "{{") {
				t.Fatal("unresolved template")
			}
		})
	}
}

func TestUnpublishedAndUnconfiguredNeverExposeDraft(t *testing.T) {
	root, c := fixture(t)
	c.Published = false
	c.Variables["service_name"] = ""
	for _, path := range []string{"", writeConfig(t, root, c)} {
		h, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range documentNames {
			response := httptest.NewRecorder()
			h.ServeHTTP(response, httptest.NewRequest("GET", "/api/"+name, nil))
			var body map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body["data"] != "" {
				t.Fatal("draft was exposed")
			}
		}
	}
}

func TestBrokenPublicationFailsClosed(t *testing.T) {
	for _, kind := range []string{"version", "missing", "blank", "unknown-document", "unknown-language", "duplicate-language", "default-missing", "traversal", "absolute", "symlink", "parent-symlink", "oversize", "invalid-utf8", "unresolved", "empty", "variable-injection"} {
		t.Run(kind, func(t *testing.T) {
			root, c := fixture(t)
			switch kind {
			case "version":
				c.Version = 2
			case "missing":
				c.Variables = nil
			case "blank":
				c.Variables["service_name"] = " "
			case "unknown-document":
				c.Documents["secrets"] = c.Documents["user-agreement"]
			case "unknown-language":
				c.Documents["user-agreement"]["../en"] = "en.md"
			case "duplicate-language":
				c.Documents["user-agreement"]["EN"] = "en.md"
			case "default-missing":
				c.DefaultLanguage = "fr"
			case "traversal":
				c.Documents["user-agreement"]["en"] = "../en.md"
			case "absolute":
				c.Documents["user-agreement"]["en"] = filepath.Join(root, "en.md")
			case "symlink":
				if err := os.Symlink(filepath.Join(root, "en.md"), filepath.Join(root, "link.md")); err != nil {
					t.Fatal(err)
				}
				c.Documents["user-agreement"]["en"] = "link.md"
			case "parent-symlink":
				if err := os.Symlink(root, filepath.Join(root, "linked")); err != nil {
					t.Fatal(err)
				}
				c.Documents["user-agreement"]["en"] = "linked/en.md"
			case "oversize":
				if err := os.WriteFile(filepath.Join(root, "en.md"), []byte(strings.Repeat("a", maxDocument+1)), 0600); err != nil {
					t.Fatal(err)
				}
			case "invalid-utf8":
				if err := os.WriteFile(filepath.Join(root, "en.md"), []byte{255}, 0600); err != nil {
					t.Fatal(err)
				}
			case "unresolved":
				if err := os.WriteFile(filepath.Join(root, "en.md"), []byte("{{ unsupported }}"), 0600); err != nil {
					t.Fatal(err)
				}
			case "empty":
				if err := os.WriteFile(filepath.Join(root, "en.md"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "variable-injection":
				c.Variables["service_name"] = "{{other}}"
			}
			if _, err := Load(writeConfig(t, root, c)); err == nil {
				t.Fatal("invalid publication accepted")
			}
		})
	}
}

func TestStrictJSONAndRedactedErrors(t *testing.T) {
	root, _ := fixture(t)
	for _, raw := range []string{`{"unknown":"sensitive-value"}`, `{} {}`, strings.Repeat("x", maxConfig+1)} {
		path := filepath.Join(root, "site.json")
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := Load(path)
		if err == nil || strings.Contains(err.Error(), "sensitive-value") || strings.Contains(err.Error(), root) {
			t.Fatalf("unsafe error: %v", err)
		}
	}
}

func TestHTTPBoundaryAndImmutableSnapshot(t *testing.T) {
	root, c := fixture(t)
	path := writeConfig(t, root, c)
	h, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	protected := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(401) })
	mux := h.Mount(protected)
	for _, tc := range []struct {
		method, path string
		status       int
	}{{"GET", "/api/user-agreement", 200}, {"HEAD", "/api/privacy-policy", 200}, {"POST", "/api/refund-policy", 405}, {"GET", "/api/user-agreement?lang=en&lang=zh", 400}, {"GET", "/api/user-agreement?lang=../en", 400}, {"GET", "/extensions/v1/modules", 401}, {"GET", "/api/user-agreement/other", 401}} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.status {
			t.Fatalf("%s %s: %d", tc.method, tc.path, w.Code)
		}
		if tc.method == "HEAD" && w.Body.Len() != 0 {
			t.Fatal("HEAD returned a body")
		}
	}
	c.Variables["service_name"] = "New operator"
	writeConfig(t, root, c)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequest("GET", "/api/user-agreement", nil))
	if strings.Contains(response.Body.String(), "New operator") {
		t.Fatal("request read mutable configuration")
	}
	next, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if next.documents["user-agreement"]["en"].revision == h.documents["user-agreement"]["en"].revision {
		t.Fatal("document revision did not change")
	}
}

func TestConcurrentReads(t *testing.T) {
	root, c := fixture(t)
	h, err := Load(writeConfig(t, root, c))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 32; i++ {
		t.Run("reader", func(t *testing.T) {
			t.Parallel()
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", "/api/user-agreement", nil))
			if w.Code != 200 {
				t.Fatal(w.Code)
			}
		})
	}
}

func TestShippedTemplatesAreDraftsAndCanBeConfigured(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..", "config", "legal")
	path := filepath.Join(root, "site.example.json")
	h, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.documents) != 0 {
		t.Fatal("shipped policies were published")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var c configuration
	if err = json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	c.Published = true
	for key := range c.Variables {
		c.Variables[key] = "Operator-supplied value"
	}
	temp := t.TempDir()
	for _, translations := range c.Documents {
		for _, name := range translations {
			data, err := os.ReadFile(filepath.Join(root, name))
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(temp, name)
			if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(target, data, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	h, err = Load(writeConfig(t, temp, c))
	if err != nil {
		t.Fatal(err)
	}
	if len(h.documents) != 3 {
		t.Fatal("missing shipped document")
	}
	for _, translations := range h.documents {
		if len(translations) != 2 {
			t.Fatal("missing translation")
		}
	}
}

func TestExpansionIsBoundedBeforeAllocation(t *testing.T) {
	if _, err := render(strings.Repeat("{{x}}", 1000), map[string]string{"x": strings.Repeat("x", 16<<10)}); err == nil {
		t.Fatal("oversized expansion accepted")
	}
}
