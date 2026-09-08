package appcli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var controllerReceiptTemporarySuffix = regexp.MustCompile(`^[A-Za-z0-9]{12}$`)

// Stage small verified metadata atomically, never the backing archive or keys.
// Failed transfers remain at unique .partial paths for audit; they cannot
// occupy the immutable receipt path or prevent a later retry.
func (runtime *productionReleaseRuntime) stageControllerReceipt(ctx context.Context, plan productionReleasePlan, state productionReleaseControllerState, local, digest string, confirmation bool) error {
	name := controllerBackupReceiptName
	if confirmation {
		name = controllerBackupConfirmationName
	}
	if plan.Format != productionReleasePlanFormat || !plan.WithBackups || plan.BackupMode != "controller-only" || local != filepath.Join(plan.ControllerWorkspace, "state", name) || !productionSHA256Pattern.MatchString(digest) {
		return errors.New("controller receipt staging requires the selected plan and exact metadata path")
	}
	receipt, err := readControllerBackupReceipt(local, plan.ControllerBackupPublicKey, digest, uint32(os.Geteuid()))
	if err != nil {
		return err
	}
	purpose := "prepare"
	if confirmation {
		purpose = "confirm"
	}
	if receipt.Purpose != purpose || receipt.DeploymentID != plan.DeploymentID || receipt.PlanSHA256 != state.PlanSHA256 {
		return errors.New("controller receipt staging context differs from the frozen plan")
	}
	if err := validateControllerBackupFreshness(receipt, runtime.now()); err != nil {
		return err
	}
	remote := filepath.Join(state.RemoteWorkspace, "state", name)
	present, err := runtime.controllerReceiptDestination(ctx, plan.TargetAlias, remote, digest, confirmation)
	if err != nil || present {
		return err
	}
	prefix := remote + ".partial."
	output, err := runtime.ssh(ctx, plan.TargetAlias, 2*time.Minute, "mktemp", "--", prefix+"XXXXXXXXXXXX")
	if err != nil {
		return errors.New("could not allocate private remote receipt staging")
	}
	temporary := strings.TrimSpace(string(output))
	if !strings.HasPrefix(temporary, prefix) || !controllerReceiptTemporarySuffix.MatchString(strings.TrimPrefix(temporary, prefix)) {
		return errors.New("remote receipt temporary path escaped its exact namespace")
	}
	if err := runtime.verifyRemoteReceiptFile(ctx, plan.TargetAlias, temporary, ""); err != nil {
		return err
	}
	if err := runtime.scpTo(ctx, local, plan.TargetAlias, temporary); err != nil {
		return fmt.Errorf("receipt transfer interrupted; partial metadata retained: %w", err)
	}
	if err := runtime.verifyRemoteReceiptFile(ctx, plan.TargetAlias, temporary, digest); err != nil {
		return err
	}
	arguments := []string{"mv", "-T"}
	if !confirmation {
		arguments = append(arguments, "--no-clobber")
	}
	arguments = append(arguments, "--", temporary, remote)
	if _, err := runtime.ssh(ctx, plan.TargetAlias, 2*time.Minute, arguments...); err != nil {
		return err
	}
	return runtime.verifyRemoteReceiptFile(ctx, plan.TargetAlias, remote, digest)
}

func (runtime *productionReleaseRuntime) controllerReceiptDestination(ctx context.Context, alias, remote, digest string, replace bool) (bool, error) {
	if _, err := runtime.ssh(ctx, alias, 2*time.Minute, "test", "!", "-L", remote); err != nil {
		return false, errors.New("remote receipt destination is a link or cannot be inspected")
	}
	if _, err := runtime.ssh(ctx, alias, 2*time.Minute, "test", "-e", remote); err != nil {
		if _, absentErr := runtime.ssh(ctx, alias, 2*time.Minute, "test", "!", "-e", remote); absentErr != nil {
			return false, errors.New("remote receipt absence could not be proven")
		}
		return false, nil
	}
	if err := runtime.verifyRemoteReceiptFile(ctx, alias, remote, ""); err != nil {
		return false, err
	}
	current, err := runtime.remoteFileSHA256(ctx, alias, remote)
	if err != nil {
		return false, err
	}
	if current == digest {
		return true, nil
	}
	if !replace {
		return false, errors.New("immutable remote receipt already contains different evidence; use a new deployment ID")
	}
	return false, nil
}

func (runtime *productionReleaseRuntime) verifyRemoteReceiptFile(ctx context.Context, alias, path, digest string) error {
	// %f includes the numeric file type and permission bits (0100600 = 8180).
	// Unlike %F it is locale-independent and treats empty mktemp files as
	// regular files too; newly allocated receipts are necessarily empty.
	output, err := runtime.ssh(ctx, alias, 2*time.Minute, "stat", "-c", "%u:%f:%h", "--", path)
	if err != nil || strings.TrimSpace(string(output)) != "0:8180:1" {
		return errors.New("remote receipt is not a private root-owned single-link regular file")
	}
	if digest != "" {
		actual, err := runtime.remoteFileSHA256(ctx, alias, path)
		if err != nil || actual != digest {
			return errors.New("remote receipt digest mismatch")
		}
	}
	return nil
}

func checkInitialControllerReceiptRetry(local string, plan productionReleasePlan, expectedDigest string, now time.Time) error {
	if _, err := os.Lstat(local); errors.Is(err, os.ErrNotExist) {
		if expectedDigest != "" {
			return errors.New("persisted initial receipt is missing; restore the original evidence or use a new deployment ID")
		}
		return nil
	} else if err != nil {
		return err
	}
	receipt, err := readControllerBackupReceipt(local, plan.ControllerBackupPublicKey, expectedDigest, uint32(os.Geteuid()))
	if err != nil {
		return err
	}
	if receipt.Purpose != "prepare" || receipt.DeploymentID != plan.DeploymentID {
		return errors.New("initial receipt has a different deployment identity or purpose")
	}
	if err := validateControllerBackupFreshness(receipt, now); err != nil {
		return fmt.Errorf("initial receipt cannot be renewed in place; create a new deployment ID and collect newly bound evidence: %w", err)
	}
	return nil
}
