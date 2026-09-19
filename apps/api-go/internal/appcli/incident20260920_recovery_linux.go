//go:build linux

package appcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"time"
)

const incident20260920Deployment = "manual-go-v0.2.55-20260919T172635Z"

// RunIncident20260920Recovery restores only the unchanged signed N-1 provider.
// It cannot install packages, modify balances, or accept arbitrary deployment IDs.
func RunIncident20260920Recovery(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 || args[0] != "--confirm" || args[1] != "api.lmm.best" {
		return ExitUsage
	}
	r := defaultProductionRuntime()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	result, err := r.recoverIncident20260920(ctx)
	if err != nil {
		fmt.Fprintln(stderr, "unchanged-provider recovery:", err)
		return ExitError
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return ExitError
	}
	_, _ = stdout.Write(append(data, '\n'))
	return ExitOK
}

func validateIncident20260920(m productionManifest, s productionStatus) error {
	if m.DeploymentID != incident20260920Deployment || s.DeploymentID != m.DeploymentID || s.Phase != "ROLLBACK_REQUIRED" || m.ExpectedVersion != "0.2.55" || m.OldVersion != "0.2.52" ||
		m.Go.CandidateGitRevision != "ae5bf90d7bf3cc97a8a1cb5d3a69ffec4b592267" || m.Go.RollbackSHA256 != "b957626852cd85c83690352f177e358f4bff7f6953d5b3ef6ddf8203c09daf46" ||
		m.Go.RollbackIdentity != "lmm-api-go-bin 0.2.52-1" || m.Web.CandidateIdentity != "lmm-api-web-bin 0.1.81-1" || !m.Go.Changed || m.Web.Changed || !m.ObservationStartedUTC.IsZero() {
		return errors.New("not the exact unchanged-provider incident")
	}
	g := m.BillingGate
	if g == nil || !g.StopVerified || !g.AdmissionClosed || g.AdmissionReopened || g.Sequence != 1 || g.GoPID <= 1 || len(g.GoInvocationID) != 32 || !productionSHA256Pattern.MatchString(g.ShutdownJournalSHA256) || g.StopStartedUTC.IsZero() {
		return errors.New("verified original shutdown and closed admission required")
	}
	if m.Frontend.OldTarget != m.Frontend.NewTarget || m.Frontend.OldIndexSHA256 != m.Frontend.NewIndexSHA256 {
		return errors.New("frontend must remain unchanged")
	}
	return nil
}

