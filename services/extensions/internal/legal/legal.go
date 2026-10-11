// Package legal serves an operator-reviewed snapshot of external policy files.
// It owns no identity, funds, database connection, or background worker.
package legal

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/BurntSushi/toml"
)

const maxDocument = 128 << 10
const maxConfig = 64 << 10

var languagePattern = regexp.MustCompile(`^[a-z]{2,3}(?:-[a-z0-9]{2,8}){0,3}$`)
var variablePattern = regexp.MustCompile(`\{\{([a-z][a-z0-9_]*)\}\}`)
var variableName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
var documentNames = []string{"user-agreement", "privacy-policy", "refund-policy"}

type configuration struct {
	Version         int                          `toml:"version"`
	Published       bool                         `toml:"published"`
	DefaultLanguage string                       `toml:"default_language"`
	TemplateRoot    string                       `toml:"template_root,omitempty"`
	Variables       map[string]string            `toml:"variables"`
	Documents       map[string]map[string]string `toml:"documents"`
}

type document struct {
	text     string
	revision string
}

// Handler is immutable after Load. No request reads files or calls the core.
type Handler struct {
	defaultLanguage string
	documents       map[string]map[string]document
}

// Load never publishes the supplied draft by default. An empty path disables
// all policies; a broken published configuration fails startup, not open.
func Load(path string) (*Handler, error) {
	h := &Handler{documents: make(map[string]map[string]document)}
	if path == "" {
		return h, nil
	}
	raw, err := readBounded(path, maxConfig)
	if err != nil {
		return nil, errors.New("cannot read legal configuration")
	}
	var c configuration
	metadata, err := toml.NewDecoder(bytes.NewReader(raw)).Decode(&c)
	// Parser errors can include operator values and source lines. Return a fixed
	// message; never put private configuration or filenames in startup errors.
	if err != nil || len(metadata.Undecoded()) != 0 {
		return nil, errors.New("invalid legal configuration TOML")
	}
	// The decoder can match struct fields without regard to case. TOML keys are
	// case-sensitive: accept only our exact schema names, including empty tables.
	for _, key := range metadata.Keys() {
		if len(key) == 0 {
			return nil, errors.New("invalid legal configuration TOML")
		}
		switch key[0] {
		case "version", "published", "default_language", "template_root", "variables", "documents":
		default:
			return nil, errors.New("invalid legal configuration TOML")
		}
	}
	c.DefaultLanguage = normalizeLanguage(c.DefaultLanguage)
	if c.Version != 1 || !languagePattern.MatchString(c.DefaultLanguage) {
		return nil, errors.New("legal configuration requires version 1 and a valid default_language")
	}
	h.defaultLanguage = c.DefaultLanguage
	templateRoot := filepath.Dir(path)
	if c.TemplateRoot != "" {
		if !safeAbsoluteDirectory(c.TemplateRoot) {
			return nil, errors.New("invalid legal template_root")
		}
		templateRoot = c.TemplateRoot
		// A disabled draft can name an installation path that does not exist on
		// a developer machine. Publication requires the real root to be safe.
		if c.Published && checkTemplateRoot(templateRoot) != nil {
			return nil, errors.New("invalid legal template_root")
		}
	}
	if len(c.Documents) > len(documentNames) || len(c.Variables) > 64 {
		return nil, errors.New("too many legal documents or variables")
	}
	for key, value := range c.Variables {
		if !variableName.MatchString(key) || len(value) > 16<<10 || !utf8.ValidString(value) || strings.ContainsAny(value, "\x00") || strings.Contains(value, "{{") || strings.Contains(value, "}}") {
			return nil, errors.New("invalid legal template variable")
		}
	}
	for name, translations := range c.Documents {
		if !knownDocument(name) || len(translations) == 0 || len(translations) > 8 {
			return nil, errors.New("unknown legal document or invalid translation count")
		}
		normalized := make(map[string]string)
		for language, file := range translations {
			language = normalizeLanguage(language)
			if !languagePattern.MatchString(language) || normalized[language] != "" || !safeRelativeFile(file) {
				return nil, errors.New("invalid legal language or template path")
			}
			normalized[language] = file
		}
		if _, exists := normalized[c.DefaultLanguage]; !exists {
			return nil, fmt.Errorf("legal document %s has no default language", name)
		}
		if !c.Published {
			continue
		}
		h.documents[name] = make(map[string]document)
		for language, file := range normalized {
			// Operator-owned configuration is mounted read-only. Reject files
			// writable by the group or others, symlinks, and non-regular files,
			// including every relative parent of a template.
			source, err := readTemplate(templateRoot, file)
			if err != nil {
				return nil, fmt.Errorf("cannot read legal template %s/%s", name, language)
			}
			content, err := render(string(source), c.Variables)
			if err != nil {
				return nil, fmt.Errorf("invalid legal template %s/%s: %w", name, language, err)
			}
			sum := sha256.Sum256([]byte(content))
			h.documents[name][language] = document{content, hex.EncodeToString(sum[:])}
		}
	}
	if c.Published && len(h.documents) == 0 {
		return nil, errors.New("published legal configuration has no documents")
	}
	return h, nil
}

