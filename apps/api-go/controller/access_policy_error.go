package controller

import (
	"fmt"
	"html/template"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

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
// reflects validated, length-limited diagnostic values into the response.
func GetAccessPolicyErrorPage(c *gin.Context) {
	if !loopbackPeer(c.Request.RemoteAddr) ||
		strings.TrimSpace(c.GetHeader("X-LMM-Internal-Error")) != accessPolicyErrorHeader ||
		strings.TrimSpace(c.GetHeader(accessPolicyResultHeader)) != accessPolicyDenied {
		c.Status(http.StatusNotFound)
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
	page := accessPolicyErrorPage(language, accessPolicyErrorDiagnostics(c, requestID))
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
	originalURI := strings.TrimSpace(c.GetHeader(accessPolicyOriginalURIHeader))
	if queryStart := strings.IndexByte(originalURI, '?'); queryStart >= 0 {
		originalURI = originalURI[:queryStart]
	}
	if accessPolicyAPIPath(originalURI) {
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

type accessPolicyErrorDetails struct {
	ClientIP       string
	IPVersion      string
	EdgeCountry    string
	PolicyDecision string
	RequestID      string
	CheckedAt      string
	Browser        string
	Host           string
	Reachability   string
	DiagnosticText string
}

func accessPolicyErrorPage(language string, diagnostics accessPolicyErrorDetails) string {
	const pageTemplate = `<!doctype html>
<html lang="{{.Language}}">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <title>{{.Title}}</title>
  <style>
    :root{color-scheme:dark;--bg:#111311;--panel:#1d211e;--line:#3b433d;--text:#f0f2eb;--muted:#b7beb6;--accent:#a8d5b5;--soft:#a8d5b51c}
    *{box-sizing:border-box}body{margin:0;min-height:100vh;display:grid;place-items:center;background:var(--bg);color:var(--text);font:16px/1.6 system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;padding:24px}
    main{width:min(760px,100%);padding:38px;border:1px solid var(--line);border-radius:18px;background:linear-gradient(145deg,var(--panel),#161916);box-shadow:0 20px 70px #0008}
    .mark{width:12px;height:12px;border-radius:50%;background:var(--accent);margin-bottom:22px;box-shadow:0 0 0 8px var(--soft)}
    h1{font-size:clamp(24px,5vw,36px);line-height:1.2;margin:0 0 16px}h2{font-size:20px;line-height:1.3;margin:0 0 8px}p{margin:0 0 12px;color:var(--muted)}small{display:block;margin-top:24px;color:#8f9990}
    section{margin-top:28px;padding-top:24px;border-top:1px solid var(--line)}
    .diagnostic-intro{margin-bottom:16px}
    dl{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:10px;margin:0}
    .item{min-width:0;padding:12px 14px;border:1px solid #3b433d99;border-radius:10px;background:#11131180}
    dt{font-size:12px;color:#8f9990;margin-bottom:3px}dd{margin:0;overflow-wrap:anywhere;color:var(--text)}code{font:13px/1.5 ui-monospace,SFMono-Regular,Menlo,monospace;color:var(--accent)}
    textarea{display:block;width:100%;min-height:150px;margin-top:8px;padding:12px;border:1px solid var(--line);border-radius:10px;background:#111311;color:var(--text);font:12px/1.5 ui-monospace,SFMono-Regular,Menlo,monospace;resize:vertical}
    .copy-hint{font-size:13px;margin-top:10px}.privacy{font-size:12px;margin-top:10px}.status{color:var(--accent)}
    @media (max-width:560px){main{padding:26px 20px}dl{grid-template-columns:1fr}}
  </style>
</head>
<body><main>
  <div class="mark" aria-hidden="true"></div>
  <h1>{{.Title}}</h1>
  <p>{{.Message}}</p>
  <p>{{.Hint}}</p>
  <section aria-labelledby="why-title">
    <h2 id="why-title">{{.WhyTitle}}</h2>
    <p>{{.WhyDescription}}</p>
  </section>
  <section aria-labelledby="troubleshooting-title">
    <h2 id="troubleshooting-title">{{.TroubleshootingTitle}}</h2>
    <p class="diagnostic-intro">{{.TroubleshootingDescription}}</p>
    <dl>
      <div class="item"><dt>{{.ClientIPLabel}}</dt><dd><code>{{.Diagnostics.ClientIP}}</code></dd></div>
      <div class="item"><dt>{{.IPVersionLabel}}</dt><dd>{{.Diagnostics.IPVersion}}</dd></div>
      <div class="item"><dt>{{.EdgeCountryLabel}}</dt><dd>{{.Diagnostics.EdgeCountry}}</dd></div>
      <div class="item"><dt>{{.PolicyDecisionLabel}}</dt><dd>{{.Diagnostics.PolicyDecision}}</dd></div>
      <div class="item"><dt>{{.ReachabilityLabel}}</dt><dd class="status">{{.Diagnostics.Reachability}}</dd></div>
      <div class="item"><dt>{{.HostLabel}}</dt><dd><code>{{.Diagnostics.Host}}</code></dd></div>
      <div class="item"><dt>{{.RequestIDLabel}}</dt><dd><code>{{.Diagnostics.RequestID}}</code></dd></div>
      <div class="item"><dt>{{.CheckedAtLabel}}</dt><dd>{{.Diagnostics.CheckedAt}}</dd></div>
      <div class="item"><dt>{{.BrowserLabel}}</dt><dd>{{.Diagnostics.Browser}}</dd></div>
    </dl>
    <p class="copy-hint">{{.CopyHint}}</p>
    <textarea aria-label="{{.DiagnosticTextLabel}}" readonly spellcheck="false">{{.Diagnostics.DiagnosticText}}</textarea>
    <p class="privacy">{{.PrivacyNote}}</p>
  </section>
  <small>{{.Footer}}</small>
</main></body>
</html>`
	templateData := struct {
		Language                   string
		Title                      string
		Message                    string
		Hint                       string
		WhyTitle                   string
		WhyDescription             string
		TroubleshootingTitle       string
		TroubleshootingDescription string
		ClientIPLabel              string
		IPVersionLabel             string
		EdgeCountryLabel           string
		PolicyDecisionLabel        string
		ReachabilityLabel          string
		HostLabel                  string
		RequestIDLabel             string
		CheckedAtLabel             string
		BrowserLabel               string
		DiagnosticTextLabel        string
		CopyHint                   string
		PrivacyNote                string
		Footer                     string
		Diagnostics                accessPolicyErrorDetails
	}{Language: language, Diagnostics: diagnostics}
	if language == "en" {
		templateData.Title = "This network request was rejected"
		templateData.Message = "The edge IP access router matched a reject rule and stopped this request before it reached the application."
		templateData.Hint = "Try a permitted network path, or send the diagnostic details below to support if you believe the route decision is incorrect."
		templateData.WhyTitle = "Why did I see this page?"
		templateData.WhyDescription = "DNS and HTTPS reached the lmm.best edge successfully. An ordered IP or region route rule then returned reject."
		templateData.TroubleshootingTitle = "Troubleshooting"
		templateData.TroubleshootingDescription = "These details help identify the client IP, edge country, and route decision that caused the rejection."
		templateData.ClientIPLabel = "Detected client IP"
		templateData.IPVersionLabel = "IP version"
		templateData.EdgeCountryLabel = "Edge region"
		templateData.PolicyDecisionLabel = "Policy decision"
		templateData.ReachabilityLabel = "Connectivity"
		templateData.HostLabel = "Host"
		templateData.RequestIDLabel = "Request ID"
		templateData.CheckedAtLabel = "Checked at"
		templateData.BrowserLabel = "Browser / OS"
		templateData.DiagnosticTextLabel = "Diagnostic details"
		templateData.CopyHint = "Send the following diagnostic details to support. Do not include cookies or access tokens."
		templateData.PrivacyNote = "Cookies, Authorization headers, and the full proxy chain are not shown. The client IP is displayed only so you can verify the network you are using."
		templateData.Footer = "IP access routing · lmm.best"
	} else {
		templateData.Title = "当前网络请求已被拒绝"
		templateData.Message = "边缘 IP 访问路由命中了 reject 规则，请求已在到达应用前被拦截。"
		templateData.Hint = "请更换允许访问的网络路径；如果你认为路由判定有误，请将下方诊断信息提供给客服。"
		templateData.WhyTitle = "为什么会看到这个页面？"
		templateData.WhyDescription = "域名解析和 HTTPS 已成功到达 lmm.best 边缘节点；随后有一条按顺序匹配的 IP 或地区路由规则返回了 reject。"
		templateData.TroubleshootingTitle = "疑难解答"
		templateData.TroubleshootingDescription = "以下信息可帮助确认导致拒绝的出口 IP、边缘地区和路由判定。"
		templateData.ClientIPLabel = "检测到的出口 IP"
		templateData.IPVersionLabel = "IP 类型"
		templateData.EdgeCountryLabel = "边缘地区判定"
		templateData.PolicyDecisionLabel = "策略判定"
		templateData.ReachabilityLabel = "连接状态"
		templateData.HostLabel = "访问域名"
		templateData.RequestIDLabel = "请求编号"
		templateData.CheckedAtLabel = "检测时间"
		templateData.BrowserLabel = "浏览器 / 系统"
		templateData.DiagnosticTextLabel = "诊断信息"
		templateData.CopyHint = "请将以下诊断信息提供给客服；不要附带 Cookie 或访问令牌。"
		templateData.PrivacyNote = "页面不会显示 Cookie、Authorization 或完整代理链；出口 IP 仅用于你核对当前使用的网络。"
		templateData.Footer = "IP 访问路由 · lmm.best"
	}
	template := template.Must(template.New("access-policy-error").Parse(pageTemplate))
	var rendered strings.Builder
	if err := template.Execute(&rendered, templateData); err != nil {
		// The template is a package constant; keep a safe response even if a
		// future edit accidentally makes it invalid.
		return "<!doctype html><meta charset=\"utf-8\"><title>Access unavailable</title>"
	}
	return rendered.String()
}

func accessPolicyErrorDiagnostics(c *gin.Context, requestID string) accessPolicyErrorDetails {
	clientIP := accessPolicyClientIP(c)
	ipVersion := "unknown"
	if parsed := net.ParseIP(clientIP); parsed != nil {
		if parsed.To4() != nil {
			ipVersion = "IPv4"
		} else {
			ipVersion = "IPv6"
		}
	}

	edgeCountry := strings.ToUpper(strings.TrimSpace(c.GetHeader("X-LMM-Edge-Country")))
	if edgeCountry == "" && strings.TrimSpace(c.GetHeader("X-LMM-CN-Source")) == "1" {
		edgeCountry = "CN"
	}
	if edgeCountry == "" {
		edgeCountry = "unknown"
	}

	host := strings.TrimSpace(c.Request.Host)
	if host == "" {
		host = "unknown"
	}
	browser := truncateAccessPolicyValue(strings.TrimSpace(c.GetHeader("User-Agent")), 180)
	if browser == "" {
		browser = "unknown"
	}
	checkedAt := time.Now().UTC().Format(time.RFC3339)
	policyDecision := "route_reject"
	reachability := "edge_reached"
	diagnostics := accessPolicyErrorDetails{
		ClientIP:       clientIP,
		IPVersion:      ipVersion,
		EdgeCountry:    edgeCountry,
		PolicyDecision: policyDecision,
		RequestID:      requestID,
		CheckedAt:      checkedAt,
		Browser:        browser,
		Host:           truncateAccessPolicyValue(host, 120),
		Reachability:   reachability,
	}
	diagnostics.DiagnosticText = fmt.Sprintf(
		"site=%s\nclient_ip=%s\nip_version=%s\nedge_country=%s\npolicy_decision=%s\nconnectivity=%s\nrequest_id=%s\nchecked_at=%s\nbrowser=%s",
		diagnostics.Host,
		diagnostics.ClientIP,
		diagnostics.IPVersion,
		diagnostics.EdgeCountry,
		diagnostics.PolicyDecision,
		diagnostics.Reachability,
		diagnostics.RequestID,
		diagnostics.CheckedAt,
		diagnostics.Browser,
	)
	return diagnostics
}

func accessPolicyClientIP(c *gin.Context) string {
	for _, candidate := range []string{
		c.GetHeader("X-Original-Client-IP"),
		c.GetHeader("X-Real-IP"),
		c.ClientIP(),
	} {
		if ip := parseAccessPolicyIP(candidate); ip != "" {
			return ip
		}
	}
	return "unknown"
}

func parseAccessPolicyIP(value string) string {
	value = strings.TrimSpace(value)
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	value = strings.Trim(value, "[]")
	parsed := net.ParseIP(value)
	if parsed == nil {
		return ""
	}
	return parsed.String()
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
