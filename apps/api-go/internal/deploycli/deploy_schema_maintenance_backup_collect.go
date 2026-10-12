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
		return "", 0, schemaMaintenanceBackupFail("STREAM_AUTHORITY")
	}
	child, err := schemaMaintenanceBackupDumpEnvironment(environment, schema)
	if err != nil {
		return "", 0, schemaMaintenanceBackupFail("STREAM_ENVIRONMENT")
	}
	for _, executable := range []string{commandPGDump, commandAge} {
		stage := "STREAM_PG_DUMP_EXECUTABLE"
		if executable == commandAge {
			stage = "STREAM_AGE_EXECUTABLE"
		}
		info, err := os.Lstat(executable)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Mode().Perm()&0100 == 0 {
			return "", 0, schemaMaintenanceBackupFail(stage)
		}
		owner, _, ok := deploymentFileOwnership(info)
		if !ok || owner != 0 {
			return "", 0, schemaMaintenanceBackupFail(stage)
		}
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
	defer cancel()
	dump := exec.CommandContext(ctx, commandPGDump, "--format=custom", "--no-password", "--schema="+schema, "--dbname="+databaseURL)
	dump.Env = child
	age := exec.CommandContext(ctx, commandAge, "--encrypt", "--recipient", recipient)
	age.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C"}
	return runSchemaMaintenanceBackupPipe(ctx, dump, age, cipher)
}

