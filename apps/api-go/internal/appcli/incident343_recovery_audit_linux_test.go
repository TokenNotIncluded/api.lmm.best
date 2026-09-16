//go:build linux

package appcli

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
)

var auditManifest343 = []byte("{\"deployment_id\":\"test\",\"billing_closed\":true}\n")
var auditStatus343 = []byte("{\"phase\":\"ROLLBACK_REQUIRED\"}\n")

func failedAudit343(t *testing.T) (string, string) {
	t.Helper()
	state := t.TempDir()
	if err := os.Chmod(state, 0700); err != nil {
		t.Fatal(err)
	}
	original, err := prepareIncident343RecoveryAudit(state, auditManifest343, auditStatus343, os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"before-manifest.json": auditManifest343, "before-status.json": auditStatus343,
		"before-schema.database.dump": {},
	} {
		if err := os.WriteFile(filepath.Join(original, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return state, original
}

func TestIncident343AuditFreshAndSinglePreservingRetry(t *testing.T) {
	state, original := failedAudit343(t)
	before, err := os.Stat(original)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := prepareIncident343RecoveryAudit(state, auditManifest343, auditStatus343, os.Geteuid())
	if err != nil || retry == original || filepath.Base(retry) != "incident-343-schema-recovery-backup-retry-1" {
		t.Fatalf("retry=%s err=%v", retry, err)
	}
	after, err := os.Stat(original)
	if err != nil || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("original audit directory changed")
	}
	for name, want := range map[string][]byte{
		"before-manifest.json": auditManifest343, "before-status.json": auditStatus343,
		"before-schema.database.dump": {},
	} {
		got, err := os.ReadFile(filepath.Join(original, name))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatal("original evidence changed", name)
		}
	}
	info, err := os.Stat(retry)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("retry directory is not private")
	}
	if _, err := prepareIncident343RecoveryAudit(state, auditManifest343, auditStatus343, os.Geteuid()); err == nil {
		t.Fatal("replayed the backup retry")
	}
}

func TestIncident343AuditRejectsUnsafePreviousEvidence(t *testing.T) {
	for _, kind := range []string{"manifest-changed", "status-changed", "nonempty-backup", "completed-backup", "schema-created", "unknown-file", "missing-file", "symlink-file", "hardlink-file", "fifo", "world-readable-file", "world-readable-directory", "symlink-directory", "wrong-owner"} {
		t.Run(kind, func(t *testing.T) {
			state, original := failedAudit343(t)
			owner := os.Geteuid()
			manifest := append([]byte(nil), auditManifest343...)
			status := append([]byte(nil), auditStatus343...)
			backup := filepath.Join(original, "before-schema.database.dump")
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			switch kind {
			case "manifest-changed":
				manifest = []byte("{\"deployment_id\":\"other\"}\n")
			case "status-changed":
				status = []byte("{\"phase\":\"AWAITING_CONFIRMATION\"}\n")
			case "nonempty-backup":
				must(os.WriteFile(backup, []byte("PGDMP"), 0600))
			case "completed-backup":
				must(os.WriteFile(filepath.Join(original, "database.sha256"), []byte("receipt"), 0600))
			case "schema-created":
				must(os.WriteFile(filepath.Join(original, "schema-created.json"), []byte("{}"), 0600))
			case "unknown-file":
				must(os.WriteFile(filepath.Join(original, "unknown"), nil, 0600))
			case "missing-file":
				must(os.Remove(backup))
			case "symlink-file":
				must(os.Remove(backup))
				must(os.Symlink(filepath.Join(t.TempDir(), "missing"), backup))
			case "hardlink-file":
				must(os.Link(backup, filepath.Join(t.TempDir(), "alias")))
			case "fifo":
				must(os.Remove(backup))
				must(syscall.Mkfifo(backup, 0600))
			case "world-readable-file":
				must(os.Chmod(backup, 0644))
			case "world-readable-directory":
				must(os.Chmod(original, 0755))
			case "symlink-directory":
				moved := filepath.Join(state, "moved")
				must(os.Rename(original, moved))
				must(os.Symlink(moved, original))
			case "wrong-owner":
				owner++
			}
			if _, err := prepareIncident343RecoveryAudit(state, manifest, status, owner); err == nil {
				t.Fatal("unsafe previous evidence accepted")
			}
			if _, err := os.Lstat(filepath.Join(state, "incident-343-schema-recovery-backup-retry-1")); !os.IsNotExist(err) {
				t.Fatal("retry directory created despite failed checks")
			}
		})
	}
}

func TestIncident343AuditRejectsInvalidIdentityAndOrphanRetry(t *testing.T) {
	for _, kind := range []string{"invalid-json", "oversize", "negative-owner", "orphan-retry", "symlink-state"} {
		t.Run(kind, func(t *testing.T) {
			state := t.TempDir()
			if err := os.Chmod(state, 0700); err != nil {
				t.Fatal(err)
			}
			manifest := auditManifest343
			owner := os.Geteuid()
			switch kind {
			case "invalid-json":
				manifest = []byte("not JSON")
			case "oversize":
				manifest = bytes.Repeat([]byte(" "), (1<<20)+1)
			case "negative-owner":
				owner = -1
			case "orphan-retry":
				if err := os.Mkdir(filepath.Join(state, "incident-343-schema-recovery-backup-retry-1"), 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink-state":
				link := filepath.Join(t.TempDir(), "state")
				if err := os.Symlink(state, link); err != nil {
					t.Fatal(err)
				}
				state = link
			}
			if _, err := prepareIncident343RecoveryAudit(state, manifest, auditStatus343, owner); err == nil {
				t.Fatal("invalid identity or orphan retry accepted")
			}
		})
	}
}

func TestIncident343AuditConcurrentRetryIsExclusive(t *testing.T) {
	state, _ := failedAudit343(t)
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < cap(results); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := prepareIncident343RecoveryAudit(state, auditManifest343, auditStatus343, os.Geteuid())
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("expected exactly one exclusive retry, got %d", successes)
	}
}
