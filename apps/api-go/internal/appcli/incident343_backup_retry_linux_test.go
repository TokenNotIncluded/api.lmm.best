//go:build linux

package appcli

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func retryAuditFixture(t *testing.T) (string, []byte, []byte) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "audit")
	m, s := []byte("{\"id\":\"incident\"}\n"), []byte("{\"phase\":\"ROLLBACK_REQUIRED\"}\n")
	if _, err := prepareIncident343BackupAudit(root, m, s, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"before-manifest.json": m, "before-status.json": s, "before-schema.database.dump": nil} {
		if err := os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root, m, s
}

func TestIncident343BackupRetryPreservesAllOriginalEvidence(t *testing.T) {
	r, m, s := retryAuditFixture(t)
	got, err := prepareIncident343BackupAudit(r, m, s, uint32(os.Geteuid()))
	if err != nil || got != filepath.Join(r, "after-backup-permission-fix") {
		t.Fatalf("%s %v", got, err)
	}
	for name, want := range map[string][]byte{"before-manifest.json": m, "before-status.json": s, "before-schema.database.dump": nil} {
		data, err := os.ReadFile(filepath.Join(r, name))
		if err != nil || string(data) != string(want) {
			t.Fatal("original evidence changed", name, err)
		}
	}
	if _, err := prepareIncident343BackupAudit(r, m, s, uint32(os.Geteuid())); err == nil {
		t.Fatal("second retry accepted")
	}
}

func TestIncident343BackupRetryRejectsProgressAndStateChanges(t *testing.T) {
	for _, name := range []string{"data", "checksum", "ddl", "manifest", "status", "missing", "extra", "public-file", "public-directory", "symlink", "hardlink", "fifo"} {
		t.Run(name, func(t *testing.T) {
			r, m, s := retryAuditFixture(t)
			dump := filepath.Join(r, "before-schema.database.dump")
			var err error
			switch name {
			case "data":
				err = os.WriteFile(dump, []byte("nonempty"), 0600)
			case "checksum":
				err = os.WriteFile(filepath.Join(r, "database.sha256"), []byte("digest"), 0600)
			case "ddl":
				err = os.WriteFile(filepath.Join(r, "schema-created.json"), []byte("{}"), 0600)
			case "manifest":
				m = []byte("changed")
			case "status":
				s = []byte("changed")
			case "missing":
				err = os.Remove(dump)
			case "extra":
				err = os.WriteFile(filepath.Join(r, "other"), nil, 0600)
			case "public-file":
				err = os.Chmod(dump, 0644)
			case "public-directory":
				err = os.Chmod(r, 0755)
			case "symlink":
				err = os.Remove(dump)
				if err == nil {
					err = os.Symlink(filepath.Join(r, "before-status.json"), dump)
				}
			case "hardlink":
				err = os.Link(dump, filepath.Join(filepath.Dir(r), "outside-link"))
			case "fifo":
				err = os.Remove(dump)
				if err == nil {
					err = syscall.Mkfifo(dump, 0600)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = prepareIncident343BackupAudit(r, m, s, uint32(os.Geteuid())); err == nil {
				t.Fatal("unsafe retry accepted")
			}
			if _, err = os.Lstat(filepath.Join(r, "after-backup-permission-fix")); !os.IsNotExist(err) {
				t.Fatal("unexpected retry directory")
			}
		})
	}
}

func TestIncident343BackupRetryRejectsDirectorySymlinkAndWrongOwner(t *testing.T) {
	r, m, s := retryAuditFixture(t)
	if _, err := prepareIncident343BackupAudit(r, m, s, uint32(os.Geteuid()+1)); err == nil {
		t.Fatal("wrong owner accepted")
	}
	link := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(r, link); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareIncident343BackupAudit(link, m, s, uint32(os.Geteuid())); err == nil {
		t.Fatal("directory symlink accepted")
	}
}
