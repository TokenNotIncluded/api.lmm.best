package controller

import (
	"html/template"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/gin-gonic/gin"
)

const (
	accessPolicyErrorHeader          = "access-policy"
	accessPolicyResultHeader         = "X-LMM-Access-Policy"
	accessPolicyOriginalURIHeader    = "X-LMM-Original-URI"
	accessPolicyOriginalAcceptHeader = "X-LMM-Original-Accept"
	accessPolicyDenied               = "denied"
	accessPolicyRejectedErrorCode    = "IP_ACCESS_ROUTE_REJECTED"
	accessPolicyRejectedErrorType    = "access_policy_error"
	accessPolicyRejectedMessage      = "Request rejected by IP access policy."
)

// GetAccessPolicyErrorPage renders the edge-policy response through the Go
// service so the user-facing error page is versioned with the application.
// Nginx is the only caller: the handler rejects public requests and only
// reflects only the validated, length-limited request ID into the HTML page.
func GetAccessPolicyErrorPage(c *gin.Context) {
	if !loopbackPeer(c.Request.RemoteAddr) ||
		strings.TrimSpace(c.GetHeader("X-LMM-Internal-Error")) != accessPolicyErrorHeader ||
		strings.TrimSpace(c.GetHeader(accessPolicyResultHeader)) != accessPolicyDenied {
		c.Status(http.StatusNotFound)
		return
	}

	// AI/relay paths never disclose that IP/region policy is the reason for the
	// rejection: an anonymous or key-less request to these paths sees a plain
	// 404, identical to abortRelayAsNotFound, so it cannot be distinguished
	// from a path that simply does not exist. Every other path (the public
	// site, static assets) keeps the region restriction page below.
	if accessPolicyAPIPath(accessPolicyOriginalPath(c)) {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "Not Found"})
		return
	}

	requestID := accessPolicyRequestID(c)
	c.Header(common.RequestIdKey, requestID)
	c.Header("Cache-Control", "private, no-store, max-age=0")
	c.Header("Pragma", "no-cache")
	c.Header("Vary", "Accept, Origin")
	c.Header("X-Content-Type-Options", "nosniff")
	if accessPolicyWantsJSON(c) {
		applyAccessPolicyJSONCORS(c)
		c.JSON(http.StatusUnavailableForLegalReasons, gin.H{
			"error": gin.H{
				"code":       accessPolicyRejectedErrorCode,
				"message":    accessPolicyRejectedMessage,
				"request_id": requestID,
				"type":       accessPolicyRejectedErrorType,
			},
		})
		return
	}

	language := "zh"
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(c.GetHeader("Accept-Language"))), "en") {
		language = "en"
	}
	page := accessPolicyErrorPage(language, requestID)
	c.Data(http.StatusUnavailableForLegalReasons, "text/html; charset=utf-8", []byte(page))
}

func applyAccessPolicyJSONCORS(c *gin.Context) {
	if strings.TrimSpace(c.GetHeader("Origin")) == "" {
		return
	}
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Access-Control-Expose-Headers", common.RequestIdKey)
}

func accessPolicyWantsJSON(c *gin.Context) bool {
	if accessPolicyAPIPath(accessPolicyOriginalPath(c)) {
		return true
	}

	for mediaRange := range strings.SplitSeq(strings.ToLower(c.GetHeader(accessPolicyOriginalAcceptHeader)), ",") {
		mediaType, parameters, err := mime.ParseMediaType(strings.TrimSpace(mediaRange))
		if err != nil {
			continue
		}
		if quality, ok := parameters["q"]; ok {
			parsedQuality, err := strconv.ParseFloat(quality, 64)
			if err != nil || parsedQuality <= 0 {
				continue
			}
		}
		if mediaType == "application/json" || mediaType == "text/json" || strings.HasSuffix(mediaType, "+json") {
			return true
		}
	}
	return false
}

// accessPolicyOriginalPath strips the query string from the original
// (pre-nginx-rejection) request URI forwarded by the edge.
func accessPolicyOriginalPath(c *gin.Context) string {
	originalURI := strings.TrimSpace(c.GetHeader(accessPolicyOriginalURIHeader))
	if queryStart := strings.IndexByte(originalURI, '?'); queryStart >= 0 {
		originalURI = originalURI[:queryStart]
	}
	return originalURI
}

func accessPolicyAPIPath(path string) bool {
	for _, prefix := range []string{
		"/api",
		"/mcp",
		"/v1",
		"/v1beta",
		"/pg",
		"/mj",
		"/suno",
		"/kling/v1",
		"/jimeng",
	} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	if path == "/dashboard/billing/subscription" || path == "/dashboard/billing/usage" {
		return true
	}

	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(segments) < 2 || segments[0] == "" || segments[1] != "mj" {
		return false
	}
	// These ^~ frontend prefixes win before the dynamic /:mode/mj Nginx
	// regex, so their matching paths must keep the browser-facing HTML page.
	return segments[0] != "oauth" && segments[0] != "static"
}

