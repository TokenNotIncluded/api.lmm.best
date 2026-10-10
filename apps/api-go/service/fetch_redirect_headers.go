package service

import (
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// fetchOrigin includes scheme and effective port. A child domain or a different
// port is not the same credential recipient. DNS-equivalent host spellings are
// normalized only for this comparison; destination policy is still checked.
func fetchOrigin(u *url.URL) string {
	if u == nil {
		return ""
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else if u.Scheme == "http" {
			port = "80"
		}
	}
	if n, err := strconv.Atoi(port); err == nil {
		port = strconv.Itoa(n)
	}
	return strings.ToLower(u.Scheme) + "://" + net.JoinHostPort(host, port)
}

// net/http copies headers from the initial request again on later redirects.
// Check the entire chain, not only the immediately preceding origin. A list of
// known credential names is insufficient: integrations can use custom headers.
func stripCrossOriginFetchHeaders(req *http.Request, via []*http.Request) {
	if req == nil || req.URL == nil {
		return
	}
	current := fetchOrigin(req.URL)
	for _, previous := range via {
		if previous == nil || fetchOrigin(previous.URL) != current {
			clean := make(http.Header)
			for name, values := range req.Header {
				switch http.CanonicalHeaderKey(name) {
				case "Accept", "Accept-Encoding", "Accept-Language", "Range", "If-Range", "If-None-Match", "If-Modified-Since", "User-Agent":
					clean[name] = append([]string(nil), values...)
				}
			}
			req.Header = clean
			req.Host = ""
			// URL userinfo would otherwise create Authorization after this callback.
			clone := *req.URL
			clone.User = nil
			req.URL = &clone
			return
		}
	}
}

// Validate the destination with the existing policy before following a redirect.
func checkCredentialSafeFetchRedirect(req *http.Request, via []*http.Request) error {
	if err := checkProtectedFetchRedirect(req, via); err != nil {
		return err
	}
	stripCrossOriginFetchHeaders(req, via)
	return nil
}
