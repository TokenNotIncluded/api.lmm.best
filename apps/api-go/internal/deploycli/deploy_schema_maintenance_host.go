package deploycli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const schemaMaintenanceGuardianProtocol = "lmm-schema-maintenance-lock-v1"
const schemaMaintenanceAdmissionKind = "lmm-schema-maintenance"

// This is a projection of the dedicated, immutable maintenance plan. It grants
// no package installation, migration or financial-transition permission.
type productionSchemaMaintenanceHostBinding struct {
	TransitionID          string `json:"transition_id"`
	PlanPath              string `json:"plan_path"`
	PlanSHA256            string `json:"plan_sha256"`
	Host                  string `json:"host"`
	CapsulePath           string `json:"capsule_path"`
	CapsuleSHA256         string `json:"capsule_sha256"`
	GuardianSocket        string `json:"guardian_socket"`
	GuardianBindingPath   string `json:"guardian_binding_path"`
	GuardianBindingSHA256 string `json:"guardian_binding_sha256"`
	GuardianProtocol      string `json:"guardian_protocol"`
	Workspace             string `json:"workspace"`
}

type productionSchemaMaintenanceHostLease interface {
	Check(context.Context) error
	Close() error
}

// validate is wired by the dedicated maintenance owner to its actual signed
// plan and physical owner checks. There is intentionally no CLI approval bit.
type productionSchemaMaintenanceHost struct {
	runtime  *productionRuntime
	binding  productionSchemaMaintenanceHostBinding
	lease    productionSchemaMaintenanceHostLease
	validate func(context.Context) error
}

type productionSchemaMaintenanceHostCapture struct {
	Format                  int       `json:"format"`
	TransitionID            string    `json:"transition_id"`
	PlanSHA256              string    `json:"plan_sha256"`
	Host                    string    `json:"host"`
	Phase                   string    `json:"phase"`
	CapsuleSHA256           string    `json:"capsule_sha256"`
	ProviderSHA256          string    `json:"provider_sha256"`
	PID                     int       `json:"pid"`
	InvocationID            string    `json:"invocation_id"`
	OriginalAdmissionSHA256 string    `json:"original_admission_sha256"`
	ClosedAdmissionSHA256   string    `json:"closed_admission_sha256"`
	NginxPID                int       `json:"nginx_pid"`
	NginxInvocationID       string    `json:"nginx_invocation_id"`
	NginxGenerationSHA256   string    `json:"nginx_generation_sha256"`
	StopStartedUTC          time.Time `json:"stop_started_utc,omitempty"`
	ShutdownJournalSHA256   string    `json:"shutdown_journal_sha256,omitempty"`
	ConfirmedCapsuleSHA256  string    `json:"confirmed_capsule_sha256,omitempty"`
	ConfirmedProviderSHA256 string    `json:"confirmed_provider_sha256,omitempty"`
	ConfirmedPID            int       `json:"confirmed_pid,omitempty"`
	ConfirmedInvocationID   string    `json:"confirmed_invocation_id,omitempty"`
}

func schemaMaintenanceHostWorkspace(paths productionPaths, id string) string {
	return filepath.Join(filepath.Dir(paths.WorkRoot), "schema-maintenance", id)
}

func (runtime *productionRuntime) newSchemaMaintenanceHost(ctx context.Context, binding productionSchemaMaintenanceHostBinding, validate func(context.Context) error) (*productionSchemaMaintenanceHost, error) {
	if runtime.effectiveUID() != 0 {
		return nil, errors.New("schema maintenance host requires root")
	}
	host, err := runtime.hostname()
	registered := false
	for _, origin := range merchantStartupOrigins {
		registered = registered || host == origin.Host
	}
	if err != nil || !registered || validate == nil || !productionIDPattern.MatchString(binding.TransitionID) || !productionSHA256Pattern.MatchString(binding.PlanSHA256) ||
		!productionSHA256Pattern.MatchString(binding.CapsuleSHA256) || !productionSHA256Pattern.MatchString(binding.GuardianBindingSHA256) || host != binding.Host ||
		binding.GuardianProtocol != schemaMaintenanceGuardianProtocol || binding.Workspace != schemaMaintenanceHostWorkspace(runtime.paths, binding.TransitionID) ||
		binding.PlanPath != filepath.Join(binding.Workspace, "schema-maintenance-plan.json") || binding.GuardianBindingPath != filepath.Join(binding.Workspace, "state", "guardian-binding.json") ||
		binding.GuardianSocket != filepath.Join(binding.Workspace, "state", "guardian.sock") {
		return nil, errors.New("schema maintenance host does not match its exact immutable local authority")
	}
	if err := validate(ctx); err != nil {
		return nil, err
	}
	if _, err := runtime.loadMerchantStoreCapsule(binding.CapsulePath, binding.CapsuleSHA256); err != nil {
		return nil, err
	}
	lease, err := receiveSchemaMaintenanceHostLocks(ctx, binding, runtime.requiredOwnerUID)
	if err != nil {
		return nil, err
	}
	h := &productionSchemaMaintenanceHost{runtime: runtime, binding: binding, lease: lease, validate: validate}
	if err := h.check(ctx); err != nil {
		_ = lease.Close()
		return nil, err
	}
	return h, nil
}

