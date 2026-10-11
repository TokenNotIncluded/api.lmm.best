package deploycli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSchemaMaintenanceSchedulerNativeProcessFixture(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("fixture must exercise the ordinary controller UID")
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	lock, err := acquireSchemaMaintenanceSchedulerLock(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close() })
	before, err := lock.Stat()
	if err != nil {
		t.Fatal(err)
	}
	child := func(expected string) {
		t.Helper()
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		command := exec.Command(executable, "-test.run=^TestSchemaMaintenanceSchedulerNativeProcessFixtureChild$", "-test.v")
		command.Env = append(os.Environ(), "LMM_SCHEMA_SCHEDULER_FIXTURE_ROOT="+root, "LMM_SCHEMA_SCHEDULER_FIXTURE_EXPECT="+expected)
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("child %s: %v: %s", expected, err, out)
		}
	}
	child("blocked")
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	child("acquired")
	after, err := os.Stat(filepath.Join(root, "state", "schema-scheduler.lock"))
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("persistent lock inode changed: %v", err)
	}
}

func TestSchemaMaintenanceSchedulerNativeProcessFixtureChild(t *testing.T) {
	root := os.Getenv("LMM_SCHEMA_SCHEDULER_FIXTURE_ROOT")
	if root == "" {
		t.Skip("fixture subprocess only")
	}
	lock, err := acquireSchemaMaintenanceSchedulerLock(root)
	if os.Getenv("LMM_SCHEMA_SCHEDULER_FIXTURE_EXPECT") == "blocked" {
		if err == nil {
			_ = lock.Close()
			t.Fatal("second native process acquired held complete-phase lock")
		}
		return
	}
	if err != nil {
		t.Fatalf("released lock unavailable to next native process: %v", err)
	}
	_ = lock.Close()
}
