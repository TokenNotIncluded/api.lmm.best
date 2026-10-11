package deploycli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

const schemaMaintenanceBackupCipherName = "schema-database.age"
const schemaMaintenanceBackupCollectionName = "schema-backup-set.json"
const schemaMaintenanceBackupRecipientName = "schema-age-recipient.txt"
const schemaMaintenanceBackupExportIntentName = "schema-backup-export-intent.json"

// This optional runner capability accepts only an already sealed PostgreSQL
// read-only connection, a schema, a public recipient and an output stream. It
// cannot select an executable, shell text or output path. The identity remains
// on the unprivileged controller.
type schemaMaintenanceBackupStreamRunner interface {
	StreamSchemaMaintenanceBackup(context.Context, string, []string, string, string, io.Writer) (string, int64, error)
}

func (osProductionCommandRunner) StreamSchemaMaintenanceBackup(parent context.Context, databaseURL string, environment []string, schema, recipient string, cipher io.Writer) (string, int64, error) {
	if !isDatabaseSchema(schema) || !schemaMaintenanceBackupRecipient(recipient) || cipher == nil {
		return "", 0, errors.New("schema backup streaming authority is invalid")
	}
	for _, executable := range []string{commandPGDump, commandAge} {
		info, err := os.Lstat(executable)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Mode().Perm()&0100 == 0 {
			return "", 0, errors.New("schema backup fixed native executable is unsafe")
		}
		owner, _, ok := deploymentFileOwnership(info)
		if !ok || owner != 0 {
			return "", 0, errors.New("schema backup fixed native executable owner is unsafe")
		}
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
	defer cancel()
	dump := exec.CommandContext(ctx, commandPGDump, "--format=custom", "--no-password", "--schema="+schema, "--dbname="+databaseURL)
	dump.Env = append([]string(nil), environment...)
	age := exec.CommandContext(ctx, commandAge, "--encrypt", "--recipient", recipient)
	age.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C"}
	return runSchemaMaintenanceBackupPipe(ctx, dump, age, cipher)
}

// Only ciphertext reaches destination. Errors omit subprocess diagnostics and
// arguments because either can contain connection credentials.
func runSchemaMaintenanceBackupPipe(ctx context.Context, dump, age *exec.Cmd, cipher io.Writer) (string, int64, error) {
	fail := func() (string, int64, error) {
		return "", 0, errors.New("schema backup encrypted stream failed; reconcile the fixed export before retrying")
	}
	stream, err := dump.StdoutPipe()
	if err != nil {
		return fail()
	}
	defer stream.Close()
	plain := newSchemaMaintenanceBackupHashWriter(io.Discard)
	sink, err := age.StdinPipe()
	if err != nil {
		return fail()
	}
	age.Stdout = cipher
	dump.Stderr, age.Stderr = io.Discard, io.Discard
	dump.WaitDelay, age.WaitDelay = time.Second, time.Second
	if age.Start() != nil {
		_ = sink.Close()
		return fail()
	}
	if dump.Start() != nil {
		_ = sink.Close()
		_ = age.Process.Kill()
		_ = age.Wait()
		return fail()
	}
	ageDone := make(chan error, 1)
	go func() {
		ageErr := age.Wait()
		if ageErr != nil {
			// Closing the producer read end unblocks io.Copy even if pg_dump has
			// stalled without writing anything after the encryption failure.
			_ = dump.Process.Kill()
			_ = stream.Close()
			_ = sink.Close()
		}
		ageDone <- ageErr
	}()
	_, copyErr := io.Copy(sink, io.TeeReader(stream, plain))
	copyErr = errors.Join(copyErr, sink.Close())
	if copyErr != nil {
		_ = dump.Process.Kill()
		_ = age.Process.Kill()
	}
	ageErr := <-ageDone
	dumpErr := dump.Wait()
	if ageErr != nil || dumpErr != nil || copyErr != nil || ctx.Err() != nil || plain.bytes <= 0 || string(plain.prefix) != "PGDMP" {
		return fail()
	}
	return hex.EncodeToString(plain.hash.Sum(nil)), plain.bytes, nil
}

type schemaMaintenanceBackupHashWriter struct {
	destination io.Writer
	hash        hash.Hash
	bytes       int64
	prefix      []byte
}

func newSchemaMaintenanceBackupHashWriter(destination io.Writer) *schemaMaintenanceBackupHashWriter {
	return &schemaMaintenanceBackupHashWriter{destination: destination, hash: sha256.New(), prefix: make([]byte, 0, 5)}
}
func (w *schemaMaintenanceBackupHashWriter) Write(raw []byte) (int, error) {
	if int64(len(raw)) > controllerBackupMaxArchive-w.bytes {
		return 0, errors.New("schema backup stream exceeds the archive limit")
	}
	n, err := w.destination.Write(raw)
	if n < 0 || n > len(raw) {
		return 0, errors.New("schema backup destination returned an invalid write count")
	}
	if n > 0 {
		_, _ = w.hash.Write(raw[:n])
		w.bytes += int64(n)
		if len(w.prefix) < 5 {
			count := 5 - len(w.prefix)
			if count > n {
				count = n
			}
			w.prefix = append(w.prefix, raw[:count]...)
		}
	}
	if n != len(raw) && err == nil {
		err = io.ErrShortWrite
	}
	return n, err
}
func schemaMaintenanceBackupRecipient(value string) bool {
	if len(value) != 62 || !strings.HasPrefix(value, "age1") {
		return false
	}
	for _, c := range value[4:] {
		if !strings.ContainsRune("023456789acdefghjklmnpqrstuvwxyz", c) {
			return false
		}
	}
	return true
}

// collectBackup runs in the original primary daemon and retains its adopted
// three-descriptor lease. No second adoption or PG owner claim is performed.
func (c *productionSchemaMaintenanceController) collectBackup(ctx context.Context) (productionSchemaMaintenanceBackupSet, error) {
	var result productionSchemaMaintenanceBackupSet
	fail := func() (productionSchemaMaintenanceBackupSet, error) {
		return productionSchemaMaintenanceBackupSet{}, errors.New("schema backup collection failed; reconcile the fixed native export before retrying")
	}
	if c == nil || c.runtime == nil || c.host == nil || c.origin.Host != c.plan.PrimaryHost || c.fence != nil || c.finishUncertain || c.finished {
		return fail()
	}
	streamer, ok := c.runtime.runner.(schemaMaintenanceBackupStreamRunner)
	if !ok || c.host.check(ctx) != nil || c.checkPlan(ctx) != nil {
		return fail()
	}
	stopped, err := c.stoppedOrigins(ctx)
	if err != nil {
		return fail()
	}
	stops, err := schemaMaintenanceBackupStopBindings(c.plan, c.planSHA, stopped)
	if err != nil {
		return fail()
	}
	state := filepath.Join(c.origin.Workspace, "state")
	if controllerBackupDirectory(state, 0) != nil {
		return fail()
	}
	recipientRaw, err := c.runtime.readExistingSchemaSealedFile(filepath.Join(state, schemaMaintenanceBackupRecipientName), true)
	recipient := strings.TrimSuffix(string(recipientRaw), "\n")
	if err != nil || controllerBackupDigest(recipientRaw) != c.plan.BackupPolicy.RecipientSHA256 || string(recipientRaw) != recipient+"\n" || !schemaMaintenanceBackupRecipient(recipient) {
		return fail()
	}
	// This fixed O_EXCL ciphertext is also the one-shot dispatch intent. Failure
	// and acknowledgment loss preserve it for explicit reconciliation.
	target := filepath.Join(state, schemaMaintenanceBackupCipherName)
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fail()
	}
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
	}()
	// Persist the native one-shot reservation before any source connection or
	// pg_dump dispatch. A crash must leave the fixed export present too.
	if file.Sync() != nil || schemaMaintenanceBackupSyncDirectory(state) != nil {
		return fail()
	}
	before, err := c.captureBackupSource(ctx)
	if err != nil {
		return fail()
	}
	old, err := c.runtime.loadMerchantStoreCapsule(c.origin.OldCapsule.Path, c.origin.OldCapsule.SHA256)
	if err != nil {
		return fail()
	}
	child, err := c.runtime.merchantStoreCapsuleEnvironment(ctx, old, c.origin.OldCapsule.SHA256, "")
	if err != nil {
		return fail()
	}
	values := map[string]string{}
	for _, entry := range child {
		key, value, valid := strings.Cut(entry, "=")
		if !valid {
			return fail()
		}
		if _, exists := values[key]; exists {
			return fail()
		}
		values[key] = value
	}
	databaseURL, environment, err := productionSealedDatabaseCommand(values)
	if err != nil {
		return fail()
	}
	ciphertext := newSchemaMaintenanceBackupHashWriter(file)
	plainSHA, plainBytes, err := streamer.StreamSchemaMaintenanceBackup(ctx, databaseURL, environment, c.plan.PreSchema.Schema, recipient, ciphertext)
	if err != nil {
		return fail()
	}
	syncErr, closeErr := file.Sync(), file.Close()
	closed = true
	if syncErr != nil || closeErr != nil {
		return fail()
	}
	archive := controllerBackupArchive{CiphertextSHA256: hex.EncodeToString(ciphertext.hash.Sum(nil)), PlaintextSHA256: plainSHA, CiphertextBytes: ciphertext.bytes, PlaintextBytes: plainBytes}
	if validateSchemaMaintenanceBackupArchive(archive) != nil || controllerBackupVerifyFile(target, 0, archive.CiphertextBytes, archive.CiphertextSHA256) != nil {
		return fail()
	}
	after, err := c.captureBackupSource(ctx)
	if err != nil || !reflect.DeepEqual(before, after) {
		return fail()
	}
	again, err := c.stoppedOrigins(ctx)
	if err != nil || !reflect.DeepEqual(stopped, again) || c.host.check(ctx) != nil || c.checkPlan(ctx) != nil {
		return fail()
	}
	now := time.Now()
	if c.runtime.now != nil {
		now = c.runtime.now()
	}
	result = productionSchemaMaintenanceBackupSet{Format: schemaMaintenanceBackupSetFormat, TransitionID: c.plan.TransitionID, PrePlanSHA256: c.planSHA, PreSchema: c.plan.PreSchema, CapturedUTC: utcSecond(now), RecipientSHA256: c.plan.BackupPolicy.RecipientSHA256, StoppedOrigins: stops, SourceBefore: before, SourceAfter: after, Archive: archive}
	if validateSchemaMaintenanceBackupSet(result, c.plan, c.planSHA, stops, now) != nil {
		return fail()
	}
	for _, capture := range stopped {
		if result.CapturedUTC.Before(capture.StopStartedUTC) {
			return fail()
		}
	}
	raw, err := json.MarshalIndent(result, "", "  ")
	if err != nil || len(raw)+1 > schemaMaintenanceBackupSetMaxBytes || c.runtime.writeSchemaMaintenanceRawNewFile(filepath.Join(state, schemaMaintenanceBackupCollectionName), append(raw, '\n')) != nil {
		return fail()
	}
	return result, nil
}

