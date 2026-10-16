// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package forum

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

const testSession = "synthetic_session_for_forum_tests_1"
const otherSession = "synthetic_session_for_forum_tests_2"

type grant struct {
	actor    int64
	action   Action
	resource Resource
}

// @mark forum.authority role=test
// Scripted contract double ONLY. These tests do not prove real Core sessions,
// A01 gates, API-key rejection, host-token rejection or revocation integration.
// Missing exact grants deny, rather than a success-only authorizer or bool role.
type scriptedAuthority struct {
	mu       sync.Mutex
	sessions map[string]Actor
	grants   map[grant]error
	authErr  error
	gateErr  error
	checks   []grant
	hook     func(Action, Resource) error
}

func authority() *scriptedAuthority {
	return &scriptedAuthority{sessions: map[string]Actor{testSession: {UserID: 11}, otherSession: {UserID: 22}}, grants: map[grant]error{}}
}
func (a *scriptedAuthority) allow(actor int64, resource Resource, actions ...Action) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, action := range actions {
		a.grants[grant{actor, action, resource}] = nil
	}
}
func (a *scriptedAuthority) Authenticate(ctx context.Context, token string) (Actor, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Actor{}, err
	}
	if a.authErr != nil {
		return Actor{}, a.authErr
	}
	actor, ok := a.sessions[token]
	if !ok {
		return Actor{}, ErrUnauthorized
	}
	return actor, nil
}
func (a *scriptedAuthority) Authorize(ctx context.Context, token string, actor Actor, action Action, resource Resource) error {
	a.mu.Lock()
	g := grant{actor.UserID, action, resource}
	a.checks = append(a.checks, g)
	current, active := a.sessions[token]
	decision, exists := a.grants[g]
	fault, hook := a.gateErr, a.hook
	a.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if !active || current != actor {
		return ErrUnauthorized
	}
	if fault != nil {
		return fault
	}
	if !exists {
		return ErrForbidden
	}
	if decision != nil {
		return decision
	}
	if hook != nil {
		return hook(action, resource)
	}
	return nil
}
func fixture(t *testing.T) (*Service, *memoryRepository, *scriptedAuthority, Board, Topic) {
	t.Helper()
	m, a := memory(), authority()
	s, err := New(m, a)
	if err != nil {
		t.Fatal(err)
	}
	a.allow(11, Resource{Kind: ForumResource}, ListBoards, CreateBoard)
	b, err := s.AddBoard(context.Background(), testSession, "board", "General")
	if err != nil {
		t.Fatal(err)
	}
	a.allow(11, boardResource(b.ID), ListTopics, CreateTopic)
	topic, err := s.AddTopic(context.Background(), testSession, "topic", b.ID, "Hello", "Plain <script>not executable</script>")
	if err != nil {
		t.Fatal(err)
	}
	a.allow(11, topicResource(topic), ReadTopic, ListReplies, CreateReply, LockTopic, PinTopic, HideTopic)
	return s, m, a, b, topic
}
func requireError(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error=%v, want %v", got, want)
	}
}

// @mark forum.repository role=test
func TestForumCreateListDetailAndBoundedPagination(t *testing.T) {
	s, _, _, b, topic := fixture(t)
	ctx := context.Background()
	if b.AuthorID != 11 || topic.AuthorID != 11 {
		t.Fatal("untrusted author")
	}
	got, err := s.Topic(ctx, testSession, topic.ID)
	if err != nil || got != topic {
		t.Fatalf("detail=%+v %v", got, err)
	}
	for i := 0; i < 3; i++ {
		r, e := s.AddReply(ctx, testSession, fmt.Sprintf("r%d", i), topic.ID, fmt.Sprintf("Reply %d\n\tText", i))
		if e != nil || r.AuthorID != 11 {
			t.Fatalf("reply=%+v %v", r, e)
		}
	}
	first, err := s.Replies(ctx, testSession, topic.ID, PageRequest{Limit: 2})
	if err != nil || len(first.Items) != 2 || first.NextAfterID != first.Items[1].ID {
		t.Fatalf("page=%+v %v", first, err)
	}
	last, err := s.Replies(ctx, testSession, topic.ID, PageRequest{AfterID: first.NextAfterID, Limit: 2})
	if err != nil || len(last.Items) != 1 || last.NextAfterID != 0 || last.Items[0].ID <= first.NextAfterID {
		t.Fatalf("last=%+v %v", last, err)
	}
	boards, err := s.Boards(ctx, testSession, PageRequest{Limit: 1})
	if err != nil || len(boards.Items) != 1 || boards.Items[0] != b {
		t.Fatalf("boards=%+v %v", boards, err)
	}
	topics, err := s.Topics(ctx, testSession, b.ID, PageRequest{Limit: 100})
	if err != nil || len(topics.Items) != 1 {
		t.Fatalf("topics=%+v %v", topics, err)
	}
	for _, p := range []PageRequest{{Limit: 0}, {Limit: 101}, {Limit: -1}, {AfterID: -1, Limit: 1}} {
		_, err = s.Boards(ctx, testSession, p)
		requireError(t, err, ErrInvalid)
	}
}

