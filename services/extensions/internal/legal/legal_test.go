package legal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
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
	var data bytes.Buffer
	if err := toml.NewEncoder(&data).Encode(c); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "site.toml")
	if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
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

func TestStrictTOMLAndRedactedErrors(t *testing.T) {
	root, _ := fixture(t)
	const base = "version = 1\npublished = false\ndefault_language = \"zh-CN\"\n"
	cases := map[string]string{
		"unknown":              base + `unknown = "sensitive-value"`,
		"unknown-empty-table":  base + "[sensitive-value]\n",
		"case-alias":           "Version = 1\npublished = false\ndefault_language = \"zh-CN\"\n",
		"case-alias-published": base + "Published = false\n",
		"duplicate-key":        base + "version = 1\n",
		"duplicate-case-alias": base + "Version = 1\n",
		"duplicate-table":      base + "[variables]\nservice_name = \"Example\"\n[variables]\noperator_name = \"sensitive-value\"\n",
		"duplicate-map-key":    base + "[variables]\nservice_name = \"Example\"\nservice_name = \"sensitive-value\"\n",
		"duplicate-inline-key": base + "variables = { service_name = \"Example\", service_name = \"sensitive-value\" }\n",
		"malformed":            base + "variables = { service_name = \"sensitive-value\"\n",
		"wrong-version-type":   "version = \"sensitive-value\"\npublished = false\ndefault_language = \"zh-CN\"\n",
		"wrong-published-type": "version = 1\npublished = \"sensitive-value\"\ndefault_language = \"zh-CN\"\n",
		"wrong-variable-type":  base + "[variables]\nservice_name = 12\n",
		"wrong-document-type":  base + "[documents.user-agreement]\nzh-CN = 12\n",
		"nested-variable":      base + "[variables.service_name]\nsecret = \"sensitive-value\"\n",
		"json-rejected":        `{"version":1,"published":false,"default_language":"zh-CN","secret":"sensitive-value"}`,
		"empty":                "",
		"oversize":             strings.Repeat("x", maxConfig+1),
		"invalid-utf8":         base + string([]byte{255}),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(root, "site.toml")
			if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if err == nil || strings.Contains(err.Error(), "sensitive-value") || strings.Contains(err.Error(), root) {
				t.Fatalf("unsafe error: %v", err)
			}
		})
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
	path := filepath.Join(root, "site.example.toml")
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
	if _, err = toml.NewDecoder(bytes.NewReader(data)).Decode(&c); err != nil {
		t.Fatal(err)
	}
	c.Published = true
	for key := range c.Variables {
		c.Variables[key] = "Operator-supplied value"
	}
	temp := t.TempDir()
	c.TemplateRoot = temp
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

func TestTemplateRootSupportsSeparateInstalledDirectory(t *testing.T) {
	root, c := fixture(t)
	configDir := t.TempDir()
	c.TemplateRoot = root
	h, err := Load(writeConfig(t, configDir, c))
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/user-agreement?lang=en", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "English policy") {
		t.Fatal(w.Code, w.Body.String())
	}
	// The returned handler is a snapshot; later template replacement is not read.
	if err := os.WriteFile(filepath.Join(root, "en.md"), []byte("Replacement policy"), 0600); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/user-agreement?lang=en", nil))
	if strings.Contains(w.Body.String(), "Replacement policy") {
		t.Fatal("template root was read per request")
	}
}

func TestTemplateRootRejectsUnsafeDirectories(t *testing.T) {
	for _, kind := range []string{"relative", "unclean", "trailing-slash", "backslash", "nul", "missing", "file", "symlink", "parent-symlink"} {
		t.Run(kind, func(t *testing.T) {
			root, c := fixture(t)
			configDir := t.TempDir()
			switch kind {
			case "relative":
				c.TemplateRoot = "templates"
			case "unclean":
				c.TemplateRoot = root + "/../" + filepath.Base(root)
			case "trailing-slash":
				c.TemplateRoot = root + "/"
			case "backslash":
				c.TemplateRoot = root + "/\\folder"
			case "nul":
				c.TemplateRoot = root + "\x00"
			case "missing":
				c.TemplateRoot = filepath.Join(root, "absent")
			case "file":
				c.TemplateRoot = filepath.Join(root, "en.md")
			case "symlink":
				link := filepath.Join(configDir, "linked")
				if err := os.Symlink(root, link); err != nil {
					t.Fatal(err)
				}
				c.TemplateRoot = link
			case "parent-symlink":
				child := filepath.Join(root, "child")
				if err := os.Mkdir(child, 0700); err != nil {
					t.Fatal(err)
				}
				link := filepath.Join(configDir, "linked")
				if err := os.Symlink(root, link); err != nil {
					t.Fatal(err)
				}
				c.TemplateRoot = filepath.Join(link, "child")
			}
			_, err := Load(writeConfig(t, configDir, c))
			if err == nil || strings.Contains(err.Error(), root) || strings.Contains(err.Error(), configDir) {
				t.Fatalf("unsafe root accepted/error: %v", err)
			}
		})
	}
	// Unpublished examples may name a future installation root without opening it.
	root, c := fixture(t)
	c.Published = false
	c.TemplateRoot = filepath.Join(t.TempDir(), "future-install")
	h, err := Load(writeConfig(t, root, c))
	if err != nil || len(h.documents) != 0 {
		t.Fatal(h, err)
	}
	// Lexical path rules also apply while publication is disabled.
	c.TemplateRoot = "relative"
	if _, err = Load(writeConfig(t, root, c)); err == nil {
		t.Fatal("invalid unpublished root accepted")
	}
}

func TestConfigRequiresBoundedRegularFile(t *testing.T) {
	root, c := fixture(t)
	target := writeConfig(t, root, c)
	link := filepath.Join(root, "linked.toml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(root, "missing.toml"), root, link} {
		if _, err := Load(path); err == nil || strings.Contains(err.Error(), root) {
			t.Fatalf("unsafe config accepted/error: %s %v", path, err)
		}
	}
}

func TestOperatorFilesRejectGroupOrOtherWrite(t *testing.T) {
	for _, target := range []string{"configuration", "template"} {
		for _, mode := range []os.FileMode{0600, 0640, 0644, 0444, 0620, 0602, 0666} {
			t.Run(fmt.Sprintf("%s/%04o", target, mode), func(t *testing.T) {
				root, c := fixture(t)
				path := writeConfig(t, root, c)
				file := path
				if target == "template" {
					file = filepath.Join(root, "en.md")
				}
				// Chmod after creation so the process umask cannot remove the
				// unsafe write bits that this regression exercises.
				if err := os.Chmod(file, mode); err != nil {
					t.Fatal(err)
				}
				h, err := Load(path)
				if mode&0022 != 0 {
					if h != nil || err == nil || strings.Contains(err.Error(), root) {
						t.Fatalf("unsafe permissions accepted or exposed path: %v %v", h, err)
					}
					return
				}
				if err != nil || len(h.documents) != 1 {
					t.Fatalf("safe operator file rejected: %v", err)
				}
			})
		}
	}
}