func (h *productionSchemaMaintenanceHost) check(ctx context.Context) error {
	if h == nil || h.runtime == nil || h.validate == nil || h.lease == nil {
		return errors.New("schema maintenance host has no actual guardian lease")
	}
	if err := h.validate(ctx); err != nil {
		return err
	}
	return h.lease.Check(ctx)
}

func (h *productionSchemaMaintenanceHost) Close() error {
	if h == nil || h.lease == nil {
		return nil
	}
	return h.lease.Close()
}

func schemaMaintenanceAdmissionBarrier(original []byte, id string) ([]byte, error) {
	if !productionIDPattern.MatchString(id) || bytes.Contains(original, []byte(schemaMaintenanceAdmissionKind)) || bytes.Contains(original, []byte(merchantStartupAdmissionKind)) {
		return nil, errors.New("schema maintenance admission contains another owner")
	}
	body := schemaMaintenanceAdmissionKind + ":" + id
	if _, err := billingBarrierBody(original, body); err != nil {
		return nil, err
	}
	return append([]byte("add_header Retry-After '60' always;\nadd_header X-LMM-Schema-Maintenance '"+id+"' always;\nreturn 503 '"+body+"';\n"), original...), nil
}

func (h *productionSchemaMaintenanceHost) writeNew(name string, raw []byte) error {
	if filepath.Base(name) != name || name == "." || name == ".." {
		return errors.New("schema maintenance evidence name is not fixed")
	}
	state := filepath.Join(h.binding.Workspace, "state")
	if err := h.runtime.merchantStorePrivateDirectory(state, false); err != nil {
		return err
	}
	path := filepath.Join(state, name)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(raw)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return syncDirectory(state)
}

func (h *productionSchemaMaintenanceHost) writeCapture(name string, capture productionSchemaMaintenanceHostCapture) error {
	raw, err := json.MarshalIndent(capture, "", "  ")
	if err != nil {
		return err
	}
	return h.writeNew(name, append(raw, '\n'))
}

func (h *productionSchemaMaintenanceHost) readCapture(name string) (productionSchemaMaintenanceHostCapture, error) {
	var capture productionSchemaMaintenanceHostCapture
	if filepath.Base(name) != name || name == "." || name == ".." {
		return capture, errors.New("schema maintenance capture name is not fixed")
	}
	raw, err := h.runtime.readExistingSchemaSealedFile(filepath.Join(h.binding.Workspace, "state", name), true)
	if err != nil || len(raw) > 16384 {
		return capture, errors.New("schema maintenance actual capture is unavailable")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&capture) != nil || decoder.Decode(&struct{}{}) != io.EOF || capture.Format != 1 || capture.TransitionID != h.binding.TransitionID || capture.PlanSHA256 != h.binding.PlanSHA256 || capture.Host != h.binding.Host || capture.CapsuleSHA256 != h.binding.CapsuleSHA256 || capture.PID <= 1 || !existingSchemaInvocationPattern.MatchString(capture.InvocationID) || !productionSHA256Pattern.MatchString(capture.ProviderSHA256) || !productionSHA256Pattern.MatchString(capture.OriginalAdmissionSHA256) || !productionSHA256Pattern.MatchString(capture.ClosedAdmissionSHA256) {
		return capture, errors.New("schema maintenance actual capture differs from immutable host binding")
	}
	canonical, _ := json.MarshalIndent(capture, "", "  ")
	if !bytes.Equal(append(canonical, '\n'), raw) {
		return capture, errors.New("schema maintenance capture is not canonical")
	}
	if capture.Phase == "ADMISSION_RESTORED" && (!productionSHA256Pattern.MatchString(capture.ConfirmedCapsuleSHA256) || !productionSHA256Pattern.MatchString(capture.ConfirmedProviderSHA256) || capture.ConfirmedPID <= 1 || !existingSchemaInvocationPattern.MatchString(capture.ConfirmedInvocationID)) {
		return capture, errors.New("schema maintenance restored capture has no actual confirmed writer generation")
	}
	return capture, nil
}