// @mark forum.authority role=test
func TestForumAuthorityFailsClosedAndSeparatesActionResource(t *testing.T) {
	s, m, a, b, topic := fixture(t)
	ctx := context.Background()
	before := m.writeCalls
	_, err := s.Moderate(ctx, otherSession, HideTopic, topic.ID, 1, true, "not allowed")
	requireError(t, err, ErrForbidden)
	// An exact topic read grant cannot authorize a different action or topic.
	a.allow(22, topicResource(topic), ReadTopic)
	if _, err = s.Topic(ctx, otherSession, topic.ID); err != nil {
		t.Fatal(err)
	}
	_, err = s.AddReply(ctx, otherSession, "forbidden", topic.ID, "x")
	requireError(t, err, ErrForbidden)
	a.allow(22, Resource{Kind: TopicResource, ID: topic.ID, TopicID: topic.ID, BoardID: b.ID + 1}, CreateReply)
	_, err = s.AddReply(ctx, otherSession, "wrong-board", topic.ID, "x")
	requireError(t, err, ErrForbidden)
	a.allow(22, boardResource(b.ID), ListTopics)
	_, err = s.AddTopic(ctx, otherSession, "wrong-action", b.ID, "x", "x")
	requireError(t, err, ErrForbidden)
	a.mu.Lock()
	a.grants[grant{11, HideTopic, topicResource(topic)}] = ErrForbidden
	a.mu.Unlock()
	_, err = s.Moderate(ctx, testSession, HideTopic, topic.ID, 1, true, "explicit deny")
	requireError(t, err, ErrForbidden)
	count := len(a.checks)
	_, err = s.Moderate(ctx, testSession, Action("forum.topics.unknown"), topic.ID, 1, true, "x")
	requireError(t, err, ErrForbidden)
	if len(a.checks) != count || m.writeCalls != before {
		t.Fatal("unmapped/denied action reached gate or storage write")
	}
	a.mu.Lock()
	a.gateErr = errors.New("private upstream detail")
	a.mu.Unlock()
	_, err = s.Topic(ctx, testSession, topic.ID)
	requireError(t, err, ErrUnavailable)
	a.mu.Lock()
	a.gateErr = nil
	delete(a.sessions, testSession)
	a.mu.Unlock()
	_, err = s.Topic(ctx, testSession, topic.ID)
	requireError(t, err, ErrUnauthorized)
	_, err = s.Topic(ctx, "api-key-not-a-session", topic.ID)
	requireError(t, err, ErrUnauthorized)
	var noRepo *memoryRepository
	var noAuth *scriptedAuthority
	_, err = New(noRepo, a)
	requireError(t, err, ErrUnavailable)
	_, err = New(m, noAuth)
	requireError(t, err, ErrUnavailable)
	err = s.gate(ctx, testSession, Actor{11}, LockTopic, boardResource(b.ID))
	requireError(t, err, ErrForbidden)
}

