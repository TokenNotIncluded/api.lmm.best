// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package forum

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

type reviewCountingAuthority struct {
	*scriptedAuthority
	calls int
}

func (a *reviewCountingAuthority) Authenticate(
	ctx context.Context, token string,
) (Actor, error) {
	a.calls++
	return a.scriptedAuthority.Authenticate(ctx, token)
}

// @mark forum.review.authority role=test
func TestIndependentEveryActionReauthenticatesAndUsesExactGrant(t *testing.T) {
	actions := []Action{
		ListBoards, CreateBoard, ListTopics, CreateTopic,
		ReadTopic, ListReplies, CreateReply,
		LockTopic, PinTopic, HideTopic, HideReply,
	}

	for _, action := range actions {
		t.Run(string(action), func(t *testing.T) {
			s, m, a, b, topic := fixture(t)

			reply, err := s.AddReply(
				context.Background(), testSession,
				"review-seed", topic.ID, "review seed",
			)
			if err != nil {
				t.Fatal(err)
			}

			counted := &reviewCountingAuthority{
				scriptedAuthority: a,
			}
			s.authority = counted

			resource := topicResource(topic)
			switch action {
			case ListBoards, CreateBoard:
				resource = Resource{Kind: ForumResource}
			case ListTopics, CreateTopic:
				resource = boardResource(b.ID)
			case HideReply:
				resource = Resource{
					Kind: ReplyResource, ID: reply.ID,
					BoardID: b.ID, TopicID: topic.ID,
				}
			}

			run := func() error {
				ctx := context.Background()
				switch action {
				case ListBoards:
					_, e := s.Boards(
						ctx, otherSession,
						PageRequest{Limit: 1},
					)
					return e
				case CreateBoard:
					_, e := s.AddBoard(
						ctx, otherSession,
						"review-key", "review board",
					)
					return e
				case ListTopics:
					_, e := s.Topics(
						ctx, otherSession, b.ID,
						PageRequest{Limit: 1},
					)
					return e
				case CreateTopic:
					_, e := s.AddTopic(
						ctx, otherSession, "review-key",
						b.ID, "review topic", "review text",
					)
					return e
				case ReadTopic:
					_, e := s.Topic(ctx, otherSession, topic.ID)
					return e
				case ListReplies:
					_, e := s.Replies(
						ctx, otherSession, topic.ID,
						PageRequest{Limit: 1},
					)
					return e
				case CreateReply:
					_, e := s.AddReply(
						ctx, otherSession, "review-key",
						topic.ID, "review reply",
					)
					return e
				default:
					id := topic.ID
					if action == HideReply {
						id = reply.ID
					}
					_, e := s.Moderate(
						ctx, otherSession, action,
						id, 1, true, "review reason",
					)
					return e
				}
			}

			before := m.writeCalls
			start := len(a.checks)
			requireError(t, run(), ErrForbidden)

			want := grant{22, action, resource}
			if counted.calls != 1 ||
				m.writeCalls != before ||
				len(a.checks) != start+1 ||
				a.checks[start] != want {
				t.Fatal("missing exact gate or denied mutation")
			}

			a.allow(22, resource, action)
			if err := run(); err != nil {
				t.Fatal(err)
			}
			if counted.calls != 2 ||
				a.checks[len(a.checks)-1] != want {
				t.Fatal("authorization cached or misbound")
			}

			a.mu.Lock()
			delete(a.sessions, otherSession)
			a.mu.Unlock()

			before = m.writeCalls
			gates := len(a.checks)
			requireError(t, run(), ErrUnauthorized)

			if counted.calls != 3 ||
				len(a.checks) != gates ||
				m.writeCalls != before {
				t.Fatal("revoked session reused")
			}
		})
	}
}

// @mark forum.review.http role=test
func TestIndependentStrictInputsAndPaginationExtremes(t *testing.T) {
	s, m, _, _, topic := fixture(t)
	h := s.Handler()
	before := m.writeCalls

	invalid := []string{
		`{"body":"x","b\u006fdy":"y"}`,
		`{"body":"x","role":{"authority":{"admin":true}}}`,
		`{"body":{"nested":{"unknown":[1,2,3]}}}`,
		`{"body":"x","actor_id":22}`,
		`{"body":"x","author_id":22}`,
		`{"body":"x","role":"root"}`,
		`{"body":"\udfff\ud800"}`,
		`{"body":"\ud800\\udc00"}`,
		"{\"body\":\"" + string([]byte{0xc0, 0xaf}) + "\"}",
		"{\"body\":\"" + string([]byte{0xed, 0xa0, 0x80}) + "\"}",
	}

	for i, body := range invalid {
		w := request(
			h, "POST",
			fmt.Sprintf("/topics/%d/replies", topic.ID),
			body, fmt.Sprintf("bad%d", i),
		)
		statusIs(t, w, 400)
	}

	for _, query := range []string{
		"after_id=9223372036854775808",
		"after_id=00000000000000000000",
		"limit=18446744073709551615",
		"%6cimit=1&limit=2",
		"after_id=%2B1",
		"limit=1&%75nknown=0",
		"after_id=%FF",
	} {
		statusIs(t, request(
			h, "GET", "/boards?"+query, "", "",
		), 400)
	}

	if m.writeCalls != before {
		t.Fatal("invalid input reached write")
	}

	w := request(
		h, "GET",
		"/boards?limit=100&after_id=9223372036854775807",
		"", "",
	)
	statusIs(t, w, 200)
	if w.Body.String() != "{\"items\":[],\"next_after_id\":0}\n" {
		t.Fatal(w.Body.String())
	}

	valid := []string{
		`{"body":"\ud800\udc00"}`,
		`{"body":"literal \\ud800"}`,
		`{"body":"` + strings.Repeat("界", MaxBodyRunes) + `"}`,
	}
	for i, body := range valid {
		statusIs(t, request(
			h, "POST",
			fmt.Sprintf("/topics/%d/replies", topic.ID),
			body, fmt.Sprintf("valid-review%d", i),
		), 201)
	}

	r := httptest.NewRequest("GET", "/boards", nil)
	r.Header.Set(
		"X-LMM-User-Credential",
		testSession+","+otherSession,
	)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	statusIs(t, w, 401)
}
