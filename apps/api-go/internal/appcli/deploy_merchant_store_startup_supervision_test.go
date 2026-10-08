//go:build linux

package appcli

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type merchantStartupStateFixture struct {
	state map[string]string
	after func()
}

func (fixture *merchantStartupStateFixture) Run(_ context.Context, command productionCommand) ([]byte, error) {
	if command.Name != commandSystemctl || len(command.Args) != 4 || command.Args[0] != "show" || command.Args[1] != "lmm-api.service" || command.Args[2] != "--all" {
		return nil, errors.New("fixture refuses non-read-only startup state commands")
	}
	var output strings.Builder
	for key, value := range fixture.state {
		fmt.Fprintf(&output, "%s=%s\n", key, value)
	}
	if fixture.after != nil {
		fixture.after()
	}
	return []byte(output.String()), nil
}

func testMerchantStartupSupervision(t *testing.T, timeout string) (*productionRuntime, *merchantStartupStateFixture, productionMerchantStoreCapsule, *uint64, string) {
	t.Helper()
	invocation := strings.Repeat("a", 32)
	fixture := &merchantStartupStateFixture{state: map[string]string{"InvocationID": invocation, "ActiveState": "activating", "SubState": "start-pre", "MainPID": "0", "ControlPID": strconv.Itoa(os.Getpid()), "TimeoutStartUSec": timeout, "InactiveExitTimestampMonotonic": "1000000"}}
	now := uint64(1000000)
	runtime := &productionRuntime{runner: fixture, startupMonotonicUS: func() (uint64, error) { return now, nil }, startupWait: func(ctx context.Context, duration time.Duration) error {
		now += uint64(duration / time.Microsecond)
		return ctx.Err()
	}}
	return runtime, fixture, productionMerchantStoreCapsule{Service: "lmm-api.service"}, &now, invocation
}

func TestMerchantStartupSystemdTimespan(t *testing.T) {
	for text, want := range map[string]uint64{"1min 30s": 90_000000, "4min": 240_000000, "1.000001s": 1000001, "500.123ms": 500123, "1h 2min 3s 4ms 5us": 3723_004005} {
		got, err := merchantStoreSystemdTimespanUS(text)
		if err != nil || got != want {
			t.Fatalf("%q: got %d, %v; want %d", text, got, err, want)
		}
	}
	for _, text := range []string{"", "0", "0s", "infinity", "+1s", "-1s", "1.0000001s", "1.1us", "1e3s", "18446744073709551615s", "18446744073709551615us 1us"} {
		if _, err := merchantStoreSystemdTimespanUS(text); !errors.Is(err, errMerchantStartupBudget) {
			t.Fatalf("invalid finite budget %q accepted: %v", text, err)
		}
	}
}

