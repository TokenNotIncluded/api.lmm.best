package humangrant

import (
	"net/http"
	"net/mail"
	"strings"
)

type mailOperation struct {
	ID              string
	RequestID       string
	DestinationHash string
	Created         int64
}
type localMail struct {
	OperationID string
	Destination string
	State       string
	Message     string
	Created     int64
	Expires     int64
}
type invite struct {
	RequestID       string
	DestinationHash string
	OperationID     string
}

func normalizeEmail(raw string) (string, bool) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if len(value) < 3 || len(value) > 254 || strings.ContainsAny(raw, "\r\n") {
		return "", false
	}
	for _, c := range value {
		if c < 33 || c > 126 {
			return "", false
		}
	}
	parsed, err := mail.ParseAddress(value)
	if err != nil || parsed.Name != "" || parsed.Address != value || !strings.Contains(value, "@") {
		return "", false
	}
	return value, true
}
func (t *transaction) email(req *Request, body []byte, source string) response {
	var input EmailRequest
	if !strictJSON(body, &input) || !safeID.MatchString(input.IdempotencyKey) {
		return denied(400, "invalid_request")
	}
	destination, ok := normalizeEmail(input.Email)
	if !ok {
		return denied(400, "invalid_request")
	}
	if terminal(req.Phase) || req.Phase == "approved" {
		return denied(409, "invalid_state")
	}
	destHash := fingerprint([]byte(destination))
	opKey := fingerprint([]byte(req.Actor + ":" + input.IdempotencyKey))
	var op mailOperation
	if t.get("mail-operation", opKey, &op) {
		if op.RequestID != req.ID || op.DestinationHash != destHash {
			return denied(409, "idempotency_conflict")
		}
		return acceptedMail(op.ID, max(int64(0), req.LastEmail+60-t.now))
	}
	// Bound accepted-operation bookkeeping separately from recipient/send policy.
	// These limits contain no recipient activity or suppression information.
	if !t.limits([]limit{{"mail-submit-actor:" + req.Actor, 3600, 100}, {"mail-submit-source:" + fingerprint([]byte(source)), 3600, 100}, {"mail-submit-service", 3600, 1000}}) {
		return jsonResponse(429, limited(60))
	}
	op = mailOperation{ID: t.random(), RequestID: req.ID, DestinationHash: destHash, Created: t.now}
	t.put("mail-operation", opKey, req.Actor, t.now+retention, op)
	var blocked bool
	var last int64
	t.get("mail-block", destHash, &blocked)
	t.get("mail-cooldown", destHash, &last)
	allowed := !blocked && (last == 0 || t.now-last >= 60) && (req.LastEmail == 0 || t.now-req.LastEmail >= 60)
	if allowed {
		allowed = t.limits([]limit{{"mail-destination-hour:" + destHash, 3600, 3}, {"mail-destination-day:" + destHash, 86400, 5}, {"mail-actor:" + req.Actor, 3600, 10}, {"mail-source:" + fingerprint([]byte(source)), 3600, 10}, {"mail-service-hour", 3600, 100}, {"mail-service-day", 86400, 500}})
	}
	if allowed {
		token := t.random()
		t.put("invite", fingerprint([]byte(token)), destHash, t.now+retention, invite{req.ID, destHash, op.ID})
		// Local transport is a private durable DB outbox. No SMTP connection, real
		// delivery claim, subject override, sender override or caller-supplied URL.
		message := "From: HumanGrant <no-reply@localhost.invalid>\r\nTo: " + destination + "\r\nSubject: HumanGrant authorization request\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nAn AI asks to act with your permission.\r\nReview: " + t.e.Origin + "/human/" + req.Link + "\r\nRefuse or block: " + t.e.Origin + "/invite/" + token + "\r\nOpening either link does not approve or reject access.\r\nDo not share account passwords, cookies or private keys.\r\n"
		t.put("outbox", op.ID, "local", req.Expires+86400, localMail{op.ID, destination, "local_spooled", message, t.now, req.Expires})
		t.put("mail-cooldown", destHash, "", t.now+86400, t.now)
	}
	req.LastEmail = t.now
	req.EmailStatus = "accepted"
	t.saveRequest(*req)
	t.audit("email.accepted", req.Actor, "", req.ID)
	return acceptedMail(op.ID, 60)
}
func acceptedMail(id string, retry int64) response {
	return jsonResponse(202, map[string]any{"operation_id": id, "email_request_status": "accepted", "email_retry_after_seconds": retry, "message": "The request was accepted. This does not confirm delivery."})
}
func (t *transaction) mailDecision(r *http.Request, body []byte) response {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/invite/"), "/")
	if len(parts) < 1 || len(parts) > 2 || !safeID.MatchString(parts[0]) {
		return denied(404, "not_found")
	}
	var invitation invite
	if !t.get("invite", fingerprint([]byte(parts[0])), &invitation) {
		return denied(404, "not_found")
	}
	context := "invite:" + invitation.OperationID
	if len(parts) == 1 && r.Method == "GET" {
		f, c := t.newFlow(context, Human{}, false)
		return t.page("invite", map[string]any{"token": parts[0], "csrf": f.CSRF}, c)
	}
	if len(parts) != 2 || r.Method != "POST" || (parts[1] != "reject" && parts[1] != "block") || !emptyJSON(body) {
		return denied(400, "invalid_request")
	}
	if _, ok := t.checkFlow(r, context); !ok {
		return denied(403, "csrf_failed")
	}
	if parts[1] == "block" {
		t.put("mail-block", invitation.DestinationHash, "", 0, true)
	}
	var req Request
	if t.get("request", invitation.RequestID, &req) {
		t.expire(&req)
		if !terminal(req.Phase) && req.Phase != "approved" {
			req.Phase = "rejected"
			req.Updated = t.now
			t.saveRequest(req)
			t.audit("email.refused", req.Actor, "", req.ID)
		}
	}
	return jsonResponse(200, map[string]string{"status": "recorded"})
}