// Mount exposes only these public read routes. All other routes keep their
// original authentication and authorization. Never proxy /extensions to a browser.
func (h *Handler) Mount(protected http.Handler) http.Handler {
	mux := http.NewServeMux()
	for _, name := range documentNames {
		mux.Handle("/api/"+name, h)
	}
	mux.Handle("/", protected)
	return mux
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/")
	if !knownDocument(name) || r.URL.Path != "/api/"+name {
		http.NotFound(w, r)
		return
	}
	query := r.URL.Query()
	language := normalizeLanguage(query.Get("lang"))
	if len(query["lang"]) > 1 || (language != "" && !languagePattern.MatchString(language)) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	translations := h.documents[name]
	selected := language
	d, found := translations[selected]
	if !found {
		if base, _, regional := strings.Cut(language, "-"); regional {
			selected = base
			d, found = translations[selected]
		}
	}
	if !found {
		selected = h.defaultLanguage
		d = translations[selected]
	}
	body := struct {
		Success  bool   `json:"success"`
		Data     string `json:"data"`
		Language string `json:"language,omitempty"`
		Revision string `json:"revision,omitempty"`
	}{true, d.text, selected, d.revision}
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_ = json.NewEncoder(w).Encode(body)
}

func normalizeLanguage(language string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(language), "_", "-"))
}

func knownDocument(name string) bool {
	for _, allowed := range documentNames {
		if name == allowed {
			return true
		}
	}
	return false
}

func safeRelativeFile(file string) bool {
	if file == "" || strings.ContainsAny(file, "\\\x00") || filepath.IsAbs(file) || filepath.Clean(file) != file || filepath.Ext(file) != ".md" {
		return false
	}
	for _, part := range strings.Split(file, "/") {
		if part == ".." || part == "." || strings.HasPrefix(part, ".") {
			return false
		}
	}
	return true
}

func safeAbsoluteDirectory(root string) bool {
	return root != "" && len(root) <= 4096 && filepath.IsAbs(root) && filepath.Clean(root) == root && !strings.ContainsAny(root, "\\\x00")
}

// Check every absolute parent, including the configured root itself. Lstat on
// only the final directory would follow a symlink in an earlier component.
func checkTemplateRoot(root string) error {
	path := string(filepath.Separator)
	for _, part := range strings.Split(strings.TrimPrefix(root, path), path) {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if err != nil || info == nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("invalid template directory")
		}
	}
	return nil
}

func readTemplate(root, file string) ([]byte, error) {
	path := root
	for _, part := range strings.Split(file, "/") {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("symlink not allowed")
		}
	}
	return readBounded(path, maxDocument)
}

func readBounded(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || info == nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Size() > limit {
		return nil, errors.New("not a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit || !utf8.Valid(data) {
		return nil, errors.New("invalid text file")
	}
	return data, nil
}

func render(source string, variables map[string]string) (string, error) {
	var result strings.Builder
	offset := 0
	for _, match := range variablePattern.FindAllStringSubmatchIndex(source, -1) {
		value, exists := variables[source[match[2]:match[3]]]
		if !exists || strings.TrimSpace(value) == "" {
			return "", errors.New("a required variable is empty or missing")
		}
		literal := source[offset:match[0]]
		if result.Len()+len(literal)+len(value) > maxDocument {
			return "", errors.New("rendered document is too large")
		}
		result.WriteString(literal)
		result.WriteString(value)
		offset = match[1]
	}
	if result.Len()+len(source)-offset > maxDocument {
		return "", errors.New("rendered document is too large")
	}
	result.WriteString(source[offset:])
	content := result.String()
	if strings.Contains(content, "{{") || strings.Contains(content, "}}") {
		return "", errors.New("unsupported or unresolved placeholder")
	}
	if strings.TrimSpace(content) == "" || strings.ContainsRune(content, '\x00') {
		return "", errors.New("empty or invalid document")
	}
	return content, nil
}
