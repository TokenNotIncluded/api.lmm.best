package appcli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const refundTaskDrainCapability = "REFUND_TASK_DRAIN_CAPABILITY"

// A package-owned capability is trusted only after the installed package,
// release metadata and running executable match the frozen transaction.
func (runtime *productionRuntime) trackedRefundWriter(ctx context.Context, manifest *productionManifest) (bool, error) {
	marker := filepath.Join(filepath.Dir(runtime.paths.GoRevisionFile), refundTaskDrainCapability)
	info, err := os.Lstat(marker)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o644 {
		return false, errors.New("unsafe refund drain capability")
	}
	owner, links, ok := deploymentFileOwnership(info)
	if !ok || owner != runtime.requiredOwnerUID || links != 1 {
		return false, errors.New("unowned refund drain capability")
	}
	data, err := os.ReadFile(marker)
	if err != nil || !bytes.Equal(data, []byte("v1\n")) {
		return false, errors.New("unknown refund drain capability")
	}
	owned, err := runtime.runner.Run(ctx, productionCommand{Name: commandPacman, Args: []string{"-Qqo", "--", marker}})
	if err != nil || strings.TrimSpace(string(owned)) != productionAURPackageName {
		return false, errors.New("refund drain capability has no verified package owner")
	}
	matched := false
	for _, rollback := range []bool{true, false} {
		if runtime.verifyTransitionInstalled(ctx, manifest.Go, rollback, true) == nil && runtime.verifyTransitionCLI(ctx, manifest.Go, rollback) == nil {
			matched = true
			break
		}
	}
	if !matched {
		return false, errors.New("refund drain provider does not match the transaction")
	}
	g := manifest.BillingGate
	if g == nil || g.GoPID <= 1 {
		return false, errors.New("refund drain writer identity missing")
	}
	state, err := runtime.billingUnitState(ctx, runtime.paths.Service)
	if err != nil {
		return false, errors.New("refund drain writer invocation changed")
	}
	if state["ActiveState"] == "active" {
		if state["MainPID"] != strconv.Itoa(g.GoPID) || state["InvocationID"] != g.GoInvocationID {
			return false, errors.New("refund drain writer PID changed")
		}
		installed, err := sha256File(filepath.Join(filepath.Dir(runtime.paths.InstalledBinary), backendGoName))
		if err != nil {
			return false, err
		}
		readHash := func(pid int) (string, error) { return sha256File(fmt.Sprintf("/proc/%d/exe", pid)) }
		if runtime.billingExecutableSHA256 != nil {
			readHash = runtime.billingExecutableSHA256
		}
		running, err := readHash(g.GoPID)
		if err != nil || running != installed {
			return false, errors.New("running refund drain writer differs from installed binary")
		}
	} else if err := cleanBillingUnitExit(state, g.GoPID); err != nil {
		return false, err
	}
	return true, nil
}
