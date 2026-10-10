package deploycli

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitDeploymentPlanBindsIndependentToolAndBackend(t *testing.T) {
	plan := testProductionReleasePlan(t, t.TempDir())
	plan.GoCandidate.DeployEngineSHA256 = strings.Repeat("9", 64)
	plan.OperatorBinary = productionReleaseFilePlan{Path: filepath.Join(plan.ControllerWorkspace, deployEngineName), SHA256: plan.GoCandidate.DeployEngineSHA256}
	if err := validateProductionReleasePlan(plan); err != nil {
		t.Fatal(err)
	}
	state := productionReleaseControllerState{RemoteWorkspace: "/var/lib/lmm-api-go-deploy/work/split-test"}
	rt := &productionReleaseRuntime{}
	args := rt.productionApplyArguments(plan, state)
	field := func(name string) string {
		for i, arg := range args {
			if arg == name && i+1 < len(args) {
				return args[i+1]
			}
		}
		return ""
	}
	if field("--probe-binary") != filepath.Join(state.RemoteWorkspace, "staging", backendGoName) || field("--operator-binary") != productionRemoteEnginePath(plan, state) {
		t.Fatalf("mixed executable roles: %v", args)
	}
	if !strings.Contains(strings.Join(args, " "), productionRemoteEnginePath(plan, state)+" operator production apply") {
		t.Fatalf("activation used backend: %v", args)
	}
	for _, change := range []struct {
		name   string
		mutate func(*productionReleasePlan)
	}{
		{"backend as tool", func(p *productionReleasePlan) { p.OperatorBinary = p.ProbeBinary }},
		{"wrong digest", func(p *productionReleasePlan) { p.OperatorBinary.SHA256 = strings.Repeat("0", 64) }},
		{"missing declaration", func(p *productionReleasePlan) { p.GoCandidate.DeployEngineSHA256 = "" }},
		{"invalid rollback tool", func(p *productionReleasePlan) { p.GoRollback.DeployEngineSHA256 = "wrong" }},
		{"tool on web package", func(p *productionReleasePlan) { p.WebCandidate.DeployEngineSHA256 = strings.Repeat("8", 64) }},
	} {
		t.Run(change.name, func(t *testing.T) {
			changed := plan
			change.mutate(&changed)
			if validateProductionReleasePlan(changed) == nil {
				t.Fatal("invalid executable binding accepted")
			}
		})
	}
}

type deploymentInventoryRunner struct {
	listing   string
	err       error
	extracted bool
}

func (r *deploymentInventoryRunner) Run(_ context.Context, c productionCommand) ([]byte, error) {
	if c.Name != commandBsdtar {
		return nil, errors.New("unexpected command")
	}
	if c.Args[0] == "-tf" {
		return []byte(r.listing), r.err
	}
	r.extracted = true
	return []byte("tool"), nil
}
func TestDeploymentInventoryErrorsDoNotBecomeLegacyFallback(t *testing.T) {
	for _, tc := range []struct {
		name, listing string
		err           error
		wantError     bool
	}{
		{"legacy absence", "usr/bin/lmm-api-go\n", nil, false},
		{"inventory error", "", errors.New("cannot read"), true},
		{"duplicate", deployEnginePackageMember + "\n" + deployEnginePackageMember + "\n", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &deploymentInventoryRunner{listing: tc.listing, err: tc.err}
			body, err := readOptionalDeployEngine(context.Background(), r, "fixture", false)
			if (err != nil) != tc.wantError || body != nil || r.extracted {
				t.Fatalf("unsafe fallback: body=%q err=%v extracted=%v", body, err, r.extracted)
			}
		})
	}
}
