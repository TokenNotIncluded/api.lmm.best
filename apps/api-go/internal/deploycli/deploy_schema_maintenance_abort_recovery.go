package deploycli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Abort authorization is provided only by the original frozen native daemon.
// A missing local owner file or an empty database owner set proves no history.
func (h *productionSchemaMaintenanceHost) checkAbortNeverClaimed(ctx context.Context, checkNeverClaimed func(context.Context) error, requireLease bool) error {
	if h == nil || h.runtime == nil || h.validate == nil || checkNeverClaimed == nil {
		return errors.New("schema abort lacks its original frozen never-claimed native authority")
	}
	if requireLease {
		if err := h.check(ctx); err != nil {
			return err
		}
	} else if err := h.validate(ctx); err != nil {
		return err
	}
	if err := h.runtime.merchantStorePrivateDirectory(filepath.Join(h.binding.Workspace, "state"), false); err != nil {
		return err
	}
	// Even zero-byte/unsafe/dangling intent objects are permanent evidence that
	// this path cannot classify the transition as NEVER_CLAIMED. The primary
	// daemon additionally proves its serialized historical phase and PG state.
	for _, name := range []string{"schema-claim-intent.json", "schema-owner.json", "schema-worker-intent.json", "schema-worker-result.json", "schema-worker-output.log", "schema-outcome.json", "owner-released.json", "recovery-binding.json", "recovery-startup-profile.json", "recovery-hook-intent.json", "recovery-hook-bound.json", "recovery-start-intent.json", "recovery-started.json", "host-finished.json"} {
		if _, err := os.Lstat(filepath.Join(h.binding.Workspace, "state", name)); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("schema abort refuses possibly claimed or post-schema evidence at %s", name)
		}
	}
	return checkNeverClaimed(ctx)
}

func validateSchemaMaintenanceAbortOldCapsule(c productionMerchantStoreCapsule, host productionSchemaMaintenanceOriginPlan, plan productionSchemaMaintenancePlan, paths productionPaths) error {
	if err := validateMerchantStoreCapsule(c, paths); err != nil {
		return err
	}
	if c.Format != 1 || c.StartupPolicy != "per-start" || c.Host != host.Host || filepath.Join(c.Root, "capsule.json") != host.OldCapsule.Path || c.Candidate != host.Recovery.Candidate || c.Rollback != host.Recovery.Rollback || c.Writer.RequiredCapability != plan.RequiredCapability || c.Writer.RequiredCapability > 7 || c.Writer.Role != plan.Role {
		return errors.New("schema abort changed its original ordinary retained provider or startup authority")
	}
	schema := *c.ExistingSchemaContract
	schema.StartupSHA256, schema.SignedUnitSHA256 = "", ""
	if schema != plan.PreSchema {
		return errors.New("schema abort old capsule does not bind the unchanged complete pre-DDL catalog")
	}
	return nil
}