// This page must remain self-contained: a rejected browser cannot load the
// application's styles or scripts. Its semantic colors mirror the console.
func accessPolicyErrorPage(language string, requestID string) string {
	const pageTemplate = `<!doctype html>
<html lang="{{.Language}}">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <link rel="icon" href="data:,">
  <title>{{.Title}}</title>
  <style>
    :root{color-scheme:light dark;--background:#faf9f5;--foreground:#141413;--card:color-mix(in oklch,#faf9f5 55%,white);--muted-foreground:#5e5a52;--border:oklch(.905 .004 95);--primary:#141413;--primary-foreground:#faf9f5}
    @media(prefers-color-scheme:dark){:root{--background:oklch(.235 0 0);--foreground:oklch(.965 0 0);--card:oklch(.285 0 0);--muted-foreground:oklch(.78 0 0);--border:oklch(1 0 0 / 10%);--primary:#faf9f5;--primary-foreground:#141413}}
    *{box-sizing:border-box}body{margin:0;min-height:100vh;min-height:100svh;display:grid;place-items:center;background:var(--background);color:var(--foreground);font:16px/1.65 system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI","PingFang SC","Microsoft YaHei",sans-serif;padding:32px 20px}
    main{width:min(600px,100%);padding:40px;border:1px solid var(--border);border-radius:.5rem;background:var(--card);box-shadow:0 1px 3px #0000000a}
    .heading{display:flex;align-items:center;gap:16px;margin-bottom:24px}.icon{display:grid;place-items:center;width:52px;height:52px;flex:none;border:1px solid var(--border);border-radius:.5rem}.icon svg{width:28px;height:28px}
    h1{font-size:clamp(24px,4vw,30px);line-height:1.3;letter-spacing:-.025em;margin:0;font-weight:700}.status{display:block;margin-bottom:6px;color:var(--muted-foreground);font:12px/1.4 ui-monospace,SFMono-Regular,Menlo,monospace;letter-spacing:.08em}
    p{margin:0;color:var(--muted-foreground)}.message{font-size:18px;font-weight:600;color:var(--foreground);margin-bottom:8px}.help{margin-top:24px}
    .request{display:grid;gap:8px;border-top:1px solid var(--border);padding-top:20px;margin:24px 0 0}dt{font-size:13px;color:var(--muted-foreground)}dd{margin:0;min-width:0}code{font:13px/1.6 ui-monospace,SFMono-Regular,Menlo,monospace;overflow-wrap:anywhere;user-select:all}
    .actions{margin-top:28px}button{min-height:44px;padding:10px 18px;border:1px solid transparent;border-radius:.5rem;background:var(--primary);color:var(--primary-foreground);font:inherit;font-size:14px;font-weight:600;line-height:1.5;cursor:pointer}button:focus-visible{outline:2px solid var(--foreground);outline-offset:4px}button:hover:not(:disabled){opacity:.9}button:disabled{opacity:.45;cursor:default}
    @media(max-width:420px){body{padding:20px 16px}main{padding:28px 20px}.heading{gap:12px}.icon{width:44px;height:44px}.icon svg{width:24px;height:24px}.message{font-size:16px}}
  </style>
</head>
<body>
<main aria-labelledby="page-title">
  <div class="heading">
    <div class="icon" aria-hidden="true"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3c3 3 4 6 4 9s-1 6-4 9c-3-3-4-6-4-9s1-6 4-9"/></svg></div>
    <div><span class="status">HTTP 451</span><h1 id="page-title">{{.Title}}</h1></div>
  </div>
  <p class="message">{{.Message}}</p>
  <p>{{.Reason}}</p>
  <p class="help">{{.Help}}</p>
  <dl class="request"><div><dt>{{.RequestIDLabel}}</dt><dd><code>{{.RequestID}}</code></dd></div></dl>
  <div class="actions"><button type="button" id="go-back" disabled>{{.Back}}</button></div>
</main>
<script>if(history.length>1){const back=document.getElementById('go-back');back.disabled=false;back.addEventListener('click',()=>history.back())}</script>
</body>
</html>`
	templateData := struct {
		Language       string
		Title          string
		Message        string
		Reason         string
		Help           string
		Back           string
		RequestIDLabel string
		RequestID      string
	}{Language: language, RequestID: requestID}
	if language == "en" {
		templateData.Title = "Regional Access Restriction"
		templateData.Message = "Access is restricted in your region."
		templateData.Reason = "This is an access restriction, not a temporary network outage or server failure."
		templateData.Help = "If you believe this decision is incorrect, provide the request ID to the website administrator."
		templateData.Back = "Go back"
		templateData.RequestIDLabel = "Request ID"
	} else {
		templateData.Title = "地区访问限制"
		templateData.Message = "当前访问受地区限制。"
		templateData.Reason = "这是访问限制，并非暂时断网或服务器故障。"
		templateData.Help = "若认为判定有误，请向网站管理员提供请求编号。"
		templateData.Back = "返回上一页"
		templateData.RequestIDLabel = "请求编号"
	}
	page := template.Must(template.New("access-policy-error").Parse(pageTemplate))
	var rendered strings.Builder
	if err := page.Execute(&rendered, templateData); err != nil {
		return "<!doctype html><meta charset=\"utf-8\"><title>Access unavailable</title>"
	}
	return rendered.String()
}

func accessPolicyRequestID(c *gin.Context) string {
	for _, candidate := range []string{
		c.GetString(common.RequestIdKey),
		c.GetHeader(common.RequestIdKey),
		c.GetHeader("X-Request-ID"),
	} {
		if value := truncateAccessPolicyValue(strings.TrimSpace(candidate), 128); value != "" {
			return value
		}
	}
	return common.NewRequestId()
}

func truncateAccessPolicyValue(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	return value[:max] + "…"
}
