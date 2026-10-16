// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package forum

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// @mark forum.review.visibility role=test
func TestForumTopicRejectsChangedGrantResource(t *testing.T) {
	// IDs/ancestry are immutable by Repository contract. A faulty adapter must
	// still fail closed rather than use a grant for a different resource.
	for _, field := range []string{"id", "board_id"} {
		t.Run(field, func(t *testing.T) {
			s, m, a, _, topic := fixture(t)
			a.hook = func(action Action, resource Resource) error {
				if action == ReadTopic {
					if resource != topicResource(topic) {
						t.Fatal("wrong initial grant resource")
					}
					m.mu.Lock()
					changed := m.topics[topic.ID]
					if field == "id" {
						changed.ID++
					} else {
						changed.BoardID++
					}
					changed.Body = "wrong-resource-secret"
					m.topics[topic.ID] = changed
					m.mu.Unlock()
				}
				return nil
			}
			w := request(s.Handler(), "GET", fmt.Sprintf("/topics/%d", topic.ID), "", "")
			statusIs(t, w, 403)
			for _, secret := range []string{topic.Body, "not executable", "wrong-resource-secret"} {
				if strings.Contains(w.Body.String(), secret) {
					t.Fatal("mismatched grant leaked body")
				}
			}
		})
	}
}

// @mark forum.review.visibility role=test
func TestForumTopicReturnsOnlyPostGrantSnapshot(t *testing.T) {
	s, m, a, _, topic := fixture(t)
	a.hook = func(action Action, _ Resource) error {
		if action == ReadTopic {
			m.mu.Lock()
			current := m.topics[topic.ID]
			current.Pinned = true
			current.Version++
			m.topics[topic.ID] = current
			m.mu.Unlock()
		}
		return nil
	}
	got, err := s.Topic(context.Background(), testSession, topic.ID)
	if err != nil || !got.Pinned || got.Version != topic.Version+1 || got.ID != topic.ID || got.BoardID != topic.BoardID {
		t.Fatalf("post-grant snapshot=%+v err=%v", got, err)
	}
}
