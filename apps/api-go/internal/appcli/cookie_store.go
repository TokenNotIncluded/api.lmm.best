package appcli

import (
	"encoding/json"
	"fmt"
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

const cookieFileFormat = 1

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

type persistentJar struct {
	jar     http.CookieJar
	path    string
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
	data, err := readPrivateOptionalFile(jar.path, 1<<20)
	if err != nil {
		return fmt.Errorf("load cookie file: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	var state cookieFile
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("parse cookie file: %w", err)
	}
	if state.Format != cookieFileFormat {
		return fmt.Errorf("unsupported cookie file format: %d", state.Format)
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
	data, err := json.Marshal(cookieFile{Format: cookieFileFormat, Cookies: cookies})
	if err != nil {
		return fmt.Errorf("encode cookie file: %w", err)
	}
	data = append(data, '\n')
	if err := writePrivateFile(jar.path, data); err != nil {
		return fmt.Errorf("save cookie file: %w", err)
	}
	return nil
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
