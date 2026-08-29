//go:build !windows

package appcli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeFrontendPackageRunner struct {
	calls      []string
	failPrefix string
}

func (runner *fakeFrontendPackageRunner) Run(_ context.Context, name string, args ...string) error {
	call := filepath.Base(name) + " " + strings.Join(args, " ")
	runner.calls = append(runner.calls, call)
	if runner.failPrefix != "" && strings.HasPrefix(call, runner.failPrefix) {
		return errors.New("injected command failure")
	}
	return nil
}

type frontendPackageFixture struct {
	runtime frontendPackageRuntime
	runner  *fakeFrontendPackageRunner
	options frontendPackageActivateOptions
}

func newFrontendPackageFixture(t *testing.T) frontendPackageFixture {
	t.Helper()
	root := filepath.Join(t.TempDir(), "frontend")
	source := filepath.Join(t.TempDir(), "dist")
	if err := os.MkdirAll(filepath.Join(source, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("<!doctype html>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "assets", "app.js"), []byte("console.log('ok')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	revisionFile := filepath.Join(t.TempDir(), "REVISION")
	if err := os.WriteFile(revisionFile, []byte(strings.Repeat("a", 40)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &fakeFrontendPackageRunner{}
	return frontendPackageFixture{
		runner: runner,
		runtime: frontendPackageRuntime{
			runner: runner,
			now: func() time.Time {
				return time.Date(2026, 8, 29, 16, 0, 0, 0, time.UTC)
			},
			effectiveUID: func() int { return 0 },
		},
		options: frontendPackageActivateOptions{
			PackageVersion: "0.1.52-1", Root: root, Source: source,
			RevisionFile: revisionFile, Keep: 3,
		},
	}
}

func TestFrontendPackageActivatePublishesThroughManualState(t *testing.T) {
	fixture := newFrontendPackageFixture(t)
	state, err := fixture.runtime.activate(context.Background(), fixture.options)
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != "CONFIRMED" || state.Previous != "" {
		t.Fatalf("state=%#v", state)
	}
	current, err := currentFrontendRelease(fixture.options.Root)
	if err != nil {
		t.Fatal(err)
	}
	if current != state.Release {
		t.Fatalf("current=%q release=%q", current, state.Release)
	}
	wantCalls := []string{"nginx -t", "nginx -t", "systemctl reload nginx.service", "systemctl is-active --quiet nginx.service"}
	if fmt.Sprint(fixture.runner.calls) != fmt.Sprint(wantCalls) {
		t.Fatalf("commands=%v", fixture.runner.calls)
	}
	statePath, err := frontendPackageStatePath(fixture.options.Root, state.Release)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state mode=%#o", info.Mode().Perm())
	}
	second, err := fixture.runtime.activate(context.Background(), fixture.options)
	if err != nil || second.Phase != "CONFIRMED" {
		t.Fatalf("idempotent activation state=%#v error=%v", second, err)
	}
	if len(fixture.runner.calls) != len(wantCalls) {
		t.Fatalf("idempotent activation reran commands: %v", fixture.runner.calls)
	}
}

func TestFrontendPackageActivateFailureRequiresExplicitRollback(t *testing.T) {
	fixture := newFrontendPackageFixture(t)
	fixture.runner.failPrefix = "systemctl reload"
	state, err := fixture.runtime.activate(context.Background(), fixture.options)
	if err == nil {
		t.Fatal("activation unexpectedly succeeded")
	}
	statePath, pathErr := frontendPackageStatePath(fixture.options.Root, state.Release)
	if pathErr != nil {
		t.Fatal(pathErr)
	}
	persisted, readErr := readFrontendPackageState(statePath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if persisted.Phase != "ROLLBACK_REQUIRED" || persisted.Failure != "frontend-activation" {
		t.Fatalf("persisted=%#v", persisted)
	}
	current, currentErr := currentFrontendRelease(fixture.options.Root)
	if currentErr != nil || current != state.Release {
		t.Fatalf("automatic rollback occurred: current=%q error=%v", current, currentErr)
	}
	if _, retryErr := fixture.runtime.activate(context.Background(), fixture.options); retryErr == nil || !strings.Contains(retryErr.Error(), "explicit rollback") {
		t.Fatalf("retry error=%v", retryErr)
	}
}

func TestFrontendPackageActivatePreflightFailureDoesNotMutate(t *testing.T) {
	fixture := newFrontendPackageFixture(t)
	fixture.runner.failPrefix = "nginx -t"
	state, err := fixture.runtime.activate(context.Background(), fixture.options)
	if err == nil {
		t.Fatal("preflight unexpectedly succeeded")
	}
	statePath, pathErr := frontendPackageStatePath(fixture.options.Root, state.Release)
	if pathErr != nil {
		t.Fatal(pathErr)
	}
	persisted, readErr := readFrontendPackageState(statePath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if persisted.Phase != "FAILED_PREARM" {
		t.Fatalf("persisted=%#v", persisted)
	}
	if _, currentErr := os.Lstat(filepath.Join(fixture.options.Root, "current")); !errors.Is(currentErr, os.ErrNotExist) {
		t.Fatalf("preflight changed current link: %v", currentErr)
	}
}

func TestFrontendPackageActivateRejectsUnsafeInputsAndNonRoot(t *testing.T) {
	fixture := newFrontendPackageFixture(t)
	fixture.options.PackageVersion = "../bad"
	if _, err := fixture.runtime.activate(context.Background(), fixture.options); err == nil {
		t.Fatal("unsafe package version was accepted")
	}
	fixture = newFrontendPackageFixture(t)
	if err := os.WriteFile(fixture.options.RevisionFile, []byte("not-a-revision\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.runtime.activate(context.Background(), fixture.options); err == nil || !strings.Contains(err.Error(), "revision") {
		t.Fatalf("invalid revision error=%v", err)
	}
	fixture = newFrontendPackageFixture(t)
	fixture.runtime.effectiveUID = func() int { return 1000 }
	if _, err := fixture.runtime.activate(context.Background(), fixture.options); err == nil || !strings.Contains(err.Error(), "root") {
		t.Fatalf("non-root error=%v", err)
	}
}

func TestParseFrontendPackageActivateOptionsRequiresVersion(t *testing.T) {
	if _, err := parseFrontendPackageActivateOptions(nil, os.Stderr); err == nil || !strings.Contains(err.Error(), "package-version") {
		t.Fatalf("missing version error=%v", err)
	}
	if _, err := parseFrontendPackageActivateOptions([]string{"--package-version", "0.1.52-1", "extra"}, os.Stderr); err == nil || !strings.Contains(err.Error(), "positional") {
		t.Fatalf("positional error=%v", err)
	}
}
