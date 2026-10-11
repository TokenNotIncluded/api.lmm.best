package deploycli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type productionSchemaMaintenanceDeadHolderStart struct {
	Format        string                                 `json:"format"`
	BindingSHA256 string                                 `json:"binding_sha256"`
	Host          string                                 `json:"host"`
	OldCapsule    productionReleaseFilePlan              `json:"old_capsule"`
	Stopped       productionSchemaMaintenanceHostCapture `json:"stopped"`
	StartedUTC    time.Time                              `json:"started_utc"`
}
type productionSchemaMaintenanceDeadHolderFinished struct {
	Format                string                                 `json:"format"`
	BindingSHA256         string                                 `json:"binding_sha256"`
	Host                  string                                 `json:"host"`
	GuardianBindingSHA256 string                                 `json:"guardian_binding_sha256"`
	StartIntentSHA256     string                                 `json:"start_intent_sha256"`
	FinishIntentSHA256    string                                 `json:"finish_intent_sha256"`
	RestoredCaptureSHA256 string                                 `json:"restored_capture_sha256"`
	Capture               productionSchemaMaintenanceHostCapture `json:"capture"`
	FinishedUTC           time.Time                              `json:"finished_utc"`
}

func (d *productionSchemaMaintenanceDeadHolderController) oldForRecovery(ctx context.Context) (productionMerchantStoreCapsule, productionSchemaMaintenanceDeadHolderBinding, string, error) {
	c := d.base
	var old productionMerchantStoreCapsule
	binding, sha, err := d.binding(ctx)
	if err != nil {
		return old, binding, sha, err
	}
	if err := d.checkRecovered(ctx); err != nil {
		return old, binding, sha, err
	}
	old, err = c.runtime.loadMerchantStoreCapsule(c.origin.OldCapsule.Path, c.origin.OldCapsule.SHA256)
	if err != nil {
		return old, binding, sha, err
	}
	if err := validateSchemaMaintenanceAbortOldCapsule(old, c.origin, c.plan, c.runtime.paths); err != nil {
		return old, binding, sha, err
	}
	return old, binding, sha, nil
}
func (d *productionSchemaMaintenanceDeadHolderController) readStart(sha string) (productionSchemaMaintenanceDeadHolderStart, string, error) {
	c := d.base
	var intent productionSchemaMaintenanceDeadHolderStart
	raw, err := c.runtime.readExistingSchemaSealedFile(filepath.Join(c.origin.Workspace, "state", "dead-holder-start-intent.json"), true)
	if err != nil {
		return intent, "", err
	}
	if err := decodeControllerBackupJSON(raw, &intent); err != nil {
		return intent, "", err
	}
	canonical, _ := json.MarshalIndent(intent, "", "  ")
	stopped, err := c.host.readCapture("writer-stopped.json")
	if err != nil || !bytes.Equal(raw, append(canonical, '\n')) || intent.Format != "lmm-schema-maintenance-dead-holder-start-v1" || intent.BindingSHA256 != sha || intent.Host != c.origin.Host || intent.OldCapsule != c.origin.OldCapsule || intent.Stopped.Phase != "WRITER_STOPPED" || !sameSchemaMaintenanceWriterCapture(intent.Stopped, stopped) || intent.Stopped.ShutdownJournalSHA256 != stopped.ShutdownJournalSHA256 || !intent.Stopped.StopStartedUTC.Equal(stopped.StopStartedUTC) || intent.StartedUTC.IsZero() || intent.StartedUTC.Location() != time.UTC || intent.StartedUTC.Before(stopped.StopStartedUTC) {
		return intent, "", errors.New("dead holder old start lost its exact independent one-shot intent")
	}
	return intent, startupContentSHA256(raw), nil
}
func (d *productionSchemaMaintenanceDeadHolderController) startable(ctx context.Context) (productionSchemaMaintenanceHostCapture, error) {
	c := d.base
	var capture productionSchemaMaintenanceHostCapture
	old, _, _, err := d.oldForRecovery(ctx)
	if err != nil {
		return capture, err
	}
	state, err := c.runtime.billingUnitState(ctx, old.Service)
	if err != nil {
		return capture, err
	}
	if state["ActiveState"] == "active" && state["SubState"] == "running" {
		capture, err = d.ready(ctx)
		if err != nil || capture.Phase != "DEAD_PREWORKER_RUNNING" {
			return capture, errors.New("dead holder startable peer is not the exact closed retained generation")
		}
		return capture, nil
	}
	capture, err = c.host.captureStopped(ctx)
	if err != nil || capture.Phase != "WRITER_STOPPED" {
		return capture, errors.New("dead holder startable requires the original sealed stop")
	}
	if err := c.runtime.qualifyMerchantStoreCapsule(ctx, old, c.origin.OldCapsule.SHA256, "", true); err != nil {
		return capture, err
	}
	if err := c.host.verifyRecoveryOwnersReleased(ctx, old, c.origin.OldCapsule.SHA256); err != nil {
		return capture, err
	}
	return capture, d.checkLeasedLocal(ctx)
}
func (d *productionSchemaMaintenanceDeadHolderController) checkOrigins(ctx context.Context, ready, restored bool) error {
	c := d.base
	binding, _, err := d.binding(ctx)
	if err != nil {
		return err
	}
	action := "dead-startable"
	if ready {
		action = "dead-ready"
	}
	for i, origin := range c.plan.Origins {
		var observed productionSchemaMaintenanceDeadHolderCapture
		if origin.Host == c.origin.Host {
			observed, err = d.capture(ctx, ready)
		} else {
			err = d.remote(ctx, origin, action, &observed)
		}
		if err != nil {
			return err
		}
		boundRaw, _ := json.MarshalIndent(binding, "", "  ")
		preparedRaw, _ := json.MarshalIndent(binding.Origins[i], "", "  ")
		if observed.BindingSHA256 != startupContentSHA256(append(boundRaw, '\n')) || observed.PreparedSHA256 != startupContentSHA256(append(preparedRaw, '\n')) {
			return errors.New("dead holder peer readiness lost its exact recovered binding")
		}
		capture := observed.Capture
		if capture.Format != 1 || capture.Host != origin.Host || capture.TransitionID != c.plan.TransitionID || capture.PlanSHA256 != c.planSHA || capture.CapsuleSHA256 != origin.OldCapsule.SHA256 || capture.ProviderSHA256 != origin.Recovery.Candidate.PayloadSHA256 || capture.PID <= 1 || !existingSchemaInvocationPattern.MatchString(capture.InvocationID) || capture.StopStartedUTC.IsZero() || !productionSHA256Pattern.MatchString(capture.ShutdownJournalSHA256) {
			return errors.New("dead holder peer lacks its original sealed writer generation")
		}
		if capture.Phase == "WRITER_STOPPED" && !ready && !restored {
			continue
		}
		if capture.ConfirmedCapsuleSHA256 != origin.OldCapsule.SHA256 || capture.ConfirmedProviderSHA256 != origin.Recovery.Candidate.PayloadSHA256 || capture.ConfirmedPID <= 1 || !existingSchemaInvocationPattern.MatchString(capture.ConfirmedInvocationID) || (capture.Phase != "DEAD_PREWORKER_RUNNING" && capture.Phase != "ADMISSION_RESTORED") || (!ready && capture.Phase != "DEAD_PREWORKER_RUNNING") || (restored && capture.Phase != "ADMISSION_RESTORED") {
			return errors.New("dead holder peer has no actual retained generation in the required admission phase")
		}
	}
	return nil
}
func (d *productionSchemaMaintenanceDeadHolderController) startOld(ctx context.Context) (productionSchemaMaintenanceHostCapture, error) {
	c := d.base
	var capture productionSchemaMaintenanceHostCapture
	for _, name := range []string{"dead-holder-start-intent.json", "dead-holder-started.json"} {
		if _, err := os.Lstat(filepath.Join(c.origin.Workspace, "state", name)); !errors.Is(err, os.ErrNotExist) {
			return capture, errors.New("dead holder start was attempted; observe without replay")
		}
	}
	old, _, sha, err := d.oldForRecovery(ctx)
	if err != nil {
		return capture, err
	}
	capture, err = d.startable(ctx)
	if err != nil || capture.Phase != "WRITER_STOPPED" {
		return capture, errors.New("dead holder start requires the exact original stopped writer")
	}
	if err := d.checkOrigins(ctx, false, false); err != nil {
		return capture, err
	}
	intent := productionSchemaMaintenanceDeadHolderStart{"lmm-schema-maintenance-dead-holder-start-v1", sha, c.origin.Host, c.origin.OldCapsule, capture, c.runtime.now().UTC()}
	if intent.StartedUTC.IsZero() || intent.StartedUTC.Before(capture.StopStartedUTC) {
		return capture, errors.New("dead holder start chronology is invalid")
	}
	if err := c.runtime.startupBaselineNewFile(filepath.Join(c.origin.Workspace, "state", "dead-holder-start-intent.json"), intent); err != nil {
		return capture, err
	}
	if err := d.checkOrigins(ctx, false, false); err != nil {
		return capture, err
	}
	fresh, err := d.startable(ctx)
	if err != nil || fresh.Phase != "WRITER_STOPPED" || !sameSchemaMaintenanceWriterCapture(fresh, intent.Stopped) || !fresh.StopStartedUTC.Equal(intent.Stopped.StopStartedUTC) || fresh.ShutdownJournalSHA256 != intent.Stopped.ShutdownJournalSHA256 {
		return capture, errors.New("dead holder retained writer changed before its one-shot start")
	}
	sealed, _, err := d.readStart(sha)
	if err != nil || sealed != intent {
		return capture, errors.New("dead holder start intent changed before dispatch")
	}
	if err := d.checkLeasedLocal(ctx); err != nil {
		return capture, err
	}
	if _, err := c.runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"start", old.Service}}); err != nil {
		return capture, errors.New("dead holder start result is unknown; do not replay")
	}
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for attempt := 0; attempt < 60; attempt++ {
		if err := d.checkLeasedLocal(ctx); err != nil {
			return capture, err
		}
		if err := c.host.verifyRecoveryOwnersReleased(waitCtx, old, c.origin.OldCapsule.SHA256); err == nil {
			capture, err = d.ready(ctx)
			if err != nil || capture.Phase != "DEAD_PREWORKER_RUNNING" {
				return capture, errors.New("dead holder old writer readiness is not proved")
			}
			return capture, c.host.writeCapture("dead-holder-started.json", capture)
		}
		select {
		case <-waitCtx.Done():
			return capture, errors.New("dead holder per-start release is unknown; do not replay")
		case <-time.After(500 * time.Millisecond):
		}
	}
	return capture, errors.New("dead holder old start remains uncertain; do not replay")
}
func (d *productionSchemaMaintenanceDeadHolderController) ready(ctx context.Context) (productionSchemaMaintenanceHostCapture, error) {
	c := d.base
	var capture productionSchemaMaintenanceHostCapture
	old, _, sha, err := d.oldForRecovery(ctx)
	if err != nil {
		return capture, err
	}
	intent, _, err := d.readStart(sha)
	if err != nil {
		return capture, err
	}
	capture = intent.Stopped
	before, err := c.runtime.billingUnitState(ctx, old.Service)
	if err != nil {
		return capture, err
	}
	if err := c.host.captureRecoveryRunningAdmission(ctx, &capture, old, c.origin.OldCapsule); err != nil {
		return capture, err
	}
	if err := c.runtime.qualifyMerchantStoreCapsule(ctx, old, c.origin.OldCapsule.SHA256, "", true); err != nil {
		return capture, err
	}
	if err := c.host.verifyRecoveryOwnersReleased(ctx, old, c.origin.OldCapsule.SHA256); err != nil {
		return capture, err
	}
	if err := c.runtime.verifyPortableRunningWriter(ctx, old, c.origin.OldCapsule.SHA256, "", false); err != nil {
		return capture, err
	}
	if err := c.host.verifyRecoveryOwnersReleased(ctx, old, c.origin.OldCapsule.SHA256); err != nil {
		return capture, err
	}
	after, err := c.runtime.billingUnitState(ctx, old.Service)
	pid, parseErr := strconv.Atoi(after["MainPID"])
	if err != nil || parseErr != nil || pid <= 1 || before["MainPID"] != after["MainPID"] || before["InvocationID"] != after["InvocationID"] || after["ExecMainPID"] != after["MainPID"] || after["ActiveState"] != "active" || after["SubState"] != "running" || !existingSchemaInvocationPattern.MatchString(after["InvocationID"]) || after["InvocationID"] == intent.Stopped.InvocationID {
		return capture, errors.New("dead holder retained generation changed during qualification")
	}
	if err := c.host.captureRecoveryRunningAdmission(ctx, &capture, old, c.origin.OldCapsule); err != nil {
		return capture, err
	}
	final, err := c.runtime.billingUnitState(ctx, old.Service)
	if err != nil || final["MainPID"] != after["MainPID"] || final["ExecMainPID"] != after["MainPID"] || final["InvocationID"] != after["InvocationID"] || final["ActiveState"] != "active" || final["SubState"] != "running" || sha256MustEqual(filepath.Join("/proc", strconv.Itoa(pid), "exe"), old.Candidate.PayloadSHA256) != nil {
		return capture, errors.New("dead holder retained generation changed before admission proof")
	}
	if capture.Phase != "ADMISSION_RESTORED" {
		capture.Phase = "DEAD_PREWORKER_RUNNING"
	}
	capture.ConfirmedCapsuleSHA256, capture.ConfirmedProviderSHA256 = c.origin.OldCapsule.SHA256, old.Candidate.PayloadSHA256
	capture.ConfirmedPID, capture.ConfirmedInvocationID = pid, after["InvocationID"]
	if d.finished {
		if err := d.checkFinished(capture, sha); err != nil {
			return capture, err
		}
	} else if err := d.checkLeasedLocal(ctx); err != nil {
		return capture, err
	}
	return capture, d.checkRecovered(ctx)
}
func (d *productionSchemaMaintenanceDeadHolderController) reopen(ctx context.Context) (productionSchemaMaintenanceHostCapture, error) {
	c := d.base
	var capture productionSchemaMaintenanceHostCapture
	old, _, _, err := d.oldForRecovery(ctx)
	if err != nil {
		return capture, err
	}
	verify := func(ctx context.Context) error {
		if err := d.checkRecovered(ctx); err != nil {
			return err
		}
		if err := d.checkOrigins(ctx, true, false); err != nil {
			return err
		}
		return d.checkLeasedLocal(ctx)
	}
	return c.host.restoreAdmission(ctx, filepath.Join(old.Root, "capsule.json"), c.origin.OldCapsule.SHA256, verify)
}