func TestForumPlainTextValidation(t *testing.T) {
	s, _, _, b, topic := fixture(t)
	ctx := context.Background()
	for _, body := range []string{"", " \n\t", "bad\x00text", string([]byte{0xff}), strings.Repeat("界", MaxBodyRunes+1), "bad\rtext"} {
		_, err := s.AddReply(ctx, testSession, "invalid", topic.ID, body)
		requireError(t, err, ErrInvalid)
	}
	for _, title := range []string{"", " ", "bad\ntext", strings.Repeat("界", MaxTitleRunes+1)} {
		_, err := s.AddTopic(ctx, testSession, "invalid", b.ID, title, "body")
		requireError(t, err, ErrInvalid)
	}
	if _, err := s.AddTopic(ctx, testSession, "boundary", b.ID, strings.Repeat("界", MaxTitleRunes), strings.Repeat("界", MaxBodyRunes)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddReply(ctx, testSession, "literal", topic.ID, `<img src=x onerror=alert(1)>`); err != nil {
		t.Fatal(err)
	}
}

// @mark forum.repository role=test
func TestForumIdempotencyBindsActorActionParentAndContent(t *testing.T) {
	s, m, a, b, topic := fixture(t)
	ctx := context.Background()
	r, err := s.AddReply(ctx, testSession, "same", topic.ID, "original")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.AddReply(ctx, testSession, "same", topic.ID, "original")
	if err != nil || replay != r {
		t.Fatalf("replay=%+v %v", replay, err)
	}
	_, err = s.AddReply(ctx, testSession, "same", topic.ID, "changed")
	requireError(t, err, ErrConflict)
	_, err = s.AddTopic(ctx, testSession, "same", b.ID, "other action", "original")
	requireError(t, err, ErrConflict)
	second, err := s.AddTopic(ctx, testSession, "second", b.ID, "Second", "body")
	if err != nil {
		t.Fatal(err)
	}
	a.allow(11, topicResource(second), CreateReply)
	_, err = s.AddReply(ctx, testSession, "same", second.ID, "original")
	requireError(t, err, ErrConflict)
	a.allow(22, topicResource(topic), CreateReply)
	other, err := s.AddReply(ctx, otherSession, "same", topic.ID, "original")
	if err != nil || other.ID == r.ID || other.AuthorID != 22 {
		t.Fatalf("actor scope=%+v %v", other, err)
	}
	if len(m.replies) != 2 {
		t.Fatal("duplicate creates")
	}
	// Replay resolves current state, not stale cached bodies.
	a.allow(11, Resource{Kind: ReplyResource, ID: r.ID, TopicID: topic.ID, BoardID: b.ID}, HideReply)
	_, err = s.Moderate(ctx, testSession, HideReply, r.ID, 1, true, "remove")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.AddReply(ctx, testSession, "same", topic.ID, "original")
	requireError(t, err, ErrNotFound)
}
func TestForumConcurrentCreateSameKey(t *testing.T) {
	s, m, _, _, topic := fixture(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	ids := make(chan int64, 32)
	errs := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := s.AddReply(ctx, testSession, "concurrent", topic.ID, "same")
			if err != nil {
				errs <- err
			} else {
				ids <- r.ID
			}
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	var id int64
	for got := range ids {
		if id != 0 && got != id {
			t.Fatal("non-idempotent result")
		}
		id = got
	}
	if len(m.replies) != 1 || id == 0 {
		t.Fatal("duplicate/missing reply")
	}
}

// @mark forum.moderation role=test
func TestForumModerationCASAuditAndHiddenVisibility(t *testing.T) {
	s, m, a, b, topic := fixture(t)
	ctx := context.Background()
	r, err := s.AddReply(ctx, testSession, "r", topic.ID, "hidden reply secret")
	if err != nil {
		t.Fatal(err)
	}
	a.allow(11, Resource{Kind: ReplyResource, ID: r.ID, TopicID: topic.ID, BoardID: b.ID}, HideReply)
	receipt, err := s.Moderate(ctx, testSession, PinTopic, topic.ID, 1, true, "pin reason")
	if err != nil || receipt.Version != 2 {
		t.Fatalf("pin=%+v %v", receipt, err)
	}
	_, err = s.Moderate(ctx, testSession, LockTopic, topic.ID, 1, true, "stale")
	requireError(t, err, ErrConflict)
	if len(m.audits) != 1 || m.topics[topic.ID].Locked {
		t.Fatal("stale CAS mutated state/audit")
	}
	m.failAudit = true
	_, err = s.Moderate(ctx, testSession, LockTopic, topic.ID, 2, true, "audit failure")
	requireError(t, err, ErrUnavailable)
	if len(m.audits) != 1 || m.topics[topic.ID].Version != 2 || m.topics[topic.ID].Locked {
		t.Fatal("non-atomic failed audit")
	}
	m.failAudit = false
	_, err = s.Moderate(ctx, testSession, HideReply, r.ID, 1, true, "hide reply")
	if err != nil {
		t.Fatal(err)
	}
	replies, err := s.Replies(ctx, testSession, topic.ID, PageRequest{Limit: 1})
	if err != nil || len(replies.Items) != 0 || replies.NextAfterID != 0 {
		t.Fatalf("hidden reply leaked %+v %v", replies, err)
	}
	_, err = s.Moderate(ctx, testSession, HideTopic, topic.ID, 2, true, "hide topic")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Topic(ctx, testSession, topic.ID)
	requireError(t, err, ErrNotFound)
	_, err = s.Replies(ctx, testSession, topic.ID, PageRequest{Limit: 1})
	requireError(t, err, ErrNotFound)
	_, err = s.AddReply(ctx, testSession, "new", topic.ID, "new")
	requireError(t, err, ErrNotFound)
	_, err = s.AddTopic(ctx, testSession, "topic", b.ID, "Hello", topic.Body)
	requireError(t, err, ErrNotFound)
	topics, err := s.Topics(ctx, testSession, b.ID, PageRequest{Limit: 1})
	if err != nil || len(topics.Items) != 0 || topics.NextAfterID != 0 {
		t.Fatalf("hidden topic leaked %+v %v", topics, err)
	}
	last := m.audits[len(m.audits)-1]
	if last.ActorID != 11 || last.Action != HideTopic || last.Resource != topicResource(topic) || last.BeforeVersion != 2 || last.AfterVersion != 3 || last.Reason != "hide topic" {
		t.Fatalf("audit=%+v", last)
	}
}

// @mark forum.moderation role=test
func TestForumConcurrentModerationCASOnlyOneAudit(t *testing.T) {
	s, m, _, _, topic := fixture(t)
	var wg sync.WaitGroup
	results := make(chan error, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Moderate(context.Background(), testSession, PinTopic, topic.ID, 1, true, "race")
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else {
			requireError(t, err, ErrConflict)
		}
	}
	if success != 1 || len(m.audits) != 1 || m.topics[topic.ID].Version != 2 {
		t.Fatalf("success=%d audits=%d", success, len(m.audits))
	}
}
func TestForumReplyRechecksLockAfterAuthorizationAndReplaysKnownCreate(t *testing.T) {
	s, _, a, _, topic := fixture(t)
	ctx := context.Background()
	original, err := s.AddReply(ctx, testSession, "original", topic.ID, "known")
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	a.hook = func(action Action, _ Resource) error {
		if action == CreateReply {
			once.Do(func() {
				_, e := s.Moderate(ctx, testSession, LockTopic, topic.ID, 1, true, "between lookup and write")
				if e != nil {
					t.Error(e)
				}
			})
		}
		return nil
	}
	_, err = s.AddReply(ctx, testSession, "racing", topic.ID, "must fail")
	requireError(t, err, ErrLocked)
	replay, err := s.AddReply(ctx, testSession, "original", topic.ID, "known")
	if err != nil || replay != original {
		t.Fatalf("known replay=%+v %v", replay, err)
	}
	_, err = s.Moderate(ctx, testSession, LockTopic, topic.ID, 2, false, "unlock")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddReply(ctx, testSession, "racing", topic.ID, "must fail"); err != nil {
		t.Fatal(err)
	}
}
func TestForumConcurrentLockAndReply(t *testing.T) {
	for i := 0; i < 40; i++ {
		s, m, _, _, topic := fixture(t)
		ctx := context.Background()
		start := make(chan struct{})
		result := make(chan error, 2)
		go func() {
			<-start
			_, e := s.Moderate(ctx, testSession, LockTopic, topic.ID, 1, true, "race")
			result <- e
		}()
		go func() { <-start; _, e := s.AddReply(ctx, testSession, "race", topic.ID, "reply"); result <- e }()
		close(start)
		e1, e2 := <-result, <-result
		if e1 != nil && !errors.Is(e1, ErrLocked) {
			t.Fatal(e1)
		}
		if e2 != nil && !errors.Is(e2, ErrLocked) {
			t.Fatal(e2)
		}
		if !m.topics[topic.ID].Locked || len(m.audits) != 1 {
			t.Fatal("lock lost")
		}
		_, err := s.AddReply(ctx, testSession, "after", topic.ID, "after lock")
		requireError(t, err, ErrLocked)
		if len(m.replies) > 1 {
			t.Fatal("reply committed after lock")
		}
	}
}

func TestForumCancellationAndUnknownCommitNeverRetried(t *testing.T) {
	s, m, _, _, topic := fixture(t)
	ctx := context.Background()
	before := m.writeCalls
	m.unknownCommit = true
	_, err := s.AddReply(ctx, testSession, "unknown", topic.ID, "committed but ACK lost")
	requireError(t, err, ErrUnknownCommit)
	if m.writeCalls != before+1 || len(m.replies) != 1 {
		t.Fatal("blind retry or lost commit")
	}
	m.unknownCommit = false
	reconciled, err := s.AddReply(ctx, testSession, "unknown", topic.ID, "committed but ACK lost")
	if err != nil || reconciled.ID != 1 || len(m.replies) != 1 {
		t.Fatalf("reconcile=%+v %v", reconciled, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	before = m.writeCalls
	_, err = s.AddReply(cancelled, testSession, "cancelled", topic.ID, "no write")
	requireError(t, err, ErrUnavailable)
	if m.writeCalls != before {
		t.Fatal("cancelled auth reached write")
	}
	m.failWrite = errors.New("simulated rollback")
	_, err = s.AddReply(ctx, testSession, "failed", topic.ID, "no write")
	if err == nil || len(m.replies) != 1 {
		t.Fatal("failed write mutated data")
	}
}
