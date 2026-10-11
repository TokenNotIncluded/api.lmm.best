package main

import (
	"bytes"
	"io"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/internal/appcli"
)

func TestSchemaOnlyWorkerRejectsIncompleteOrOrdinaryMigrationFlagsBeforeDatabase(t *testing.T) {
	t.Setenv("SQL_DSN", "must-not-open-this-database")
	for _, args := range [][]string{nil, {"--apply"}, {"--maintenance-owner", "/run/owner.json"}, {"--maintenance-owner-sha256", "a"}, {"unexpected"}} {
		if code := runSchemaOnlyMigrationCommand(args, io.Discard, io.Discard); code != appcli.ExitUsage {
			t.Fatalf("args %q: code=%d, want usage rejection", args, code)
		}
	}
	var stderr bytes.Buffer
	if code := runSchemaOnlyMigrationCommand([]string{"--maintenance-owner", "/run/nonexistent-owner", "--maintenance-owner-sha256", "invalid"}, io.Discard, &stderr); code != appcli.ExitError {
		t.Fatalf("invalid binding code=%d", code)
	}
	if bytes.Contains(stderr.Bytes(), []byte("must-not-open-this-database")) {
		t.Fatal("database credentials were exposed")
	}
}