// closeAdmission saves pre-change facts before touching nginx. Its caller must
// reconcile an interrupted invocation through capture; this is never a retry.
func (h *productionSchemaMaintenanceHost) closeAdmission(ctx context.Context) (productionSchemaMaintenanceHostCapture, error) {
	var capture productionSchemaMaintenanceHostCapture
	if err := h.check(ctx); err != nil {
		return capture, err
	}
	c, err := h.runtime.loadMerchantStoreCapsule(h.binding.CapsulePath, h.binding.CapsuleSHA256)
	if err != nil || c.Format != 1 || c.StartupPolicy != "per-start" {
		return capture, errors.New("schema maintenance requires the actual ordinary per-start capsule")
	}
	if err := h.runtime.qualifyMerchantStoreCapsule(ctx, c, h.binding.CapsuleSHA256, "", true); err != nil {
		return capture, err
	}
	if err := h.runtime.verifyPortableRunningWriter(ctx, c, h.binding.CapsuleSHA256, "", false); err != nil {
		return capture, err
	}
	state, err := h.runtime.billingUnitState(ctx, c.Service)
	pid, parseErr := strconv.Atoi(state["MainPID"])
	if err != nil || parseErr != nil || pid <= 1 || !existingSchemaInvocationPattern.MatchString(state["InvocationID"]) {
		return capture, errors.New("schema maintenance old writer generation is unavailable")
	}
	if err := h.verifyTrackedProvider(ctx, c); err != nil {
		return capture, err
	}
	path := filepath.Join(h.runtime.paths.NginxRoot, "lmm-api-locations.conf")
	original, err := h.runtime.readExistingSchemaSealedFile(path, false)
	if err != nil {
		return capture, err
	}
	barrier, err := schemaMaintenanceAdmissionBarrier(original, h.binding.TransitionID)
	if err != nil {
		return capture, err
	}
	capture = productionSchemaMaintenanceHostCapture{Format: 1, TransitionID: h.binding.TransitionID, PlanSHA256: h.binding.PlanSHA256, Host: h.binding.Host, Phase: "CLOSE_INTENT", CapsuleSHA256: h.binding.CapsuleSHA256, ProviderSHA256: c.Candidate.PayloadSHA256, PID: pid, InvocationID: state["InvocationID"], OriginalAdmissionSHA256: startupContentSHA256(original), ClosedAdmissionSHA256: startupContentSHA256(barrier)}
	if err := h.writeNew("admission-original.conf", original); err != nil {
		return capture, err
	}
	if err := h.writeCapture("close-intent.json", capture); err != nil {
		return capture, err
	}
	if err := h.check(ctx); err != nil {
		return capture, err
	}
	if err := h.runtime.installAdmissionBarrier(ctx, barrier); err != nil {
		return capture, err
	}
	if err := h.runtime.awaitAdmissionDrain(ctx, func(ctx context.Context) bool { return h.probeAdmission(ctx) == nil }); err != nil {
		return capture, err
	}
	if err := h.requireRunningGeneration(ctx, capture); err != nil {
		return capture, err
	}
	if err := h.captureAdmission(ctx, &capture); err != nil {
		return capture, err
	}
	capture.Phase = "ADMISSION_CLOSED"
	if err := h.check(ctx); err != nil {
		return capture, err
	}
	return capture, h.writeCapture("admission-closed.json", capture)
}

// The complete signed package is already reverified by qualifyMerchantStoreCapsule.
// Read its exact marker directly so Ubuntu does not acquire a pacman dependency.
func (h *productionSchemaMaintenanceHost) verifyTrackedProvider(ctx context.Context, c productionMerchantStoreCapsule) error {
	marker := "usr/share/doc/lmm-api-go-bin/" + refundTaskDrainCapability
	pkg := filepath.Join(c.Root, c.Candidate.PackagePath)
	if h.runtime.merchantStoreCapsuleFile(pkg, 0600) != nil || sha256MustEqual(pkg, c.Candidate.PackageSHA256) != nil {
		return errors.New("schema maintenance tracked provider package changed")
	}
	data, err := h.runtime.runner.Run(ctx, productionCommand{Name: commandBsdtar, Args: []string{"-xOf", pkg, marker}, Env: []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C"}, OutputLimit: 1024})
	if err != nil || !bytes.Equal(data, []byte("v1\n")) || sha256MustEqual(pkg, c.Candidate.PackageSHA256) != nil {
		return errors.New("schema maintenance refuses a provider without the signed refund-task drain capability")
	}
	return nil
}

func (h *productionSchemaMaintenanceHost) requireRunningGeneration(ctx context.Context, capture productionSchemaMaintenanceHostCapture) error {
	state, err := h.runtime.billingUnitState(ctx, h.runtime.paths.Service)
	if err != nil || state["MainPID"] != strconv.Itoa(capture.PID) || state["ExecMainPID"] != state["MainPID"] || state["InvocationID"] != capture.InvocationID || state["ActiveState"] != "active" || state["SubState"] != "running" || sha256MustEqual(filepath.Join("/proc", strconv.Itoa(capture.PID), "exe"), capture.ProviderSHA256) != nil {
		return errors.New("schema maintenance old writer generation changed")
	}
	return nil
}

// closedOrigins is the dedicated controller's fixed two-origin actual capture
// operation, never a caller-provided all_closed boolean or selectable receipt.
func (h *productionSchemaMaintenanceHost) stopWriter(ctx context.Context, closedOrigins func(context.Context) error) (productionSchemaMaintenanceHostCapture, error) {
	capture, err := h.readCapture("close-intent.json")
	if err != nil {
		return capture, err
	}
	if closedOrigins == nil || capture.Phase != "CLOSE_INTENT" {
		return capture, errors.New("schema maintenance stop requires the actual closed-origin controller")
	}
	if err := h.check(ctx); err != nil {
		return capture, err
	}
	if err := closedOrigins(ctx); err != nil {
		return capture, err
	}
	if err := h.captureAdmission(ctx, &capture); err != nil {
		return capture, err
	}
	if err := h.requireRunningGeneration(ctx, capture); err != nil {
		return capture, err
	}
	c, err := h.runtime.loadMerchantStoreCapsule(h.binding.CapsulePath, h.binding.CapsuleSHA256)
	if err != nil {
		return capture, err
	}
	if err := h.verifyTrackedProvider(ctx, c); err != nil {
		return capture, err
	}
	capture.Phase, capture.StopStartedUTC = "STOP_INTENT", h.runtime.now().UTC()
	if err := h.writeCapture("stop-intent.json", capture); err != nil {
		return capture, err
	}
	if err := h.check(ctx); err != nil {
		return capture, err
	}
	if _, err := h.runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"stop", h.runtime.paths.Service}}); err != nil {
		return capture, err
	}
	return h.sealStopped(ctx)
}