func TestMerchantStartupBudgetCannotRenewOrExtend(t *testing.T) {
	runtime, fixture, capsule, now, invocation := testMerchantStartupSupervision(t, "4min")
	*now += 68_000000
	ctx, cancel, err := runtime.bindMerchantStoreStartupBudget(context.Background(), capsule, invocation, 0, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	parent := ctx.Value(merchantStoreStartupBudgetKey{}).(merchantStoreStartupBudget)
	if parent.deadlineUS != 241_000000 {
		t.Fatalf("elapsed initialization renewed deadline: %+v", parent)
	}
	*now += 10_000000
	fixture.state["TimeoutStartUSec"] = "1h"
	child, childCancel, err := runtime.bindMerchantStoreStartupBudget(context.Background(), capsule, invocation, parent.deadlineUS, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	defer childCancel()
	if child.Value(merchantStoreStartupBudgetKey{}).(merchantStoreStartupBudget).deadlineUS != parent.deadlineUS {
		t.Fatal("holder refreshed its deadline")
	}
	short, shortCancel, err := runtime.bindMerchantStoreStartupBudget(context.Background(), capsule, invocation, 81_000000, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	defer shortCancel()
	if short.Value(merchantStoreStartupBudgetKey{}).(merchantStoreStartupBudget).deadlineUS != 81_000000 {
		t.Fatal("holder ignored an earlier parent deadline")
	}
	fixture.state["TimeoutStartUSec"] = "1min 30s"
	if err := runtime.merchantStoreStartupGuard(ctx, capsule, invocation, true); err != nil {
		t.Fatal(err)
	}
	*now = 91_000000
	if err := runtime.merchantStoreStartupGuard(ctx, capsule, invocation, true); !errors.Is(err, errMerchantStartupDeadline) {
		t.Fatalf("actual shorter manager budget not enforced: %v", err)
	}
}

func TestMerchantStartupRejectsMissingForeignAndOverflowBudget(t *testing.T) {
	for _, change := range []struct{ key, value string }{{"TimeoutStartUSec", "infinity"}, {"TimeoutStartUSec", "0"}, {"TimeoutStartUSec", "garbage"}, {"InactiveExitTimestampMonotonic", "0"}, {"InactiveExitTimestampMonotonic", "2000000"}, {"InactiveExitTimestampMonotonic", "18446744073709551614"}, {"InactiveExitTimestampMonotonic", "+1000000"}, {"InvocationID", strings.Repeat("b", 32)}, {"ControlPID", "1"}} {
		t.Run(change.key+"="+change.value, func(t *testing.T) {
			runtime, fixture, capsule, _, invocation := testMerchantStartupSupervision(t, "4min")
			fixture.state[change.key] = change.value
			if _, cancel, err := runtime.bindMerchantStoreStartupBudget(context.Background(), capsule, invocation, math.MaxUint64, os.Getpid()); err == nil {
				cancel()
				t.Fatal("invalid actual manager budget accepted")
			}
		})
	}
	runtime, fixture, capsule, _, invocation := testMerchantStartupSupervision(t, "4min")
	delete(fixture.state, "TimeoutStartUSec")
	if _, cancel, err := runtime.bindMerchantStoreStartupBudget(context.Background(), capsule, invocation, 0, os.Getpid()); err == nil {
		cancel()
		t.Fatal("missing manager budget accepted")
	}
}

func TestMerchantStartupSlowInitializationOverSixtySeconds(t *testing.T) {
	runtime, _, capsule, now, invocation := testMerchantStartupSupervision(t, "4min")
	ctx, cancel, err := runtime.bindMerchantStoreStartupBudget(context.Background(), capsule, invocation, 0, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	probes := 0
	err = runtime.waitMerchantStoreStartupHolder(ctx, capsule, invocation, func(context.Context) error {
		probes++
		if *now < 69_000000 {
			return errors.New("not ready: private child error must not be logged")
		}
		return nil
	})
	if err != nil || probes <= 240 || *now != 69_000000 {
		t.Fatalf("slow initialization still uses sixty-second cutoff: probes=%d now=%d err=%v", probes, *now, err)
	}
}

func TestMerchantStartupExpiredBudgetNeverClaims(t *testing.T) {
	runtime, fixture, capsule, now, invocation := testMerchantStartupSupervision(t, "1min")
	ctx, cancel, err := runtime.bindMerchantStoreStartupBudget(context.Background(), capsule, invocation, 0, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	err = runtime.waitMerchantStoreStartupHolder(ctx, capsule, invocation, func(context.Context) error { return errors.New("secret DSN must not appear") })
	if !errors.Is(err, errMerchantStartupDeadline) || strings.Contains(err.Error(), "secret") || *now != 61_000000 {
		t.Fatalf("finite deadline did not fail closed safely: now=%d err=%v", *now, err)
	}
	claimed := false
	guard := func(ctx context.Context) error {
		return runtime.merchantStoreStartupGuard(ctx, capsule, invocation, true)
	}
	if err := merchantStoreGuardedStartupClaim(ctx, guard, func(context.Context, func(context.Context) error) error { claimed = true; return nil }); !errors.Is(err, errMerchantStartupDeadline) || claimed {
		t.Fatalf("late qualification claimed durable ownership: claimed=%v err=%v", claimed, err)
	}
	fixture.state["TimeoutStartUSec"] = "infinity"
	if _, _, err := runtime.bindMerchantStoreStartupBudget(context.Background(), capsule, invocation, math.MaxUint64, os.Getpid()); !errors.Is(err, errMerchantStartupBudget) {
		t.Fatalf("unbounded manager input accepted: %v", err)
	}
}

func TestMerchantStartupChangedTargetNeverClaims(t *testing.T) {
	for _, change := range []struct{ key, value string }{{"InvocationID", strings.Repeat("b", 32)}, {"ActiveState", "failed"}, {"MainPID", "123"}, {"ControlPID", "1"}, {"SubState", "start-post"}, {"InactiveExitTimestampMonotonic", "1000001"}} {
		t.Run(change.key, func(t *testing.T) {
			runtime, fixture, capsule, now, invocation := testMerchantStartupSupervision(t, "4min")
			ctx, cancel, err := runtime.bindMerchantStoreStartupBudget(context.Background(), capsule, invocation, 0, os.Getpid())
			if err != nil {
				t.Fatal(err)
			}
			defer cancel()
			// Qualification completed after the original sixty-second cutoff.
			*now += 68_000000
			fixture.state[change.key] = change.value
			claimed := false
			err = merchantStoreGuardedStartupClaim(ctx, func(ctx context.Context) error {
				return runtime.merchantStoreStartupGuard(ctx, capsule, invocation, true)
			}, func(context.Context, func(context.Context) error) error { claimed = true; return nil })
			if err == nil || claimed {
				t.Fatalf("invalid target claimed: %v, %v", err, claimed)
			}
		})
	}
}

func TestMerchantStartupDeadlineDuringStateReadAndCancellation(t *testing.T) {
	runtime, fixture, capsule, now, invocation := testMerchantStartupSupervision(t, "4min")
	ctx, cancel, err := runtime.bindMerchantStoreStartupBudget(context.Background(), capsule, invocation, 0, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	fixture.after = func() { *now = 241_000000 }
	if err := runtime.merchantStoreStartupGuard(ctx, capsule, invocation, true); !errors.Is(err, errMerchantStartupDeadline) {
		t.Fatalf("state read crossed deadline: %v", err)
	}
	fixture.after = nil
	*now = 2_000000
	cancel()
	if err := runtime.merchantStoreStartupGuard(ctx, capsule, invocation, true); !errors.Is(err, errMerchantStartupCanceled) {
		t.Fatalf("canceled parent accepted: %v", err)
	}
}

func TestMerchantStartupPostCommitFailureDoesNotPublish(t *testing.T) {
	runtime, fixture, capsule, _, invocation := testMerchantStartupSupervision(t, "4min")
	ctx, cancel, err := runtime.bindMerchantStoreStartupBudget(context.Background(), capsule, invocation, 0, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	committed, published := false, false
	err = merchantStoreGuardedStartupClaim(ctx, func(ctx context.Context) error {
		return runtime.merchantStoreStartupGuard(ctx, capsule, invocation, true)
	}, func(ctx context.Context, guard func(context.Context) error) error {
		if err := guard(ctx); err != nil {
			return err
		}
		committed = true
		fixture.state["ActiveState"] = "failed"
		return nil
	})
	if err == nil {
		published = true
	}
	if !committed || published || !errors.Is(err, errMerchantStartupTarget) {
		t.Fatalf("post-commit failure published authority: committed=%v published=%v err=%v", committed, published, err)
	}
}

func TestMerchantStartupSuccessfulParentExitAllowsBoundedHandoff(t *testing.T) {
	runtime, fixture, capsule, now, invocation := testMerchantStartupSupervision(t, "4min")
	ctx, cancel, err := runtime.bindMerchantStoreStartupBudget(context.Background(), capsule, invocation, 0, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	budget := ctx.Value(merchantStoreStartupBudgetKey{}).(merchantStoreStartupBudget)
	fixture.state["ControlPID"] = "0"
	if err := runtime.merchantStoreStartupGuard(ctx, capsule, invocation, false); !errors.Is(err, errMerchantStartupTarget) {
		t.Fatalf("pre-handshake parent disappearance accepted: %v", err)
	}
	// The holder has successfully written the exact live-owner Held reply.
	*budget.handoff = true
	for _, stage := range []string{"start-pre", "start", "start-post", "running"} {
		fixture.state["SubState"] = stage
		if stage == "start-post" || stage == "running" {
			fixture.state["MainPID"] = strconv.Itoa(os.Getpid())
		}
		if stage == "running" {
			fixture.state["ActiveState"] = "active"
		}
		if err := runtime.merchantStoreStartupGuard(ctx, capsule, invocation, false); err != nil {
			t.Fatalf("legal post-handshake phase %s rejected: %v", stage, err)
		}
		if err := runtime.merchantStoreStartupGuard(ctx, capsule, invocation, true); !errors.Is(err, errMerchantStartupTarget) {
			t.Fatalf("new Held/claim accepted after parent exit in %s: %v", stage, err)
		}
	}
	fixture.state["ActiveState"] = "failed"
	if err := runtime.merchantStoreStartupGuard(ctx, capsule, invocation, false); !errors.Is(err, errMerchantStartupTarget) {
		t.Fatalf("terminal handoff accepted: %v", err)
	}
	fixture.state["ActiveState"] = "active"
	*now = budget.deadlineUS
	if err := runtime.merchantStoreStartupGuard(ctx, capsule, invocation, false); !errors.Is(err, errMerchantStartupDeadline) {
		t.Fatalf("handoff renewed its deadline: %v", err)
	}
}

func TestMerchantStartupConnectionCanceledBeforeItsProtocolTimeout(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := merchantStoreBoundConnection(ctx, client)
	defer stop()
	done := make(chan error, 1)
	go func() { var byte [1]byte; _, err := client.Read(byte[:]); done <- err }()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancellation left a successful handshake")
		}
	case <-time.After(time.Second):
		t.Fatal("canceled handshake remains blocked on the independent ninety-second timeout")
	}
}

type merchantStartupChildFixture struct {
	unit       string
	invocation string
	process    *exec.Cmd
	stopped    bool
}

func (fixture *merchantStartupChildFixture) Run(ctx context.Context, command productionCommand) ([]byte, error) {
	if command.Name != commandSystemctl || len(command.Args) < 2 || command.Args[1] != fixture.unit || ctx.Err() != nil {
		return nil, errors.New("fixture refuses unrelated child commands")
	}
	switch command.Args[0] {
	case "show":
		pid := fixture.process.Process.Pid
		return []byte(fmt.Sprintf("MainPID=%d\nExecMainPID=%d\nExecMainCode=0\nExecMainStatus=0\nActiveState=active\nSubState=running\nResult=success\nControlGroup=/owned-fixture\nRestart=no\nInvocationID=%s\n", pid, pid, fixture.invocation)), nil
	case "stop":
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 15*time.Second {
			return nil, errors.New("cleanup did not use an independent finite context")
		}
		fixture.stopped = true
		if err := fixture.process.Process.Signal(syscall.SIGTERM); err != nil {
			return nil, err
		}
		_ = fixture.process.Wait()
		return nil, nil
	}
	return nil, errors.New("fixture refuses other mutations")
}

func TestMerchantStartupOwnedCancellationChild(t *testing.T) {
	if os.Getenv("LMM_STARTUP_CANCELLATION_CHILD") != "1" {
		t.Skip("own child only")
	}
	for {
		time.Sleep(time.Second)
	}
}

func TestMerchantStartupCancellationOnlyStopsExactCreatedChild(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprintf("changed_generation=%v", changed), func(t *testing.T) {
			provider, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			args := []string{"-test.run=^TestMerchantStartupOwnedCancellationChild$"}
			child := exec.Command(provider, args...)
			child.Env = append(os.Environ(), "LMM_STARTUP_CANCELLATION_CHILD=1")
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if child.ProcessState == nil {
					_ = child.Process.Kill()
					_ = child.Wait()
				}
			}()
			digest, err := sha256File(provider)
			if err != nil {
				t.Fatal(err)
			}
			invocation := strings.Repeat("a", 32)
			capsule := productionMerchantStoreCapsule{DeploymentID: "startup-supervision-owned-test", Writer: &productionMerchantStoreWriterContract{Candidate: productionMerchantStoreWriterTarget{PayloadSHA256: digest}}}
			fixture := &merchantStartupChildFixture{unit: merchantStoreStartUnit(capsule.DeploymentID, invocation), invocation: strings.Repeat("b", 32), process: child}
			runtime := &productionRuntime{runner: fixture}
			holder := &merchantStoreCreatedStartupHolder{}
			if err := runtime.captureMerchantStoreStartupHolder(context.Background(), capsule, fixture.unit, provider, args, holder); err != nil {
				t.Fatal(err)
			}
			if changed {
				fixture.invocation = strings.Repeat("c", 32)
			}
			err = runtime.cancelMerchantStoreStartupHolder(capsule, invocation, holder)
			if changed {
				if err == nil || fixture.stopped {
					t.Fatalf("changed holder was stopped: err=%v stopped=%v", err, fixture.stopped)
				}
			} else if err != nil || !fixture.stopped || !errors.Is(merchantStoreStartupProcessGone(child.Process.Pid), os.ErrNotExist) {
				t.Fatalf("exact child termination not proved: err=%v stopped=%v", err, fixture.stopped)
			}
		})
	}
}
