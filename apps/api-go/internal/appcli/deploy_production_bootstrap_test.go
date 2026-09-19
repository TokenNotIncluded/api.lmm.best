package appcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

type bootstrapRunner struct {
	plan                                       productionReleasePlan
	help, response, badEntry, wrongTransaction string
	helpError, createError, exists             bool
	completedBeforeError                       bool
	creates                                    []string
}

func (r *bootstrapRunner) Run(_ context.Context, command productionCommand) ([]byte, error) {
	if command.Name != commandSSH || len(command.Args) < 4 {
		return nil, errors.New("unexpected non-SSH command")
	}
	a := command.Args[3:]
	path := a[len(a)-1]
	paths := defaultProductionPaths()
	root := filepath.Join(paths.WorkRoot, r.plan.DeploymentID)
	marker := "format=1\ndeployment_id=" + r.plan.DeploymentID + "\nrole=target\ncreated_at_utc=2026-09-19T00:00:00Z\n"
	transaction := "format=1\ndeployment_id=" + r.plan.DeploymentID + "\nstatus=ACTIVE\n"
	if r.wrongTransaction != "" {
		transaction = r.wrongTransaction
	}
	switch a[0] {
	case "readlink":
		return []byte(backendGoName + "\n"), nil
	case "sha256sum":
		return []byte(r.plan.GoRollback.PayloadSHA256 + "  " + path + "\n"), nil
	case "stat":
		if path == r.badEntry {
			return []byte("0:a1ff:1:100\n"), nil
		}
		if path == filepath.Join("/usr/bin", backendGoName) {
			return []byte("0:81ed:1\n"), nil
		}
		if path == filepath.Join(root, productionWorkspaceMarker) {
			return []byte(fmt.Sprintf("0:8180:1:%d\n", len(marker))), nil
		}
		if path == filepath.Join(paths.TransactionLock, productionTransactionMarker) {
			return []byte(fmt.Sprintf("0:8180:1:%d\n", len(transaction))), nil
		}
		if !r.exists {
			return nil, errors.New("missing")
		}
		return []byte("0:41c0:2:4096\n"), nil
	case "test":
		if a[1] == "-d" && !r.exists {
			return nil, errors.New("absent")
		}
		if path == r.badEntry {
			return nil, errors.New("activation evidence exists")
		}
		return nil, nil
	case "head":
		if path == filepath.Join(root, productionWorkspaceMarker) {
			return []byte(marker), nil
		}
		if path == filepath.Join(paths.TransactionLock, productionTransactionMarker) {
			return []byte(transaction), nil
		}
	case productionOperatorBinary:
		if len(a) == 2 && a[1] == "help" {
			if r.helpError {
				return nil, errors.New("SSH disconnected during help")
			}
			return []byte(r.help), nil
		}
		if len(a) == 7 && a[2] == "production" && a[3] == "workspace" && a[4] == "create" && a[6] == r.plan.DeploymentID {
			r.creates = append(r.creates, a[1])
			if r.createError {
				if r.completedBeforeError {
					r.exists = true
				}
				return nil, errors.New("SSH disconnected after dispatch")
			}
			return []byte(r.response), nil
		}
	}
	return nil, fmt.Errorf("unexpected bootstrap command: %v", a)
}

func bootstrapFixture(t *testing.T) (*productionReleaseRuntime, *bootstrapRunner) {
	t.Helper()
	plan := productionReleasePlan{TargetAlias: productionTargetAlias, DeploymentID: "bootstrap-test"}
	plan.GoRollback.PayloadSHA256 = strings.Repeat("a", 64)
	paths := defaultProductionPaths()
	response, err := json.Marshal(productionWorkspaceResult{DeploymentID: plan.DeploymentID, Workspace: filepath.Join(paths.WorkRoot, plan.DeploymentID), Transaction: paths.TransactionLock, TransactionSet: true})
	if err != nil {
		t.Fatal(err)
	}
	runner := &bootstrapRunner{plan: plan, help: "Usage:\n  /usr/bin/lmm-api-deploy build|frontend|production ...\n", response: string(response)}
	return &productionReleaseRuntime{runner: runner}, runner
}

