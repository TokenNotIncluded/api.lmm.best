//go:build linux

package appcli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIncident343StoppedStateDistinguishesFailedFromRunning(t *testing.T) {
	failed := "LoadState=loaded\nMainPID=0\nControlPID=0\nActiveState=failed\nSubState=failed\nControlGroup=\n"
	inactive := strings.ReplaceAll(failed, "ActiveState=failed\nSubState=failed", "ActiveState=inactive\nSubState=dead")
	for _, good := range []string{failed, inactive, strings.TrimSuffix(failed, "\n")} {
		if err := parseIncident343StoppedState([]byte(good)); err != nil {
			t.Fatal(err)
		}
	}
	for _, bad := range []string{
		"", strings.Repeat("x", 8193),
		strings.Replace(failed, "MainPID=0", "MainPID=21", 1),
		strings.Replace(failed, "ControlPID=0", "ControlPID=21", 1),
		strings.Replace(failed, "MainPID=0", "MainPID=00", 1),
		strings.Replace(failed, "LoadState=loaded", "LoadState=not-found", 1),
		strings.Replace(failed, "ControlGroup=\n", "", 1),
		strings.Replace(failed, "ControlPID=0\n", "", 1),
		strings.Replace(failed, "ControlGroup=", "ControlGroup=/system.slice/lmm-api.service", 1),
		strings.Replace(failed, "ActiveState=failed", "ActiveState=active", 1),
		strings.Replace(failed, "SubState=failed", "SubState=start-post", 1),
		failed + "MainPID=0\n", failed + "unknown=value\n", failed + "garbage\n",
	} {
		if err := parseIncident343StoppedState([]byte(bad)); err == nil {
			t.Fatal("accepted missing, ambiguous or running-unit evidence")
		}
	}
}

type incident343StopVerifierTest struct {
	called  bool
	failure error
}

func (r *incident343StopVerifierTest) Run(context.Context, productionCommand) ([]byte, error) {
	return nil, errors.New("must not fall back")
}
func (r *incident343StopVerifierTest) VerifyIncident343Stopped(context.Context, string) error {
	r.called = true
	return r.failure
}

func TestIncident343StoppedVerifierFailureCannotFallBack(t *testing.T) {
	want := errors.New("kernel evidence unavailable")
	runner := &incident343StopVerifierTest{failure: want}
	runtime := &productionRuntime{runner: runner}
	if err := runtime.verifyIncident343Stopped(context.Background()); !errors.Is(err, want) || !runner.called {
		t.Fatal("stopped verifier failure bypassed")
	}
	runner.failure = nil
	if err := runtime.verifyIncident343Stopped(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestIncident343ForwardAuditRejectsAdditionalProgress(t *testing.T) {
	for _, mutation := range []string{"extra", "symlink", "world-readable", "missing", "public-directory"} {
		t.Run(mutation, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "audit")
			if err := os.Mkdir(directory, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, "before-status.json")
			if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			runtime := &productionRuntime{requiredOwnerUID: os.Geteuid()}
			if err := runtime.requireIncident343Entries(directory, []string{"before-status.json"}); err != nil {
				t.Fatal(err)
			}
			var err error
			switch mutation {
			case "extra":
				err = os.WriteFile(filepath.Join(directory, "schema-created.json"), nil, 0600)
			case "symlink":
				if err = os.Remove(path); err == nil {
					err = os.Symlink("/dev/null", path)
				}
			case "world-readable":
				err = os.Chmod(path, 0644)
			case "missing":
				err = os.Remove(path)
			case "public-directory":
				err = os.Chmod(directory, 0755)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := runtime.requireIncident343Entries(directory, []string{"before-status.json"}); err == nil {
				t.Fatal("unsafe progressed audit accepted")
			}
		})
	}
}
