package appcli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConservativeGoMemoryOverrideIsPreserved(t *testing.T) {
	cases := []struct {
		name, config string
		allowed      bool
	}{
		{"existing mitigation", "[Service]\nEnvironment=\"TMPDIR=/var/lib/lmm-api-go/tmp\"\nEnvironment=\"GOMEMLIMIT=192MiB\"\n", true},
		{"packaged ceiling", "[Service]\nEnvironment=\"GOMEMLIMIT=256MiB\"\n", true},
		{"larger heap", "[Service]\nEnvironment=\"GOMEMLIMIT=512MiB\"\n", false},
		{"cgroup change", "[Service]\nMemoryMax=512M\nEnvironment=\"GOMEMLIMIT=192MiB\"\n", false},
		{"duplicate", "[Service]\nEnvironment=\"GOMEMLIMIT=192MiB\"\nEnvironment=\"GOMEMLIMIT=128MiB\"\n", false},
		{"extra action", "[Service]\nEnvironment=\"GOMEMLIMIT=192MiB\"\nExecStart=/unreviewed\n", false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			p := filepath.Join(root, "95-memory-mitigation.conf")
			if err := os.WriteFile(p, []byte(test.config), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := validateMemoryOverrides(root); (err == nil) != test.allowed {
				t.Fatalf("preflight allowed=%v, err=%v", test.allowed, err)
			}
			if err := retireKnownMemoryOverrides(root); (err == nil) != test.allowed {
				t.Fatalf("retire allowed=%v, err=%v", test.allowed, err)
			}
			data, err := os.ReadFile(p)
			if err != nil || string(data) != test.config {
				t.Fatal("custom protection was modified")
			}
		})
	}
}

func TestPublicServicePhaseNeverPublishesPrivateFailure(t *testing.T) {
	root := t.TempDir()
	runtime := &productionRuntime{paths: productionPaths{FrontendRoot: root}, now: func() time.Time { return time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC) }}
	privateRoot := t.TempDir()
	workspace := productionWorkspace{id: "release-test", statusPath: filepath.Join(privateRoot, "status.json")}
	if err := runtime.writeStatus(workspace, productionStatus{Phase: "ROLLBACK_REQUIRED", Failure: "private-db-password", Reason: "internal configuration path"}); err != nil {
		t.Fatal(err)
	}
	private, _ := os.ReadFile(workspace.statusPath)
	if !strings.Contains(string(private), "private-db-password") {
		t.Fatal("private audit lost diagnostics")
	}
	data, err := os.ReadFile(filepath.Join(root, publicServiceStatusFilename))
	if err != nil {
		t.Fatal(err)
	}
	var status publicServiceStatus
	if err := json.Unmarshal(data, &status); err != nil {
		t.Fatal(err)
	}
	if status.State != "recovering" || status.EstimatedRecoveryAt != nil || status.Message != "" {
		t.Fatalf("unexpected public status: %+v", status)
	}
	if strings.Contains(string(data), "failure") || strings.Contains(string(data), "reason") || strings.Contains(string(data), "private-db-password") || strings.Contains(string(data), "internal configuration path") {
		t.Fatal("private diagnostic fields escaped")
	}
}

func TestPublicServiceEstimateRemainsBoundToDeployment(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	until := now.Add(time.Hour)
	runtime := &productionRuntime{paths: productionPaths{FrontendRoot: root}, now: func() time.Time { return now }}
	initial := publicServiceStatus{State: "maintenance", Service: "LMM Best API", DeploymentID: "one", UpdatedAt: now, EstimatedRecoveryAt: &until, Message: "Planned update"}
	if err := writePublicServiceStatus(root, initial); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"one", "two"} {
		if err := runtime.publishServicePhase(id, "MIGRATING"); err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(filepath.Join(root, publicServiceStatusFilename))
		var status publicServiceStatus
		if err := json.Unmarshal(data, &status); err != nil {
			t.Fatal(err)
		}
		if (status.EstimatedRecoveryAt != nil) != (id == "one") {
			t.Fatal("estimate crossed deployment boundary")
		}
		if status.Message != "" {
			t.Fatal("old phase explanation survived a phase change")
		}
	}
}

func TestPublicServiceStatusRejectsSymlinkAndUnknownState(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, publicServiceStatusFilename)); err != nil {
		t.Fatal(err)
	}
	status := publicServiceStatus{State: "maintenance", Service: "LMM Best", DeploymentID: "one", UpdatedAt: time.Now()}
	if err := writePublicServiceStatus(root, status); err == nil {
		t.Fatal("symlink accepted")
	}
	data, _ := os.ReadFile(outside)
	if string(data) != "preserve" {
		t.Fatal("outside file changed")
	}
	status.State = "arbitrary"
	if err := writePublicServiceStatus(t.TempDir(), status); err == nil {
		t.Fatal("unknown state accepted")
	}
}
