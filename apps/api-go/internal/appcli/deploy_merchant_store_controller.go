package appcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"time"
)

func (runtime *productionReleaseRuntime) previewRemoteMerchantStoreWriters(ctx context.Context, plan productionReleasePlan, state productionReleaseControllerState) error {
	if !plan.GoChanged && plan.MerchantStoreWriter == nil {
		return nil
	}
	if err := validateMerchantStoreWriterPlan(plan); err != nil {
		return err
	}
	if err := validateMerchantStoreWriterContract(plan.MerchantStoreWriter); err != nil {
		return err
	}
	provider, err := runtime.remoteCandidateCommand(ctx, plan, state)
	if err != nil {
		return err
	}
	for _, rollback := range []bool{false, true} {
		arguments := []string{provider, "operator", "production", "writer-check", "--workspace", state.RemoteWorkspace,
			"--release-plan", filepath.Join(state.RemoteWorkspace, "staging", productionReleasePlanFilename), "--release-plan-sha256", state.PlanSHA256}
		expected := plan.MerchantStoreWriter.Candidate
		if rollback {
			arguments = append(arguments, "--rollback-target")
			expected = plan.MerchantStoreWriter.Rollback
		}
		body, err := runtime.ssh(ctx, plan.TargetAlias, 2*time.Minute, arguments...)
		if err != nil {
			return errors.New("staged candidate or retained merchant writer status qualification failed")
		}
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		var actual productionMerchantStoreWriterTarget
		if decoder.Decode(&actual) != nil || decoder.Decode(&struct{}{}) != io.EOF || actual != expected {
			return errors.New("remote merchant writer qualification differs from the immutable artifact/status contract")
		}
	}
	return nil
}
