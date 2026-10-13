// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
// Real private PostgreSQL storage and Web handlers, with controlled Core identity.
// This is developer self-testing, not live Core/model or independent acceptance.
package pgtest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/access"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/storage"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/webcompat"
	_ "github.com/lib/pq"
)

func greetingRequest(mux http.Handler, method, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/api/assistant/workspace/greeting", strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func greetingResponse(t *testing.T, w *httptest.ResponseRecorder) assistant.GreetingPreference {
	t.Helper()
	var envelope struct {
		Success bool                         `json:"success"`
		Data    assistant.GreetingPreference `json:"data"`
	}
	must(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	if w.Code != 200 || !envelope.Success || envelope.Data.Templates == nil || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
	return envelope.Data
}

func TestGreetingPostgresWebPersistenceDefaultsIsolationAndRevocation(t *testing.T) {
	f := historyDatabase(t)
	ctx := context.Background()
	auth, err := webcompat.NewAuthenticator(f.identity, []byte(strings.Repeat("h", 43)))
	must(t, err)
	mux := http.NewServeMux()
	webcompat.RegisterAssistant(mux, auth, f.service)
	initial := greetingResponse(t, greetingRequest(mux, "GET", f.token, ""))
	if len(initial.Templates) != 0 || initial.Revision != 0 || initial.UpdatedAt != 0 {
		t.Fatal(initial)
	}
	for _, template := range []string{"$unknown", strings.Repeat("😀", 513)} {
		body, err := json.Marshal(map[string]any{"language": "en", "template": template, "revision": 0})
		must(t, err)
		if w := greetingRequest(mux, "PUT", f.token, string(body)); w.Code != 400 {
			t.Fatal("invalid template reached persistence", w.Code, w.Body.String())
		}
	}
	// GET and rejected PUT must not manufacture a persisted row or overwrite display preferences.
	account := access.Account{Kind: "personal", ID: f.userID}
	var count int
	must(t, f.db.QueryRow(`SELECT count(*) FROM lmm_assistant.records WHERE scope=$1`, account.Key()).Scan(&count))
	if count != 0 {
		t.Fatal("read persisted defaults", count)
	}
	must(t, f.repo.Within(ctx, account.Key(), func(tx storage.Tx) error {
		return tx.Put("preferences", "display", assistant.Preferences{Theme: "dark", Language: "fr", Currency: "USD"})
	}))
	saved := greetingResponse(t, greetingRequest(mux, "PUT", f.token, `{"language":"en","template":"Hello,$name $$ $time","revision":0}`))
	if saved.Templates["en"] != "Hello,$name $$ $time" || saved.Revision != 1 || saved.UpdatedAt <= 0 {
		t.Fatal(saved)
	}
	w := greetingRequest(mux, "PUT", f.token, `{"language":"en","template":"Hello,$name $$ $time","revision":0}`)
	if w.Code != 409 {
		t.Fatal("replay was not rejected", w.Code, w.Body.String())
	}
	other := greetingResponse(t, greetingRequest(mux, "GET", f.other, ""))
	if len(other.Templates) != 0 || other.Revision != 0 {
		t.Fatal("administrator read another user's greeting", other)
	}
	greetingResponse(t, greetingRequest(mux, "PUT", f.other, `{"language":"en","template":"Other user","revision":0}`))
	if got := greetingResponse(t, greetingRequest(mux, "GET", f.token, "")); !reflect.DeepEqual(got, saved) {
		t.Fatal("other user overwrote greeting", got)
	}
	// A fresh pool/service sees the same preference, not process-local state.
	second, err := sql.Open("postgres", f.dsn)
	must(t, err)
	defer second.Close()
	repo2, err := assistant.NewPostgres(ctx, second, f.database)
	must(t, err)
	rust, err := access.New(f.identity)
	must(t, err)
	restarted, err := assistant.NewHistory(repo2, rust, []byte(strings.Repeat("c", 32)))
	must(t, err)
	read, err := restarted.Greeting(ctx, f.token)
	must(t, err)
	if !reflect.DeepEqual(saved, read) {
		t.Fatal(saved, read)
	}
	localized, err := restarted.UpdateGreeting(ctx, f.token, "zh-TW", "歡迎 $name", 1)
	must(t, err)
	reset, err := restarted.UpdateGreeting(ctx, f.token, "en", " \n\t", 2)
	must(t, err)
	if reset.Revision != 3 || len(reset.Templates) != 1 || reset.Templates["zh-TW"] != localized.Templates["zh-TW"] {
		t.Fatal("reset changed another locale", reset)
	}
	must(t, f.repo.Within(ctx, account.Key(), func(tx storage.Tx) error {
		var display assistant.Preferences
		if err := tx.Get("preferences", "display", &display); err != nil {
			return err
		}
		if display.Theme != "dark" || display.Language != "fr" {
			return errors.New("greeting overwrote unrelated display preferences")
		}
		return nil
	}))
	for _, method := range []string{"GET", "PUT"} {
		if w := greetingRequest(mux, method, "", `{}`); w.Code != 401 {
			t.Fatal(method, w.Code)
		}
	}
	// Manual preferences do not make the missing billed model adapter available.
	chat := httptest.NewRequest("POST", "/api/assistant/chat", strings.NewReader(`{"message":"generate a greeting"}`))
	chat.Header.Set("Authorization", "Bearer "+f.token)
	chatResponse := httptest.NewRecorder()
	mux.ServeHTTP(chatResponse, chat)
	if chatResponse.Code != 503 || !strings.Contains(chatResponse.Body.String(), `"success":false`) {
		t.Fatal("manual greeting falsely enabled generation", chatResponse.Code, chatResponse.Body.String())
	}
	f.identity.Revoke(f.token)
	for _, method := range []string{"GET", "PUT"} {
		if w := greetingRequest(mux, method, f.token, `{"language":"en","template":"revoked","revision":3}`); w.Code != 401 {
			t.Fatal(method, w.Code)
		}
	}
	if _, err = restarted.UpdateGreeting(ctx, f.token, "en", "revoked", 3); !errors.Is(err, access.ErrUnauthorized) {
		t.Fatal(err)
	}
}

func TestGreetingPostgresConcurrentFirstWriteHasOneWinnerAcrossPools(t *testing.T) {
	f := historyDatabase(t)
	ctx := context.Background()
	second, err := sql.Open("postgres", f.dsn)
	must(t, err)
	defer second.Close()
	repo2, err := assistant.NewPostgres(ctx, second, f.database)
	must(t, err)
	auth, err := access.New(f.identity)
	must(t, err)
	s2, err := assistant.NewHistory(repo2, auth, []byte(strings.Repeat("c", 32)))
	must(t, err)
	var wg sync.WaitGroup
	results := make(chan error, 12)
	for n := 0; n < 12; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			s := f.service
			if n%2 == 0 {
				s = s2
			}
			_, err := s.UpdateGreeting(ctx, f.token, "en", fmt.Sprintf("winner-%d", n), 0)
			results <- err
		}(n)
	}
	wg.Wait()
	close(results)
	wins, conflicts := 0, 0
	for err := range results {
		if err == nil {
			wins++
		} else if errors.Is(err, access.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 11 {
		t.Fatal(wins, conflicts)
	}
	got, err := s2.Greeting(ctx, f.token)
	must(t, err)
	if got.Revision != 1 || len(got.Templates) != 1 || !strings.HasPrefix(got.Templates["en"], "winner-") {
		t.Fatal(got)
	}
}

func TestGreetingPostgresFullLocaleBoundsValidationAndRollback(t *testing.T) {
	f := historyDatabase(t)
	ctx := context.Background()
	for _, input := range []struct {
		language, template string
		revision           int64
	}{
		{"zh-CN", "hello", 0}, {"en", "$unknown", 0}, {"en", "\x00", 0},
		{"en", strings.Repeat("😀", 513), 0}, {"en", "hello", -1},
		{"en", "hello", 9007199254740992},
	} {
		if _, err := f.service.UpdateGreeting(ctx, f.token, input.language, input.template, input.revision); !errors.Is(err, access.ErrInvalid) {
			t.Fatal(err)
		}
	}
	account := access.Account{Kind: "personal", ID: f.userID}
	var count int
	must(t, f.db.QueryRow(`SELECT count(*) FROM lmm_assistant.records WHERE scope=$1`, account.Key()).Scan(&count))
	if count != 0 {
		t.Fatal("invalid write persisted a shell", count)
	}
	var saved assistant.GreetingPreference
	for n, language := range []string{"en", "zh", "zh-TW", "fr", "ja", "ru", "vi"} {
		var err error
		// JSON-escaped control characters are valid in the existing contract;
		// seven 512-rune templates exceed a single 16 KiB record's capacity.
		saved, err = f.service.UpdateGreeting(ctx, f.token, language, strings.Repeat("\x01", 512), int64(n))
		must(t, err)
	}
	read, err := f.service.Greeting(ctx, f.token)
	must(t, err)
	if !reflect.DeepEqual(read, saved) || len(read.Templates) != 7 || read.Revision != 7 {
		t.Fatal("full locale preferences did not round-trip")
	}
	_, err = f.service.UpdateGreeting(ctx, f.token, "ja", strings.Repeat("😀", 512), 7)
	must(t, err)
	// Verify the same transaction primitive rolls back both template/version
	// writes if a later persistence operation fails.
	err = f.repo.Within(ctx, account.Key(), func(tx storage.Tx) error {
		if err := tx.Put("preferences", "overview-greeting", map[string]any{"revision": 999, "updated_at": 1}); err != nil {
			return err
		}
		if err := tx.Put("preferences", "overview-greeting:en", map[string]any{"template": "should roll back"}); err != nil {
			return err
		}
		return tx.Put("preferences", "oversize", map[string]string{"value": strings.Repeat("a", 17<<10)})
	})
	if !errors.Is(err, access.ErrInvalid) {
		t.Fatal(err)
	}
	read, err = f.service.Greeting(ctx, f.token)
	must(t, err)
	if read.Revision != 8 || read.Templates["en"] != strings.Repeat("\x01", 512) || read.Templates["ja"] != strings.Repeat("😀", 512) {
		t.Fatal("failed persistence transaction changed preferences")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = f.service.UpdateGreeting(cancelled, f.token, "en", "cancelled", 8); err == nil {
		t.Fatal("cancelled write succeeded")
	}
	unchanged, err := f.service.Greeting(ctx, f.token)
	must(t, err)
	if !reflect.DeepEqual(read, unchanged) {
		t.Fatal("cancelled write changed preferences")
	}
}

func TestGreetingPostgresCorruptVersionFailsClosedAndSafeIntegerExhaustion(t *testing.T) {
	f := historyDatabase(t)
	ctx := context.Background()
	account := access.Account{Kind: "personal", ID: f.userID}
	must(t, f.repo.Within(ctx, account.Key(), func(tx storage.Tx) error {
		return tx.Put("preferences", "overview-greeting", map[string]any{"revision": 9007199254740991, "updated_at": 1})
	}))
	if _, err := f.service.UpdateGreeting(ctx, f.token, "en", "overflow", 9007199254740991); !errors.Is(err, access.ErrLimit) {
		t.Fatal(err)
	}
	must(t, f.repo.Within(ctx, account.Key(), func(tx storage.Tx) error {
		return tx.Put("preferences", "overview-greeting", map[string]any{"revision": -1, "updated_at": 1})
	}))
	if _, err := f.service.Greeting(ctx, f.token); !errors.Is(err, access.ErrUnavailable) {
		t.Fatal("corrupt persisted version became successful defaults", err)
	}
	if _, err := f.service.UpdateGreeting(ctx, f.token, "en", "overwrite", 0); !errors.Is(err, access.ErrUnavailable) {
		t.Fatal("write repaired corruption without authority", err)
	}
}
