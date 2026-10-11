package humangrant

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type Engine struct {
	Store  Store
	Origin string
	Local  bool
	Now    func() time.Time
}
type transaction struct {
	tx       Tx
	e        *Engine
	now      int64
	err      error
	settings Settings
}

func (t *transaction) get(kind, id string, value any) bool {
	if t.err != nil {
		return false
	}
	ok, err := t.tx.Get(kind, id, value)
	t.err = err
	return ok
}
func (t *transaction) put(kind, id, owner string, expires int64, value any) {
	if t.err == nil {
		t.err = t.tx.Put(kind, id, owner, expires, value)
	}
}
func (t *transaction) remove(kind, id string) {
	if t.err == nil {
		t.err = t.tx.Delete(kind, id)
	}
}
func (t *transaction) random() string {
	var b [32]byte
	if t.err == nil {
		_, t.err = rand.Read(b[:])
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}
func (t *transaction) audit(action, actor, grantor, target string) string {
	id := t.random()
	t.put("audit", id, grantor, t.now+retention, map[string]any{"id": id, "at": t.now, "action": action, "actor": actor, "grantor_id": grantor, "target_id": target})
	return id
}
func (t *transaction) saveRequest(r Request) { t.put("request", r.ID, r.Actor, r.Created+retention, r) }
func (t *transaction) saveGrant(g Grant) {
	t.put("grant", g.ID, g.Human.ID, g.Expires+retention, saveGrant(g))
}
func (t *transaction) grant(id string) (Grant, bool) {
	var s storedGrant
	ok := t.get("grant", id, &s)
	return s.grant(), ok
}
func (t *transaction) current(h Human) (Account, bool) {
	if t.err != nil {
		return Account{}, false
	}
	a, ok, err := t.tx.CurrentHuman(h)
	t.err = err
	return a, ok
}
func (t *transaction) authenticate(r *http.Request) (Human, Account, bool) {
	if t.err != nil {
		return Human{}, Account{}, false
	}
	h, a, ok, err := t.tx.Authenticate(r)
	t.err = err
	return h, a, ok
}

func (e *Engine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	if !cleanPath(r) {
		writeResponse(w, denied(400, "invalid_request"))
		return
	}
	if e.Store == nil {
		writeResponse(w, denied(503, "unavailable"))
		return
	}
	// Read at most 16 KiB + one byte before decoding. The host also imposes
	// request deadlines and a header/body limit on these routes.
	var body []byte
	var err error
	if r.Body != nil {
		body, err = io.ReadAll(io.LimitReader(r.Body, BodyLimit+1))
	}
	if err != nil {
		writeResponse(w, denied(400, "invalid_request"))
		return
	}
	if len(body) > BodyLimit {
		writeResponse(w, denied(413, "body_too_large"))
		return
	}
	if r.Method == "GET" && len(body) != 0 {
		writeResponse(w, denied(400, "invalid_request"))
		return
	}
	result := denied(500, "internal_error")
	err = e.Store.Transaction(r.Context(), func(tx Tx) error {
		now := time.Now()
		if e.Now != nil {
			now = e.Now()
		}
		t := transaction{tx: tx, e: e, now: now.Unix()}
		if !t.get("settings", "site", &t.settings) {
			t.settings = Settings{Enabled: true, Epoch: 1}
			t.put("settings", "site", "", 0, t.settings)
		}
		result = t.dispatch(r, body)
		return t.err
	})
	if err != nil {
		result = denied(500, "internal_error")
	}
	writeResponse(w, result)
}
func writeResponse(w http.ResponseWriter, result response) {
	if result.status == 0 {
		result.status = 500
	}
	for _, cookie := range result.cookies {
		http.SetCookie(w, cookie)
	}
	if result.redirect != "" {
		w.Header().Set("Location", result.redirect)
	}
	if e, ok := result.value.(*ProtocolError); ok && e.Retry > 0 {
		w.Header().Set("Retry-After", strconv.FormatInt(e.Retry, 10))
	}
	if result.contentType != "" {
		w.Header().Set("Content-Type", result.contentType)
	} else {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
	}
	// Only same-origin static JavaScript is used. User text is never HTML/JS.
	if strings.HasPrefix(result.contentType, "text/html") {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	}
	w.WriteHeader(result.status)
	if result.body != nil {
		_, _ = w.Write(result.body)
	} else if result.value != nil {
		_ = json.NewEncoder(w).Encode(result.value)
	}
}
func (t *transaction) dispatch(r *http.Request, body []byte) response {
	p := r.URL.Path
	if p == "/humangrant/config" && r.Method == "GET" {
		return jsonResponse(200, map[string]any{"enabled": t.settings.Enabled, "ready": t.e.Origin != "", "client_id": ClientID, "target_service": ServiceID, "protocol_version": Version})
	}
	if t.e.Origin == "" {
		return denied(503, "unavailable")
	}
	source, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		source = r.RemoteAddr
	}
	if source == "" {
		source = "unknown"
	}
	if !t.limits([]limit{{"http-source:" + fingerprint([]byte(source)), 60, 180}, {"http-service", 60, 3000}}) {
		return jsonResponse(429, limited(60))
	}
	if p == "/humangrant/client.js" && r.Method == "GET" {
		return response{status: 200, contentType: "text/javascript; charset=utf-8", body: clientJS}
	}
	if p == "/humangrant/ui.js" && r.Method == "GET" {
		return response{status: 200, contentType: "text/javascript; charset=utf-8", body: uiJS}
	}
	if p == "/humangrant/style.css" && r.Method == "GET" {
		return response{status: 200, contentType: "text/css; charset=utf-8", body: styleCSS}
	}
	if p == BrowserPrefix+"/settings" {
		return t.adminSettings(r, body)
	}
	if p == "/human/grants" && r.Method == "GET" {
		return t.page("grants", map[string]any{}, nil)
	}
	if p == BrowserPrefix+"/grants" || strings.HasPrefix(p, BrowserPrefix+"/grants/") {
		return t.humanGrants(r, body)
	}
	if strings.HasPrefix(p, "/invite/") {
		return t.mailDecision(r, body)
	}
	if !t.settings.Enabled {
		return denied(403, "feature_disabled")
	}
	if p == "/humangrant" && r.Method == "GET" {
		return t.page("agent", map[string]any{"origin": t.e.Origin, "client_id": ClientID, "target_service": ServiceID, "action": ProfileRead}, nil)
	}
	if strings.HasPrefix(p, "/human/") && r.Method == "GET" {
		return t.review(r)
	}
	if strings.HasPrefix(p, BrowserPrefix+"/human/") {
		return t.humanDecision(r, body)
	}
	if !strings.HasPrefix(p, "/v1/") {
		return denied(404, "not_found")
	}
	proof, invalid := VerifySignature(r, body, t.e.Origin, t.now)
	if invalid != nil {
		return jsonResponse(invalid.Status, invalid)
	}
	nonceID := fingerprint([]byte(proof.Actor + ":" + proof.Nonce))
	var reserved bool
	if t.get("nonce", nonceID, &reserved) {
		return denied(401, "signature_replay")
	}
	t.put("nonce", nonceID, "", proof.Expires, true)
	if p == "/v1/session" && r.Method == "GET" {
		return t.session(proof)
	}
	if strings.HasPrefix(p, "/v1/accounts/") {
		return t.profile(r, proof)
	}
	if proof.Credential != "anonymous" {
		return denied(403, "forbidden")
	}
	if p == "/v1/requests" && r.Method == "POST" {
		return t.create(body, proof, source)
	}
	parts := strings.Split(strings.TrimPrefix(p, "/v1/requests/"), "/")
	if !strings.HasPrefix(p, "/v1/requests/") || len(parts) > 2 || !safeID.MatchString(parts[0]) {
		return denied(404, "not_found")
	}
	var req Request
	if !t.get("request", parts[0], &req) {
		return denied(404, "not_found")
	}
	if req.Actor != proof.Actor || req.PublicKey != proof.PublicKey {
		return denied(403, "forbidden")
	}
	t.expire(&req)
	if r.Method == "GET" && (len(parts) == 1 || parts[1] == "status.md") {
		if req.LastPoll != 0 && t.now-req.LastPoll < req.PollAfter {
			req.PollAfter = min(int64(60), req.PollAfter+5)
			t.saveRequest(req)
			return jsonResponse(429, limited(req.PollAfter))
		}
		req.LastPoll = t.now
		t.saveRequest(req)
		state := t.state(req)
		if len(parts) == 2 {
			return response{status: 200, contentType: "text/markdown; charset=utf-8", body: stateMarkdown(state)}
		}
		return jsonResponse(200, state)
	}
	if len(parts) != 2 || r.Method != "POST" {
		return denied(404, "not_found")
	}
	switch parts[1] {
	case "claim":
		if !emptyJSON(body) {
			return denied(400, "invalid_request")
		}
		return t.claim(&req)
	case "cancel":
		if !emptyJSON(body) {
			return denied(400, "invalid_request")
		}
		if terminal(req.Phase) {
			return denied(409, "invalid_state")
		}
		req.Phase = "cancelled"
		req.Updated = t.now
		t.saveRequest(req)
		if g, ok := t.grant(req.GrantID); ok {
			g.Revoked = t.now
			t.saveGrant(g)
		}
		t.audit("cancel", req.Actor, "", req.ID)
		return jsonResponse(200, map[string]string{"status": "cancelled"})
	case "email":
		return t.email(&req, body, source)
	}
	return denied(404, "not_found")
}
func terminal(phase string) bool {
	return phase == "succeeded" || phase == "rejected" || phase == "cancelled" || phase == "expired"
}
func (t *transaction) expire(r *Request) {
	if terminal(r.Phase) {
		return
	}
	phase := ""
	if r.Epoch != t.settings.Epoch {
		phase = "cancelled"
	} else if r.Expires <= t.now {
		phase = "expired"
	}
	if phase != "" {
		r.Phase = phase
		r.Updated = t.now
		t.saveRequest(*r)
		if g, ok := t.grant(r.GrantID); ok {
			g.Revoked = t.now
			t.saveGrant(g)
		}
	}
}
func (t *transaction) create(body []byte, proof Proof, source string) response {
	var input CreateRequest
	if !strictJSON(body, &input) || input.ClientID != ClientID || input.TargetService != ServiceID || !validPairs(input.Permissions) || utf8.RuneCountInString(input.Purpose) < 1 || utf8.RuneCountInString(input.Purpose) > 240 {
		return denied(400, "invalid_request")
	}
	for _, c := range input.Purpose {
		if unicode.IsControl(c) {
			return denied(400, "invalid_request")
		}
	}
	if input.ExpectedHuman != nil && !accountID.MatchString(*input.ExpectedHuman) {
		return denied(400, "invalid_request")
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(body, &fields)
	if raw, exists := fields["session_ttl_seconds"]; exists {
		if string(raw) == "null" || input.SessionTTL < 60 {
			return denied(400, "invalid_request")
		}
	} else {
		input.SessionTTL = 3600
	}
	if input.SessionTTL < 60 || input.SessionTTL > 86400 {
		return denied(400, "invalid_request")
	}
	if !t.limits([]limit{{"create-service", 3600, 100}, {"create-actor:" + proof.Actor, 3600, 10}, {"create-source:" + fingerprint([]byte(source)), 3600, 10}}) {
		return jsonResponse(429, limited(60))
	}
	req := Request{ID: t.random(), Link: t.random(), Actor: proof.Actor, PublicKey: proof.PublicKey, Input: input, Epoch: t.settings.Epoch, Created: t.now, Updated: t.now, Expires: t.now + 600, Phase: "waiting_human", PollAfter: 5, EmailStatus: "not_requested"}
	t.saveRequest(req)
	t.put("human-link", req.Link, "", req.Created+retention, req.ID)
	t.audit("request", req.Actor, "", req.ID)
	return jsonResponse(201, t.state(req))
}
func (t *transaction) state(req Request) State {
	status, text := "human_login", "人类登录中"
	switch req.Phase {
	case "waiting_human":
		status, text = "waiting", "等待人类中"
	case "succeeded":
		status, text = "success", "登录成功"
	case "rejected":
		status, text = "rejected", "已拒绝"
	case "cancelled":
		status, text = "cancelled", "已取消"
	case "expired":
		status, text = "expired", "已过期"
	}
	s := State{ProtocolVersion: Version, RequestID: req.ID, Status: status, StatusText: text, Phase: req.Phase, Created: req.Created, Updated: req.Updated, Checked: t.now, Expires: req.Expires, PollAfter: req.PollAfter, HumanURL: t.e.Origin + "/human/" + req.Link, Permissions: req.Input.Permissions, EmailStatus: req.EmailStatus, EmailRetry: max(int64(0), req.LastEmail+60-t.now), Service: req.Input.TargetService, Client: req.Input.ClientID, ForwardingPrompt: "Open " + t.e.Origin + "/human/" + req.Link + " to verify your account and choose the access to approve. This link is for the human, not for the AI to claim a credential.", GrantStatus: "none"}
	if req.LastEmail == 0 {
		s.EmailRetry = 0
	}
	if g, ok := t.grant(req.GrantID); ok {
		s.GrantID = &g.ID
		s.GrantExpires = &g.Expires
		s.GrantStatus = t.grantStatus(g)
	}
	return s
}
func stateMarkdown(s State) []byte {
	encoded, _ := json.Marshal(s)
	var values map[string]json.RawMessage
	_ = json.Unmarshal(encoded, &values)
	fields := []string{"protocol_version", "request_id", "status", "status_text", "phase", "created_at", "status_updated_at", "checked_at", "expires_at", "poll_after_seconds", "human_authorization_url", "requested_permissions", "email_request_status", "email_retry_after_seconds", "target_service", "client_id", "forwarding_prompt", "grant_id", "grant_status", "grant_expires_at"}
	var out strings.Builder
	out.WriteString("# HumanGrant\n\n")
	for _, field := range fields {
		out.WriteString(field + ": " + string(values[field]) + "\n")
	}
	return []byte(out.String())
}
func (t *transaction) grantStatus(g Grant) string {
	if g.Revoked != 0 || g.Epoch != t.settings.Epoch || !t.settings.Enabled {
		return "revoked"
	}
	if g.Expires <= t.now {
		return "expired"
	}
	if _, ok := t.current(g.Human); !ok {
		return "account_disabled"
	}
	if len(t.effective(g)) == 0 {
		return "permissions_removed"
	}
	if g.TokenHash == "" {
		return "pending_claim"
	}
	return "active"
}
func (t *transaction) effective(g Grant) []Permission {
	pairs := []Permission{}
	if g.Client != ClientID || g.Service != ServiceID {
		return pairs
	}
	for _, p := range g.Approved {
		if p.Action == ProfileRead && p.Resource == g.Human.ID && includes(g.Requested, p) {
			pairs = append(pairs, p)
		}
	}
	return pairs
}
func (t *transaction) claim(req *Request) response {
	if req.Phase == "succeeded" {
		return denied(409, "credential_already_issued")
	}
	if req.Phase == "expired" {
		return denied(410, "expired")
	}
	if req.Phase != "approved" {
		return denied(409, "invalid_state")
	}
	g, ok := t.grant(req.GrantID)
	if !ok || t.grantStatus(g) != "pending_claim" {
		return denied(403, "forbidden")
	}
	token := t.random()
	g.TokenHash = fingerprint([]byte(token))
	t.saveGrant(g)
	t.put("session", g.TokenHash, g.Human.ID, g.Expires, g.ID)
	req.Phase = "succeeded"
	req.Updated = t.now
	t.saveRequest(*req)
	t.audit("claim", g.Actor, g.Human.ID, g.ID)
	return jsonResponse(200, map[string]any{"access_token": token, "token_type": "HumanGrant", "grant_id": g.ID, "expires_at": g.Expires})
}
func (t *transaction) resolve(p Proof) (Grant, bool) {
	if p.Credential == "anonymous" {
		return Grant{}, false
	}
	hash := fingerprint([]byte(p.Credential))
	var id string
	if !t.get("session", hash, &id) {
		return Grant{}, false
	}
	g, ok := t.grant(id)
	if !ok || g.Actor != p.Actor || g.PublicKey != p.PublicKey || subtle.ConstantTimeCompare([]byte(g.TokenHash), []byte(hash)) != 1 || t.grantStatus(g) != "active" {
		return Grant{}, false
	}
	return g, true
}
func (t *transaction) session(p Proof) response {
	g, ok := t.resolve(p)
	if !ok {
		return denied(401, "invalid_session")
	}
	return jsonResponse(200, map[string]any{"actor": map[string]string{"kind": "ai", "id": g.Actor}, "grantor_id": g.Human.ID, "grant_id": g.ID, "client_id": g.Client, "target_service": g.Service, "expires_at": g.Expires, "audit_id": g.AuditID, "effective_permissions": t.effective(g)})
}
func (t *transaction) profile(r *http.Request, p Proof) response {
	g, ok := t.resolve(p)
	if !ok {
		return denied(401, "invalid_session")
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/accounts/"), "/")
	if r.Method != "GET" || len(parts) != 2 || parts[1] != "profile" || !includes(t.effective(g), Permission{ProfileRead, parts[0]}) {
		return denied(403, "forbidden")
	}
	account, ok := t.current(g.Human)
	if !ok {
		return denied(403, "forbidden")
	}
	t.audit("account.profile.read", g.Actor, g.Human.ID, g.ID)
	return jsonResponse(200, map[string]any{"actor": map[string]string{"kind": "ai", "id": g.Actor}, "grantor_id": g.Human.ID, "account": account})
}

type limit struct {
	key    string
	window int64
	max    int64
}
type counter struct{ Count int64 }

func (t *transaction) limits(limits []limit) bool {
	ids := make([]string, len(limits))
	counts := make([]counter, len(limits))
	for i, l := range limits {
		ids[i] = fingerprint([]byte(l.key + ":" + strconv.FormatInt(t.now/l.window, 10)))
		t.get("limit", ids[i], &counts[i])
		if counts[i].Count >= l.max {
			return false
		}
	}
	for i, l := range limits {
		counts[i].Count++
		t.put("limit", ids[i], "", (t.now/l.window+1)*l.window+1, counts[i])
	}
	return t.err == nil
}
func sameHuman(a, b Human) bool { return a == b && a.ID != "" }
func (t *transaction) cookie(value string, age int) *http.Cookie {
	name := "__Host-lmm-hg"
	secure := true
	if t.e.Local && strings.HasPrefix(t.e.Origin, "http:") {
		name = "lmm-hg-local"
		secure = false
	}
	return &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: age, HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode}
}
func (t *transaction) cookieValue(r *http.Request) string {
	name := t.cookie("", 0).Name
	value := ""
	count := 0
	for _, c := range r.Cookies() {
		if c.Name == name {
			value = c.Value
			count++
		}
	}
	if count != 1 {
		return ""
	}
	if _, ok := decodeCanonical(value, 32, base64.RawURLEncoding); !ok {
		return ""
	}
	return value
}
func (t *transaction) newFlow(context string, h Human, verified bool) (Flow, *http.Cookie) {
	key := t.random()
	f := Flow{CSRF: t.random(), Context: context, Human: h, Verified: verified, Expires: t.now + 600}
	t.put("flow", fingerprint([]byte(key)), h.ID, f.Expires, f)
	return f, t.cookie(key, 600)
}
func (t *transaction) checkFlow(r *http.Request, context string) (Flow, bool) {
	var f Flow
	origin, ok := oneHeader(r.Header, "Origin")
	csrf, one := oneHeader(r.Header, "X-HG-CSRF")
	key := t.cookieValue(r)
	if !ok || !one || origin != t.e.Origin || key == "" || !t.get("flow", fingerprint([]byte(key)), &f) || f.Context != context || f.Expires <= t.now || subtle.ConstantTimeCompare([]byte(csrf), []byte(f.CSRF)) != 1 {
		return Flow{}, false
	}
	return f, true
}
func (t *transaction) requestForLink(link string) (Request, bool) {
	var id string
	var req Request
	if !safeID.MatchString(link) || !t.get("human-link", link, &id) || !t.get("request", id, &req) {
		return req, false
	}
	return req, true
}
func (t *transaction) review(r *http.Request) response {
	link := strings.TrimPrefix(r.URL.Path, "/human/")
	req, ok := t.requestForLink(link)
	if !ok {
		return denied(404, "not_found")
	}
	// GET does not advance phase, authenticate, approve, reject or expire a request.
	f, cookie := t.newFlow(req.ID, Human{}, false)
	return t.page("human", map[string]any{"link": link, "request_id": req.ID, "actor": req.Actor, "public_key": req.PublicKey, "service": req.Input.TargetService, "client": req.Input.ClientID, "purpose": req.Input.Purpose, "permissions": req.Input.Permissions, "expected_human_id": req.Input.ExpectedHuman, "expires_at": req.Expires, "max_grant_expires_at": t.now + req.Input.SessionTTL, "phase": req.Phase, "csrf": f.CSRF}, cookie)
}
func (t *transaction) humanDecision(r *http.Request, body []byte) response {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, BrowserPrefix+"/human/"), "/")
	if len(parts) != 2 || r.Method != "POST" {
		return denied(404, "not_found")
	}
	req, ok := t.requestForLink(parts[0])
	if !ok {
		return denied(404, "not_found")
	}
	t.expire(&req)
	f, ok := t.checkFlow(r, req.ID)
	if !ok {
		return denied(403, "csrf_failed")
	}
	if terminal(req.Phase) || req.Phase == "approved" {
		return denied(409, "invalid_state")
	}
	switch parts[1] {
	case "begin":
		if !emptyJSON(body) {
			return denied(400, "invalid_request")
		}
		if req.Phase == "waiting_human" {
			req.Phase = "authenticating_human"
			req.Updated = t.now
			t.saveRequest(req)
		}
		return jsonResponse(200, map[string]string{"status": "authenticating_human"})
	case "verify":
		if !emptyJSON(body) {
			return denied(400, "invalid_request")
		}
		h, account, ok := t.authenticate(r)
		if !ok {
			return denied(401, "human_login_required")
		}
		if req.Input.ExpectedHuman != nil && *req.Input.ExpectedHuman != h.ID {
			return denied(403, "expected_human_mismatch")
		}
		if req.Phase == "waiting_human" {
			return denied(409, "invalid_state")
		}
		t.remove("flow", fingerprint([]byte(t.cookieValue(r))))
		rotated, cookie := t.newFlow(req.ID, h, true)
		if req.Phase != "awaiting_consent" {
			req.Phase = "awaiting_consent"
			req.Updated = t.now
			t.saveRequest(req)
		}
		result := jsonResponse(200, map[string]any{"status": "awaiting_consent", "human_id": h.ID, "account": account, "csrf": rotated.CSRF})
		result.cookies = []*http.Cookie{cookie}
		return result
	case "approve", "reject":
		if !f.Verified || req.Phase != "awaiting_consent" {
			return denied(403, "forbidden")
		}
		h, _, ok := t.authenticate(r)
		if !ok || !sameHuman(h, f.Human) {
			return denied(403, "forbidden")
		}
		if req.Input.ExpectedHuman != nil && *req.Input.ExpectedHuman != h.ID {
			return denied(403, "expected_human_mismatch")
		}
		if parts[1] == "reject" {
			if !emptyJSON(body) {
				return denied(400, "invalid_request")
			}
			req.Phase = "rejected"
			req.Updated = t.now
			t.saveRequest(req)
			t.audit("reject", req.Actor, h.ID, req.ID)
			return jsonResponse(200, map[string]string{"status": "rejected"})
		}
		var consent Approval
		if !strictJSON(body, &consent) || !validPairs(consent.Permissions) || consent.ExpiresAt <= t.now || consent.ExpiresAt > t.now+req.Input.SessionTTL {
			return denied(400, "invalid_request")
		}
		for _, pair := range consent.Permissions {
			if pair.Resource != h.ID || !includes(req.Input.Permissions, pair) {
				return denied(403, "forbidden")
			}
		}
		if _, ok := t.current(h); !ok {
			return denied(403, "forbidden")
		}
		g := Grant{ID: t.random(), RequestID: req.ID, Human: h, Actor: req.Actor, PublicKey: req.PublicKey, Client: req.Input.ClientID, Service: req.Input.TargetService, Requested: req.Input.Permissions, Approved: consent.Permissions, Created: t.now, Expires: consent.ExpiresAt, Epoch: req.Epoch}
		g.AuditID = t.audit("approve", g.Actor, h.ID, g.ID)
		t.saveGrant(g)
		req.Phase = "approved"
		req.Updated = t.now
		req.GrantID = g.ID
		t.saveRequest(req)
		t.remove("flow", fingerprint([]byte(t.cookieValue(r))))
		return jsonResponse(200, map[string]any{"status": "approved", "grant_id": g.ID, "expires_at": g.Expires})
	}
	return denied(404, "not_found")
}
func (t *transaction) adminSettings(r *http.Request, body []byte) response {
	h, account, ok := t.authenticate(r)
	if !ok || !account.Root {
		return denied(403, "forbidden")
	}
	if r.Method == "GET" {
		f, c := t.newFlow("settings", h, true)
		result := jsonResponse(200, map[string]any{"enabled": t.settings.Enabled, "csrf": f.CSRF})
		result.cookies = []*http.Cookie{c}
		return result
	}
	if r.Method != "PUT" {
		return denied(405, "invalid_request")
	}
	f, ok := t.checkFlow(r, "settings")
	if !ok || !sameHuman(f.Human, h) {
		return denied(403, "csrf_failed")
	}
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if !strictJSON(body, &input) || input.Enabled == nil {
		return denied(400, "invalid_request")
	}
	if t.settings.Enabled != *input.Enabled {
		t.settings.Enabled = *input.Enabled
		t.settings.Epoch++
		t.put("settings", "site", "", 0, t.settings)
		t.audit("settings", h.ID, h.ID, "site")
	}
	return jsonResponse(200, map[string]any{"enabled": t.settings.Enabled})
}
func (t *transaction) humanGrants(r *http.Request, body []byte) response {
	h, _, ok := t.authenticate(r)
	if !ok {
		return denied(401, "human_login_required")
	}
	if r.URL.Path == BrowserPrefix+"/grants" && r.Method == "GET" {
		rows, err := t.tx.List("grant", h.ID, 200)
		if err != nil {
			t.err = err
			return denied(500, "internal_error")
		}
		list := []map[string]any{}
		for _, raw := range rows {
			var s storedGrant
			if err := json.Unmarshal(raw, &s); err != nil {
				t.err = err
				return denied(500, "internal_error")
			}
			g := s.grant()
			list = append(list, map[string]any{"grant_id": g.ID, "request_id": g.RequestID, "grantor_id": g.Human.ID, "actor_id": g.Actor, "public_key_id": g.Actor, "client_id": g.Client, "target_service": g.Service, "permissions": g.Approved, "created_at": g.Created, "expires_at": g.Expires, "revoked_at": g.Revoked, "status": t.grantStatus(g), "audit_id": g.AuditID})
		}
		f, c := t.newFlow("grants", h, true)
		result := jsonResponse(200, map[string]any{"grants": list, "csrf": f.CSRF, "truncated": len(rows) == 200})
		result.cookies = []*http.Cookie{c}
		return result
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, BrowserPrefix+"/grants/"), "/")
	if len(parts) != 2 || parts[1] != "revoke" || !safeID.MatchString(parts[0]) || r.Method != "POST" || !emptyJSON(body) {
		return denied(400, "invalid_request")
	}
	f, ok := t.checkFlow(r, "grants")
	if !ok || !sameHuman(h, f.Human) {
		return denied(403, "csrf_failed")
	}
	g, ok := t.grant(parts[0])
	if !ok || g.Human.ID != h.ID {
		return denied(403, "forbidden")
	}
	if g.Revoked == 0 {
		g.Revoked = t.now
		t.saveGrant(g)
		t.audit("revoke", g.Actor, h.ID, g.ID)
	}
	return jsonResponse(200, map[string]string{"status": "revoked"})
}