// captureStopped reconciles a possibly accepted stop without repeating it. It
// requires the original intent, exact normal exit and bounded complete journal.
func (h *productionSchemaMaintenanceHost) captureStopped(ctx context.Context) (productionSchemaMaintenanceHostCapture, error) {
	capture, err := h.readCapture("stop-intent.json")
	if err != nil {
		return capture, err
	}
	if capture.Phase != "STOP_INTENT" || capture.StopStartedUTC.IsZero() {
		return capture, errors.New("schema maintenance has no original writer stop intent")
	}
	closeIntent, err := h.readCapture("close-intent.json")
	if err != nil || closeIntent.Phase != "CLOSE_INTENT" || !sameSchemaMaintenanceWriterCapture(closeIntent, capture) {
		return capture, errors.New("schema maintenance stop intent differs from its original closed writer generation")
	}
	if err := h.check(ctx); err != nil {
		return capture, err
	}
	state, err := h.runtime.billingUnitState(ctx, h.runtime.paths.Service)
	if err != nil || state["InvocationID"] != capture.InvocationID || cleanBillingUnitExit(state, capture.PID) != nil {
		return capture, errors.New("schema maintenance writer did not verifiably exit normally")
	}
	c, err := h.runtime.loadMerchantStoreCapsule(h.binding.CapsulePath, h.binding.CapsuleSHA256)
	if err != nil || capture.ProviderSHA256 != c.Candidate.PayloadSHA256 || h.runtime.verifyMerchantStorePortableInstalled(c, capture.ProviderSHA256) != nil {
		return capture, errors.New("schema maintenance stopped provider differs from the actual old capsule")
	}
	// This rechecks old loaded startup/config/search-path bytes without comparing
	// its old catalog digest, which intentionally becomes stale after the DDL.
	if _, err := h.runtime.merchantStoreCapsuleEnvironment(ctx, c, h.binding.CapsuleSHA256, ""); err != nil {
		return capture, err
	}
	journal, err := h.shutdownJournal(ctx, capture)
	if err != nil || len(journal) >= 1<<20 || validateSchemaMaintenanceShutdown(journal) != nil {
		return capture, errors.New("schema maintenance shutdown/refund/flush journal is incomplete or unsuccessful")
	}
	if err := h.captureAdmission(ctx, &capture); err != nil {
		return capture, err
	}
	capture.Phase, capture.ShutdownJournalSHA256 = "WRITER_STOPPED_UNSEALED", startupContentSHA256(journal)
	if _, err := os.Lstat(filepath.Join(h.binding.Workspace, "state", "writer-stopped.json")); err == nil {
		stored, err := h.readCapture("writer-stopped.json")
		if err != nil || stored.Phase != "WRITER_STOPPED" || !sameSchemaMaintenanceWriterCapture(stored, capture) || !stored.StopStartedUTC.Equal(capture.StopStartedUTC) || stored.ShutdownJournalSHA256 != capture.ShutdownJournalSHA256 {
			return capture, errors.New("schema maintenance stopped receipt differs from its original intent/journal")
		}
		capture.Phase = "WRITER_STOPPED"
	} else if !errors.Is(err, os.ErrNotExist) {
		return capture, err
	}
	if err := h.check(ctx); err != nil {
		return capture, err
	}
	return capture, nil
}