func (r *productionRuntime) recoverIncident20260920(ctx context.Context) (productionStatus, error) {
	fail := func(err error) (productionStatus, error) { return productionStatus{}, err }
	if r.effectiveUID() != 0 {
		return fail(errors.New("root required"))
	}
	host, err := r.hostname()
	if err != nil || host != r.paths.ExpectedHost {
		return fail(errors.New("wrong production host"))
	}
	w, err := r.openWorkspace(filepath.Join(r.paths.WorkRoot, incident20260920Deployment))
	if err != nil {
		return fail(err)
	}
	lock, err := r.acquireGlobalLock(ctx)
	if err != nil {
		return fail(err)
	}
	defer func() { _ = unlockDeploymentFile(lock); _ = lock.Close() }()
	m, err := r.readManifestForRollback(w)
	if err != nil {
		return fail(err)
	}
	s, err := r.readStatus(w)
	if err != nil {
		return fail(err)
	}
	if err = validateIncident20260920(m, s); err != nil {
		return fail(err)
	}
	if err = r.validateTransactionLock(w); err != nil {
		return fail(err)
	}
	if err = r.verifyRollbackManifestArchives(ctx, m); err != nil {
		return fail(err)
	}
	if err = r.verifyManifestInstalled(ctx, m, true); err != nil {
		return fail(err)
	}
	if err = r.verifyTransitionCLI(m.Go, true); err != nil {
		return fail(err)
	}
	if err = r.verifyPreStopEdgeState(w, m); err != nil {
		return fail(err)
	}
	if err = verifyFrontendIdentity(r.paths.FrontendRoot, m.Frontend.OldTarget, m.Frontend.OldIndexSHA256); err != nil {
		return fail(err)
	}
	state, err := r.billingUnitState(ctx, r.paths.Service)
	if err != nil {
		return fail(err)
	}
	if state["ActiveState"] != "inactive" || state["InvocationID"] != m.BillingGate.GoInvocationID {
		return fail(errors.New("original writer is not stopped"))
	}
	if err = cleanBillingUnitExit(state, m.BillingGate.GoPID); err != nil {
		return fail(err)
	}
	g := m.BillingGate
	since := fmt.Sprintf("@%d.%06d", g.StopStartedUTC.Unix(), g.StopStartedUTC.Nanosecond()/1000)
	journal, err := r.runner.Run(ctx, productionCommand{Name: commandJournalctl, Args: []string{"--no-pager", "--output=cat", "--since", since, "-u", r.paths.Service, "_PID=" + strconv.Itoa(g.GoPID), "_SYSTEMD_INVOCATION_ID=" + g.GoInvocationID}})
	if err != nil {
		return fail(err)
	}
	if fmt.Sprintf("%x", sha256Bytes(journal)) != g.ShutdownJournalSHA256 {
		return fail(errors.New("shutdown journal digest changed"))
	}
	if err = validateBillingShutdownJournal(journal); err != nil {
		return fail(err)
	}
	_, databaseURL, environment, err := r.recoveryDatabase(ctx, w, m)
	if err != nil {
		return fail(err)
	}
	// Save the two exact new default grants before a bounded, migration-locked repair.
	query := fmt.Sprintf(`SELECT COALESCE(json_agg(t ORDER BY id),'[]'::json) FROM %s.casbin_rule t WHERE ptype='p' AND v0='role:admin' AND v1='acquisition'`, m.DatabaseSchema)
	snapshot, err := r.runner.Run(ctx, productionCommand{Name: commandPSQL, Args: []string{"-X", "--no-password", "-At", "-v", "ON_ERROR_STOP=1", "-c", query, databaseURL}, Env: environment, Sensitive: true, Timeout: 15 * time.Second, OutputLimit: 16384})
	if err != nil {
		return fail(err)
	}
	if err = validateIncident20260920Policies(snapshot); err != nil {
		return fail(err)
	}
	if err = writeAtomicRegularFile(filepath.Join(w.stateDir, "incident20260920-policies.json"), snapshot, 0600); err != nil {
		return fail(err)
	}
	query = incident20260920RepairSQL(m.DatabaseSchema)
	if _, err = r.runner.Run(ctx, productionCommand{Name: commandPSQL, Args: []string{"-X", "--no-password", "-At", "-v", "ON_ERROR_STOP=1", "-c", query, databaseURL}, Env: environment, Sensitive: true, Timeout: 30 * time.Second, OutputLimit: 16384}); err != nil {
		return fail(err)
	}
	if err = r.runMigration(ctx, w, m, migrationRun{name: "incident20260920-old-verify", binary: r.paths.InstalledBinary, mode: "verify"}); err != nil {
		return fail(err)
	}
	if err = r.verifyManifestInstalled(ctx, m, true); err != nil {
		return fail(err)
	}
	if _, err = r.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"enable", "--now", r.paths.Service}}); err != nil {
		return fail(err)
	}
	if err = r.probeBackendLocalEventuallyWithBinary(ctx, w, m, r.paths.InstalledBinary, m.OldVersion); err != nil {
		return fail(err)
	}
	if err = r.verifyServiceRestartBaseline(ctx, m); err != nil {
		return fail(err)
	}
	if err = r.reopenBillingAdmission(ctx, w, &m); err != nil {
		return fail(err)
	}
	if err = r.probeReleaseWithBinary(ctx, w, r.paths.InstalledBinary, m.OldVersion, m.Frontend.OldIndexSHA256); err != nil {
		return fail(err)
	}
	result := productionStatus{Phase: "ROLLED_BACK", Version: m.OldVersion, Previous: m.ExpectedVersion, Reason: "unchanged-provider-authz-recovered"}
	if err = r.writeStatus(w, result); err != nil {
		return fail(err)
	}
	result, err = r.readStatus(w)
	if err != nil {
		return fail(err)
	}
	if err = r.finalizeTransactionFiles(w); err != nil {
		return fail(err)
	}
	return result, nil
}

func validateIncident20260920Policies(data []byte) error {
	var rows []struct {
		Ptype string `json:"ptype"`
		V0    string `json:"v0"`
		V1    string `json:"v1"`
		V2    string `json:"v2"`
		V3    string `json:"v3"`
		V4    string `json:"v4"`
		V5    string `json:"v5"`
	}
	if json.Unmarshal(data, &rows) != nil || len(rows) != 2 {
		return errors.New("expected exactly two new built-in grants")
	}
	seen := map[string]bool{}
	for _, v := range rows {
		if v.Ptype != "p" || v.V0 != "role:admin" || v.V1 != "acquisition" || (v.V2 != "read" && v.V2 != "write") || v.V3 != "allow" || v.V4 != "" || v.V5 != "" || seen[v.V2] {
			return errors.New("unexpected authorization grant; no repair allowed")
		}
		seen[v.V2] = true
	}
	return nil
}

func incident20260920RepairSQL(schema string) string {
	return fmt.Sprintf(`BEGIN; SET LOCAL lock_timeout='5s'; SET LOCAL statement_timeout='15s';
 DO $repair$ DECLARE n int; BEGIN
 IF NOT pg_try_advisory_xact_lock(5498135663004418049) THEN RAISE EXCEPTION 'migration lock unavailable'; END IF;
 SELECT count(*) INTO n FROM %s.casbin_rule WHERE ptype='p' AND v0='role:admin' AND v1='acquisition';
 IF n<>2 THEN RAISE EXCEPTION 'grant set changed'; END IF;
 DELETE FROM %s.casbin_rule WHERE ptype='p' AND v0='role:admin' AND v1='acquisition' AND v2 IN ('read','write') AND v3='allow' AND v4='' AND v5='';
 GET DIAGNOSTICS n=ROW_COUNT; IF n<>2 THEN RAISE EXCEPTION 'grant shape changed'; END IF;
 END $repair$; COMMIT;`, schema, schema)
}
