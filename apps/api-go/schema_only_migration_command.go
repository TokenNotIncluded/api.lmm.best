package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/internal/appcli"
	"github.com/LIghtJUNction/api.lmm.best/internal/deploymentfence"
	"github.com/LIghtJUNction/api.lmm.best/model"
)

// This worker is called only by the dedicated native maintenance controller.
// It deliberately never enters InitResources or reads a mutable .env file.
func runSchemaOnlyMigrationCommand(args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("migrate --schema-only", flag.ContinueOnError)
	set.SetOutput(stderr)
	path := set.String("maintenance-owner", "", "exact native schema-maintenance owner file")
	digest := set.String("maintenance-owner-sha256", "", "SHA256 of the exact canonical owner bytes")
	if err := set.Parse(args); err != nil || set.NArg() != 0 || *path == "" || *digest == "" {
		return appcli.ExitUsage
	}
	owner, body, err := deploymentfence.ReadSchemaMaintenanceOwner(*path, *digest)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "schema-only migration: maintenance owner binding unavailable")
		return appcli.ExitError
	}
	// The immutable owner binds the schema worker to the same reviewed API
	// payload as the native controller, not whichever binary is on PATH.
	executable, err := os.Open("/proc/self/exe")
	if err != nil {
		return appcli.ExitError
	}
	hash := sha256.New()
	_, hashErr := io.Copy(hash, executable)
	closeErr := executable.Close()
	if hashErr != nil || closeErr != nil || hex.EncodeToString(hash.Sum(nil)) != owner.ProviderSHA256 {
		_, _ = fmt.Fprintln(stderr, "schema-only migration: actual worker executable differs from native owner")
		return appcli.ExitError
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	db, err := model.OpenSchemaOnlyMigrationDatabase(ctx)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "schema-only migration: dedicated PostgreSQL connection unavailable")
		return appcli.ExitError
	}
	pool, err := db.DB()
	if err != nil {
		return appcli.ExitError
	}
	defer pool.Close()
	if err := model.ApplySchemaOnlyMigration(ctx, db, owner, body); err != nil {
		_, _ = fmt.Fprintf(stderr, "schema-only migration failed: %v\n", err)
		return appcli.ExitError
	}
	_, _ = fmt.Fprintln(stdout, "PostgreSQL schema-only maintenance transaction committed; application data migrations were not run")
	return appcli.ExitOK
}
