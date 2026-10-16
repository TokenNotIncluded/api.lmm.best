// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package forum

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules"
)

func request(handler http.Handler, method, path, body, key string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("X-LMM-User-Credential", testSession)
	r.Header.Set("Content-Type", "application/json")
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}
func statusIs(t *testing.T, w *httptest.ResponseRecorder, status int) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status=%d want=%d body=%s", w.Code, status, w.Body.String())
	}
}

// @mark forum.http role=test
func TestForumHTTPLifecycle(t *testing.T) {
	s, m, a, b, topic := fixture(t)
	h := s.Handler()
	topicPath := fmt.Sprintf("/topics/%d", topic.ID)
	w := request(h, "GET", "/boards?limit=1", "", "")
	statusIs(t, w, 200)
	if !strings.Contains(w.Body.String(), `"name":"General"`) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Body.String())
	}
	w = request(h, "GET", fmt.Sprintf("/boards/%d/topics", b.ID), "", "")
	statusIs(t, w, 200)
	w = request(h, "GET", topicPath, "", "")
	statusIs(t, w, 200)
	if strings.Contains(w.Body.String(), "<script>") || !strings.Contains(w.Body.String(), `\u003cscript\u003e`) {
		t.Fatal("text was not JSON escaped")
	}
	w = request(h, "POST", fmt.Sprintf("/boards/%d/topics", b.ID), `{"title":"New","body":"Text"}`, "http-topic")
	statusIs(t, w, 201)
	w = request(h, "POST", topicPath+"/replies", `{"body":"Reply plain text"}`, "http-reply")
	statusIs(t, w, 201)
	var reply Reply
	if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil || reply.AuthorID != 11 {
		t.Fatalf("%+v %v", reply, err)
	}
	w = request(h, "POST", topicPath+"/replies", `{"body":"Reply plain text"}`, "http-reply")
	statusIs(t, w, 201)
	if len(m.replies) != 1 {
		t.Fatal("duplicate")
	}
	w = request(h, "POST", topicPath+"/replies", `{"body":"Different"}`, "http-reply")
	statusIs(t, w, 409)
	a.allow(11, Resource{Kind: ReplyResource, ID: reply.ID, BoardID: b.ID, TopicID: topic.ID}, HideReply)
	w = request(h, "POST", fmt.Sprintf("/replies/%d/hide", reply.ID), `{"expected_version":1,"reason":"hide"}`, "")
	statusIs(t, w, 200)
	w = request(h, "GET", topicPath+"/replies?limit=1", "", "")
	statusIs(t, w, 200)
	if strings.Contains(w.Body.String(), "Reply plain text") || w.Body.String() != "{\"items\":[],\"next_after_id\":0}\n" {
		t.Fatal(w.Body.String())
	}
	w = request(h, "POST", topicPath+"/pin", `{"expected_version":1,"reason":"pin","value":true}`, "")
	statusIs(t, w, 200)
	w = request(h, "POST", topicPath+"/lock", `{"expected_version":1,"reason":"stale","value":true}`, "")
	statusIs(t, w, 409)
	w = request(h, "POST", topicPath+"/lock", `{"expected_version":2,"reason":"lock","value":true}`, "")
	statusIs(t, w, 200)
	w = request(h, "POST", topicPath+"/replies", `{"body":"Late"}`, "late")
	statusIs(t, w, 409)
	w = request(h, "POST", topicPath+"/hide", `{"expected_version":3,"reason":"hide"}`, "")
	statusIs(t, w, 200)
	for _, path := range []string{topicPath, topicPath + "/replies"} {
		w = request(h, "GET", path, "", "")
		statusIs(t, w, 404)
		if strings.Contains(w.Body.String(), "Plain") {
			t.Fatal("hidden text leak")
		}
	}
}

