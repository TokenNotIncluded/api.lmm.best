// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
// Real isolated PostgreSQL persistence tests with controlled identity fixtures.
// They do not prove live Rust session authorization or billed model execution.
package pgtest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/access"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/testkit"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/webcompat"
)

type historyFixture struct {
	db           *sql.DB
	repo         *assistant.Postgres
	service      *assistant.HistoryService
	identity     *testkit.Identity
	token, other string
	userID       int64
	dsn          string
	database     string
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func historyDatabase(t *testing.T) historyFixture {
	t.Helper()
	path := os.Getenv("LMM_ASSISTANT_HISTORY_TEST_DSN_FILE")
	info, err := os.Lstat(path)
	if err != nil || !filepath.IsAbs(path) || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		t.Fatal("a private local fixture DSN file is required")
	}
	data, err := os.ReadFile(path)
	must(t, err)
	dsn := strings.TrimSpace(string(data))
	u, err := url.Parse(dsn)
	must(t, err)
	if u.Hostname() != "127.0.0.1" || u.Port() == "" || !strings.HasPrefix(strings.TrimPrefix(u.Path, "/"), "lmm_assistant_") {
		t.Fatal("a dedicated local assistant fixture database is required")
	}
	db, err := sql.Open("postgres", dsn)
	must(t, err)
	db.SetMaxOpenConns(4)
	t.Cleanup(func() { db.Close() })
	name := strings.TrimPrefix(u.Path, "/")
	repo, err := assistant.NewPostgres(context.Background(), db, name)
	must(t, err)
	var restricted bool
	must(t, db.QueryRow(`SELECT NOT(rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls) FROM pg_roles WHERE rolname=current_user`).Scan(&restricted))
	if !restricted {
		t.Fatal("history tests require restricted runtime, not installation owner")
	}
	identity := testkit.NewIdentity()
	token := "lmms_" + strings.Repeat("a", 43)
	other := "lmms_" + strings.Repeat("b", 43)
	uid := time.Now().UnixNano()%9007199254740000 + 1
	identity.Set(token, uid, 1)
	identity.Set(other, uid+1, 6)
	auth, err := access.New(identity)
	must(t, err)
	service, err := assistant.NewHistory(repo, auth, []byte(strings.Repeat("c", 32)))
	must(t, err)
	return historyFixture{db, repo, service, identity, token, other, uid, dsn, name}
}

func TestHistoryPostgresCompletedTurnsSurviveRestartAndRejectCrossOwner(t *testing.T) {
	f := historyDatabase(t)
	ctx := context.Background()
	secret := "lmmk_" + strings.Repeat("s", 43)
	id, err := f.service.RecordCompletedTurn(ctx, f.token, 0, "first", "Hello "+secret, "Stored answer")
	must(t, err)
	detail, err := f.service.Detail(ctx, f.token, id, 100)
	must(t, err)
	if len(detail.Messages) != 2 || strings.Contains(detail.Messages[0].Content, secret) || detail.Messages[1].Content != "Stored answer" {
		t.Fatal("stored transcript incorrect")
	}
	second, err := sql.Open("postgres", f.dsn)
	must(t, err)
	defer second.Close()
	repo2, err := assistant.NewPostgres(ctx, second, f.database)
	must(t, err)
	auth, err := access.New(f.identity)
	must(t, err)
	restarted, err := assistant.NewHistory(repo2, auth, []byte(strings.Repeat("c", 32)))
	must(t, err)
	replayed, err := restarted.RecordCompletedTurn(ctx, f.token, 0, "first", "Hello "+secret, "Stored answer")
	must(t, err)
	if replayed != id {
		t.Fatal("replay allocated another conversation")
	}
	if _, err = restarted.RecordCompletedTurn(ctx, f.token, id, "first", "changed", "Stored answer"); !errors.Is(err, access.ErrConflict) {
		t.Fatal(err)
	}
	detail, err = restarted.Detail(ctx, f.token, id, 100)
	must(t, err)
	if len(detail.Messages) != 2 {
		t.Fatal("replay appended duplicate messages")
	}
	if _, err = restarted.List(ctx, f.other, f.userID, false, 30, ""); !errors.Is(err, access.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = restarted.Detail(ctx, f.other, id, 100); !errors.Is(err, access.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = restarted.Archive(ctx, f.other, id, true); !errors.Is(err, access.ErrNotFound) {
		t.Fatal(err)
	}
	archived, err := restarted.Archive(ctx, f.token, id, true)
	must(t, err)
	if !archived.Archived {
		t.Fatal(archived)
	}
	if _, err = restarted.Archive(ctx, f.token, id, true); !errors.Is(err, access.ErrConflict) {
		t.Fatal(err)
	}
	page, err := restarted.List(ctx, f.token, 0, false, 30, "")
	must(t, err)
	if len(page.Conversations) != 0 {
		t.Fatal("archive remained active")
	}
	page, err = restarted.List(ctx, f.token, 0, true, 30, "")
	must(t, err)
	if len(page.Conversations) != 1 {
		t.Fatal("archive was not persisted")
	}
	_, err = restarted.Archive(ctx, f.token, id, false)
	must(t, err)
	// Legacy HTTP reads use the actual PostgreSQL repository and its envelope.
	webAuth, err := webcompat.NewAuthenticator(f.identity, []byte(strings.Repeat("h", 43)))
	must(t, err)
	mux := http.NewServeMux()
	webcompat.RegisterAssistant(mux, webAuth, restarted)
	r := httptest.NewRequest("GET", fmt.Sprintf("/api/assistant/conversations/%d", id), nil)
	r.Header.Set("Authorization", "Bearer "+f.token)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Stored answer") || !strings.Contains(w.Body.String(), `"success":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	f.identity.Revoke(f.token)
	if _, err = restarted.Detail(ctx, f.token, id, 100); !errors.Is(err, access.ErrUnauthorized) {
		t.Fatal(err)
	}
}

func TestHistoryPostgresConcurrentReplayAndBoundedTranscript(t *testing.T) {
	f := historyDatabase(t)
	ctx := context.Background()
	id, err := f.service.RecordCompletedTurn(ctx, f.token, 0, "initial", "Initial question", "Initial answer")
	must(t, err)
	second, err := sql.Open("postgres", f.dsn)
	must(t, err)
	defer second.Close()
	repo2, err := assistant.NewPostgres(ctx, second, f.database)
	must(t, err)
	auth, err := access.New(f.identity)
	must(t, err)
	service2, err := assistant.NewHistory(repo2, auth, []byte(strings.Repeat("c", 32)))
	must(t, err)
	var wg sync.WaitGroup
	failures := make(chan error, 12)
	for n := 0; n < 12; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			s := f.service
			if n%2 == 0 {
				s = service2
			}
			got, e := s.RecordCompletedTurn(ctx, f.token, id, "concurrent", "Same question", "Same answer")
			if e == nil && got != id {
				e = errors.New("replay changed conversation")
			}
			failures <- e
		}(n)
	}
	wg.Wait()
	close(failures)
	for e := range failures {
		must(t, e)
	}
	detail, err := service2.Detail(ctx, f.token, id, 100)
	must(t, err)
	if len(detail.Messages) != 4 {
		t.Fatal("concurrent turn was duplicated", len(detail.Messages))
	}
	for n := 0; n < 65; n++ {
		_, err = service2.RecordCompletedTurn(ctx, f.token, id, fmt.Sprintf("bounded-%d", n), strings.Repeat("中", 3000), strings.Repeat("文", 3000))
		must(t, err)
	}
	var count, bytes int64
	must(t, f.db.QueryRow(`SELECT count(*),COALESCE(sum(octet_length(content)),0) FROM lmm_assistant.history_messages WHERE conversation_id=$1`, id).Scan(&count, &bytes))
	if count > 128 || bytes > 262144 || count < 2 {
		t.Fatal(count, bytes)
	}
	// The durable receipt remains valid even after its old text was trimmed.
	got, err := f.service.RecordCompletedTurn(ctx, f.token, id, "initial", "Initial question", "Initial answer")
	must(t, err)
	if got != id {
		t.Fatal(got)
	}
}

