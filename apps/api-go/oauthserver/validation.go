package oauthserver

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

const maxFormBytes = 16 * 1024

const (
	transactionPrefix = "lmm_ot_"
	consentPrefix     = "lmm_oc_"
	codePrefix        = "lmm_oa_"
	accessPrefix      = "lmm_at_"
	refreshPrefix     = "lmm_rt_"
)

func newSecret(prefix string) (string, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(bytes[:]), nil
}

func validSecret(value, prefix string) bool {
	return strings.HasPrefix(value, prefix) && validChallenge(strings.TrimPrefix(value, prefix))
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func browserDigest(value string) string { return digest("oauth-browser\x00" + value) }

func equalSecret(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func validBrowserBinding(binding string) bool {
	return len(binding) >= 32 && len(binding) <= 512 && printableASCII(binding)
}

func printableASCII(value string) bool {
	for i := range len(value) {
		if value[i] < 0x21 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

func validChallenge(challenge string) bool {
	if len(challenge) != 43 {
		return false
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(challenge)
	return err == nil && len(decoded) == 32
}

func validVerifier(verifier string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	for i := range len(verifier) {
		if !unreserved(verifier[i]) {
			return false
		}
	}
	return true
}

func unreserved(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~", rune(c))
}

func matchesChallenge(verifier, challenge string) bool {
	if !validVerifier(verifier) {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	return equalSecret(base64.RawURLEncoding.EncodeToString(sum[:]), challenge)
}

// parseForm preserves multiplicity until validation. Never replace this with
// Values.Get, gin.PostForm or Request.Form (which merges body and URL values).
func parseForm(raw string, allowed ...string) (url.Values, error) {
	if len(raw) == 0 || len(raw) > maxFormBytes || strings.Contains(raw, "#") {
		return nil, protocolError("invalid_request")
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return nil, protocolError("invalid_request")
	}
	for key, entries := range values {
		if !contains(allowed, key) || len(entries) != 1 || entries[0] == "" || len(entries[0]) > 2048 {
			return nil, protocolError("invalid_request")
		}
		for _, c := range entries[0] {
			if c < 0x20 || c == 0x7f {
				return nil, protocolError("invalid_request")
			}
		}
	}
	return values, nil
}

func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func parseScopes(raw string) ([]string, error) {
	if raw == "" || len(raw) > 2048 {
		return nil, protocolError("invalid_scope")
	}
	values := strings.Split(raw, " ")
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			return nil, protocolError("invalid_scope")
		}
		for i := range len(value) {
			// RFC 6749 scope-token: %x21 / %x23-5B / %x5D-7E.
			if value[i] < 0x21 || value[i] > 0x7e || value[i] == '"' || value[i] == '\\' {
				return nil, protocolError("invalid_scope")
			}
		}
		seen[value] = true
	}
	sort.Strings(values)
	return values, nil
}

func scopesWithin(scopes, allowed []string) bool {
	for _, scope := range scopes {
		if !contains(allowed, scope) {
			return false
		}
	}
	return true
}

func canonicalPath(path string) bool {
	if !strings.HasPrefix(path, "/") || strings.Contains(path, "//") {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == "." || part == ".." {
			return false
		}
		for i := range len(part) {
			if !unreserved(part[i]) {
				return false
			}
		}
	}
	return true
}

func strictURL(raw string) (*url.URL, bool) {
	if raw == "" || len(raw) > 1024 || !printableASCII(raw) || strings.ContainsAny(raw, "?#%\\") {
		return nil, false
	}
	u, err := url.Parse(raw)
	return u, err == nil && u.User == nil && u.Opaque == "" && u.Host != "" && u.RawPath == "" && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && u.String() == raw
}

func validHTTPS(raw string) bool {
	u, ok := strictURL(raw)
	if !ok || u.Scheme != "https" || u.Host != strings.ToLower(u.Host) || (u.Path != "" && !canonicalPath(u.Path)) {
		return false
	}
	// This profile uses configured DNS names, not IP/localhost issuers/resources.
	// No URL equivalence or suffix matching is used in permission decisions.
	if u.Port() != "" || strings.Contains(u.Host, ":") || !strings.Contains(u.Host, ".") || strings.HasSuffix(u.Host, ".") {
		return false
	}
	allNumeric := true
	for _, label := range strings.Split(u.Host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := range len(label) {
			c := label[i]
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
			if c < '0' || c > '9' {
				allNumeric = false
			}
		}
	}
	return !allNumeric && len(u.Host) <= 253
}

func validIssuer(raw string) bool {
	u, ok := strictURL(raw)
	return ok && len(raw) <= 512 && u.Path == "" && validHTTPS(raw)
}

func loopbackURL(raw string) (*url.URL, bool) {
	u, ok := strictURL(raw)
	if !ok || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || !canonicalPath(u.Path) {
		return nil, false
	}
	if u.Host == "127.0.0.1" {
		return u, true
	}
	port, err := strconv.Atoi(u.Port())
	return u, err == nil && port > 0 && port <= 65535 && u.Host == "127.0.0.1:"+strconv.Itoa(port)
}

func validRedirect(raw string, client NativeClient) bool {
	u, ok := loopbackURL(raw)
	if !ok {
		return false
	}
	return contains(client.RedirectURIs, "http://127.0.0.1"+u.Path)
}

func supportedBinding(binding SenderBinding) bool {
	return binding.Method == "" && binding.Thumbprint == ""
}