func (h *productionSchemaMaintenanceHost) abortOldCapsule(ctx context.Context, plan productionSchemaMaintenancePlan, preSHA string, binding productionSchemaMaintenanceAbortBinding, checkNeverClaimed func(context.Context) error, finishedReadOnly bool) (productionMerchantStoreCapsule, int, string, error) {
	var c productionMerchantStoreCapsule
	if h == nil || h.runtime == nil || h.binding.TransitionID != plan.TransitionID || h.binding.PlanSHA256 != preSHA {
		return c, -1, "", errors.New("schema abort changed its original local preplan authority")
	}
	raw, err := canonicalProductionSchemaMaintenanceAbortBinding(binding, plan, preSHA, h.runtime.paths)
	if err != nil {
		return c, -1, "", err
	}
	actual, err := h.runtime.readExistingSchemaSealedFile(filepath.Join(h.binding.Workspace, "state", "abort-binding.json"), true)
	if err != nil || !bytes.Equal(raw, actual) {
		return c, -1, "", errors.New("schema abort lacks its exact immutable independent abort binding")
	}
	digest := startupContentSHA256(raw)
	if finishedReadOnly {
		if _, err := h.checkAbortReadyAuthority(ctx, plan, preSHA, binding, checkNeverClaimed); err != nil {
			return c, -1, "", err
		}
	} else if err := h.checkAbortNeverClaimed(ctx, checkNeverClaimed, true); err != nil {
		return c, -1, "", err
	}
	index := -1
	for i, origin := range plan.Origins {
		if origin.Host == h.binding.Host {
			index = i
		}
	}
	if index < 0 {
		return c, -1, "", errors.New("schema abort host is not one original fixed origin")
	}
	origin := binding.Origins[index]
	if origin.OldCapsule.Path != h.binding.CapsulePath || origin.OldCapsule.SHA256 != h.binding.CapsuleSHA256 || origin.GuardianBinding.Path != h.binding.GuardianBindingPath || origin.GuardianBinding.SHA256 != h.binding.GuardianBindingSHA256 {
		return c, -1, "", errors.New("schema abort replaced its original retained capsule or guardian")
	}
	for _, ref := range []productionReleaseFilePlan{origin.Prepared, origin.StoppedCapture, origin.GuardianBinding} {
		body, err := h.runtime.readExistingSchemaSealedFile(ref.Path, true)
		if err != nil || startupContentSHA256(body) != ref.SHA256 {
			return c, -1, "", errors.New("schema abort original preparation, stop or guardian evidence changed")
		}
	}
	prepared, err := h.runtime.loadSchemaMaintenanceAbortPrepared(origin.Prepared.Path, origin.Prepared.SHA256, plan, preSHA)
	if err != nil || prepared.Host != h.binding.Host || prepared.OldCapsule != origin.OldCapsule || prepared.StoppedCapture != origin.StoppedCapture || prepared.GuardianBinding != origin.GuardianBinding {
		return c, -1, "", errors.New("schema abort preparation differs from its exact original host binding")
	}
	c, err = h.runtime.loadMerchantStoreCapsule(origin.OldCapsule.Path, origin.OldCapsule.SHA256)
	if err != nil {
		return c, -1, "", err
	}
	if err := validateSchemaMaintenanceAbortOldCapsule(c, plan.Origins[index], plan, h.runtime.paths); err != nil {
		return c, -1, "", err
	}
	return c, index, digest, nil
}

type productionSchemaMaintenanceAbortStartIntent struct {
	Format               string                                 `json:"format"`
	TransitionID         string                                 `json:"transition_id"`
	PrePlanSHA256        string                                 `json:"pre_plan_sha256"`
	Host                 string                                 `json:"host"`
	AbortBindingSHA256   string                                 `json:"abort_binding_sha256"`
	PreparedSHA256       string                                 `json:"prepared_sha256"`
	OldCapsule           productionReleaseFilePlan              `json:"old_capsule"`
	StoppedCaptureSHA256 string                                 `json:"stopped_capture_sha256"`
	Stopped              productionSchemaMaintenanceHostCapture `json:"stopped"`
	StartedUTC           time.Time                              `json:"started_utc"`
}

func (h *productionSchemaMaintenanceHost) requireNewAbortIntent(name string) error {
	if h == nil || h.runtime == nil || (name != "abort-start-intent.json" && name != "abort-finish-intent.json" && name != "abort-host-finished.json") {
		return errors.New("schema abort intent path is not fixed")
	}
	if err := h.runtime.merchantStorePrivateDirectory(filepath.Join(h.binding.Workspace, "state"), false); err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Join(h.binding.Workspace, "state", name)); err == nil {
		return errors.New("schema abort intent already exists; inspect without replay")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (h *productionSchemaMaintenanceHost) readAbortStartIntent(plan productionSchemaMaintenancePlan, preSHA string, binding productionSchemaMaintenanceAbortBinding, index int, bindingSHA string) (productionSchemaMaintenanceAbortStartIntent, string, error) {
	var intent productionSchemaMaintenanceAbortStartIntent
	raw, err := h.runtime.readExistingSchemaSealedFile(filepath.Join(h.binding.Workspace, "state", "abort-start-intent.json"), true)
	if err != nil || len(raw) > 32768 {
		return intent, "", errors.New("schema abort start lacks its exact original one-shot intent")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&intent) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return intent, "", errors.New("schema abort start intent is malformed")
	}
	canonical, err := json.MarshalIndent(intent, "", "  ")
	origin := binding.Origins[index]
	stored, stopErr := h.readCapture("writer-stopped.json")
	stopRaw, rawErr := h.runtime.readExistingSchemaSealedFile(origin.StoppedCapture.Path, true)
	if err != nil || !bytes.Equal(append(canonical, '\n'), raw) || stopErr != nil || rawErr != nil || startupContentSHA256(stopRaw) != origin.StoppedCapture.SHA256 || stored.Phase != "WRITER_STOPPED" || intent.Format != "lmm-schema-maintenance-abort-start-intent-v1" || intent.TransitionID != plan.TransitionID || intent.PrePlanSHA256 != preSHA || intent.Host != h.binding.Host || intent.AbortBindingSHA256 != bindingSHA || intent.PreparedSHA256 != origin.Prepared.SHA256 || intent.OldCapsule != origin.OldCapsule || intent.StoppedCaptureSHA256 != origin.StoppedCapture.SHA256 || intent.Stopped.Phase != "WRITER_STOPPED" || !sameSchemaMaintenanceWriterCapture(intent.Stopped, stored) || !intent.Stopped.StopStartedUTC.Equal(stored.StopStartedUTC) || intent.Stopped.ShutdownJournalSHA256 != stored.ShutdownJournalSHA256 || intent.StartedUTC.IsZero() || intent.StartedUTC.Location() != time.UTC || intent.StartedUTC.Before(stored.StopStartedUTC) {
		return intent, "", errors.New("schema abort start intent changed its original stopped generation or independent authority")
	}
	return intent, startupContentSHA256(raw), nil
}

