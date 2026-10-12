// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
// PostgreSQL and OS-process evidence only. Identity and model completion are
// synthetic; these tests do not substitute for a billed Core model delegation.
package pgtest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/access"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/testkit"
)

type processHistoryConfig struct {
	DSN      string `json:"dsn"`
	Database string `json:"database"`
}

func TestA08HistoryProcessHelper(t *testing.T) {
	mode := os.Getenv("LMM_A08_HISTORY_PROCESS")
	if mode == "" {
		t.Skip("subprocess helper")
	}
	path := os.Getenv("LMM_A08_HISTORY_CONFIG")
	info, err := os.Lstat(path)
	if err != nil || !filepath.IsAbs(path) || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		t.Fatal("private synthetic database configuration required")
	}
	data, err := os.ReadFile(path)
	var config processHistoryConfig
	if err != nil || json.Unmarshal(data, &config) != nil {
		t.Fatal("invalid synthetic database configuration")
	}
	u, err := url.Parse(config.DSN)
	if err != nil || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") || !strings.HasPrefix(config.Database, "mk09_a08history_") || u.Path != "/"+config.Database {
		t.Fatal("a new local history fixture is required")
	}
	db, err := sql.Open("postgres", config.DSN)
	if err != nil {
		t.Fatal("could not open the synthetic database")
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var restricted bool
	if err := db.QueryRowContext(ctx, `SELECT NOT(rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls) FROM pg_roles WHERE rolname=current_user`).Scan(&restricted); err != nil || !restricted {
		t.Fatal("runtime history role is not restricted")
	}
	repo, err := assistant.NewPostgres(ctx, db, config.Database)
	must(t, err)
	identity := testkit.NewIdentity()
	token, other := testkit.Token("a08history"), testkit.Token("a08other")
	identity.Set(token, 1, 1)
	identity.Set(other, 2, 6)
	auth, err := access.New(identity)
	must(t, err)
	service, err := assistant.NewHistory(repo, auth, []byte(strings.Repeat("c", 32)))
	must(t, err)
	input := "Cookie: one=firstSyntheticSecret; two=secondSyntheticSecret"
	answer := `{"password":"thirdSyntheticSecret","safe":"answer"}`
	id, err := service.RecordCompletedTurn(ctx, token, 0, "durable-turn", input, answer)
	must(t, err)
	if mode == "write-and-exit" {
		os.Exit(23) // Neither SQL pool cleanup nor deferred application cleanup runs.
	}
	if mode != "replay" {
		t.Fatal("unknown fixture mode")
	}
	detail, err := service.Detail(ctx, token, id, 100)
	must(t, err)
	if len(detail.Messages) != 2 {
		t.Fatal("restarted process duplicated or lost a completed turn")
	}
	for _, message := range detail.Messages {
		for _, secret := range []string{"firstSyntheticSecret", "secondSyntheticSecret", "thirdSyntheticSecret"} {
			if strings.Contains(message.Content, secret) {
				t.Fatal("persisted history exposed a synthetic credential")
			}
		}
	}
	if _, err := service.Detail(ctx, other, id, 100); !errors.Is(err, access.ErrNotFound) {
		t.Fatal("a different owner could read the recovered transcript")
	}
	if _, err := service.RecordCompletedTurn(ctx, token, id, "durable-turn", "changed input", answer); !errors.Is(err, access.ErrConflict) {
		t.Fatal("restarted process accepted a changed duplicate")
	}
	identity.Revoke(token)
	if _, err := service.Detail(ctx, token, id, 100); !errors.Is(err, access.ErrUnauthorized) {
		t.Fatal("revoked credentials still read the recovered transcript")
	}
}

func TestA08HistorySurvivesActualProcessExitWithRestrictedPostgresRole(t *testing.T) {
	dsn := os.Getenv("LMM_MK09_TEST_DSN")
	base, err := url.Parse(dsn)
	if err != nil || dsn == "" || (base.Hostname() != "127.0.0.1" && base.Hostname() != "localhost") {
		t.Fatal("LMM_MK09_TEST_DSN must name a disposable local PostgreSQL instance")
	}
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal("could not open disposable test PostgreSQL")
	}
	t.Cleanup(func() { admin.Close() })
	role := fmt.Sprintf("mk09_a08_runtime_%d", time.Now().UnixNano())
	// Generated identifier and test-only password, never operator credentials.
	_, err = admin.Exec(`CREATE ROLE ` + role + ` LOGIN PASSWORD 'a08-synthetic-only' NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS`)
	must(t, err)
	t.Cleanup(func() {
		if _, err := admin.Exec(`DROP ROLE ` + role); err != nil {
			t.Error("could not remove the synthetic runtime role")
		}
	})
	db, _, name := database(t, admin, base, "a08history")
	_, err = db.Exec(assistant.Schema)
	must(t, err)
	_, err = db.Exec(`GRANT USAGE ON SCHEMA lmm_assistant TO ` + role + `;
GRANT SELECT ON lmm_assistant.contract TO ` + role + `;
GRANT SELECT,INSERT,UPDATE,DELETE ON lmm_assistant.records,lmm_assistant.conversations,lmm_assistant.history_messages,lmm_assistant.turn_receipts,lmm_assistant.preset_stats TO ` + role + `;
GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA lmm_assistant TO ` + role)
	must(t, err)
	u := *base
	u.Path, u.User = "/"+name, url.UserPassword(role, "a08-synthetic-only")
	dir := t.TempDir()
	must(t, os.Chmod(dir, 0700))
	path := filepath.Join(dir, "fixture.json")
	data, err := json.Marshal(processHistoryConfig{u.String(), name})
	must(t, err)
	must(t, os.WriteFile(path, data, 0600))
	executable, err := os.Executable()
	must(t, err)
	for _, mode := range []string{"write-and-exit", "replay"} {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		cmd := exec.CommandContext(ctx, executable, "-test.run=^TestA08HistoryProcessHelper$", "-test.count=1")
		cmd.Env = append(os.Environ(), "LMM_A08_HISTORY_PROCESS="+mode, "LMM_A08_HISTORY_CONFIG="+path)
		output, err := cmd.CombinedOutput()
		cancel()
		if mode == "write-and-exit" {
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 23 {
				t.Fatalf("writer process failed: %s", output)
			}
		} else if err != nil {
			t.Fatalf("restarted reader failed: %s", output)
		}
	}
	var conversations, messages, receipts int
	must(t, db.QueryRow(`SELECT (SELECT count(*) FROM lmm_assistant.conversations),(SELECT count(*) FROM lmm_assistant.history_messages),(SELECT count(*) FROM lmm_assistant.turn_receipts)`).Scan(&conversations, &messages, &receipts))
	if conversations != 1 || messages != 2 || receipts != 1 {
		t.Fatal("process replay did not preserve one atomic completed turn")
	}
}
