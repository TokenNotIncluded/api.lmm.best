//go:build linux

package appcli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func incident343AuditFixture(t *testing.T) (*productionRuntime, productionWorkspace, productionManifest, productionStatus, string) {
	t.Helper()
	r := &productionRuntime{requiredOwnerUID: uint32(os.Geteuid())}
	w := productionWorkspace{stateDir: t.TempDir()}
	m := productionManifest{DeploymentID: "test-incident343", ExpectedVersion: "0.2.51"}
	s := productionStatus{DeploymentID: m.DeploymentID, Phase: "ROLLBACK_REQUIRED"}
	root, err := r.prepareIncident343Audit(w, m, s)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "before-schema.database.dump"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	return r, w, m, s, root
}

func TestIncident343AuditResumesOnlyZeroByteBackupOnce(t *testing.T) {
	r, w, m, s, original := incident343AuditFixture(t)
	before, err := os.ReadFile(filepath.Join(original, "before-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	retry, err := r.prepareIncident343Audit(w, m, s)
	if err != nil || retry != filepath.Join(original, "backup-retry-1") {
		t.Fatalf("retry=%q error=%v", retry, err)
	}
	for _, dir := range []string{original, retry} {
		after, err := os.ReadFile(filepath.Join(dir, "before-manifest.json"))
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("checkpoint identity changed", err)
		}
	}
	data, err := os.ReadFile(filepath.Join(original, "before-schema.database.dump"))
	if err != nil || len(data) != 0 {
		t.Fatal("failed original dump not preserved", err)
	}
	if _, err := os.Lstat(filepath.Join(retry, "before-schema.database.dump")); !os.IsNotExist(err) {
		t.Fatal("resume fabricated a new backup", err)
	}
	if _, err := r.prepareIncident343Audit(w, m, s); err == nil {
		t.Fatal("another retry was accepted")
	}
}

func TestIncident343AuditRejectsProgressOrChangedEvidence(t *testing.T) {
	for _, name := range []string{"complete-backup", "schema-marker", "checksum", "changed-manifest", "changed-status", "nonempty-dump", "missing-dump", "extra-file", "public-directory", "public-file", "symlink", "hardlink", "wrong-owner"} {
		t.Run(name, func(t *testing.T) {
			r, w, m, s, root := incident343AuditFixture(t)
			mutate := func(path string, content []byte) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, path), content, 0600); err != nil {
					t.Fatal(err)
				}
			}
			switch name {
			case "complete-backup", "nonempty-dump":
				mutate("before-schema.database.dump", []byte("PGDMP-partial-or-complete"))
			case "schema-marker":
				mutate("schema-created.json", []byte("{}"))
			case "checksum":
				mutate("database.sha256", []byte("digest"))
			case "changed-manifest":
				m.ExpectedVersion = "0.2.52"
			case "changed-status":
				s.Phase = "OBSERVING"
			case "missing-dump":
				if err := os.Remove(filepath.Join(root, "before-schema.database.dump")); err != nil {
					t.Fatal(err)
				}
			case "extra-file":
				mutate("unknown", nil)
			case "public-directory":
				if err := os.Chmod(root, 0755); err != nil {
					t.Fatal(err)
				}
			case "public-file":
				if err := os.Chmod(filepath.Join(root, "before-status.json"), 0644); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				path := filepath.Join(root, "before-schema.database.dump")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("/dev/null", path); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(filepath.Join(root, "before-schema.database.dump"), filepath.Join(w.stateDir, "linked")); err != nil {
					t.Fatal(err)
				}
			case "wrong-owner":
				r.requiredOwnerUID++
			}
			if _, err := r.prepareIncident343Audit(w, m, s); err == nil {
				t.Fatal("unsafe checkpoint accepted")
			}
			if _, err := os.Lstat(filepath.Join(root, "backup-retry-1")); !os.IsNotExist(err) {
				t.Fatal("unsafe checkpoint created retry state", err)
			}
		})
	}
}