func (h *productionSchemaMaintenanceHost) captureAbortOldStartable(ctx context.Context, plan productionSchemaMaintenancePlan, preSHA string, binding productionSchemaMaintenanceAbortBinding, checkNeverClaimed func(context.Context) error) (productionSchemaMaintenanceHostCapture, error) {
	var capture productionSchemaMaintenanceHostCapture
	c, index, _, err := h.abortOldCapsule(ctx, plan, preSHA, binding, checkNeverClaimed, false)
	if err != nil {
		return capture, err
	}
	state, err := h.runtime.billingUnitState(ctx, c.Service)
	if err != nil {
		return capture, err
	}
	if state["ActiveState"] == "active" && state["SubState"] == "running" {
		capture, err = h.captureAbortOldReady(ctx, plan, preSHA, binding, checkNeverClaimed)
		if err != nil || capture.Phase != "ABORT_RUNNING" {
			return capture, errors.New("schema abort startable peer is not the exact retained writer under closed admission")
		}
		return capture, nil
	}
	capture, err = h.captureStopped(ctx)
	if err != nil || capture.Phase != "WRITER_STOPPED" {
		return capture, errors.New("schema abort startable host has no verifiable original normal stopped generation")
	}
	stored, err := h.runtime.readExistingSchemaSealedFile(binding.Origins[index].StoppedCapture.Path, true)
	if err != nil || startupContentSHA256(stored) != binding.Origins[index].StoppedCapture.SHA256 {
		return capture, errors.New("schema abort startable host changed its original sealed shutdown")
	}
	if err := h.runtime.qualifyMerchantStoreCapsule(ctx, c, binding.Origins[index].OldCapsule.SHA256, "", true); err != nil {
		return capture, err
	}
	if err := h.verifyRecoveryOwnersReleased(ctx, c, binding.Origins[index].OldCapsule.SHA256); err != nil {
		return capture, err
	}
	return capture, h.checkAbortNeverClaimed(ctx, checkNeverClaimed, true)
}

