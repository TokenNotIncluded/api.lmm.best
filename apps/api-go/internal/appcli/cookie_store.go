package appcli

import (
	"crypto/aes"
	"crypto/cipher"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/publicsuffix"
)

const (
	cookieFileFormat          = 1
	encryptedCookieFileFormat = 2
	cookieStoreKeyEnvironment = "LMM_COOKIE_STORE_KEY"
	cookieStoreKeyBytes       = 32
	cookieStoreFileLimit      = 1 << 20
	cookieStoreAAD            = "lmm-api-cookie-store-v2"
)

type storedCookie struct {
	Origin   string        `json:"origin"`
	Name     string        `json:"name"`
	Value    string        `json:"value"`
	Path     string        `json:"path"`
	Domain   string        `json:"domain,omitempty"`
	Expires  time.Time     `json:"expires,omitempty"`
	Secure   bool          `json:"secure,omitempty"`
	HTTPOnly bool          `json:"http_only,omitempty"`
	SameSite http.SameSite `json:"same_site,omitempty"`
	HostOnly bool          `json:"host_only,omitempty"`
}

type cookieFile struct {
	Format  int            `json:"format"`
	Cookies []storedCookie `json:"cookies"`
}

type encryptedCookieFile struct {
	Format     int    `json:"format"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

type persistentJar struct {
	jar     http.CookieJar
	path    string
	key     []byte
	mu      sync.Mutex
	records map[string]storedCookie
}

func newPersistentJar(path string) (*persistentJar, error) {
	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		return nil, fmt.Errorf("create cookie jar: %w", err)
	}
	persistent := &persistentJar{
		jar:     jar,
		path:    path,
		records: make(map[string]storedCookie),
	}
	if path != "" {
		persistent.key, err = loadCookieStoreKey(path)
		if err != nil {
			return nil, fmt.Errorf("load cookie encryption key: %w", err)
		}
		if err := persistent.load(); err != nil {
			return nil, err
		}
	}
	return persistent, nil
}

func (jar *persistentJar) Cookies(target *url.URL) []*http.Cookie {
	return jar.jar.Cookies(target)
}

func (jar *persistentJar) SetCookies(target *url.URL, cookies []*http.Cookie) {
	jar.jar.SetCookies(target, cookies)

	jar.mu.Lock()
	defer jar.mu.Unlock()
	now := time.Now()
	for _, cookie := range cookies {
		record := cookieRecord(target, cookie, now)
		key := cookieKey(record)
		if cookie.MaxAge < 0 || (!record.Expires.IsZero() && !record.Expires.After(now)) {
			delete(jar.records, key)
			continue
		}
		jar.records[key] = record
	}
}

func (jar *persistentJar) load() error {
	data, err := readPrivateOptionalFile(jar.path, cookieStoreFileLimit)
	if err != nil {
		return fmt.Errorf("load cookie file: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	state, err := decryptCookieFile(data, jar.key)
	if err != nil {
		return fmt.Errorf("parse cookie file: %w", err)
	}

	now := time.Now()
	for _, record := range state.Cookies {
		if record.Name == "" || (!record.Expires.IsZero() && !record.Expires.After(now)) {
			continue
		}
		origin, err := url.Parse(record.Origin)
		if err != nil || origin.Scheme == "" || origin.Host == "" {
			return fmt.Errorf("cookie file contains an invalid origin")
		}
		cookie := &http.Cookie{
			Name:     record.Name,
			Value:    record.Value,
			Path:     record.Path,
			Domain:   record.Domain,
			Expires:  record.Expires,
			Secure:   record.Secure,
			HttpOnly: record.HTTPOnly,
			SameSite: record.SameSite,
		}
		jar.jar.SetCookies(origin, []*http.Cookie{cookie})
		jar.records[cookieKey(record)] = record
	}
	return nil
}

func (jar *persistentJar) save() error {
	if jar.path == "" {
		return nil
	}
	jar.mu.Lock()
	cookies := make([]storedCookie, 0, len(jar.records))
	now := time.Now()
	for key, record := range jar.records {
		if !record.Expires.IsZero() && !record.Expires.After(now) {
			delete(jar.records, key)
			continue
		}
		cookies = append(cookies, record)
	}
	jar.mu.Unlock()

	sort.Slice(cookies, func(left, right int) bool {
		return cookieKey(cookies[left]) < cookieKey(cookies[right])
	})
	data, err := encryptCookieFile(cookieFile{Format: cookieFileFormat, Cookies: cookies}, jar.key)
	if err != nil {
		return fmt.Errorf("encode cookie file: %w", err)
	}
	if err := writePrivateFile(jar.path, data); err != nil {
		return fmt.Errorf("save cookie file: %w", err)
	}
	return nil
}

func encryptCookieFile(state cookieFile, key []byte) ([]byte, error) {
	plaintext, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cookie cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create cookie GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(cryptorand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate cookie nonce: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, []byte(cookieStoreAAD))
	envelope := encryptedCookieFile{
		Format:     encryptedCookieFileFormat,
		Nonce:      base64.RawURLEncoding.EncodeToString(nonce),
		Ciphertext: base64.RawURLEncoding.EncodeToString(ciphertext),
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func decryptCookieFile(data, key []byte) (cookieFile, error) {
	var marker struct {
		Format int `json:"format"`
	}
	if err := json.Unmarshal(data, &marker); err != nil {
		return cookieFile{}, err
	}
	if marker.Format == cookieFileFormat {
		// Format 1 was private-permission JSON. Read it once for compatibility;
		// the next save always migrates it to authenticated encryption.
		var legacy cookieFile
		if err := json.Unmarshal(data, &legacy); err != nil {
			return cookieFile{}, err
		}
		return legacy, nil
	}
	if marker.Format != encryptedCookieFileFormat {
		return cookieFile{}, fmt.Errorf("unsupported cookie file format: %d", marker.Format)
	}
	var envelope encryptedCookieFile
	if err := json.Unmarshal(data, &envelope); err != nil {
		return cookieFile{}, err
	}
	nonce, err := base64.RawURLEncoding.DecodeString(envelope.Nonce)
	if err != nil {
		return cookieFile{}, fmt.Errorf("decode cookie nonce: %w", err)
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return cookieFile{}, fmt.Errorf("decode cookie ciphertext: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return cookieFile{}, fmt.Errorf("create cookie cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return cookieFile{}, fmt.Errorf("create cookie GCM: %w", err)
	}
	if len(nonce) != gcm.NonceSize() {
		return cookieFile{}, fmt.Errorf("cookie nonce has invalid length")
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, []byte(cookieStoreAAD))
	if err != nil {
		return cookieFile{}, fmt.Errorf("decrypt cookie file: %w", err)
	}
	var state cookieFile
	if err := json.Unmarshal(plaintext, &state); err != nil {
		return cookieFile{}, err
	}
	if state.Format != cookieFileFormat {
		return cookieFile{}, fmt.Errorf("unsupported encrypted cookie payload format: %d", state.Format)
	}
	return state, nil
}

func loadCookieStoreKey(cookiePath string) ([]byte, error) {
	if secret := strings.TrimSpace(os.Getenv(cookieStoreKeyEnvironment)); secret != "" {
		if len(secret) < cookieStoreKeyBytes {
			return nil, fmt.Errorf("%s must contain at least %d bytes", cookieStoreKeyEnvironment, cookieStoreKeyBytes)
		}
		sum := sha256.Sum256([]byte(cookieStoreAAD + "\x00" + secret))
		return sum[:], nil
	}

	keyPath := cookiePath + ".key"
	encoded, err := readPrivateOptionalFile(keyPath, 256)
	if err != nil {
		return nil, err
	}
	if len(encoded) != 0 {
		key, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(string(encoded)))
		if err != nil || len(key) != cookieStoreKeyBytes {
			return nil, fmt.Errorf("cookie key file is invalid")
		}
		return key, nil
	}

	key := make([]byte, cookieStoreKeyBytes)
	if _, err := io.ReadFull(cryptorand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate cookie key: %w", err)
	}
	file, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if os.IsExist(err) {
		return loadCookieStoreKey(cookiePath)
	}
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		_ = file.Close()
		if !committed {
			_ = os.Remove(keyPath)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return nil, err
	}
	keyData := append([]byte(base64.RawURLEncoding.EncodeToString(key)), '\n')
	if _, err := file.Write(keyData); err != nil {
		return nil, err
	}
	if err := file.Sync(); err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	committed = true
	return key, nil
}

func cookieRecord(target *url.URL, cookie *http.Cookie, now time.Time) storedCookie {
	hostOnly := strings.TrimPrefix(strings.ToLower(cookie.Domain), ".") == ""
	path := cookie.Path
	if path == "" {
		path = defaultCookiePath(target.EscapedPath())
	}
	expires := cookie.Expires
	if expires.IsZero() && cookie.MaxAge > 0 {
		expires = now.Add(time.Duration(cookie.MaxAge) * time.Second)
	}
	return storedCookie{
		Origin:   target.Scheme + "://" + target.Host,
		Name:     cookie.Name,
		Value:    cookie.Value,
		Path:     path,
		Domain:   cookie.Domain,
		Expires:  expires,
		Secure:   cookie.Secure,
		HTTPOnly: cookie.HttpOnly,
		SameSite: cookie.SameSite,
		HostOnly: hostOnly,
	}
}

func cookieKey(cookie storedCookie) string {
	domain := strings.TrimPrefix(strings.ToLower(cookie.Domain), ".")
	if cookie.HostOnly || domain == "" {
		if origin, err := url.Parse(cookie.Origin); err == nil {
			domain = strings.ToLower(origin.Hostname())
		}
	}
	hostScope := "domain"
	if cookie.HostOnly {
		hostScope = "host"
	}
	return hostScope + "\x00" + domain + "\x00" + cookie.Path + "\x00" + cookie.Name
}

func defaultCookiePath(requestPath string) string {
	if requestPath == "" || requestPath[0] != '/' {
		return "/"
	}
	lastSlash := strings.LastIndex(requestPath, "/")
	if lastSlash <= 0 {
		return "/"
	}
	return requestPath[:lastSlash]
}

func readPrivateOptionalFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("path is not a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("cookie file permissions must not grant group or other access")
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("file exceeds %d bytes", limit)
	}
	return os.ReadFile(path)
}
