package appcli

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type merchantStoreHookAuthorityFixture struct {
	runner      *fakeProductionRunner
	requests    int
	failRequest int
	failEnsure  bool
	failRelease bool
}

func (authority *merchantStoreHookAuthorityFixture) Qualify(_ context.Context, _ productionWorkspace, _ productionManifest, installed, rollback bool) error {
	role := "candidate"
	if rollback {
		role = "rollback"
	}
	if installed {
		role += "-installed"
	}
	authority.runner.events = append(authority.runner.events, "merchant-qualify:"+role)
	return nil
}
func (authority *merchantStoreHookAuthorityFixture) Ensure(context.Context, productionWorkspace, productionManifest) error {
	authority.runner.events = append(authority.runner.events, "merchant-fence:ensure")
	if authority.failEnsure {
		return errors.New("fixture holder did not establish shared ownership")
	}
	return nil
}
func (authority *merchantStoreHookAuthorityFixture) Request(_ context.Context, _ productionWorkspace, _ productionManifest, release bool) error {
	if release {
		authority.runner.events = append(authority.runner.events, "merchant-fence:release")
		if authority.failRelease {
			return errors.New("fixture same-owner CAS release failed")
		}
		return nil
	}
	authority.requests++
	authority.runner.events = append(authority.runner.events, "merchant-fence:check")
	if authority.requests == authority.failRequest {
		return errors.New("fixture actual shared session was lost")
	}
	return nil
}

func TestProductionMerchantStoreDefaultAuthorityRejectsLegacyBeforeStopOrPackageMutation(t *testing.T) {
	fixture := newProductionFixture(t)
	fixture.runtime.merchantStoreAuthority = nil // exactly the production constructor policy
	if _, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options); err == nil {
		t.Fatal("legacy unsealed GoChanged transaction was authorized")
	}
	for _, event := range fixture.runner.events {
		if event == "systemd-stop" || event == "systemd-start" || strings.HasPrefix(event, "paru-") || event == "migrate:--apply" {
			t.Fatalf("unqualified legacy provider reached mutation %q", event)
		}
	}
	if defaultProductionRuntime().merchantStoreAuthority != nil {
		t.Fatal("production constructor installed a replacement merchant authority")
	}
}

// This is a transaction hook-sequence component test. It does not claim that
// the historical fixture provider/package is a qualified merchant artifact.
func TestProductionMerchantStoreTransactionHookSequenceAndFinalRelease(t *testing.T) {
	fixture := newProductionFixture(t)
	fixture.options.WithBackups = false
	fixture.options.BackupDir = ""
	authority := &merchantStoreHookAuthorityFixture{runner: fixture.runner}
	fixture.runtime.merchantStoreAuthority = authority
	status, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options)
	if err != nil || status.Phase != "AWAITING_CONFIRMATION" {
		t.Fatalf("phase=%s error=%v", status.Phase, err)
	}
	if _, err := fixture.runtime.confirm(context.Background(), fixture.workspace); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(fixture.runner.events, "\n")
	for _, required := range []string{"merchant-qualify:candidate", "merchant-qualify:rollback", "merchant-fence:ensure", "merchant-qualify:candidate-installed", "merchant-fence:release"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("missing lifecycle hook %s", required)
		}
	}
	for index, event := range fixture.runner.events {
		if event == "paru-go" || event == "systemd-start" {
			if index == 0 || fixture.runner.events[index-1] != "merchant-fence:check" {
				t.Fatalf("%s did not immediately reprove live owner: events=%v", event, fixture.runner.events)
			}
		}
	}
	if authority.requests < 8 {
		t.Fatalf("drain/stop/package/start/reopen/observation/confirmation hooks were omitted: checks=%d", authority.requests)
	}
	if fixture.runner.events[len(fixture.runner.events)-1] != "merchant-fence:release" {
		t.Fatal("normal finalization did not end with same-owner release")
	}
}

func TestProductionMerchantStoreLostAuthorityBlocksBeforeRelevantMutation(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		ensure    bool
		request   int
		forbidden string
	}{
		{"holder startup", true, 0, "systemd-stop"},
		{"before drain", false, 1, "systemd-stop"},
		{"before stop", false, 2, "systemd-stop"},
		{"before package", false, 3, "paru-go"},
		{"before candidate start", false, 4, "systemd-start"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			fixture := newProductionFixture(t)
			fixture.runtime.merchantStoreAuthority = &merchantStoreHookAuthorityFixture{runner: fixture.runner, failEnsure: scenario.ensure, failRequest: scenario.request}
			if _, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options); err == nil {
				t.Fatal("lost holder was accepted")
			}
			for _, event := range fixture.runner.events {
				if event == scenario.forbidden {
					t.Fatalf("lost authority reached %s", event)
				}
			}
		})
	}
}