// The original per-start hook temporarily owns PG while starting. Its normal
// exact owner release must finish before either origin is considered ready.
func (h *productionSchemaMaintenanceHost) startAbortOld(ctx context.Context, plan productionSchemaMaintenancePlan, preSHA string, binding productionSchemaMaintenanceAbortBinding, checkNeverClaimed, startableOrigins func(context.Context) error) (productionSchemaMaintenanceHostCapture, error) {
	var capture productionSchemaMaintenanceHostCapture
	if err := h.requireNewAbortIntent("abort-start-intent.json"); err != nil {
		return capture, err
	}
	if _, err := os.Lstat(filepath.Join(h.binding.Workspace, "state", "abort-started.json")); !errors.Is(err, os.ErrNotExist) {
		return capture, errors.New("schema abort start has prior result evidence; inspect without replay")
	}
	if startableOrigins == nil {
		return capture, errors.New("schema abort start requires actual fixed two-origin startable observations")
	}
	c, index, bindingSHA, err := h.abortOldCapsule(ctx, plan, preSHA, binding, checkNeverClaimed, false)
	if err != nil {
		return capture, err
	}
	capture, err = h.captureAbortOldStartable(ctx, plan, preSHA, binding, checkNeverClaimed)
	if err != nil || capture.Phase != "WRITER_STOPPED" {
		return capture, errors.New("schema abort start requires the exact original stopped writer")
	}
	if err := startableOrigins(ctx); err != nil {
		return capture, err
	}
	if h.runtime.now == nil {
		return capture, errors.New("schema abort start lacks the native clock")
	}
	origin := binding.Origins[index]
	intent := productionSchemaMaintenanceAbortStartIntent{"lmm-schema-maintenance-abort-start-intent-v1", plan.TransitionID, preSHA, h.binding.Host, bindingSHA, origin.Prepared.SHA256, origin.OldCapsule, origin.StoppedCapture.SHA256, capture, h.runtime.now().UTC()}
	if intent.StartedUTC.IsZero() || intent.StartedUTC.Before(capture.StopStartedUTC) {
		return capture, errors.New("schema abort start has no valid native chronology")
	}
	if err := h.dispatchAbortOldStart(ctx, c.Service, intent, func(ctx context.Context) (productionSchemaMaintenanceHostCapture, error) {
		fresh, err := h.captureAbortOldStartable(ctx, plan, preSHA, binding, checkNeverClaimed)
		if err != nil {
			return fresh, err
		}
		if err := startableOrigins(ctx); err != nil {
			return fresh, err
		}
		return fresh, h.checkAbortNeverClaimed(ctx, checkNeverClaimed, true)
	}); err != nil {
		return capture, err
	}
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for attempt := 0; attempt < 60; attempt++ {
		if err := h.check(ctx); err != nil {
			return capture, err
		}
		if err := h.verifyRecoveryOwnersReleased(waitCtx, c, origin.OldCapsule.SHA256); err == nil {
			capture, err = h.captureAbortOldReady(ctx, plan, preSHA, binding, checkNeverClaimed)
			if err != nil || capture.Phase != "ABORT_RUNNING" || capture.ConfirmedInvocationID == intent.Stopped.InvocationID {
				return capture, errors.New("schema abort start did not prove a new retained invocation under closed admission")
			}
			return capture, h.writeCapture("abort-started.json", capture)
		}
		select {
		case <-waitCtx.Done():
			return capture, errors.New("schema abort per-start owner release remains uncertain; inspect without replay")
		case <-time.After(500 * time.Millisecond):
		}
	}
	return capture, errors.New("schema abort per-start owner did not verifiably release; inspect without replay")
}

// The durable intent and the final stopped observation enclose the only start
// dispatch. A changed or uncertain observation leaves the intent for inspection.
func (h *productionSchemaMaintenanceHost) dispatchAbortOldStart(ctx context.Context, service string, intent productionSchemaMaintenanceAbortStartIntent, beforeStart func(context.Context) (productionSchemaMaintenanceHostCapture, error)) error {
	if err := h.requireNewAbortIntent("abort-start-intent.json"); err != nil {
		return err
	}
	if beforeStart == nil || service != h.runtime.paths.Service {
		return errors.New("schema abort dispatch lacks its fixed stopped-host recheck")
	}
	if err := h.check(ctx); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(intent, "", "  ")
	if err != nil {
		return err
	}
	if err := h.writeNew("abort-start-intent.json", append(raw, '\n')); err != nil {
		return errors.New("schema abort start intent could not be sealed; preserve it and inspect without replay")
	}
	fresh, err := beforeStart(ctx)
	if err != nil {
		return err
	}
	if fresh.Phase != "WRITER_STOPPED" || !sameSchemaMaintenanceWriterCapture(fresh, intent.Stopped) || !fresh.StopStartedUTC.Equal(intent.Stopped.StopStartedUTC) || fresh.ShutdownJournalSHA256 != intent.Stopped.ShutdownJournalSHA256 {
		return errors.New("schema abort writer changed after its one-shot intent; inspect without replay")
	}
	if err := h.check(ctx); err != nil {
		return err
	}
	// The serialized native PG gate has returned and released its lock before
	// the old per-start holder can acquire its ordinary PG owner.
	if _, err := h.runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"start", service}, Timeout: merchantStoreStartupLimit + 30*time.Second}); err != nil {
		return errors.New("schema abort start result is uncertain; preserve the original intent and inspect without replay")
	}
	return nil
}

