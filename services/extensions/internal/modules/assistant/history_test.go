// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package assistant

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/access"
)

type historySpy struct {
	turn   CompletedTurn
	userID int64
	calls  int
	items  []Conversation
	click  string
}

func (r *historySpy) RecordTurn(_ context.Context, userID int64, turn CompletedTurn) (int64, error) {
	r.calls++
	r.userID, r.turn = userID, turn
	return 19, nil
}
func (r *historySpy) ListHistory(_ context.Context, userID int64, _ bool, _ HistoryPosition, _ int) ([]Conversation, error) {
	r.calls++
	r.userID = userID
	return r.items, nil
}
func (r *historySpy) ReadHistory(context.Context, int64, int64, int) (Conversation, []HistoryMessage, error) {
	r.calls++
	return Conversation{}, nil, access.ErrNotFound
}
func (r *historySpy) ArchiveHistory(context.Context, int64, int64, bool, int64) (Conversation, error) {
	r.calls++
	return Conversation{}, access.ErrNotFound
}
func (r *historySpy) ClickPreset(_ context.Context, id string) error { r.click = id; return nil }

func TestHistoryCompletedTurnRedactsBeforeRepositoryAndRechecksSession(t *testing.T) {
	s, _, identity := setup(t, Options{})
	repo := &historySpy{}
	history, err := NewHistory(repo, s.auth, []byte(strings.Repeat("c", 32)))
	if err != nil {
		t.Fatal(err)
	}
	secret := "lmmk_" + strings.Repeat("s", 43)
	id, err := history.RecordCompletedTurn(ctx, user, 0, "first-turn", "Contact a@example.com with "+secret, "Bearer verysecretvalue123")
	if err != nil || id != 19 || repo.userID != 1 {
		t.Fatal(id, err)
	}
	for _, content := range []string{repo.turn.UserContent, repo.turn.AnswerContent} {
		if strings.Contains(content, secret) || strings.Contains(content, "a@example.com") || strings.Contains(content, "verysecretvalue123") {
			t.Fatal("sensitive input reached repository")
		}
	}
	identity.Revoke(user)
	_, err = history.RecordCompletedTurn(ctx, user, id, "second-turn", "next", "reply")
	if !errors.Is(err, access.ErrUnauthorized) || repo.calls != 1 {
		t.Fatal(err, repo.calls)
	}
}

func TestHistoryCursorBindsOwnerArchiveAndExpiryAndSnapshotsKey(t *testing.T) {
	s, _, _ := setup(t, Options{})
	repo := &historySpy{items: []Conversation{{ID: 9, UpdatedAt: 123}, {ID: 8, UpdatedAt: 122}}}
	key := []byte(strings.Repeat("k", 32))
	history, err := NewHistory(repo, s.auth, key)
	if err != nil {
		t.Fatal(err)
	}
	history.now = func() time.Time { return time.Unix(1000, 0) }
	page, err := history.List(ctx, user, 0, false, 1, "")
	if err != nil || len(page.Conversations) != 1 || page.NextCursor == "" {
		t.Fatal(page, err)
	}
	key[0] = 'x'
	if _, err = history.List(ctx, user, 0, false, 1, page.NextCursor); err != nil {
		t.Fatal("caller mutated cursor key", err)
	}
	for _, tc := range []struct {
		token    string
		archived bool
		cursor   string
	}{
		{other, false, page.NextCursor}, {user, true, page.NextCursor}, {user, false, page.NextCursor + "x"},
	} {
		if _, err = history.List(ctx, tc.token, 0, tc.archived, 1, tc.cursor); !errors.Is(err, access.ErrInvalid) {
			t.Fatal(err)
		}
	}
	history.now = func() time.Time { return time.Unix(1000, 0).Add(25 * time.Hour) }
	if _, err = history.List(ctx, user, 0, false, 1, page.NextCursor); !errors.Is(err, access.ErrInvalid) {
		t.Fatal(err)
	}
}

func TestHistoryAdministratorCannotGuessAnotherOwner(t *testing.T) {
	s, _, _ := setup(t, Options{})
	repo := &historySpy{}
	history, _ := NewHistory(repo, s.auth, []byte(strings.Repeat("k", 32)))
	if _, err := history.List(ctx, other, 1, false, 30, ""); !errors.Is(err, access.ErrNotFound) || repo.calls != 0 {
		t.Fatal(err, repo.calls)
	}
	if _, err := history.Detail(ctx, user, 9007199254740992, 30); !errors.Is(err, access.ErrNotFound) || repo.calls != 0 {
		t.Fatal(err, repo.calls)
	}
}

func TestHistoryRejectsEmptyOrAmbiguousTurnBeforePersistence(t *testing.T) {
	s, _, _ := setup(t, Options{})
	repo := &historySpy{}
	history, _ := NewHistory(repo, s.auth, []byte(strings.Repeat("k", 32)))
	for _, tc := range []struct{ turn, input, answer string }{{"id", "", "answer"}, {"id", "input", " "}, {"bad id", "input", "answer"}, {"id", string([]byte{255}), "answer"}} {
		if _, err := history.RecordCompletedTurn(ctx, user, 0, tc.turn, tc.input, tc.answer); !errors.Is(err, access.ErrInvalid) {
			t.Fatal(err)
		}
	}
	if repo.calls != 0 {
		t.Fatal("invalid turn left persistence effects")
	}
}

func TestHistoryStatusIsExplicitAboutUnavailableModelAndAuthority(t *testing.T) {
	s, _, _ := setup(t, Options{})
	history, _ := NewHistory(&historySpy{}, s.auth, []byte(strings.Repeat("k", 32)))
	value, err := history.Status(ctx, user)
	if err != nil || value["enabled"] != false || value["route_available"] != false || value["permission_profile_available"] != false {
		t.Fatal(value, err)
	}
	if _, exists := value["trust_level"]; exists {
		t.Fatal("platform level invented legacy trust level")
	}
	if value["funding"].(map[string]any)["configured"] != false {
		t.Fatal(value)
	}
}

func TestHistoryPresetsRetainReviewedLocalesAndIndependentViews(t *testing.T) {
	for _, locale := range []string{"en", "zh", "zh-TW", "fr", "ja", "ru", "vi", "zh_Hant", "fr-FR"} {
		set := HistoryPresets(locale)
		if len(set.Presets) != 4 || set.Version != "backend-seed-v3" {
			t.Fatal(set)
		}
		for _, item := range set.Presets {
			if item.Prompt == "" || item.Label != item.Prompt || item.Source != "default" {
				t.Fatal(item)
			}
		}
		set.Presets[0].Prompt = "mutated"
		if HistoryPresets(locale).Presets[0].Prompt == "mutated" {
			t.Fatal("shared mutable preset view")
		}
	}
}

func TestHistoryRedactionKeepsBoundedTextAndMasksCredentialFamilies(t *testing.T) {
	cases := []string{"api_key=sk_abcdefghijklmnop", "Cookie: session-secret", "user@example.com", "4111 1111 1111 1111", "192.0.2.1", "password: mysecret", "lmms_" + strings.Repeat("s", 43), "lmmk_" + strings.Repeat("k", 43)}
	for _, input := range cases {
		value := redactAssistantHistoryBounded(input)
		if value == input || !strings.Contains(value, "REDACTED") {
			t.Fatalf("unredacted fixture: %q", input)
		}
	}
	if got := redactAssistantHistoryBounded(strings.Repeat("中", 5000)); len([]rune(got)) != 4000 {
		t.Fatal(len([]rune(got)))
	}
}
