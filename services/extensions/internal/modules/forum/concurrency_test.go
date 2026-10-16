// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package forum

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

// @mark forum.repository role=test
func TestForumConcurrentSameKeyDifferentContentConflicts(t *testing.T) {
	s, m, _, _, topic := fixture(t)
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, body := range []string{"first intent", "second intent"} {
		go func() {
			<-start
			_, err := s.AddReply(context.Background(), testSession, "contested-key", topic.ID, body)
			results <- err
		}()
	}
	close(start)
	first, second := <-results, <-results
	if !((first == nil && errors.Is(second, ErrConflict)) || (second == nil && errors.Is(first, ErrConflict))) {
		t.Fatalf("results=%v %v", first, second)
	}
	if len(m.replies) != 1 {
		t.Fatal("conflicting intent committed")
	}
}

// @mark forum.moderation role=test
func TestForumReplyRechecksHideAfterAuthorization(t *testing.T) {
	s, m, a, _, topic := fixture(t)
	var once sync.Once
	a.hook = func(action Action, _ Resource) error {
		if action == CreateReply {
			once.Do(func() {
				_, err := s.Moderate(context.Background(), testSession, HideTopic, topic.ID, 1, true, "hide between lookup/write")
				if err != nil {
					t.Error(err)
				}
			})
		}
		return nil
	}
	_, err := s.AddReply(context.Background(), testSession, "hide-race", topic.ID, "must never appear")
	requireError(t, err, ErrNotFound)
	if len(m.replies) != 0 || len(m.audits) != 1 {
		t.Fatal("hidden-parent reply committed")
	}
}
func TestForumPaginationFiltersHiddenBeforeLimitAndPinKeepsOrder(t *testing.T) {
	s, _, a, b, topic := fixture(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		created, err := s.AddTopic(ctx, testSession, fmt.Sprintf("topic%d", i), b.ID, fmt.Sprintf("title%d", i), "text")
		if err != nil {
			t.Fatal(err)
		}
		a.allow(11, topicResource(created), PinTopic, HideTopic)
	}
	_, err := s.Moderate(ctx, testSession, HideTopic, topic.ID, 1, true, "hide first")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Moderate(ctx, testSession, PinTopic, 4, 1, true, "pin last")
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.Topics(ctx, testSession, b.ID, PageRequest{Limit: 2})
	if err != nil || len(page.Items) != 2 || page.Items[0].ID != 2 || page.Items[1].ID != 3 || page.NextAfterID != 3 {
		t.Fatalf("page=%+v %v", page, err)
	}
	last, err := s.Topics(ctx, testSession, b.ID, PageRequest{Limit: 2, AfterID: 3})
	if err != nil || len(last.Items) != 1 || last.Items[0].ID != 4 || !last.Items[0].Pinned || last.NextAfterID != 0 {
		t.Fatalf("last=%+v %v", last, err)
	}
}
func TestForumUnknownModerationCommitDoesNotDuplicateAudit(t *testing.T) {
	s, m, _, _, topic := fixture(t)
	m.unknownCommit = true
	before := m.writeCalls
	_, err := s.Moderate(context.Background(), testSession, LockTopic, topic.ID, 1, true, "unknown ACK")
	requireError(t, err, ErrUnknownCommit)
	if m.writeCalls != before+1 || len(m.audits) != 1 || !m.topics[topic.ID].Locked || m.topics[topic.ID].Version != 2 {
		t.Fatal("unknown result lost or retried")
	}
	m.unknownCommit = false
	_, err = s.Moderate(context.Background(), testSession, LockTopic, topic.ID, 1, true, "unknown ACK")
	requireError(t, err, ErrConflict)
	if len(m.audits) != 1 {
		t.Fatal("duplicate audit")
	}
}
