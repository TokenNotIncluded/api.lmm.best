// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package forum

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// @mark forum.review.visibility role=test
func TestIndependentTopicHideBeforeGrantReturnsNoContent(t *testing.T) {
	s, m, a, _, topic := fixture(t)

	a.hook = func(action Action, _ Resource) error {
		if action == ReadTopic {
			_, err := s.Moderate(
				context.Background(), testSession,
				HideTopic, topic.ID, topic.Version, true,
				"hide committed before read authorization completes",
			)
			if err != nil {
				t.Fatalf("hide: %v", err)
			}
		}
		return nil
	}

	w := request(
		s.Handler(), "GET",
		fmt.Sprintf("/topics/%d", topic.ID), "", "",
	)

	if !m.topics[topic.ID].Hidden || len(m.audits) != 1 {
		t.Fatal("setup: hide did not commit")
	}
	if w.Code != 404 ||
		strings.Contains(w.Body.String(), topic.Body) ||
		strings.Contains(w.Body.String(), "not executable") {
		t.Fatalf(
			"hidden before grant completion: status=%d body=%s",
			w.Code, w.Body.String(),
		)
	}
}

// @mark forum.review.visibility role=test
func TestIndependentRepliesHideBeforeGrantReturnsNoContent(t *testing.T) {
	s, m, a, _, topic := fixture(t)

	_, err := s.AddReply(
		context.Background(), testSession,
		"review-reply", topic.ID, "review-hidden-secret",
	)
	if err != nil {
		t.Fatal(err)
	}

	a.hook = func(action Action, _ Resource) error {
		if action == ListReplies {
			_, err := s.Moderate(
				context.Background(), testSession,
				HideTopic, topic.ID, topic.Version, true,
				"hide before replies authorization completes",
			)
			if err != nil {
				t.Fatal(err)
			}
		}
		return nil
	}

	w := request(
		s.Handler(), "GET",
		fmt.Sprintf("/topics/%d/replies", topic.ID), "", "",
	)

	if !m.topics[topic.ID].Hidden {
		t.Fatal("setup: hide did not commit")
	}
	if w.Code != 404 ||
		strings.Contains(w.Body.String(), "review-hidden-secret") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
