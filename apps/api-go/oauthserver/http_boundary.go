package oauthserver

import (
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

// ReadPublicClientForm is a transport guard for future token/revoke handlers.
// It does not serve HTTP or validate TLS/proxy trust. Serve these endpoints
// only over HTTPS with limits/timeouts, no CORS wildcard and no request logging.
func ReadPublicClientForm(request *http.Request) (string, error) {
	if request == nil || request.URL == nil || request.Body == nil || request.Method != http.MethodPost || request.URL.User != nil || request.URL.Fragment != "" || request.URL.RawQuery != "" || request.URL.ForceQuery || hasHeader(request.Header, "Authorization") || hasHeader(request.Header, "DPoP") || hasHeader(request.Header, "Content-Encoding") {
		return "", protocolError("invalid_request")
	}
	contentType := headerValues(request.Header, "Content-Type")
	if len(contentType) != 1 {
		return "", protocolError("invalid_request")
	}
	mediaType, params, err := mime.ParseMediaType(contentType[0])
	if err != nil || mediaType != "application/x-www-form-urlencoded" || len(params) > 1 || len(params) == 1 && !strings.EqualFold(params["charset"], "utf-8") || request.ContentLength > maxFormBytes {
		return "", protocolError("invalid_request")
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, maxFormBytes+1))
	if err != nil || len(body) == 0 || len(body) > maxFormBytes {
		return "", protocolError("invalid_request")
	}
	return string(body), nil
}

// BearerFromRequest accepts exactly one Authorization: Bearer credential. URL
// credentials, duplicate headers and unimplemented sender proof are rejected.
// It never accepts form/query bearer tokens or falls back to ordinary API keys.
func BearerFromRequest(request *http.Request) (string, error) {
	if request == nil || request.URL == nil || request.URL.User != nil || request.URL.Fragment != "" || hasHeader(request.Header, "DPoP") {
		return "", protocolError("invalid_token")
	}
	query, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		return "", protocolError("invalid_token")
	}
	for key := range query {
		switch strings.ToLower(key) {
		case "access_token", "refresh_token", "token", "authorization", "client_secret", "code":
			return "", protocolError("invalid_token")
		}
	}
	headers := headerValues(request.Header, "Authorization")
	if len(headers) != 1 {
		return "", protocolError("invalid_token")
	}
	parts := strings.Split(headers[0], " ")
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || !validSecret(parts[1], accessPrefix) {
		return "", protocolError("invalid_token")
	}
	return parts[1], nil
}

func hasHeader(header http.Header, name string) bool {
	for key := range header {
		if strings.EqualFold(key, name) {
			return true
		}
	}
	return false
}

func headerValues(header http.Header, name string) []string {
	var values []string
	for key, entries := range header {
		if strings.EqualFold(key, name) {
			values = append(values, entries...)
		}
	}
	return values
}

// SensitiveResponseHeaders must be applied to token, revocation, authorization
// and consent responses, including errors. HTML consent additionally needs an
// integration-specific CSP and frame-ancestors 'none'.
func SensitiveResponseHeaders() http.Header {
	return http.Header{"Cache-Control": {"no-store"}, "Pragma": {"no-cache"}, "Referrer-Policy": {"no-referrer"}}
}
