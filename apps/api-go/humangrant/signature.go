// Package humangrant implements the agent-facing HumanGrant 0.1 draft profile.
// Human account verification and business access remain in the host adapter.
package humangrant

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const signatureComponents = `("@method" "@target-uri" "content-digest" "content-type" "x-hg-key" "authorization")`

var signaturePattern = regexp.MustCompile(`^hg=\("@method" "@target-uri" "content-digest" "content-type" "x-hg-key" "authorization"\);created=([0-9]{1,12});expires=([0-9]{1,12});nonce="([A-Za-z0-9_-]{32})";keyid="(ed25519:[A-Za-z0-9_-]{43})";alg="ed25519";tag="humangrant-0.1"$`)
var safeID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var accountID = regexp.MustCompile(`^[1-9][0-9]{0,17}$`)

type Proof struct {
	Actor      string
	PublicKey  string
	Credential string
	Nonce      string
	Created    int64
	Expires    int64
}

// Origin accepts a configured origin, never a Host or forwarded header.
func Origin(raw string, local bool) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.RawPath != "" {
		return "", fmt.Errorf("configure a canonical HTTPS origin")
	}
	if u.Scheme != "https" {
		if !local || u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "[::1]" && u.Hostname() != "::1" && u.Hostname() != "localhost") {
			return "", fmt.Errorf("HTTP requires explicit loopback development mode")
		}
	}
	result := u.Scheme + "://" + u.Host
	if raw != result && raw != result+"/" {
		return "", fmt.Errorf("noncanonical origin")
	}
	return result, nil
}

func fingerprint(data []byte) string {
	sum := sha256.Sum256(data)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
func decodeCanonical(value string, size int, encoding *base64.Encoding) ([]byte, bool) {
	decoded, err := encoding.Strict().DecodeString(value)
	return decoded, err == nil && len(decoded) == size && encoding.EncodeToString(decoded) == value
}
func oneHeader(headers http.Header, name string) (string, bool) {
	var result string
	count := 0
	for key, values := range headers {
		if strings.EqualFold(key, name) {
			count += len(values)
			if len(values) == 1 {
				result = values[0]
			}
		}
	}
	return result, count == 1 && !strings.ContainsAny(result, "\r\n")
}
func cleanPath(r *http.Request) bool {
	return r.URL != nil && r.URL.RawQuery == "" && !r.URL.ForceQuery && r.URL.RawPath == "" &&
		!strings.ContainsAny(r.URL.Path, "%\\\r\n") && strings.HasPrefix(r.URL.Path, "/") &&
		!strings.Contains(r.URL.Path, "//") && !strings.Contains(r.URL.Path, "/./") && !strings.Contains(r.URL.Path, "/../") &&
		(r.RequestURI == "" || r.RequestURI == r.URL.Path)
}

// VerifySignature verifies exact bytes only. The caller MUST reserve Actor/Nonce
// in the same durable store used by authorization before accepting the proof.
func VerifySignature(r *http.Request, body []byte, origin string, now int64) (Proof, *ProtocolError) {
	var p Proof
	fail := func() (Proof, *ProtocolError) { return Proof{}, failure(401, "invalid_signature") }
	if !cleanPath(r) || len(body) > BodyLimit || (r.Method == "GET" && len(body) != 0) {
		return fail()
	}
	names := []string{"Content-Type", "Content-Digest", "X-HG-Key", "Authorization", "Signature-Input", "Signature"}
	values := make([]string, len(names))
	for i, name := range names {
		value, ok := oneHeader(r.Header, name)
		if !ok {
			return fail()
		}
		values[i] = value
	}
	if values[0] != "application/json" {
		return fail()
	}
	digest := sha256.Sum256(body)
	if values[1] != "sha-256=:"+base64.StdEncoding.EncodeToString(digest[:])+":" {
		return fail()
	}
	pub, ok := decodeCanonical(values[2], ed25519.PublicKeySize, base64.RawURLEncoding)
	if !ok {
		return fail()
	}
	if !strings.HasPrefix(values[3], "HumanGrant ") {
		return fail()
	}
	p.Credential = strings.TrimPrefix(values[3], "HumanGrant ")
	if p.Credential != "anonymous" {
		if _, ok := decodeCanonical(p.Credential, 32, base64.RawURLEncoding); !ok {
			return fail()
		}
	}
	fields := signaturePattern.FindStringSubmatch(values[4])
	if fields == nil {
		return fail()
	}
	p.Created, _ = strconv.ParseInt(fields[1], 10, 64)
	p.Expires, _ = strconv.ParseInt(fields[2], 10, 64)
	if strconv.FormatInt(p.Created, 10) != fields[1] || strconv.FormatInt(p.Expires, 10) != fields[2] || p.Expires <= p.Created || p.Expires-p.Created > 60 || p.Created > now+30 || p.Expires <= now {
		return fail()
	}
	p.Nonce, p.Actor, p.PublicKey = fields[3], "ed25519:"+fingerprint(pub), values[2]
	if fields[4] != p.Actor {
		return fail()
	}
	if _, ok := decodeCanonical(p.Nonce, 24, base64.RawURLEncoding); !ok {
		return fail()
	}
	if !strings.HasPrefix(values[5], "hg=:") || !strings.HasSuffix(values[5], ":") {
		return fail()
	}
	sig, ok := decodeCanonical(strings.TrimSuffix(strings.TrimPrefix(values[5], "hg=:"), ":"), ed25519.SignatureSize, base64.StdEncoding)
	if !ok {
		return fail()
	}
	base := `"@method": ` + r.Method + "\n" + `"@target-uri": ` + origin + r.URL.Path + "\n" + `"content-digest": ` + values[1] + "\n" + `"content-type": ` + values[0] + "\n" + `"x-hg-key": ` + values[2] + "\n" + `"authorization": ` + values[3] + "\n" + `"@signature-params": ` + strings.TrimPrefix(values[4], "hg=")
	if !ed25519.Verify(pub, []byte(base), sig) {
		return fail()
	}
	return p, nil
}