func (h *productionSchemaMaintenanceHost) captureAbortOldReady(ctx context.Context, plan productionSchemaMaintenancePlan, preSHA string, binding productionSchemaMaintenanceAbortBinding, checkNeverClaimed func(context.Context) error) (productionSchemaMaintenanceHostCapture, error) {
	capture, err := h.readCapture("close-intent.json")
	if err != nil {
		return capture, err
	}
	c, index, bindingSHA, err := h.abortOldCapsule(ctx, plan, preSHA, binding, checkNeverClaimed, true)
	if err != nil {
		return capture, err
	}
	intent, _, err := h.readAbortStartIntent(plan, preSHA, binding, index, bindingSHA)
	if err != nil {
		return capture, err
	}
	origin := binding.Origins[index]
	capture = intent.Stopped
	before, err := h.runtime.billingUnitState(ctx, c.Service)
	if err != nil {
		return capture, err
	}
	if err := h.captureRecoveryRunningAdmission(ctx, &capture, c, origin.OldCapsule); err != nil {
		return capture, err
	}
	if err := h.runtime.qualifyMerchantStoreCapsule(ctx, c, origin.OldCapsule.SHA256, "", true); err != nil {
		return capture, err
	}
	if err := h.verifyRecoveryOwnersReleased(ctx, c, origin.OldCapsule.SHA256); err != nil {
		return capture, err
	}
	if err := h.runtime.verifyPortableRunningWriter(ctx, c, origin.OldCapsule.SHA256, "", false); err != nil {
		return capture, err
	}
	if err := h.verifyRecoveryOwnersReleased(ctx, c, origin.OldCapsule.SHA256); err != nil {
		return capture, err
	}
	after, err := h.runtime.billingUnitState(ctx, c.Service)
	pid, parseErr := strconv.Atoi(after["MainPID"])
	if err != nil || parseErr != nil || pid <= 1 || before["MainPID"] != after["MainPID"] || before["InvocationID"] != after["InvocationID"] || after["ExecMainPID"] != after["MainPID"] || after["ActiveState"] != "active" || after["SubState"] != "running" || !existingSchemaInvocationPattern.MatchString(after["InvocationID"]) || after["InvocationID"] == intent.Stopped.InvocationID {
		return capture, errors.New("schema abort retained writer generation is unavailable or changed during proof")
	}
	if err := h.captureRecoveryRunningAdmission(ctx, &capture, c, origin.OldCapsule); err != nil {
		return capture, err
	}
	final, err := h.runtime.billingUnitState(ctx, c.Service)
	if err != nil || final["MainPID"] != after["MainPID"] || final["ExecMainPID"] != after["MainPID"] || final["InvocationID"] != after["InvocationID"] || final["ActiveState"] != "active" || final["SubState"] != "running" || sha256MustEqual(filepath.Join("/proc", strconv.Itoa(pid), "exe"), c.Candidate.PayloadSHA256) != nil {
		return capture, errors.New("schema abort retained generation changed during final admission proof")
	}
	if capture.Phase != "ADMISSION_RESTORED" {
		capture.Phase = "ABORT_RUNNING"
	}
	capture.ConfirmedCapsuleSHA256, capture.ConfirmedProviderSHA256 = origin.OldCapsule.SHA256, c.Candidate.PayloadSHA256
	capture.ConfirmedPID, capture.ConfirmedInvocationID = pid, after["InvocationID"]
	finished, err := h.checkAbortReadyAuthority(ctx, plan, preSHA, binding, checkNeverClaimed)
	if err != nil {
		return capture, err
	}
	if finished != nil && (capture.Phase != "ADMISSION_RESTORED" || finished.PID != pid || finished.InvocationID != after["InvocationID"]) {
		return capture, errors.New("schema abort finished host changed its actual restored generation")
	}
	return capture, nil
}

func (h *productionSchemaMaintenanceHost) restoreAbortOldAdmission(ctx context.Context, plan productionSchemaMaintenancePlan, preSHA string, binding productionSchemaMaintenanceAbortBinding, checkNeverClaimed, readyOrigins func(context.Context) error) (productionSchemaMaintenanceHostCapture, error) {
	var capture productionSchemaMaintenanceHostCapture
	_, index, _, err := h.abortOldCapsule(ctx, plan, preSHA, binding, checkNeverClaimed, false)
	if err != nil {
		return capture, err
	}
	if readyOrigins == nil {
		return capture, errors.New("schema abort reopen requires actual fixed two-origin retained readiness")
	}
	verify := func(ctx context.Context) error {
		if err := h.checkAbortNeverClaimed(ctx, checkNeverClaimed, true); err != nil {
			return err
		}
		if err := readyOrigins(ctx); err != nil {
			return err
		}
		return h.checkAbortNeverClaimed(ctx, checkNeverClaimed, true)
	}
	old := binding.Origins[index].OldCapsule
	return h.restoreAdmission(ctx, old.Path, old.SHA256, verify)
}

