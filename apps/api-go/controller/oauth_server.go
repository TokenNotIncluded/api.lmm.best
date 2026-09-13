package controller

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/oauthserver"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

const oauthBrowserCookie = "__Host-lmm-oauth-flow"
const oauthBrowserContinue = "/api/user/auth/oauth2/continue"
const oauthBrowserConsent = "/api/user/auth/oauth2/consent"

type oauthBrowserFlow struct {
	RawQuery string
	CSRF     string
	Binding  string
	Identity service.OAuthBrowserIdentity
	Consent  *oauthserver.Consent
	Groups   []string
	Account  string
	Language string
	Expires  time.Time
}

type oauthRateBucket struct {
	Start time.Time
	Count int
}

// Bounded, single-use browser state is intentionally ephemeral. Restart or
// routing a form to another worker requires a fresh login; it cannot grant
// access. Production multi-worker proxies need sticky browser-flow routing.
type OAuthHTTP struct {
	Integration *service.OAuthIntegration
	mu          sync.Mutex
	flows       map[[32]byte]*oauthBrowserFlow
	rates       map[string]oauthRateBucket
}

func NewOAuthHTTP(integration *service.OAuthIntegration) *OAuthHTTP {
	return &OAuthHTTP{Integration: integration, flows: make(map[[32]byte]*oauthBrowserFlow), rates: make(map[string]oauthRateBucket)}
}

func oauthRandom() (string, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes[:]), nil
}