// Only ciphertext reaches destination. Errors omit subprocess diagnostics and
// arguments because either can contain connection credentials.
func runSchemaMaintenanceBackupPipe(ctx context.Context, dump, age *exec.Cmd, cipher io.Writer) (string, int64, error) {
	stream, err := dump.StdoutPipe()
	if err != nil {
		return "", 0, schemaMaintenanceBackupFail("PIPE_DUMP_STDOUT")
	}
	defer stream.Close()
	plain := newSchemaMaintenanceBackupHashWriter(io.Discard)
	sink, err := age.StdinPipe()
	if err != nil {
		return "", 0, schemaMaintenanceBackupFail("PIPE_AGE_STDIN")
	}
	age.Stdout = cipher
	dump.Stderr, age.Stderr = io.Discard, io.Discard
	dump.WaitDelay, age.WaitDelay = time.Second, time.Second
	if err := age.Start(); err != nil {
		_ = sink.Close()
		return "", 0, schemaMaintenanceBackupStreamFailure("PIPE_AGE_START", nil, err, nil, ctx.Err())
	}
	if err := dump.Start(); err != nil {
		_ = sink.Close()
		_ = age.Process.Kill()
		ageErr := age.Wait()
		return "", 0, schemaMaintenanceBackupStreamFailure("PIPE_DUMP_START", err, ageErr, nil, ctx.Err())
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
	if ageErr != nil || dumpErr != nil || copyErr != nil || ctx.Err() != nil {
		return "", 0, schemaMaintenanceBackupStreamFailure("PIPE_WAIT", dumpErr, ageErr, copyErr, ctx.Err())
	}
	if plain.bytes <= 0 || string(plain.prefix) != "PGDMP" {
		return "", 0, schemaMaintenanceBackupStreamFailure("PIPE_ARCHIVE_HEADER", nil, nil, nil, nil)
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
	fail := func(stage string) (productionSchemaMaintenanceBackupSet, error) {
		return productionSchemaMaintenanceBackupSet{}, schemaMaintenanceBackupFail(stage)
	}
	if c == nil || c.runtime == nil || c.host == nil || c.origin.Host != c.plan.PrimaryHost || c.fence != nil || c.finishUncertain || c.finished {
		return fail("AUTHORITY")
	}
	streamer, ok := c.runtime.runner.(schemaMaintenanceBackupStreamRunner)
	if !ok || c.host.check(ctx) != nil || c.checkPlan(ctx) != nil {
		return fail("RUNNER_LEASE_PLAN")
	}
	stopped, err := c.stoppedOrigins(ctx)
	if err != nil {
		return fail("STOPPED_ORIGINS")
	}
	stops, err := schemaMaintenanceBackupStopBindings(c.plan, c.planSHA, stopped)
	if err != nil {
		return fail("STOP_BINDINGS")
	}
	state := filepath.Join(c.origin.Workspace, "state")
	if controllerBackupDirectory(state, 0) != nil {
		return fail("STATE_DIRECTORY")
	}
	recipientRaw, err := c.runtime.readExistingSchemaSealedFile(filepath.Join(state, schemaMaintenanceBackupRecipientName), true)
	recipient := strings.TrimSuffix(string(recipientRaw), "\n")
	if err != nil || controllerBackupDigest(recipientRaw) != c.plan.BackupPolicy.RecipientSHA256 || string(recipientRaw) != recipient+"\n" || !schemaMaintenanceBackupRecipient(recipient) {
		return fail("RECIPIENT")
	}
	// This fixed O_EXCL ciphertext is also the one-shot dispatch intent. Failure
	// and acknowledgment loss preserve it for explicit reconciliation.
	target := filepath.Join(state, schemaMaintenanceBackupCipherName)
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fail("CIPHER_RESERVE")
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
		return fail("CIPHER_RESERVE_SYNC")
	}
	before, err := c.captureBackupSource(ctx)
	if err != nil {
		return fail("SOURCE_BEFORE")
	}
	old, err := c.runtime.loadMerchantStoreCapsule(c.origin.OldCapsule.Path, c.origin.OldCapsule.SHA256)
	if err != nil {
		return fail("OLD_CAPSULE")
	}
	child, err := c.runtime.merchantStoreCapsuleEnvironment(ctx, old, c.origin.OldCapsule.SHA256, "")
	if err != nil {
		return fail("CAPSULE_ENVIRONMENT")
	}
	values := map[string]string{}
	for _, entry := range child {
		key, value, valid := strings.Cut(entry, "=")
		if !valid {
			return fail("ENVIRONMENT_ENTRY")
		}
		if _, exists := values[key]; exists {
			return fail("ENVIRONMENT_DUPLICATE")
		}
		values[key] = value
	}
	databaseURL, environment, err := productionSealedDatabaseCommand(values)
	if err != nil {
		return fail("DATABASE_COMMAND")
	}
	environment, err = schemaMaintenanceBackupDumpEnvironment(environment, c.plan.PreSchema.Schema)
	if err != nil {
		return fail("DUMP_ENVIRONMENT")
	}
	ciphertext := newSchemaMaintenanceBackupHashWriter(file)
	plainSHA, plainBytes, err := streamer.StreamSchemaMaintenanceBackup(ctx, databaseURL, environment, c.plan.PreSchema.Schema, recipient, ciphertext)
	if err != nil {
		return productionSchemaMaintenanceBackupSet{}, schemaMaintenanceBackupSafeFailure(err, "STREAM_EXECUTE")
	}
	syncErr, closeErr := file.Sync(), file.Close()
	closed = true
	if syncErr != nil || closeErr != nil {
		return fail("CIPHER_SYNC_CLOSE")
	}
	archive := controllerBackupArchive{CiphertextSHA256: hex.EncodeToString(ciphertext.hash.Sum(nil)), PlaintextSHA256: plainSHA, CiphertextBytes: ciphertext.bytes, PlaintextBytes: plainBytes}
	if validateSchemaMaintenanceBackupArchive(archive) != nil {
		return fail("ARCHIVE_DECLARATION")
	}
	if controllerBackupVerifyFile(target, 0, archive.CiphertextBytes, archive.CiphertextSHA256) != nil {
		return fail("CIPHER_VERIFY")
	}
	after, err := c.captureBackupSource(ctx)
	if err != nil {
		return fail("SOURCE_AFTER")
	}
	if !reflect.DeepEqual(before, after) {
		return fail("SOURCE_COMPARE")
	}
	again, err := c.stoppedOrigins(ctx)
	if err != nil {
		return fail("STOPPED_ORIGINS_AFTER")
	}
	if !reflect.DeepEqual(stopped, again) {
		return fail("STOPPED_ORIGINS_COMPARE")
	}
	if c.host.check(ctx) != nil {
		return fail("HOST_AFTER")
	}
	if c.checkPlan(ctx) != nil {
		return fail("PLAN_AFTER")
	}
	now := time.Now()
	if c.runtime.now != nil {
		now = c.runtime.now()
	}
	result = productionSchemaMaintenanceBackupSet{Format: schemaMaintenanceBackupSetFormat, TransitionID: c.plan.TransitionID, PrePlanSHA256: c.planSHA, PreSchema: c.plan.PreSchema, CapturedUTC: utcSecond(now), RecipientSHA256: c.plan.BackupPolicy.RecipientSHA256, StoppedOrigins: stops, SourceBefore: before, SourceAfter: after, Archive: archive}
	if validateSchemaMaintenanceBackupSet(result, c.plan, c.planSHA, stops, now) != nil {
		return fail("SET_VALIDATE")
	}
	for _, capture := range stopped {
		if result.CapturedUTC.Before(capture.StopStartedUTC) {
			return fail("SET_CHRONOLOGY")
		}
	}
	raw, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fail("SET_SERIALIZE")
	}
	if len(raw)+1 > schemaMaintenanceBackupSetMaxBytes {
		return fail("SET_SIZE")
	}
	if c.runtime.writeSchemaMaintenanceRawNewFile(filepath.Join(state, schemaMaintenanceBackupCollectionName), append(raw, '\n')) != nil {
		return fail("SET_WRITE")
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
		return productionSchemaMaintenanceBackupSet{}, schemaMaintenanceBackupTransportFailure(err)
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

// pg_dump receives only the sealed PostgreSQL connection/TLS projection. App
// secrets, loader variables and root shell settings have no purpose here.
func schemaMaintenanceBackupDumpEnvironment(inspected []string, schema string) ([]string, error) {
	values := map[string]string{}
	for _, entry := range inspected {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || !productionEnvironmentKeyPattern.MatchString(key) || strings.ContainsRune(value, '\x00') {
			return nil, errors.New("schema backup inspected PostgreSQL environment is malformed")
		}
		if _, exists := values[key]; exists {
			return nil, errors.New("schema backup inspected PostgreSQL environment is duplicated")
		}
		values[key] = value
	}
	if !isDatabaseSchema(schema) || values["PGOPTIONS"] != "-c search_path="+schema+" -c default_transaction_read_only=on" {
		return nil, errors.New("schema backup requires the exact sealed read-only PostgreSQL options")
	}
	child := []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "PGPASSFILE=/dev/null", "PGSERVICEFILE=/dev/null"}
	for _, key := range []string{"PGOPTIONS", "PGHOST", "PGHOSTADDR", "PGPORT", "PGDATABASE", "PGUSER", "PGPASSWORD", "PGSSLMODE", "PGSSLCERT", "PGSSLKEY", "PGSSLROOTCERT", "PGSSLCRL", "PGSSLCRLDIR", "PGSSLNEGOTIATION", "PGCHANNELBINDING", "PGTARGETSESSIONATTRS", "PGGSSENCMODE", "PGKRBSRVNAME", "PGGSSLIB"} {
		if value, exists := values[key]; exists {
			child = append(child, key+"="+value)
		}
	}
	return child, nil
}