type productionSchemaMaintenanceAbortHostFinished struct {
	Format                string                    `json:"format"`
	TransitionID          string                    `json:"transition_id"`
	PrePlanSHA256         string                    `json:"pre_plan_sha256"`
	Host                  string                    `json:"host"`
	AbortBindingSHA256    string                    `json:"abort_binding_sha256"`
	PreparedSHA256        string                    `json:"prepared_sha256"`
	GuardianBindingSHA256 string                    `json:"guardian_binding_sha256"`
	OldCapsule            productionReleaseFilePlan `json:"old_capsule"`
	ProviderSHA256        string                    `json:"provider_sha256"`
	FinishIntentSHA256    string                    `json:"finish_intent_sha256"`
	StartIntentSHA256     string                    `json:"start_intent_sha256"`
	RestoredCaptureSHA256 string                    `json:"restored_capture_sha256"`
	PID                   int                       `json:"pid"`
	InvocationID          string                    `json:"invocation_id"`
	FinishedUTC           time.Time                 `json:"finished_utc"`
}

type productionSchemaMaintenanceAbortFinishIntent struct {
	Format     string                                       `json:"format"`
	Target     productionSchemaMaintenanceAbortHostFinished `json:"target"`
	StartedUTC time.Time                                    `json:"started_utc"`
}

func (h *productionSchemaMaintenanceHost) validateAbortFinishIntent(finished productionSchemaMaintenanceAbortHostFinished, start time.Time) error {
	raw, err := h.runtime.readExistingSchemaSealedFile(filepath.Join(h.binding.Workspace, "state", "abort-finish-intent.json"), true)
	if err != nil || len(raw) > 16384 || startupContentSHA256(raw) != finished.FinishIntentSHA256 {
		return errors.New("schema abort finish lost its exact one-shot terminal intent")
	}
	var intent productionSchemaMaintenanceAbortFinishIntent
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&intent) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("schema abort finish intent is malformed")
	}
	canonical, err := json.MarshalIndent(intent, "", "  ")
	expected := finished
	expected.FinishedUTC = time.Time{}
	expected.FinishIntentSHA256 = ""
	if err != nil || !bytes.Equal(append(canonical, '\n'), raw) || intent.Format != "lmm-schema-maintenance-abort-finish-intent-v1" || intent.Target != expected || intent.StartedUTC.IsZero() || intent.StartedUTC.Location() != time.UTC || intent.StartedUTC.Before(start) || finished.FinishedUTC.Before(intent.StartedUTC) {
		return errors.New("schema abort finish intent changed its exact retained generation or chronology")
	}
	return nil
}

