package oidcprovider

import (
	"context"
	"html/template"
	"net/http"
	"net/url"
	"time"
)

// BrowserEntryHandler keeps the authorization request in the original tab.
// A SameSite=Strict login cookie may be absent on the first cross-site GET even
// when the user is already signed in. A same-site Continue link makes the next
// request eligible without widening the existing refresh cookie or relying on
// unverified frontend redirect parameters. Login itself remains entirely LMM's.
func (p *Provider) BrowserEntryHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin")
		if r.Method != "GET" {
			p.Handler().ServeHTTP(w, r)
			return
		}
		if !p.transport(r) {
			fail(w, 400, "https_required")
			return
		}
		if !p.allowed(r) {
			fail(w, 429, "rate_limited")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
		if r.URL.Path == "/api/user/auth/oidc/authorize" {
			query, err := url.ParseQuery(r.URL.RawQuery)
			if err != nil {
				fail(w, 400, "invalid_request")
				return
			}
			if _, err = p.parseAuthorization(query); err != nil {
				fail(w, 400, "invalid_request")
				return
			}
		} else if r.URL.Path != "/api/user/auth/oidc/grants" {
			http.NotFound(w, r)
			return
		}
		identity, err := p.config.BrowserIdentity(ctx, r)
		if err == nil && p.config.ValidateIdentity(ctx, identity) == nil {
			p.Handler().ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'")
		_ = loginBridge.Execute(w, struct{ Continue string }{r.URL.RequestURI()})
	})
}

var loginBridge = template.Must(template.New("login-bridge").Parse(`<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>继续 LMM 授权</title><style>body{font:15px/1.8 system-ui,sans-serif;background:#f6f8f3;color:#263a30;padding:40px 18px}main{max-width:520px;background:white;border:1px solid #e2e8dd;border-radius:18px;padding:32px;margin:10vh auto}h1{font-size:25px}p{color:#647362}a{display:inline-block;padding:10px 18px;margin:10px 8px 0 0;border:1px solid #d6dfd0;border-radius:9px;text-decoration:none;color:#263a30}a.continue{color:white;background:#263a30}</style><main><small>LMM · 统一身份</small><h1>使用你的 LMM 账号</h1><p>已经登录，直接继续。尚未登录，在新标签页完成 LMM 登录后回到这里；原来的授权请求会保留。</p><a class="continue" href="{{.Continue}}">已登录，继续授权</a><a href="/login" target="_blank" rel="noopener noreferrer">打开 LMM 登录</a></main></html>`))