func TestBootstrapSelectsVerifiedCurrentOrLegacyNativeCLI(t *testing.T) {
	for _, protocol := range []string{"operator", "deploy"} {
		t.Run(protocol, func(t *testing.T) {
			runtime, runner := bootstrapFixture(t)
			if protocol == "deploy" {
				runner.help = "Usage:\n  lmm-api deploy production plan [signed candidate and rollback inputs]\n"
			}
			result, err := runtime.bootstrapRemoteWorkspace(context.Background(), runner.plan)
			if err != nil || !result.TransactionSet || len(runner.creates) != 1 || runner.creates[0] != protocol {
				t.Fatalf("result=%+v creates=%v err=%v", result, runner.creates, err)
			}
		})
	}
}

func TestBootstrapRejectsInvalidProviderOrHelpBeforeMutation(t *testing.T) {
	for _, name := range []string{"unknown help", "ambiguous help", "transport", "unsafe provider", "missing digest"} {
		t.Run(name, func(t *testing.T) {
			runtime, runner := bootstrapFixture(t)
			switch name {
			case "unknown help":
				runner.help = "Usage: something else"
			case "ambiguous help":
				runner.help += "lmm-api deploy production plan [signed candidate and rollback inputs]\n"
			case "transport":
				runner.helpError = true
			case "unsafe provider":
				runner.badEntry = "/usr/bin/" + backendGoName
			case "missing digest":
				runner.plan.GoRollback.PayloadSHA256 = ""
			}
			if _, err := runtime.bootstrapRemoteWorkspace(context.Background(), runner.plan); err == nil || len(runner.creates) != 0 {
				t.Fatalf("creates=%v err=%v", runner.creates, err)
			}
		})
	}
}

func TestBootstrapNeverRetriesAnAmbiguousCreate(t *testing.T) {
	runtime, runner := bootstrapFixture(t)
	runner.createError = true
	if _, err := runtime.bootstrapRemoteWorkspace(context.Background(), runner.plan); err == nil || len(runner.creates) != 1 {
		t.Fatalf("creates=%v err=%v", runner.creates, err)
	}
	// A subsequent invocation can resume only when exact native evidence exists.
	runner.exists = true
	result, err := runtime.bootstrapRemoteWorkspace(context.Background(), runner.plan)
	if err != nil || !result.TransactionSet || len(runner.creates) != 1 {
		t.Fatalf("result=%+v creates=%v err=%v", result, runner.creates, err)
	}
}

func TestBootstrapReconcilesLostSuccessfulCreateResponse(t *testing.T) {
	runtime, runner := bootstrapFixture(t)
	runner.createError, runner.completedBeforeError = true, true
	result, err := runtime.bootstrapRemoteWorkspace(context.Background(), runner.plan)
	if err != nil || !result.TransactionSet || len(runner.creates) != 1 {
		t.Fatalf("result=%+v creates=%v err=%v", result, runner.creates, err)
	}
}

func TestBootstrapRejectsMalformedCreateResponse(t *testing.T) {
	for _, response := range []string{"not json", `{}`, `{"deployment_id":"other","transaction_active":true}`, `{"deployment_id":"bootstrap-test","workspace":"/tmp/wrong","transaction_active":true}`} {
		runtime, runner := bootstrapFixture(t)
		runner.response = response
		if _, err := runtime.bootstrapRemoteWorkspace(context.Background(), runner.plan); err == nil || len(runner.creates) != 1 {
			t.Fatalf("response=%s creates=%v err=%v", response, runner.creates, err)
		}
	}
}

func TestBootstrapResumeRejectsForeignOrActivatedWorkspace(t *testing.T) {
	paths := defaultProductionPaths()
	for _, name := range []string{"foreign lock", "symlink ancestor", "manifest", "status", "symlink marker"} {
		t.Run(name, func(t *testing.T) {
			runtime, runner := bootstrapFixture(t)
			runner.exists = true
			root := filepath.Join(paths.WorkRoot, runner.plan.DeploymentID)
			switch name {
			case "foreign lock":
				runner.wrongTransaction = "format=1\ndeployment_id=other\nstatus=ACTIVE\n"
			case "symlink ancestor":
				runner.badEntry = paths.WorkRoot
			case "manifest":
				runner.badEntry = filepath.Join(root, "state", productionManifestFilename)
			case "status":
				runner.badEntry = filepath.Join(root, "state", productionStatusFilename)
			case "symlink marker":
				runner.badEntry = filepath.Join(root, productionWorkspaceMarker)
			}
			if _, err := runtime.bootstrapRemoteWorkspace(context.Background(), runner.plan); err == nil || len(runner.creates) != 0 {
				t.Fatalf("creates=%v err=%v", runner.creates, err)
			}
		})
	}
}