func (h *productionSchemaMaintenanceHost) shutdownJournal(ctx context.Context, capture productionSchemaMaintenanceHostCapture) ([]byte, error) {
	path := filepath.Join(h.binding.Workspace, "state", "shutdown.log")
	_, savedErr := os.Lstat(path)
	if savedErr != nil && !errors.Is(savedErr, os.ErrNotExist) {
		return nil, savedErr
	}
	_, receiptErr := os.Lstat(filepath.Join(h.binding.Workspace, "state", "writer-stopped.json"))
	if receiptErr != nil && !errors.Is(receiptErr, os.ErrNotExist) {
		return nil, receiptErr
	}
	if savedErr != nil && receiptErr == nil {
		return nil, errors.New("schema maintenance sealed stop receipt lost its original shutdown journal")
	}
	var saved []byte
	if savedErr == nil {
		var err error
		saved, err = h.runtime.readExistingSchemaSealedFile(path, true)
		if err != nil {
			return nil, err
		}
	}
	if receiptErr == nil {
		stored, err := h.readCapture("writer-stopped.json")
		if err != nil || stored.Phase != "WRITER_STOPPED" || !sameSchemaMaintenanceWriterCapture(stored, capture) || !stored.StopStartedUTC.Equal(capture.StopStartedUTC) || stored.ShutdownJournalSHA256 != startupContentSHA256(saved) {
			return nil, errors.New("schema maintenance stored shutdown journal is not bound to the original stop generation")
		}
		return saved, nil
	}
	// A crash may leave shutdown.log before writer-stopped.json. Cat-only log
	// bytes cannot prove which invocation produced them: re-read that exact
	// original PID/invocation/time range before sealing the leftover file.
	actual, err := h.actualShutdownJournal(ctx, capture)
	if err != nil {
		return nil, err
	}
	if savedErr == nil && !bytes.Equal(saved, actual) {
		return nil, errors.New("schema maintenance unsealed shutdown journal differs from the original writer invocation")
	}
	return actual, nil
}

func sameSchemaMaintenanceWriterCapture(left, right productionSchemaMaintenanceHostCapture) bool {
	return left.Format == right.Format && left.TransitionID == right.TransitionID && left.PlanSHA256 == right.PlanSHA256 &&
		left.Host == right.Host && left.CapsuleSHA256 == right.CapsuleSHA256 && left.ProviderSHA256 == right.ProviderSHA256 &&
		left.PID == right.PID && left.InvocationID == right.InvocationID && left.OriginalAdmissionSHA256 == right.OriginalAdmissionSHA256 &&
		left.ClosedAdmissionSHA256 == right.ClosedAdmissionSHA256
}

func (h *productionSchemaMaintenanceHost) actualShutdownJournal(ctx context.Context, capture productionSchemaMaintenanceHostCapture) ([]byte, error) {
	return h.runtime.runner.Run(ctx, productionCommand{Name: commandJournalctl, Args: []string{"--no-pager", "--output=cat", "--since", fmt.Sprintf("@%d.%06d", capture.StopStartedUTC.Unix(), capture.StopStartedUTC.Nanosecond()/1000), "-u", h.runtime.paths.Service, "_PID=" + strconv.Itoa(capture.PID), "_SYSTEMD_INVOCATION_ID=" + capture.InvocationID}, Sensitive: true, OutputLimit: 1 << 20})
}

// sealStopped is the explicit evidence-only reconciliation operation after an
// uncertain stop response. It never repeats systemctl stop or edits an intent.
func (h *productionSchemaMaintenanceHost) sealStopped(ctx context.Context) (productionSchemaMaintenanceHostCapture, error) {
	capture, err := h.captureStopped(ctx)
	if err != nil || capture.Phase == "WRITER_STOPPED" {
		return capture, err
	}
	journal, err := h.shutdownJournal(ctx, capture)
	if err != nil || startupContentSHA256(journal) != capture.ShutdownJournalSHA256 {
		return capture, errors.New("schema maintenance shutdown journal changed before sealing")
	}
	path := filepath.Join(h.binding.Workspace, "state", "shutdown.log")
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		if err := h.writeNew("shutdown.log", journal); err != nil {
			return capture, err
		}
	} else if err != nil {
		return capture, err
	}
	capture.Phase = "WRITER_STOPPED"
	if err := h.check(ctx); err != nil {
		return capture, err
	}
	return capture, h.writeCapture("writer-stopped.json", capture)
}

func validateSchemaMaintenanceShutdown(journal []byte) error {
	if err := validateBillingShutdownJournal(journal); err != nil {
		return err
	}
	// SaveQuotaDataCache is silent when no quota data or drops remain. The
	// billing validator still rejects every failed or malformed present flush.
	if bytes.Count(journal, []byte("refund_tasks execution_complete=")) != 1 {
		return errors.New("schema maintenance requires the tracked refund completion report")
	}
	return nil
}