type productionSchemaMaintenanceDeadHolderCapture struct {
	BindingSHA256  string                                 `json:"binding_sha256"`
	PreparedSHA256 string                                 `json:"prepared_sha256"`
	Capture        productionSchemaMaintenanceHostCapture `json:"capture"`
}

func (d *productionSchemaMaintenanceDeadHolderController) capture(ctx context.Context, ready bool) (productionSchemaMaintenanceDeadHolderCapture, error) {
	var result productionSchemaMaintenanceDeadHolderCapture
	_, sha, err := d.binding(ctx)
	if err != nil {
		return result, err
	}
	p, err := d.prepared(ctx)
	if err != nil {
		return result, err
	}
	raw, _ := json.MarshalIndent(p, "", "  ")
	result.BindingSHA256, result.PreparedSHA256 = sha, startupContentSHA256(append(raw, '\n'))
	if ready {
		result.Capture, err = d.ready(ctx)
	} else {
		result.Capture, err = d.startable(ctx)
	}
	return result, err
}
func (d *productionSchemaMaintenanceDeadHolderController) finish(ctx context.Context) (productionSchemaMaintenanceDeadHolderFinished, error) {
	c := d.base
	var result productionSchemaMaintenanceDeadHolderFinished
	if d.finishUncertain || d.finished {
		return result, errors.New("dead holder finish was attempted; inspect without replay")
	}
	for _, name := range []string{"dead-holder-finish-intent.json", "dead-holder-finished.json"} {
		if _, err := os.Lstat(filepath.Join(c.origin.Workspace, "state", name)); !errors.Is(err, os.ErrNotExist) {
			return result, errors.New("dead holder terminal evidence already exists or is uncertain")
		}
	}
	_, sha, err := d.binding(ctx)
	if err != nil {
		return result, err
	}
	if err := d.checkOrigins(ctx, true, true); err != nil {
		return result, err
	}
	capture, err := d.ready(ctx)
	if err != nil || capture.Phase != "ADMISSION_RESTORED" {
		return result, errors.New("dead holder finish requires actual old writer and restored admission")
	}
	_, startSHA, err := d.readStart(sha)
	if err != nil {
		return result, err
	}
	restored, err := c.runtime.readExistingSchemaSealedFile(filepath.Join(c.origin.Workspace, "state", "admission-restored.json"), true)
	if err != nil {
		return result, err
	}
	result = productionSchemaMaintenanceDeadHolderFinished{Format: "lmm-schema-maintenance-dead-holder-finished-v1", BindingSHA256: sha, Host: c.origin.Host, GuardianBindingSHA256: c.host.binding.GuardianBindingSHA256, StartIntentSHA256: startSHA, RestoredCaptureSHA256: startupContentSHA256(restored), Capture: capture}
	raw, _ := json.MarshalIndent(result, "", "  ")
	if err := c.host.writeNew("dead-holder-finish-intent.json", append(raw, '\n')); err != nil {
		return result, err
	}
	result.FinishIntentSHA256 = startupContentSHA256(append(raw, '\n'))
	if err := d.checkOrigins(ctx, true, true); err != nil {
		return result, err
	}
	sealed, err := c.runtime.readExistingSchemaSealedFile(filepath.Join(c.origin.Workspace, "state", "dead-holder-finish-intent.json"), true)
	if err != nil || !bytes.Equal(sealed, append(raw, '\n')) || startupContentSHA256(sealed) != result.FinishIntentSHA256 {
		return result, errors.New("dead holder finish intent changed after peer checks")
	}
	fresh, err := d.ready(ctx)
	if err != nil || fresh != result.Capture || fresh.Phase != "ADMISSION_RESTORED" {
		return result, errors.New("dead holder local retained generation changed after peer checks")
	}
	if err := d.checkLeasedLocal(ctx); err != nil {
		return result, err
	}
	sealed, err = c.runtime.readExistingSchemaSealedFile(filepath.Join(c.origin.Workspace, "state", "dead-holder-finish-intent.json"), true)
	if err != nil || !bytes.Equal(sealed, append(raw, '\n')) {
		return result, errors.New("dead holder finish intent changed before terminal exchange")
	}
	d.finishUncertain = true
	if err := finishSchemaMaintenanceHostLocks(ctx, c.host.lease); err != nil {
		return result, errors.New("dead holder authenticated finish is uncertain; preserve locks and never resend")
	}
	result.FinishedUTC = c.runtime.now().UTC()
	raw, _ = json.MarshalIndent(result, "", "  ")
	if err := c.host.writeNew("dead-holder-finished.json", append(raw, '\n')); err != nil {
		return result, errors.New("dead holder finish acknowledged but receipt failed; preserve daemon and reconcile")
	}
	d.finished, d.finishUncertain = true, false
	_, err = d.ready(ctx)
	return result, err
}
func (d *productionSchemaMaintenanceDeadHolderController) checkFinished(capture productionSchemaMaintenanceHostCapture, sha string) error {
	c := d.base
	raw, err := c.runtime.readExistingSchemaSealedFile(filepath.Join(c.origin.Workspace, "state", "dead-holder-finished.json"), true)
	if err != nil {
		return err
	}
	var result productionSchemaMaintenanceDeadHolderFinished
	if err := decodeControllerBackupJSON(raw, &result); err != nil {
		return err
	}
	canonical, _ := json.MarshalIndent(result, "", "  ")
	start, startSHA, err := d.readStart(sha)
	if err != nil || !bytes.Equal(raw, append(canonical, '\n')) || result.Format != "lmm-schema-maintenance-dead-holder-finished-v1" || result.BindingSHA256 != sha || result.Host != c.origin.Host || result.GuardianBindingSHA256 != c.host.binding.GuardianBindingSHA256 || result.StartIntentSHA256 != startSHA || result.FinishedUTC.IsZero() || result.FinishedUTC.Location() != time.UTC || result.FinishedUTC.Before(start.StartedUTC) || result.Capture.Phase != "ADMISSION_RESTORED" || capture.Phase != "ADMISSION_RESTORED" || result.Capture.ConfirmedPID != capture.ConfirmedPID || result.Capture.ConfirmedInvocationID != capture.ConfirmedInvocationID || result.Capture.ConfirmedCapsuleSHA256 != capture.ConfirmedCapsuleSHA256 || result.Capture.ConfirmedProviderSHA256 != capture.ConfirmedProviderSHA256 {
		return errors.New("dead holder finished readiness differs from its independent native receipt")
	}
	intent, err := c.runtime.readExistingSchemaSealedFile(filepath.Join(c.origin.Workspace, "state", "dead-holder-finish-intent.json"), true)
	if err != nil || startupContentSHA256(intent) != result.FinishIntentSHA256 {
		return errors.New("dead holder finish lost its exact one-shot intent")
	}
	expected := result
	expected.FinishedUTC = time.Time{}
	expected.FinishIntentSHA256 = ""
	expectedRaw, _ := json.MarshalIndent(expected, "", "  ")
	if !bytes.Equal(intent, append(expectedRaw, '\n')) {
		return errors.New("dead holder terminal intent changed its exact target")
	}
	restored, err := c.runtime.readExistingSchemaSealedFile(filepath.Join(c.origin.Workspace, "state", "admission-restored.json"), true)
	if err != nil || startupContentSHA256(restored) != result.RestoredCaptureSHA256 {
		return errors.New("dead holder finished admission receipt changed")
	}
	var stored productionSchemaMaintenanceHostCapture
	if decodeControllerBackupJSON(restored, &stored) != nil || stored.Phase != "ADMISSION_RESTORED" || !sameSchemaMaintenanceWriterCapture(stored, result.Capture) || stored.ConfirmedPID != result.Capture.ConfirmedPID || stored.ConfirmedInvocationID != result.Capture.ConfirmedInvocationID || stored.ConfirmedCapsuleSHA256 != result.Capture.ConfirmedCapsuleSHA256 || stored.ConfirmedProviderSHA256 != result.Capture.ConfirmedProviderSHA256 {
		return errors.New("dead holder terminal receipt lost its exact original restored capture")
	}
	return d.checkLocal(context.Background())
}