// @mark forum.http role=test
func TestForumHTTPStrictJSONTextAndHeaders(t *testing.T) {
	s, m, _, _, _ := fixture(t)
	h := s.Handler()
	before := len(m.boards)
	cases := []struct {
		name, body string
		want       int
	}{
		{"empty", "", 400}, {"null", "null", 400}, {"array", "[]", 400}, {"empty object", "{}", 400},
		{"duplicate", `{"name":"a","name":"b"}`, 400},
		{"escaped duplicate", `{"name":"a","n\u0061me":"b"}`, 400},
		{"wrong case", `{"Name":"a"}`, 400}, {"unknown actor", `{"name":"a","author_id":22}`, 400},
		{"unknown role", `{"name":"a","role":"admin"}`, 400},
		{"null field", `{"name":null}`, 400}, {"nested", `{"name":{"a":1}}`, 400},
		{"number", `{"name":42}`, 400}, {"bool", `{"name":true}`, 400},
		{"trailing object", `{"name":"a"} {}`, 400}, {"trailing garbage", `{"name":"a"}x`, 400},
		{"trailing comma", `{"name":"a",}`, 400}, {"invalid UTF8", "{\"name\":\"" + string([]byte{0xff}) + "\"}", 400},
		{"high surrogate", `{"name":"\ud800"}`, 400}, {"low surrogate", `{"name":"\udc00"}`, 400},
		{"wrong pair", `{"name":"\ud800\ud800"}`, 400}, {"NUL", `{"name":"x\u0000y"}`, 400},
		{"length", `{"name":"` + strings.Repeat("界", MaxNameRunes+1) + `"}`, 400},
		{"oversize", strings.Repeat(" ", MaxBodyBytes+1), 413},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { w := request(h, "POST", "/boards", tc.body, "invalid"); statusIs(t, w, tc.want) })
	}
	if len(m.boards) != before {
		t.Fatal("invalid JSON caused write")
	}
	for _, body := range []string{`{"name":"\ud83d\ude00"}`, `{"name":"literal \\ud800"}`, `{"name":"` + strings.Repeat("界", MaxNameRunes) + `"}`} {
		w := request(h, "POST", "/boards", body, fmt.Sprintf("valid%d", len(m.boards)))
		statusIs(t, w, 201)
	}
	for _, tc := range []struct {
		name, header, value string
		duplicate           bool
		status              int
	}{
		{"missing token", "X-LMM-User-Credential", "", false, 401},
		{"bad token", "X-LMM-User-Credential", "has space", false, 401},
		{"duplicate token", "X-LMM-User-Credential", testSession, true, 401},
		{"host authorization not removed", "Authorization", "Bearer host", false, 401},
		{"missing key", "Idempotency-Key", "", false, 400},
		{"duplicate key", "Idempotency-Key", "valid", true, 400},
		{"bad key", "Idempotency-Key", "bad:key", false, 400},
		{"long key", "Idempotency-Key", strings.Repeat("x", 129), false, 400},
		{"missing media", "Content-Type", "", false, 415},
		{"wrong media", "Content-Type", "text/plain", false, 415},
		{"bad charset", "Content-Type", "application/json; charset=latin1", false, 415},
		{"unknown media param", "Content-Type", "application/json; unused=yes", false, 415},
		{"duplicate media", "Content-Type", "application/json", true, 415},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/boards", strings.NewReader(`{"name":"valid"}`))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-LMM-User-Credential", testSession)
			r.Header.Set("Idempotency-Key", "key")
			r.Header.Set(tc.header, tc.value)
			if tc.value == "" {
				r.Header.Del(tc.header)
			}
			if tc.duplicate {
				r.Header.Add(tc.header, tc.value)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			statusIs(t, w, tc.status)
		})
	}
}
func TestForumHTTPPaginationPathAndModerationValidation(t *testing.T) {
	s, _, _, _, topic := fixture(t)
	h := s.Handler()
	for _, query := range []string{"limit=0", "limit=101", "limit=-1", "limit=1&limit=2", "after_id=-1", "after_id=9223372036854775808", "after_id=1.5", "after_id=", "unknown=1", "limit=%zz", "limit=1;after_id=0", "limit=+1"} {
		t.Run(query, func(t *testing.T) { w := request(h, "GET", "/boards?"+query, "", ""); statusIs(t, w, 400) })
	}
	for _, path := range []string{"/topics/0", "/topics/-1", "/topics/no", "/topics/9223372036854775808", "/topics/1?actor=22"} {
		w := request(h, "GET", path, "", "")
		statusIs(t, w, 400)
	}
	w := request(h, "POST", "/boards?limit=1", `{"name":"a"}`, "x")
	statusIs(t, w, 400)
	for _, body := range []string{
		`{"expected_version":1,"reason":"x"}`, `{"expected_version":0,"value":true,"reason":"x"}`,
		`{"expected_version":1,"value":null,"reason":"x"}`, `{"expected_version":1,"value":true,"reason":""}`,
		`{"expected_version":1,"value":"true","reason":"x"}`, `{"expected_version":1.0,"value":true,"reason":"x"}`,
		`{"expected_version":9223372036854775807,"value":true,"reason":"x"}`,
	} {
		w = request(h, "POST", fmt.Sprintf("/topics/%d/lock", topic.ID), body, "")
		statusIs(t, w, 400)
	}
	w = request(h, "POST", fmt.Sprintf("/topics/%d/hide", topic.ID), `{"expected_version":1,"reason":"x","value":false}`, "")
	statusIs(t, w, 400)
	w = request(h, "DELETE", fmt.Sprintf("/topics/%d", topic.ID), "", "")
	statusIs(t, w, 405)
}
func TestForumHTTPFailureMappingDoesNotLeakOrRetry(t *testing.T) {
	s, m, a, _, topic := fixture(t)
	h := s.Handler()
	a.gateErr = errors.New("private upstream secret")
	w := request(h, "GET", fmt.Sprintf("/topics/%d", topic.ID), "", "")
	statusIs(t, w, 503)
	if strings.Contains(w.Body.String(), "private") {
		t.Fatal("upstream detail leak")
	}
	a.gateErr = nil
	r := httptest.NewRequest("GET", "/boards", nil)
	r.Header.Set("X-LMM-User-Credential", otherSession)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	statusIs(t, w, 403)
	m.unknownCommit = true
	before := m.writeCalls
	w = request(h, "POST", fmt.Sprintf("/topics/%d/replies", topic.ID), `{"body":"unknown"}`, "unknown-http")
	statusIs(t, w, 503)
	if !strings.Contains(w.Body.String(), "commit_outcome_unknown") || m.writeCalls != before+1 || w.Header().Get("Retry-After") != "" {
		t.Fatal("lost unknown outcome or blind retry")
	}
}

// @mark forum.http role=test
func TestForumCompiledHostMountKeepsCredentialsSeparate(t *testing.T) {
	s, _, _, _, _ := fixture(t)
	hostCredential := strings.Repeat("h", 32)
	h, err := modules.New([]modules.Module{s}, []byte(hostCredential))
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/extensions/v1/forum/boards", nil)
	r.Header.Set("Authorization", "Bearer "+hostCredential)
	r.Header.Set("X-LMM-User-Credential", testSession)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	statusIs(t, w, 200)
	if r.Header.Get("Authorization") != "Bearer "+hostCredential {
		t.Fatal("host mutated original request")
	}
	r.Header.Del("X-LMM-User-Credential")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	statusIs(t, w, 401)
	// A host token alone is not user authority, even when syntactically valid.
	r.Header.Set("X-LMM-User-Credential", hostCredential)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	statusIs(t, w, 401)
	r.Header.Set("X-LMM-User-Credential", testSession)
	r.Header.Del("Authorization")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	statusIs(t, w, 401)
	if _, err = s.Boards(context.Background(), testSession, PageRequest{Limit: 1}); err != nil {
		t.Fatal(err)
	}
}