func (h *productionSchemaMaintenanceHost) probeAdmission(ctx context.Context) error {
	u, err := url.Parse(h.runtime.paths.PublicBaseURL)
	if err != nil || u.Scheme != "https" || u.Host != "api.lmm.best" || u.Path != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("schema maintenance admission requires the canonical production origin")
	}
	for _, route := range []string{"/api/status", "/api/user/self", "/v1/models"} {
		out, err := h.runtime.runner.Run(ctx, productionCommand{Name: "/usr/bin/curl", Args: []string{"--disable", "--silent", "--show-error", "--include", "--max-time", "5", "--noproxy", "*", "--connect-to", "api.lmm.best:443:127.0.0.1:443", "https://api.lmm.best" + route}, Env: []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C"}, Timeout: 7 * time.Second, OutputLimit: 32768})
		if err != nil || validateSchemaMaintenanceAdmissionResponse(out, h.binding.TransitionID) != nil {
			return errors.New("schema maintenance direct-origin admission proof failed")
		}
	}
	return nil
}

func validateSchemaMaintenanceAdmissionResponse(out []byte, id string) error {
	parts := bytes.SplitN(out, []byte("\r\n\r\n"), 2)
	if len(parts) != 2 || string(parts[1]) != schemaMaintenanceAdmissionKind+":"+id {
		return errors.New("schema maintenance admission body differs")
	}
	lines := strings.Split(string(parts[0]), "\r\n")
	status := strings.Fields(lines[0])
	if len(status) < 2 || status[1] != "503" {
		return errors.New("schema maintenance admission status differs")
	}
	headers := map[string]string{}
	for _, line := range lines[1:] {
		key, value, ok := strings.Cut(line, ":")
		key = strings.ToLower(key)
		if !ok || key == "" {
			return errors.New("schema maintenance admission headers are malformed")
		}
		if _, exists := headers[key]; exists {
			return errors.New("schema maintenance admission header is duplicated")
		}
		headers[key] = strings.TrimSpace(value)
	}
	if headers["retry-after"] != "60" || headers["x-lmm-schema-maintenance"] != id {
		return errors.New("schema maintenance admission canonical headers differ")
	}
	return nil
}

func (h *productionSchemaMaintenanceHost) captureAdmission(ctx context.Context, capture *productionSchemaMaintenanceHostCapture) error {
	path := filepath.Join(h.runtime.paths.NginxRoot, "lmm-api-locations.conf")
	raw, err := h.runtime.readExistingSchemaSealedFile(path, false)
	if err != nil || startupContentSHA256(raw) != capture.ClosedAdmissionSHA256 {
		return errors.New("schema maintenance persistent admission bytes changed")
	}
	before, err := h.runtime.billingUnitState(ctx, "nginx.service")
	pid, parseErr := strconv.Atoi(before["MainPID"])
	if err != nil || parseErr != nil || pid <= 1 || before["ActiveState"] != "active" || before["SubState"] != "running" || !existingSchemaInvocationPattern.MatchString(before["InvocationID"]) {
		return errors.New("schema maintenance nginx generation is unavailable")
	}
	childrenPath := filepath.Join("/proc", strconv.Itoa(pid), "task", strconv.Itoa(pid), "children")
	children, err := os.ReadFile(childrenPath)
	if err != nil || len(strings.Fields(string(children))) == 0 {
		return errors.New("schema maintenance nginx live worker generation is unavailable")
	}
	if err := h.probeAdmission(ctx); err != nil {
		return err
	}
	counter := countBillingConnections
	if h.runtime.billingConnections != nil {
		counter = h.runtime.billingConnections
	}
	n, err := counter()
	if err != nil || n != 0 {
		return errors.New("schema maintenance backend still has accepted sockets")
	}
	after, err := h.runtime.billingUnitState(ctx, "nginx.service")
	afterChildren, childErr := os.ReadFile(childrenPath)
	current, fileErr := h.runtime.readExistingSchemaSealedFile(path, false)
	if err != nil || childErr != nil || fileErr != nil || before["MainPID"] != after["MainPID"] || before["InvocationID"] != after["InvocationID"] || after["ActiveState"] != "active" || after["SubState"] != "running" || !bytes.Equal(children, afterChildren) || !bytes.Equal(raw, current) {
		return errors.New("schema maintenance loaded admission generation changed during proof")
	}
	capture.NginxPID, capture.NginxInvocationID, capture.NginxGenerationSHA256 = pid, before["InvocationID"], startupContentSHA256(children)
	return nil
}

// captureClosed reobserves an interrupted close. It neither installs nginx
// bytes nor stops/starts a writer, and never creates a successful stop receipt.
func (h *productionSchemaMaintenanceHost) captureClosed(ctx context.Context) (productionSchemaMaintenanceHostCapture, error) {
	capture, err := h.readCapture("close-intent.json")
	if err != nil {
		return capture, err
	}
	if err := h.check(ctx); err != nil {
		return capture, err
	}
	if err := h.captureAdmission(ctx, &capture); err != nil {
		return capture, err
	}
	state, err := h.runtime.billingUnitState(ctx, h.runtime.paths.Service)
	if err != nil {
		return capture, err
	}
	if state["ActiveState"] == "active" {
		if err := h.requireRunningGeneration(ctx, capture); err != nil {
			return capture, err
		}
		capture.Phase = "ADMISSION_CLOSED"
	} else {
		if state["InvocationID"] != capture.InvocationID || cleanBillingUnitExit(state, capture.PID) != nil {
			return capture, errors.New("schema maintenance capture found an unproved writer generation")
		}
		capture.Phase = "STOPPED_EXIT_ONLY"
	}
	return capture, h.check(ctx)
}

// restoreAdmission accepts only a real currently qualified capsule and the
// controller's live two-origin completion check. A restore attempt that fails
// after nginx changed puts this exact owner's barrier back when its file is
// still recognizable. Unknown configuration is never overwritten.
func (h *productionSchemaMaintenanceHost) restoreAdmission(ctx context.Context, capsulePath, capsuleSHA256 string, readyOrigins func(context.Context) error) (result productionSchemaMaintenanceHostCapture, returnErr error) {
	result, err := h.readCapture("close-intent.json")
	if err != nil {
		return result, err
	}
	if readyOrigins == nil {
		return result, errors.New("schema maintenance reopen requires the actual two-origin completion controller")
	}
	if err := h.check(ctx); err != nil {
		return result, err
	}
	if err := readyOrigins(ctx); err != nil {
		return result, err
	}
	c, err := h.runtime.loadMerchantStoreCapsule(capsulePath, capsuleSHA256)
	if err != nil || c.Format != 1 || c.StartupPolicy != "per-start" {
		return result, errors.New("schema maintenance reopen requires a real ordinary per-start capsule")
	}
	if err := h.runtime.qualifyMerchantStoreCapsule(ctx, c, capsuleSHA256, "", true); err != nil {
		return result, err
	}
	if err := h.runtime.verifyPortableRunningWriter(ctx, c, capsuleSHA256, "", false); err != nil {
		return result, err
	}
	confirmed, err := h.runtime.billingUnitState(ctx, c.Service)
	confirmedPID, parseErr := strconv.Atoi(confirmed["MainPID"])
	if err != nil || parseErr != nil || confirmedPID <= 1 || !existingSchemaInvocationPattern.MatchString(confirmed["InvocationID"]) {
		return result, errors.New("schema maintenance confirmed running generation is unavailable")
	}
	if err := h.captureAdmission(ctx, &result); err != nil {
		return result, err
	}
	original, err := h.runtime.readExistingSchemaSealedFile(filepath.Join(h.binding.Workspace, "state", "admission-original.conf"), true)
	if err != nil || startupContentSHA256(original) != result.OriginalAdmissionSHA256 {
		return result, errors.New("schema maintenance original admission snapshot changed")
	}
	barrier, err := schemaMaintenanceAdmissionBarrier(original, h.binding.TransitionID)
	if err != nil || startupContentSHA256(barrier) != result.ClosedAdmissionSHA256 {
		return result, errors.New("schema maintenance reopen does not own the actual barrier")
	}
	intent := struct {
		Format        int    `json:"format"`
		TransitionID  string `json:"transition_id"`
		PlanSHA256    string `json:"plan_sha256"`
		Host          string `json:"host"`
		CapsulePath   string `json:"capsule_path"`
		CapsuleSHA256 string `json:"capsule_sha256"`
	}{1, h.binding.TransitionID, h.binding.PlanSHA256, h.binding.Host, capsulePath, capsuleSHA256}
	intentRaw, _ := json.MarshalIndent(intent, "", "  ")
	if err := h.writeNew("reopen-intent.json", append(intentRaw, '\n')); err != nil {
		return result, err
	}
	if err := h.check(ctx); err != nil {
		return result, err
	}
	changed := false
	defer func() {
		if returnErr == nil || !changed {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := h.restoreAdmissionBarrierAfterFailedReopen(cleanupCtx, original, barrier, &result); err != nil {
			returnErr = fmt.Errorf("%w; %v", returnErr, err)
		}
	}()
	// Mark the attempt before the call: nginx test/reload errors can occur after
	// the persistent file was written, so a failed return still needs recovery.
	changed = true
	if err := h.runtime.installAdmissionBarrier(ctx, original); err != nil {
		return result, err
	}
	if err := h.check(ctx); err != nil {
		return result, err
	}
	if err := readyOrigins(ctx); err != nil {
		return result, err
	}
	if err := h.runtime.verifyPortableRunningWriter(ctx, c, capsuleSHA256, "", false); err != nil {
		return result, err
	}
	latest, err := h.runtime.billingUnitState(ctx, c.Service)
	if err != nil || latest["MainPID"] != confirmed["MainPID"] || latest["InvocationID"] != confirmed["InvocationID"] {
		return result, errors.New("schema maintenance confirmed running generation changed during reopen")
	}
	if err := h.probeRestoredAdmission(ctx, c); err != nil {
		return result, err
	}
	current, err := h.runtime.readExistingSchemaSealedFile(filepath.Join(h.runtime.paths.NginxRoot, "lmm-api-locations.conf"), false)
	if err != nil || !bytes.Equal(current, original) {
		return result, errors.New("schema maintenance admission changed during reopen proof")
	}
	result.Phase = "ADMISSION_RESTORED"
	result.ConfirmedCapsuleSHA256, result.ConfirmedProviderSHA256 = capsuleSHA256, c.Candidate.PayloadSHA256
	result.ConfirmedPID, result.ConfirmedInvocationID = confirmedPID, confirmed["InvocationID"]
	if err := h.check(ctx); err != nil {
		return result, err
	}
	if err := h.writeCapture("admission-restored.json", result); err != nil {
		return result, err
	}
	return result, nil
}

func (h *productionSchemaMaintenanceHost) restoreAdmissionBarrierAfterFailedReopen(ctx context.Context, original, barrier []byte, result *productionSchemaMaintenanceHostCapture) error {
	if err := h.check(ctx); err != nil {
		return fmt.Errorf("failed reopen lost native guardian authority; cleanup did not change admission: %w", err)
	}
	path := filepath.Join(h.runtime.paths.NginxRoot, "lmm-api-locations.conf")
	current, err := h.runtime.readExistingSchemaSealedFile(path, false)
	if err != nil || !bytes.Equal(current, original) && !bytes.Equal(current, barrier) {
		return errors.New("failed reopen could not prove admission restoration without replacing unknown configuration")
	}
	if err := h.check(ctx); err != nil {
		return fmt.Errorf("failed reopen lost native guardian authority before cleanup mutation: %w", err)
	}
	if err := h.runtime.installAdmissionBarrier(ctx, barrier); err != nil {
		return fmt.Errorf("failed reopen could not restore its closed barrier: %w", err)
	}
	if err := h.captureAdmission(ctx, result); err != nil {
		return fmt.Errorf("barrier rewritten but direct-origin closed proof failed: %w", err)
	}
	return nil
}

func (h *productionSchemaMaintenanceHost) probeRestoredAdmission(ctx context.Context, c productionMerchantStoreCapsule) error {
	version, err := packageReleaseVersion(c.Candidate.Version)
	if err != nil {
		return err
	}
	// Bind the provider to this host directly. The original edge IP policy may
	// legitimately conceal /api/status from a loopback TLS client with a 404.
	out, err := h.runtime.runner.Run(ctx, productionCommand{Name: "/usr/bin/curl", Args: []string{"--disable", "--silent", "--show-error", "--include", "--max-time", "5", "--noproxy", "*", "http://127.0.0.1:3000/api/status"}, Env: []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C"}, Timeout: 7 * time.Second, OutputLimit: 32768})
	parts := bytes.SplitN(out, []byte("\r\n\r\n"), 2)
	if err != nil || len(parts) != 2 {
		return errors.New("schema maintenance current loopback provider readiness is unavailable")
	}
	status := strings.Fields(strings.SplitN(string(parts[0]), "\r\n", 2)[0])
	var body struct {
		Success bool `json:"success"`
		Data    struct {
			Version string `json:"version"`
		} `json:"data"`
	}
	if len(status) < 2 || status[1] != "200" || json.Unmarshal(parts[1], &body) != nil || !body.Success || body.Data.Version != version {
		return errors.New("schema maintenance current loopback provider differs")
	}
	// /api/scripts is already public in both its exact and named nginx
	// locations. A successful response proves TLS admission is restored without
	// changing the IP policy or inventing edge headers. The maintenance barrier
	// still precedes this location and returns 503 while admission is closed.
	out, err = h.runtime.runner.Run(ctx, productionCommand{Name: "/usr/bin/curl", Args: []string{"--disable", "--silent", "--show-error", "--include", "--max-time", "5", "--noproxy", "*", "--connect-to", "api.lmm.best:443:127.0.0.1:443", "https://api.lmm.best/api/scripts"}, Env: []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C"}, Timeout: 7 * time.Second, OutputLimit: 32768})
	parts = bytes.SplitN(out, []byte("\r\n\r\n"), 2)
	if err != nil || len(parts) != 2 {
		return errors.New("schema maintenance reopened direct-origin public readiness is unavailable")
	}
	status = strings.Fields(strings.SplitN(string(parts[0]), "\r\n", 2)[0])
	var public struct {
		Success bool              `json:"success"`
		Data    []json.RawMessage `json:"data"`
	}
	if len(status) < 2 || status[1] != "200" || json.Unmarshal(parts[1], &public) != nil || !public.Success || public.Data == nil {
		return errors.New("schema maintenance reopened direct-origin public response differs")
	}
	return nil
}