// Controller import requests the fixed export once and receives ciphertext
// through the existing strict SSH alias. A separate actual restore must pass
// before any owner claim can use this collected backup.
func (runtime *productionReleaseRuntime) collectSchemaMaintenanceBackup(ctx context.Context, plan productionSchemaMaintenancePlan, preSHA string, stopped []productionSchemaMaintenanceHostCapture) (productionSchemaMaintenanceBackupSet, error) {
	var result productionSchemaMaintenanceBackupSet
	fail := func() (productionSchemaMaintenanceBackupSet, error) {
		return productionSchemaMaintenanceBackupSet{}, errors.New("schema backup controller import failed; reconcile the fixed native export before retrying")
	}
	canonicalPlan, planErr := canonicalProductionSchemaMaintenancePlan(plan, defaultProductionPaths())
	if planErr != nil || controllerBackupDigest(canonicalPlan) != preSHA {
		return fail()
	}
	uid := os.Geteuid()
	root := plan.BackupPolicy.ControllerDirectory
	stops, err := schemaMaintenanceBackupStopBindings(plan, preSHA, stopped)
	if uid == 0 || runtime == nil || runtime.runner == nil || err != nil || len(plan.Origins) != 2 || plan.PrimaryAlias != plan.Origins[0].Alias || plan.PrimaryHost != plan.Origins[0].Host || !pathWithinRoot("/home/lightjunction/Backups", root) || root == "/home/lightjunction/Backups" || controllerBackupDirectory(root, uid) != nil || ctx.Err() != nil {
		return fail()
	}
	for _, name := range []string{"database.age", schemaMaintenanceBackupCollectionName, ".complete", "restore-proof.json", schemaMaintenanceBackupExportIntentName} {
		if _, err = os.Lstat(filepath.Join(root, name)); !errors.Is(err, os.ErrNotExist) {
			return fail()
		}
	}
	recipient, err := controllerBackupReadPrivate(filepath.Join(root, "age-recipient.txt"), uid, 8192)
	if err != nil || controllerBackupDigest(recipient) != plan.BackupPolicy.RecipientSHA256 {
		return fail()
	}
	primary := plan.Origins[0]
	engine := filepath.Join(filepath.Dir(primary.ArtifactCapsule.Path), "tmp", "migrations", "merchant-store-candidate", deployEngineName)
	words := []string{engine, "production", "schema-maintenance", "rpc", "--plan", filepath.Join(primary.Workspace, "schema-maintenance-plan.json"), "--plan-sha256", preSHA, "--action", "backup-export"}
	for i := range words {
		words[i] = schemaMaintenanceSSHWord(words[i])
	}
	strict := []string{"-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "ConnectTimeout=10"}
	intent, err := json.MarshalIndent(struct {
		Format         string                                  `json:"format"`
		TransitionID   string                                  `json:"transition_id"`
		PrePlanSHA256  string                                  `json:"pre_plan_sha256"`
		StoppedOrigins []productionSchemaMaintenanceBackupStop `json:"stopped_origins"`
	}{"lmm-schema-backup-export-intent-v1", plan.TransitionID, preSHA, stops}, "", "  ")
	if err != nil || schemaMaintenanceBackupControllerNewFile(filepath.Join(root, schemaMaintenanceBackupExportIntentName), append(intent, '\n'), uid) != nil {
		return fail()
	}
	output, err := runtime.runner.Run(ctx, productionCommand{Name: commandSSH, Args: append(append([]string(nil), strict...), primary.Alias, strings.Join(words, " ")), Timeout: 15*time.Minute + 10*time.Second, OutputLimit: schemaMaintenanceBackupSetMaxBytes + 4096})
	if err != nil {
		return fail()
	}
	var reply schemaMaintenanceRPCReply
	if schemaMaintenanceRPCDecode(bytes.TrimSuffix(output, []byte{'\n'}), &reply) != nil || !reply.OK || reply.Protocol != schemaMaintenanceRPCProtocol || reply.Action != "backup-export" || validateSchemaMaintenanceRPCBinding(reply.Binding) != nil || reply.Binding.TransitionID != plan.TransitionID || reply.Binding.PlanSHA256 != preSHA || reply.Binding.DaemonExeSHA256 != plan.Worker.Provider.DeployEngineSHA256 || !productionSHA256Pattern.MatchString(reply.Nonce) || decodeControllerBackupJSON(reply.Result, &result) != nil {
		return fail()
	}
	typed, _ := json.Marshal(result)
	canonicalReply := reply
	canonicalReply.Result = typed
	canonical, err := schemaMaintenanceRPCMarshal(canonicalReply)
	if err != nil || !bytes.Equal(canonical, bytes.TrimSuffix(output, []byte{'\n'})) {
		return fail()
	}
	now := time.Now()
	if runtime.now != nil {
		now = runtime.now()
	}
	if validateSchemaMaintenanceBackupSet(result, plan, preSHA, stops, now) != nil {
		return fail()
	}
	for _, capture := range stopped {
		if result.CapturedUTC.Before(capture.StopStartedUTC) {
			return fail()
		}
	}
	if controllerBackupCheckSpace(root, result.Archive.CiphertextBytes) != nil {
		return fail()
	}
	scratch, err := os.MkdirTemp(root, ".schema-import-private-")
	if err != nil {
		return fail()
	}
	defer os.RemoveAll(scratch)
	if os.Chmod(scratch, 0700) != nil {
		return fail()
	}
	staged := filepath.Join(scratch, "database.age")
	remote := primary.Alias + ":" + filepath.Join(primary.Workspace, "state", schemaMaintenanceBackupCipherName)
	if _, err = runtime.runner.Run(ctx, productionCommand{Name: commandSCP, Args: append(append([]string(nil), strict...), "--", remote, staged), Timeout: 10 * time.Minute, Sensitive: true, OutputLimit: 8192}); err != nil {
		return fail()
	}
	// scp can honor the source mode; normalize only this freshly imported file.
	info, err := os.Lstat(staged)
	if err != nil || !info.Mode().IsRegular() {
		return fail()
	}
	owner, links, ok := deploymentFileOwnership(info)
	if !ok || int(owner) != uid || links != 1 || os.Chmod(staged, 0600) != nil {
		return fail()
	}
	if controllerBackupVerifyFile(staged, uid, result.Archive.CiphertextBytes, result.Archive.CiphertextSHA256) != nil {
		return fail()
	}
	target, err := os.OpenFile(filepath.Join(root, "database.age"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fail()
	}
	copyErr := controllerBackupCopyVerified(staged, uid, result.Archive.CiphertextBytes, result.Archive.CiphertextSHA256, target)
	syncErr, closeErr := target.Sync(), target.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil {
		return fail()
	}
	raw, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fail()
	}
	raw = append(raw, '\n')
	if len(raw) > schemaMaintenanceBackupSetMaxBytes {
		return fail()
	}
	for _, entry := range []struct {
		name string
		raw  []byte
	}{{schemaMaintenanceBackupCollectionName, raw}, {".complete", []byte(controllerBackupDigest(raw) + "\n")}} {
		if schemaMaintenanceBackupControllerNewFile(filepath.Join(root, entry.name), entry.raw, uid) != nil {
			return fail()
		}
	}

	return result, nil
}

func schemaMaintenanceBackupControllerNewFile(path string, raw []byte, uid int) error {
	if controllerBackupDirectory(filepath.Dir(path), uid) != nil {
		return errors.New("schema backup controller directory is unsafe")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	n, writeErr := file.Write(raw)
	if n != len(raw) && writeErr == nil {
		writeErr = io.ErrShortWrite
	}
	syncErr, closeErr := file.Sync(), file.Close()
	if err = errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	return schemaMaintenanceBackupSyncDirectory(filepath.Dir(path))
}
func schemaMaintenanceBackupSyncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