func (h *productionSchemaMaintenanceHost) checkAbortReadyAuthority(ctx context.Context, plan productionSchemaMaintenancePlan, preSHA string, binding productionSchemaMaintenanceAbortBinding, checkNeverClaimed func(context.Context) error) (*productionSchemaMaintenanceAbortHostFinished, error) {
	if err := h.check(ctx); err == nil {
		return nil, h.checkAbortNeverClaimed(ctx, checkNeverClaimed, true)
	}
	if err := h.checkAbortNeverClaimed(ctx, checkNeverClaimed, false); err != nil {
		return nil, err
	}
	bindingRaw, err := canonicalProductionSchemaMaintenanceAbortBinding(binding, plan, preSHA, h.runtime.paths)
	if err != nil {
		return nil, err
	}
	actual, err := h.runtime.readExistingSchemaSealedFile(filepath.Join(h.binding.Workspace, "state", "abort-binding.json"), true)
	if err != nil || !bytes.Equal(actual, bindingRaw) {
		return nil, errors.New("schema abort finished observation changed its original independent binding")
	}
	index := -1
	for i, o := range binding.Origins {
		if o.Host == h.binding.Host {
			index = i
		}
	}
	if index < 0 {
		return nil, errors.New("schema abort finished observation is not an original fixed host")
	}
	origin := binding.Origins[index]
	if origin.GuardianBinding.Path != h.binding.GuardianBindingPath || origin.OldCapsule.Path != h.binding.CapsulePath || origin.OldCapsule.SHA256 != h.binding.CapsuleSHA256 || h.binding.TransitionID != plan.TransitionID || h.binding.PlanSHA256 != preSHA {
		return nil, errors.New("schema abort finish changed its original local binding")
	}
	prepared, err := h.runtime.loadSchemaMaintenanceAbortPrepared(origin.Prepared.Path, origin.Prepared.SHA256, plan, preSHA)
	if err != nil || prepared.Host != h.binding.Host || prepared.OldCapsule != origin.OldCapsule || prepared.StoppedCapture != origin.StoppedCapture || prepared.GuardianBinding != origin.GuardianBinding {
		return nil, errors.New("schema abort finish lost its original frozen preparation")
	}
	guardian, err := h.runtime.readExistingSchemaSealedFile(h.binding.GuardianBindingPath, true)
	if err != nil || startupContentSHA256(guardian) != h.binding.GuardianBindingSHA256 || origin.GuardianBinding.SHA256 != h.binding.GuardianBindingSHA256 {
		return nil, errors.New("schema abort finished observation changed its original guardian binding")
	}
	intent, intentSHA, err := h.readAbortStartIntent(plan, preSHA, binding, index, startupContentSHA256(bindingRaw))
	if err != nil {
		return nil, err
	}
	raw, err := h.runtime.readExistingSchemaSealedFile(filepath.Join(h.binding.Workspace, "state", "abort-host-finished.json"), true)
	if err != nil || len(raw) > 16384 {
		return nil, errors.New("schema abort host has neither a live lease nor its independent native finish receipt")
	}
	var finished productionSchemaMaintenanceAbortHostFinished
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&finished) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, errors.New("schema abort finish receipt is malformed")
	}
	canonical, err := json.MarshalIndent(finished, "", "  ")
	if err != nil || !bytes.Equal(append(canonical, '\n'), raw) || finished.Format != "lmm-schema-maintenance-abort-host-finished-v1" || finished.TransitionID != plan.TransitionID || finished.PrePlanSHA256 != preSHA || finished.Host != h.binding.Host || finished.AbortBindingSHA256 != startupContentSHA256(bindingRaw) || finished.PreparedSHA256 != origin.Prepared.SHA256 || finished.GuardianBindingSHA256 != origin.GuardianBinding.SHA256 || finished.OldCapsule != origin.OldCapsule || finished.OldCapsule.Path != h.binding.CapsulePath || finished.OldCapsule.SHA256 != h.binding.CapsuleSHA256 || finished.ProviderSHA256 != plan.Origins[index].Recovery.Candidate.PayloadSHA256 || finished.StartIntentSHA256 != intentSHA || finished.PID <= 1 || !existingSchemaInvocationPattern.MatchString(finished.InvocationID) || finished.InvocationID == intent.Stopped.InvocationID || finished.FinishedUTC.IsZero() || finished.FinishedUTC.Location() != time.UTC || finished.FinishedUTC.Before(intent.StartedUTC) {
		return nil, errors.New("schema abort finish receipt changed its original never-claimed retained authority")
	}
	if err := h.validateAbortFinishIntent(finished, intent.StartedUTC); err != nil {
		return nil, err
	}
	restoredRaw, err := h.runtime.readExistingSchemaSealedFile(filepath.Join(h.binding.Workspace, "state", "admission-restored.json"), true)
	restored, captureErr := h.readCapture("admission-restored.json")
	if err != nil || captureErr != nil || startupContentSHA256(restoredRaw) != finished.RestoredCaptureSHA256 || restored.Phase != "ADMISSION_RESTORED" || restored.ConfirmedCapsuleSHA256 != origin.OldCapsule.SHA256 || restored.ConfirmedProviderSHA256 != finished.ProviderSHA256 || restored.ConfirmedPID != finished.PID || restored.ConfirmedInvocationID != finished.InvocationID {
		return nil, errors.New("schema abort finish receipt lost its original restored generation")
	}
	return &finished, nil
}