func TestHistoryPostgresCursorAndAggregateClicks(t *testing.T) {
	f := historyDatabase(t)
	ctx := context.Background()
	for n := 0; n < 3; n++ {
		_, err := f.service.RecordCompletedTurn(ctx, f.token, 0, fmt.Sprintf("page-%d", n), "Question", "Answer")
		must(t, err)
	}
	page, err := f.service.List(ctx, f.token, 0, false, 1, "")
	must(t, err)
	if len(page.Conversations) != 1 || page.NextCursor == "" {
		t.Fatal(page)
	}
	second, err := f.service.List(ctx, f.token, 0, false, 1, page.NextCursor)
	must(t, err)
	if len(second.Conversations) != 1 || second.Conversations[0].ID == page.Conversations[0].ID {
		t.Fatal("cursor repeated a row")
	}
	var before, after int64
	must(t, f.db.QueryRow(`SELECT clicks FROM lmm_assistant.preset_stats WHERE preset_id='getting_started'`).Scan(&before))
	var wg sync.WaitGroup
	failures := make(chan error, 12)
	for n := 0; n < 12; n++ {
		wg.Add(1)
		go func() { defer wg.Done(); failures <- f.service.PresetClick(ctx, "getting_started") }()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		must(t, err)
	}
	must(t, f.db.QueryRow(`SELECT clicks FROM lmm_assistant.preset_stats WHERE preset_id='getting_started'`).Scan(&after))
	if after != before+12 {
		t.Fatal("lost aggregate click", before, after)
	}
	if err = f.service.PresetClick(ctx, "unknown"); !errors.Is(err, access.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestHistoryPostgresInvalidTurnsLeaveNoEmptyShellAndRoleCannotInstall(t *testing.T) {
	f := historyDatabase(t)
	ctx := context.Background()
	if _, err := f.service.RecordCompletedTurn(ctx, f.token, 0, "empty", "Question", ""); !errors.Is(err, access.ErrInvalid) {
		t.Fatal(err)
	}
	page, err := f.service.List(ctx, f.token, 0, false, 30, "")
	must(t, err)
	if len(page.Conversations) != 0 {
		t.Fatal("failed turn left an empty conversation")
	}
	for _, sql := range []string{`CREATE TABLE public.unauthorized_history_fixture(x int)`, `UPDATE lmm_assistant.contract SET version=2`, `DELETE FROM lmm_assistant.turn_receipts WHERE false`} {
		if _, err := f.db.ExecContext(ctx, sql); err == nil {
			t.Fatal("restricted runtime acquired installation/receipt mutation authority")
		}
	}
}