func (h *OAuthHTTP) Guard(budget int, category string) gin.HandlerFunc {
	return func(c *gin.Context) {
		service.OAuthNoStore(c.Writer)
		if h.Integration == nil {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		if !h.Integration.OAuthRequestTransport(c.Request) {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		if !h.allowRate(category+":"+h.Integration.OAuthRateKey(c.Request), budget) {
			c.Header("Retry-After", "60")
			c.AbortWithStatusJSON(429, gin.H{"error": "temporarily_unavailable"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

func (h *OAuthHTTP) allowRate(key string, budget int) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	bucket, found := h.rates[key]
	if !found || now.Sub(bucket.Start) >= time.Minute {
		if len(h.rates) >= 4096 {
			for key, value := range h.rates {
				if now.Sub(value.Start) >= time.Minute {
					delete(h.rates, key)
				}
			}
		}
		if !found && len(h.rates) >= 4096 {
			return false
		}
		bucket = oauthRateBucket{Start: now}
	}
	bucket.Count++
	h.rates[key] = bucket
	return bucket.Count <= budget
}

func (h *OAuthHTTP) Metadata(c *gin.Context) {
	metadata := h.Integration.Core.Metadata()
	metadata.AuthorizationEndpoint = h.Integration.Issuer + "/api/oauth2/authorize"
	metadata.TokenEndpoint = h.Integration.Issuer + "/api/oauth2/token"
	metadata.RevocationEndpoint = h.Integration.Issuer + "/api/oauth2/revoke"
	// Group scopes are consent-generated and can disclose deployment structure.
	// Public discovery advertises only the initial application scopes.
	metadata.ScopesSupported = []string{service.OAuthCatalogScope, service.OAuthBalanceScope, service.OAuthInvokeScope}
	c.JSON(200, metadata)
}

func (h *OAuthHTTP) ResourceMetadata(c *gin.Context) {
	c.JSON(200, gin.H{"resource": h.Integration.Resource, "authorization_servers": []string{h.Integration.Issuer}, "scopes_supported": []string{service.OAuthCatalogScope, service.OAuthBalanceScope, service.OAuthInvokeScope}, "bearer_methods_supported": []string{"header"}})
}

func oauthProtocolFailure(c *gin.Context, err error) {
	var protocol *oauthserver.ProtocolError
	if errors.As(err, &protocol) {
		c.JSON(protocol.Status, protocol)
		return
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "server_error", "error_description": "The authorization service is unavailable."})
}

func (h *OAuthHTTP) Token(c *gin.Context) {
	if service.OAuthAlternateCredentials(c.Request) || len(c.Request.Header.Values(service.OAuthGroupHeader)) != 0 {
		c.JSON(400, gin.H{"error": "invalid_request"})
		return
	}
	raw, err := oauthserver.ReadPublicClientForm(c.Request)
	if err != nil {
		oauthProtocolFailure(c, err)
		return
	}
	response, err := h.Integration.Core.Exchange(c.Request.Context(), raw, oauthserver.SenderBinding{})
	if err != nil {
		oauthProtocolFailure(c, err)
		return
	}
	c.JSON(200, response)
}

func (h *OAuthHTTP) Revoke(c *gin.Context) {
	if service.OAuthAlternateCredentials(c.Request) || len(c.Request.Header.Values(service.OAuthGroupHeader)) != 0 {
		c.JSON(400, gin.H{"error": "invalid_request"})
		return
	}
	raw, err := oauthserver.ReadPublicClientForm(c.Request)
	if err != nil {
		oauthProtocolFailure(c, err)
		return
	}
	if err := h.Integration.Core.Revoke(c.Request.Context(), raw); err != nil {
		oauthProtocolFailure(c, err)
		return
	}
	c.Status(200)
}

func (h *OAuthHTTP) render(c *gin.Context, status int, data oauthPageData) {
	data.Copy = oauthPageCopies[data.Language]
	if data.Copy.Title == "" {
		data.Language = "en"
		data.Copy = oauthPageCopies["en"]
	}
	nonce, err := oauthRandom()
	if err != nil {
		c.Status(503)
		return
	}
	data.Nonce = nonce
	if data.Mode == "preflight" || data.Mode == "consent" {
		// Native form POSTs under no-referrer send Origin: null. Keep the
		// trusted origin for CSRF checks without disclosing paths or queries.
		c.Header("Referrer-Policy", "strict-origin")
	}
	c.Header("Content-Security-Policy", "default-src 'none'; style-src 'nonce-"+nonce+"'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	c.Header("Content-Language", data.Language)
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Status(status)
	// Template content and all field types are fixed; no template.HTML/JS/URL
	// escape hatches or third-party scripts are used.
	_ = oauthPage.Execute(c.Writer, data)
}

func oauthCookie(c *gin.Context, value string, age int) {
	http.SetCookie(c.Writer, &http.Cookie{Name: oauthBrowserCookie, Value: value, Path: "/", MaxAge: age, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
}

func (h *OAuthHTTP) putFlow(c *gin.Context, flow *oauthBrowserFlow) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	for key, value := range h.flows {
		if !value.Expires.After(now) {
			delete(h.flows, key)
		}
	}
	if len(h.flows) >= 4096 {
		return false
	}
	h.flows[sha256.Sum256([]byte(flow.Binding))] = flow
	oauthCookie(c, flow.Binding, 300)
	return true
}

func (h *OAuthHTTP) takeFlow(c *gin.Context) (*oauthBrowserFlow, url.Values, bool) {
	if !h.Integration.OAuthSameOrigin(c.Request) {
		return nil, nil, false
	}
	raw, err := oauthserver.ReadPublicClientForm(c.Request)
	if err != nil {
		return nil, nil, false
	}
	values, err := url.ParseQuery(raw)
	if err != nil || len(values["csrf"]) != 1 {
		return nil, nil, false
	}
	for key, value := range values {
		if key != "csrf" && key != "decision" || len(value) != 1 {
			return nil, nil, false
		}
	}
	var cookie string
	count := 0
	for _, item := range c.Request.Cookies() {
		if item.Name == oauthBrowserCookie {
			cookie = item.Value
			count++
		}
	}
	if count != 1 || len(cookie) != 43 {
		return nil, nil, false
	}
	key := sha256.Sum256([]byte(cookie))
	h.mu.Lock()
	defer h.mu.Unlock()
	flow := h.flows[key]
	if flow == nil || !flow.Expires.After(time.Now()) || subtle.ConstantTimeCompare([]byte(flow.CSRF), []byte(values.Get("csrf"))) != 1 {
		return nil, nil, false
	}
	delete(h.flows, key)
	oauthCookie(c, "", -1)
	return flow, values, true
}

func (h *OAuthHTTP) failed(c *gin.Context, language string) {
	h.render(c, 400, oauthPageData{Language: language, Mode: "failed"})
}

func (h *OAuthHTTP) showPreflight(c *gin.Context, raw, language string) {
	binding, err := oauthRandom()
	if err != nil {
		h.failed(c, language)
		return
	}
	csrf, err := oauthRandom()
	if err != nil {
		h.failed(c, language)
		return
	}
	flow := &oauthBrowserFlow{RawQuery: raw, Binding: binding, CSRF: csrf, Language: language, Expires: time.Now().Add(oauthserver.AuthorizationTTL)}
	if !h.putFlow(c, flow) {
		h.failed(c, language)
		return
	}
	h.render(c, 200, oauthPageData{Language: language, Mode: "preflight", CSRF: csrf, Action: oauthBrowserContinue, Resource: h.Integration.Resource})
}

func (h *OAuthHTTP) Authorize(c *gin.Context) {
	language := oauthPageLanguage(c.GetHeader("Accept-Language"))
	binding, err := oauthRandom()
	if err != nil {
		h.failed(c, language)
		return
	}
	// Validate raw query including duplicates before presenting any request. This
	// unbound preflight row is never prepared/approved and expires automatically.
	if _, err := h.Integration.Core.BeginAuthorization(c.Request.Context(), c.Request.URL.RawQuery, binding); err != nil {
		h.failed(c, language)
		return
	}
	h.showPreflight(c, c.Request.URL.RawQuery, language)
}

func (h *OAuthHTTP) Continue(c *gin.Context) {
	flow, values, ok := h.takeFlow(c)
	if !ok {
		h.failed(c, oauthPageLanguage(c.GetHeader("Accept-Language")))
		return
	}
	if flow.Consent != nil || values.Has("decision") {
		h.failed(c, flow.Language)
		return
	}
	identity, user, err := h.Integration.BrowserIdentity(c.Request.Context(), c.Request)
	if err != nil {
		h.showPreflight(c, flow.RawQuery, flow.Language)
		return
	}
	query, groups, err := h.Integration.ConsentQuery(flow.RawQuery, user)
	if err != nil {
		h.failed(c, flow.Language)
		return
	}
	// Explicitly rotate the pre-login binding once a verified identity exists.
	binding, err := oauthRandom()
	if err != nil {
		h.failed(c, flow.Language)
		return
	}
	pending, err := h.Integration.Core.BeginAuthorization(c.Request.Context(), query, binding)
	if err != nil {
		h.failed(c, flow.Language)
		return
	}
	consent, err := h.Integration.Core.TrustedPrepareConsent(c.Request.Context(), pending.Transaction, binding, identity.UserID)
	if err != nil {
		h.failed(c, flow.Language)
		return
	}
	csrf, err := oauthRandom()
	if err != nil {
		h.failed(c, flow.Language)
		return
	}
	bound := &oauthBrowserFlow{RawQuery: flow.RawQuery, CSRF: csrf, Binding: binding, Identity: identity, Consent: consent, Groups: groups, Account: user.Username, Language: flow.Language, Expires: consent.ExpiresAt}
	if !h.putFlow(c, bound) {
		h.failed(c, flow.Language)
		return
	}
	h.render(c, 200, oauthPageData{Language: flow.Language, Mode: "consent", CSRF: csrf, Action: oauthBrowserConsent, Resource: h.Integration.Resource, Account: user.Username, Groups: groups})
}

func (h *OAuthHTTP) Consent(c *gin.Context) {
	flow, values, ok := h.takeFlow(c)
	if !ok {
		h.failed(c, oauthPageLanguage(c.GetHeader("Accept-Language")))
		return
	}
	if flow.Consent == nil || values.Get("decision") != "allow" && values.Get("decision") != "deny" {
		h.failed(c, flow.Language)
		return
	}
	identity, user, err := h.Integration.BrowserIdentity(c.Request.Context(), c.Request)
	if err != nil || identity != flow.Identity {
		h.failed(c, flow.Language)
		return
	}
	current := h.Integration.AllowedGroups(user)
	for _, group := range flow.Groups {
		if !slices.Contains(current, group) {
			h.failed(c, flow.Language)
			return
		}
	}
	var response *oauthserver.AuthorizationResponse
	if values.Get("decision") == "allow" {
		response, err = h.Integration.Core.TrustedApprove(c.Request.Context(), flow.Consent.Transaction, flow.Binding, flow.Consent.Secret)
	} else {
		response, err = h.Integration.Core.TrustedDeny(c.Request.Context(), flow.Consent.Transaction, flow.Binding, flow.Consent.Secret)
	}
	if err != nil {
		h.failed(c, flow.Language)
		return
	}
	h.render(c, 200, oauthPageData{Language: flow.Language, Mode: "complete", Redirect: response.RedirectURI})
}