func (h *productionSchemaMaintenanceHost) finishAbortOld(ctx context.Context, plan productionSchemaMaintenancePlan, preSHA string, binding productionSchemaMaintenanceAbortBinding, checkNeverClaimed, readyOrigins func(context.Context) error) (productionSchemaMaintenanceAbortHostFinished, error) {
	var finished productionSchemaMaintenanceAbortHostFinished
	if err := h.requireNewAbortIntent("abort-finish-intent.json"); err != nil {
		return finished, err
	}
	if err := h.requireNewAbortIntent("abort-host-finished.json"); err != nil {
		return finished, err
	}
	if readyOrigins == nil {
		return finished, errors.New("schema abort finish requires actual fixed two-origin readiness")
	}
	_, index, bindingSHA, err := h.abortOldCapsule(ctx, plan, preSHA, binding, checkNeverClaimed, false)
	if err != nil {
		return finished, err
	}
	if err := readyOrigins(ctx); err != nil {
		return finished, err
	}
	capture, err := h.captureAbortOldReady(ctx, plan, preSHA, binding, checkNeverClaimed)
	if err != nil || capture.Phase != "ADMISSION_RESTORED" {
		return finished, errors.New("schema abort finish requires its actual retained writer and restored admission")
	}
	startIntent, startSHA, err := h.readAbortStartIntent(plan, preSHA, binding, index, bindingSHA)
	if err != nil {
		return finished, err
	}
	restoredRaw, err := h.runtime.readExistingSchemaSealedFile(filepath.Join(h.binding.Workspace, "state", "admission-restored.json"), true)
	stored, captureErr := h.readCapture("admission-restored.json")
	if err != nil || captureErr != nil || stored.Phase != "ADMISSION_RESTORED" || stored.ConfirmedCapsuleSHA256 != capture.ConfirmedCapsuleSHA256 || stored.ConfirmedProviderSHA256 != capture.ConfirmedProviderSHA256 || stored.ConfirmedPID != capture.ConfirmedPID || stored.ConfirmedInvocationID != capture.ConfirmedInvocationID || h.runtime.now == nil {
		return finished, errors.New("schema abort finish lost its actual restored receipt or native clock")
	}
	origin := binding.Origins[index]
	finished = productionSchemaMaintenanceAbortHostFinished{Format: "lmm-schema-maintenance-abort-host-finished-v1", TransitionID: plan.TransitionID, PrePlanSHA256: preSHA, Host: h.binding.Host, AbortBindingSHA256: bindingSHA, PreparedSHA256: origin.Prepared.SHA256, GuardianBindingSHA256: origin.GuardianBinding.SHA256, OldCapsule: origin.OldCapsule, ProviderSHA256: capture.ConfirmedProviderSHA256, StartIntentSHA256: startSHA, RestoredCaptureSHA256: startupContentSHA256(restoredRaw), PID: capture.ConfirmedPID, InvocationID: capture.ConfirmedInvocationID}
	intent := productionSchemaMaintenanceAbortFinishIntent{Format: "lmm-schema-maintenance-abort-finish-intent-v1", Target: finished, StartedUTC: h.runtime.now().UTC()}
	if intent.StartedUTC.IsZero() || intent.StartedUTC.Before(startIntent.StartedUTC) {
		return finished, errors.New("schema abort finish lacks valid native chronology")
	}
	raw, err := json.MarshalIndent(intent, "", "  ")
	if err != nil {
		return finished, err
	}
	// Durable one-shot intent precedes the authenticated terminal exchange.
	// An uncertain ACK or subsequent receipt failure never permits a retry.
	if err := h.writeNew("abort-finish-intent.json", append(raw, '\n')); err != nil {
		return finished, err
	}
	finished.FinishIntentSHA256 = startupContentSHA256(append(raw, '\n'))
	sealedTarget := finished
	sealedTarget.FinishedUTC = intent.StartedUTC
	if err := h.validateAbortFinishIntent(sealedTarget, startIntent.StartedUTC); err != nil {
		return finished, err
	}
	if err := h.checkAbortNeverClaimed(ctx, checkNeverClaimed, true); err != nil {
		return finished, err
	}
	if err := readyOrigins(ctx); err != nil {
		return finished, err
	}
	if err := h.checkAbortNeverClaimed(ctx, checkNeverClaimed, true); err != nil {
		return finished, err
	}
	fresh, err := h.captureAbortOldReady(ctx, plan, preSHA, binding, checkNeverClaimed)
	if err != nil || fresh.Phase != "ADMISSION_RESTORED" || fresh.ConfirmedCapsuleSHA256 != finished.OldCapsule.SHA256 || fresh.ConfirmedProviderSHA256 != finished.ProviderSHA256 || fresh.ConfirmedPID != finished.PID || fresh.ConfirmedInvocationID != finished.InvocationID {
		return finished, errors.New("schema abort finish changed the sealed retained writer generation before release")
	}
	if err := h.checkAbortNeverClaimed(ctx, checkNeverClaimed, true); err != nil {
		return finished, err
	}
	if err := finishSchemaMaintenanceHostLocks(ctx, h.lease); err != nil {
		return finished, errors.New("schema abort native finish is uncertain; preserve its one-shot intent and do not retry")
	}
	finished.FinishedUTC = h.runtime.now().UTC()
	if err := h.validateAbortFinishIntent(finished, startIntent.StartedUTC); err != nil {
		return finished, err
	}
	raw, err = json.MarshalIndent(finished, "", "  ")
	if err != nil {
		return finished, err
	}
	if err := h.writeNew("abort-host-finished.json", append(raw, '\n')); err != nil {
		return finished, errors.New("schema abort authenticated finish completed but its receipt is unavailable; preserve intent and do not retry")
	}
	if _, err := h.captureAbortOldReady(ctx, plan, preSHA, binding, checkNeverClaimed); err != nil {
		return finished, err
	}
	return finished, nil
}
