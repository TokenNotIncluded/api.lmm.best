package deploycli

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"
)

const schemaMaintenanceBackupSetFormat = "lmm-schema-maintenance-backup-set-v1"
const schemaMaintenanceBackupProofFormat = "lmm-schema-maintenance-backup-restore-v1"
const schemaMaintenanceBackupRestoreKind = "isolated-bwrap-full-pg-restore-v1"
const schemaMaintenanceBackupProofName = "schema-backup-verification.json"

type productionSchemaMaintenanceBackupStop struct {
	Alias         string `json:"alias"`
	Host          string `json:"host"`
	CaptureSHA256 string `json:"capture_sha256"`
}
type productionSchemaMaintenanceBackupSet struct {
	Format          string                                  `json:"format"`
	TransitionID    string                                  `json:"transition_id"`
	PrePlanSHA256   string                                  `json:"pre_plan_sha256"`
	PreSchema       productionExistingSchemaContract        `json:"pre_schema"`
	CapturedUTC     time.Time                               `json:"captured_utc"`
	RecipientSHA256 string                                  `json:"recipient_sha256"`
	StoppedOrigins  []productionSchemaMaintenanceBackupStop `json:"stopped_origins"`
	SourceBefore    productionSchemaMaintenanceBackupSource `json:"source_before"`
	SourceAfter     productionSchemaMaintenanceBackupSource `json:"source_after"`
	Archive         controllerBackupArchive                 `json:"archive"`
}
type productionSchemaMaintenanceBackupProof struct {
	Format                   string                                  `json:"format"`
	TransitionID             string                                  `json:"transition_id"`
	PrePlanSHA256            string                                  `json:"pre_plan_sha256"`
	PreSchema                productionExistingSchemaContract        `json:"pre_schema"`
	CapturedUTC              time.Time                               `json:"captured_utc"`
	VerifiedUTC              time.Time                               `json:"verified_utc"`
	RecipientSHA256          string                                  `json:"recipient_sha256"`
	StoppedOrigins           []productionSchemaMaintenanceBackupStop `json:"stopped_origins"`
	BackupSetSHA256          string                                  `json:"backup_set_sha256"`
	Archive                  controllerBackupArchive                 `json:"archive"`
	SourceSHA256             string                                  `json:"source_sha256"`
	RestoredLogicalSHA256    string                                  `json:"restored_logical_sha256"`
	SourceLogicalSHA256      string                                  `json:"source_logical_sha256"`
	RestoredSystemIdentifier string                                  `json:"restored_system_identifier"`
	RestoredDatabase         string                                  `json:"restored_database"`
	RestoreKind              string                                  `json:"restore_kind"`
	ActualRestore            bool                                    `json:"actual_restore"`
	NoTCP                    bool                                    `json:"no_tcp"`
	TableCount               int                                     `json:"table_count"`
	TotalRows                int64                                   `json:"total_rows"`
}

func schemaMaintenanceBackupLogicalSHA(s productionSchemaMaintenanceBackupSource) string {
	s = schemaMaintenanceBackupWithoutPhysicalIdentity(s)
	raw, _ := json.Marshal(s)
	return controllerBackupDigest(raw)
}
func validateSchemaMaintenanceBackupArchive(a controllerBackupArchive) error {
	if !productionSHA256Pattern.MatchString(a.CiphertextSHA256) || !productionSHA256Pattern.MatchString(a.PlaintextSHA256) || a.CiphertextBytes <= 0 || a.CiphertextBytes > controllerBackupMaxArchive || a.PlaintextBytes <= 0 || a.PlaintextBytes > a.CiphertextBytes {
		return errors.New("schema backup archive declaration is invalid")
	}
	return nil
}
func validateSchemaMaintenanceBackupSet(s productionSchemaMaintenanceBackupSet, p productionSchemaMaintenancePlan, sha string, stops []productionSchemaMaintenanceBackupStop, now time.Time) error {
	if s.Format != schemaMaintenanceBackupSetFormat || s.TransitionID != p.TransitionID || s.PrePlanSHA256 != sha || s.PreSchema != p.PreSchema || s.RecipientSHA256 != p.BackupPolicy.RecipientSHA256 || !reflect.DeepEqual(s.StoppedOrigins, stops) || s.CapturedUTC.IsZero() || s.CapturedUTC.Location() != time.UTC || s.CapturedUTC.After(now.Add(30*time.Second)) || s.CapturedUTC.Before(now.Add(-24*time.Hour)) || validateSchemaMaintenanceBackupArchive(s.Archive) != nil || schemaMaintenanceBackupSourceIdentity(s.SourceBefore, p.PreSchema) != nil || !reflect.DeepEqual(s.SourceBefore, s.SourceAfter) {
		return errors.New("schema backup collection does not bind the exact quiesced source")
	}
	return nil
}
func validateSchemaMaintenanceBackupProof(p productionSchemaMaintenanceBackupProof, plan productionSchemaMaintenancePlan, sha string, stops []productionSchemaMaintenanceBackupStop, now time.Time) error {
	if p.Format != schemaMaintenanceBackupProofFormat || p.TransitionID != plan.TransitionID || p.PrePlanSHA256 != sha || p.PreSchema != plan.PreSchema || p.RecipientSHA256 != plan.BackupPolicy.RecipientSHA256 || !reflect.DeepEqual(p.StoppedOrigins, stops) || validateSchemaMaintenanceBackupArchive(p.Archive) != nil || p.RestoreKind != schemaMaintenanceBackupRestoreKind || !p.ActualRestore || !p.NoTCP || p.RestoredDatabase != "schema_restore" || p.RestoredSystemIdentifier == "" || p.RestoredSystemIdentifier == plan.PreSchema.SystemIdentifier || p.TableCount <= 0 || p.TotalRows < 0 || p.SourceLogicalSHA256 != p.RestoredLogicalSHA256 || p.CapturedUTC.IsZero() || p.VerifiedUTC.IsZero() || p.CapturedUTC.Location() != time.UTC || p.VerifiedUTC.Location() != time.UTC || p.VerifiedUTC.Before(p.CapturedUTC) || p.VerifiedUTC.After(now.Add(30*time.Second)) || p.CapturedUTC.Before(now.Add(-24*time.Hour)) {
		return errors.New("schema backup proof lacks a fresh independent full restore")
	}
	for _, hash := range []string{p.BackupSetSHA256, p.SourceSHA256, p.SourceLogicalSHA256, p.RestoredLogicalSHA256} {
		if !productionSHA256Pattern.MatchString(hash) {
			return errors.New("schema backup proof digest is invalid")
		}
	}
	return nil
}
func schemaMaintenanceBackupDecodeProof(raw []byte, public string) (productionSchemaMaintenanceBackupProof, error) {
	var proof productionSchemaMaintenanceBackupProof
	var envelope controllerBackupEnvelope
	key, err := controllerBackupVerificationKey(public)
	if err != nil || len(raw) > controllerBackupReceiptMaxBytes || decodeControllerBackupJSON(raw, &envelope) != nil || envelope.Format != 1 || len(envelope.Payload) == 0 || len(envelope.Payload) > controllerBackupReceiptMaxBytes || len(envelope.Signature) != ed25519.SignatureSize*2 || envelope.Signature != strings.ToLower(envelope.Signature) {
		return proof, errors.New("schema backup signed envelope is invalid")
	}
	sig, err := hex.DecodeString(envelope.Signature)
	if err != nil || !controllerBackupStrongPoint(sig[:32]) || !ed25519.Verify(key, envelope.Payload, sig) || decodeControllerBackupJSON(envelope.Payload, &proof) != nil {
		return proof, errors.New("schema backup proof signature or payload is invalid")
	}
	canonical, _ := json.Marshal(proof)
	if !bytes.Equal(canonical, envelope.Payload) {
		return proof, errors.New("schema backup proof payload is not canonical")
	}
	return proof, nil
}

// Target-side gate. The age identity and plaintext never reach either host.
func (runtime *productionRuntime) verifySchemaMaintenanceBackup(ctx context.Context, plan productionSchemaMaintenancePlan, preSHA string, stopped []productionSchemaMaintenanceHostCapture) error {
	stops, err := schemaMaintenanceBackupStopBindings(plan, preSHA, stopped)
	if err != nil || ctx.Err() != nil {
		return errors.New("schema backup native stopped-origin closure unavailable")
	}
	path := filepath.Join(plan.Origins[0].Workspace, "state", schemaMaintenanceBackupProofName)
	raw, err := runtime.readExistingSchemaSealedFile(path, true)
	if err != nil {
		return errors.New("schema backup native signed proof unavailable")
	}
	proof, err := schemaMaintenanceBackupDecodeProof(raw, plan.BackupPolicy.ControllerPublicKey)
	if err != nil {
		return err
	}
	now := time.Now()
	if runtime.now != nil {
		now = runtime.now()
	}
	if err = validateSchemaMaintenanceBackupProof(proof, plan, preSHA, stops, now); err != nil {
		return err
	}
	for _, s := range stopped {
		if proof.CapturedUTC.Before(s.StopStartedUTC) {
			return errors.New("schema backup predates the stopped writer generation")
		}
	}
	return nil
}

// Controller only. This accepts no restore DSN. It returns signed bytes only
// after actual full restore, source-content equality and plaintext cleanup.
// Caller writes O_EXCL at RestoreProofPath and stages unchanged to primary.
func (runtime *productionReleaseRuntime) prepareSchemaMaintenanceBackup(ctx context.Context, plan productionSchemaMaintenancePlan, preSHA string, stopped []productionSchemaMaintenanceHostCapture, identity string) (out []byte, err error) {
	fail := func() ([]byte, error) {
		return nil, errors.New("schema backup independent restore verification failed")
	}
	uid := os.Geteuid()
	root := plan.BackupPolicy.ControllerDirectory
	stops, e := schemaMaintenanceBackupStopBindings(plan, preSHA, stopped)
	if e != nil || uid == 0 || runtime.runner == nil || ctx.Err() != nil || plan.BackupPolicy.Kind != "controller-only-encrypted-quiesced-v1" || !pathWithinRoot("/home/lightjunction/Backups", root) || root == "/home/lightjunction/Backups" || plan.BackupPolicy.RestoreProofPath != filepath.Join(root, "restore-proof.json") || controllerBackupDirectory(root, uid) != nil {
		return fail()
	}
	if _, e = os.Lstat(plan.BackupPolicy.RestoreProofPath); !errors.Is(e, os.ErrNotExist) {
		return fail()
	}
	raw, e := controllerBackupReadPrivate(filepath.Join(root, "schema-backup-set.json"), uid, schemaMaintenanceBackupSetMaxBytes)
	if e != nil {
		return fail()
	}
	var set productionSchemaMaintenanceBackupSet
	if decodeControllerBackupJSON(raw, &set) != nil {
		return fail()
	}
	now := time.Now()
	if runtime.now != nil {
		now = runtime.now()
	}
	if validateSchemaMaintenanceBackupSet(set, plan, preSHA, stops, now) != nil {
		return fail()
	}
	for _, s := range stopped {
		if set.CapturedUTC.Before(s.StopStartedUTC) {
			return fail()
		}
	}
	setSHA := controllerBackupDigest(raw)
	complete, e := controllerBackupReadPrivate(filepath.Join(root, ".complete"), uid, 65)
	if e != nil || string(complete) != setSHA+"\n" {
		return fail()
	}
	recipient, e := controllerBackupReadPrivate(filepath.Join(root, "age-recipient.txt"), uid, 8192)
	if e != nil || controllerBackupDigest(recipient) != plan.BackupPolicy.RecipientSHA256 {
		return fail()
	}
	key, e := controllerBackupKey(productionReleasePlan{ControllerWorkspace: root, ControllerBackupPublicKey: plan.BackupPolicy.ControllerPublicKey})
	if e != nil {
		return fail()
	}
	identityRaw, e := controllerBackupReadPrivate(identity, uid, 8192)
	if e != nil || controllerBackupCheckSpace(root, set.Archive.CiphertextBytes) != nil {
		return fail()
	}
	scratch, e := os.MkdirTemp(root, ".restore-private-")
	if e != nil {
		return fail()
	}
	defer func() {
		if cleanup := os.RemoveAll(scratch); cleanup != nil {
			out = nil
			err = errors.New("schema backup private restore cleanup failed")
		}
	}()
	if os.Chmod(scratch, 0700) != nil {
		return fail()
	}
	privateWrite := func(name string, b []byte) error {
		f, e := os.OpenFile(filepath.Join(scratch, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return e
		}
		_, w := f.Write(b)
		return errors.Join(w, f.Sync(), f.Close())
	}
	if privateWrite("identity", identityRaw) != nil {
		return fail()
	}
	cipher, e := os.OpenFile(filepath.Join(scratch, "database.age"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return fail()
	}
	copyErr := controllerBackupCopyVerified(filepath.Join(root, "database.age"), uid, set.Archive.CiphertextBytes, set.Archive.CiphertextSHA256, cipher)
	closeErr := cipher.Close()
	if copyErr != nil || closeErr != nil {
		return fail()
	}
	if privateWrite("database.pgc", nil) != nil {
		return fail()
	}
	if _, e = runtime.runner.Run(ctx, productionCommand{Name: commandAge, Args: []string{"--decrypt", "--identity", filepath.Join(scratch, "identity"), "--output", filepath.Join(scratch, "database.pgc"), filepath.Join(scratch, "database.age")}, Dir: scratch, Env: []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "HOME=" + scratch, "TMPDIR=" + scratch}, Sensitive: true, Timeout: 10 * time.Minute, OutputLimit: 8192}); e != nil {
		return fail()
	}
	if controllerBackupVerifyFile(filepath.Join(scratch, "database.pgc"), uid, set.Archive.PlaintextBytes, set.Archive.PlaintextSHA256) != nil {
		return fail()
	}
	archive, e := controllerBackupOpenPrivate(filepath.Join(scratch, "database.pgc"), uid)
	if e != nil {
		return fail()
	}
	magic := make([]byte, 5)
	_, readErr := io.ReadFull(archive, magic)
	closeErr = archive.Close()
	if readErr != nil || closeErr != nil || string(magic) != "PGDMP" {
		return fail()
	}
	query, e := schemaMaintenanceBackupSourceQuery(plan.PreSchema.Schema)
	if e != nil {
		return fail()
	}
	roles := ""
	if !slices.Contains(set.SourceBefore.Roles, plan.Role) {
		return fail()
	}
	for _, role := range set.SourceBefore.Roles {
		if role != plan.Role {
			roles += "CREATE ROLE " + schemaMaintenanceQuoteIdentifier(role) + " NOLOGIN;\n"
		}
	}
	if privateWrite("roles.sql", []byte(roles)) != nil || privateWrite("snapshot.sql", []byte(query)) != nil {
		return fail()
	}
	if e = runSchemaMaintenanceBackupSandbox(ctx, scratch, plan.Role); e != nil {
		return fail()
	}
	restoredRaw, e := controllerBackupReadPrivate(filepath.Join(scratch, "restored-source.json"), uid, schemaMaintenanceBackupSourceMaxBytes)
	if e != nil {
		return fail()
	}
	restored, e := decodeSchemaMaintenanceBackupSource(bytes.TrimSpace(restoredRaw))
	if e != nil || restored.Database != "schema_restore" || restored.SystemIdentifier == set.SourceBefore.SystemIdentifier || !schemaMaintenanceBackupLogicalEqual(set.SourceBefore, restored) {
		return fail()
	}
	noTCP, e := controllerBackupReadPrivate(filepath.Join(scratch, "no-tcp"), uid, 16)
	if e != nil || string(noTCP) != "t\n" {
		return fail()
	}
	if controllerBackupVerifyFile(filepath.Join(scratch, "database.pgc"), uid, set.Archive.PlaintextBytes, set.Archive.PlaintextSHA256) != nil || controllerBackupVerifyFile(filepath.Join(root, "database.age"), uid, set.Archive.CiphertextBytes, set.Archive.CiphertextSHA256) != nil {
		return fail()
	}
	again, e := controllerBackupReadPrivate(filepath.Join(root, "schema-backup-set.json"), uid, schemaMaintenanceBackupSetMaxBytes)
	if e != nil || !bytes.Equal(again, raw) {
		return fail()
	}
	again, e = controllerBackupReadPrivate(filepath.Join(root, ".complete"), uid, 65)
	if e != nil || !bytes.Equal(again, complete) {
		return fail()
	}
	var total int64
	for _, table := range restored.Tables {
		total += table.Rows
		if total < 0 {
			return fail()
		}
	}
	beforeRaw, _ := json.Marshal(set.SourceBefore)
	now = time.Now()
	if runtime.now != nil {
		now = runtime.now()
	}
	proof := productionSchemaMaintenanceBackupProof{Format: schemaMaintenanceBackupProofFormat, TransitionID: plan.TransitionID, PrePlanSHA256: preSHA, PreSchema: plan.PreSchema, CapturedUTC: set.CapturedUTC, VerifiedUTC: utcSecond(now), RecipientSHA256: plan.BackupPolicy.RecipientSHA256, StoppedOrigins: stops, BackupSetSHA256: setSHA, Archive: set.Archive, SourceSHA256: controllerBackupDigest(beforeRaw), SourceLogicalSHA256: schemaMaintenanceBackupLogicalSHA(set.SourceBefore), RestoredLogicalSHA256: schemaMaintenanceBackupLogicalSHA(restored), RestoredSystemIdentifier: restored.SystemIdentifier, RestoredDatabase: restored.Database, RestoreKind: schemaMaintenanceBackupRestoreKind, ActualRestore: true, NoTCP: true, TableCount: len(restored.Tables), TotalRows: total}
	if validateSchemaMaintenanceBackupProof(proof, plan, preSHA, stops, now) != nil {
		return fail()
	}
	payload, e := json.Marshal(proof)
	if e != nil {
		return fail()
	}
	return json.Marshal(controllerBackupEnvelope{Format: 1, Payload: payload, Signature: hex.EncodeToString(ed25519.Sign(key, payload))})
}

// The entire actual restore runs in one private mount/user/PID/network namespace.
// Its dump SQL cannot see home, identities, SSH agents, production sockets or
// network. Only the scoped scratch is writable; PostgreSQL ownership/ACL is kept.
func runSchemaMaintenanceBackupSandbox(ctx context.Context, scratch, role string) error {
	if os.Remove(filepath.Join(scratch, "identity")) != nil {
		return errors.New("schema restore identity cleanup failed")
	}
	script := `set -eu
umask 077
mkdir /work/socket
/usr/bin/initdb -D /work/pgdata -U "$1" --no-locale --encoding=UTF8 --auth-local=trust --auth-host=reject >/work/initdb.log 2>&1
trap '/usr/bin/pg_ctl -D /work/pgdata -m immediate -w stop >/work/stop.log 2>&1 || true' EXIT HUP INT TERM
/usr/bin/pg_ctl -D /work/pgdata -l /work/postgres.log -o '-c listen_addresses= -c unix_socket_directories=/work/socket -c unix_socket_permissions=0700 -c shared_buffers=16MB -c max_connections=8 -c autovacuum=off' -w start >/work/start.log 2>&1
/usr/bin/psql -X -q -v ON_ERROR_STOP=1 -f /work/roles.sql
/usr/bin/psql -X -q -v ON_ERROR_STOP=1 -c 'CREATE DATABASE schema_restore'
export PGDATABASE=schema_restore
/usr/bin/pg_restore --format=custom --no-password --exit-on-error --single-transaction --dbname=schema_restore /work/database.pgc >/work/restore.log 2>&1
/usr/bin/psql -X -q -A -t -v ON_ERROR_STOP=1 -c "SELECT current_setting('listen_addresses')='' AND inet_server_addr() IS NULL" >/work/no-tcp
/usr/bin/psql -X -q -A -t -v ON_ERROR_STOP=1 -f /work/snapshot.sql >/work/restored-source.json
/usr/bin/pg_ctl -D /work/pgdata -m fast -w stop >/work/stop.log 2>&1
trap - EXIT HUP INT TERM
`
	for _, name := range []string{"database.pgc", "roles.sql", "snapshot.sql"} {
		info, e := os.Lstat(filepath.Join(scratch, name))
		if e != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
			return errors.New("schema restore scratch input is unsafe")
		}
	}
	args := []string{"--unshare-all", "--die-with-parent", "--new-session", "--cap-drop", "ALL", "--ro-bind", "/usr", "/usr", "--symlink", "usr/bin", "/bin", "--symlink", "usr/lib", "/lib", "--symlink", "usr/lib", "/lib64", "--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp", "--dir", "/etc", "--ro-bind", "/etc/passwd", "/etc/passwd", "--ro-bind", "/etc/group", "/etc/group", "--ro-bind", "/etc/nsswitch.conf", "/etc/nsswitch.conf", "--bind", scratch, "/work", "--ro-bind", filepath.Join(scratch, "database.pgc"), "/work/database.pgc", "--ro-bind", filepath.Join(scratch, "roles.sql"), "/work/roles.sql", "--ro-bind", filepath.Join(scratch, "snapshot.sql"), "/work/snapshot.sql", "--chdir", "/work", "--clearenv", "--setenv", "PATH", "/usr/bin:/bin", "--setenv", "LANG", "C", "--setenv", "LC_ALL", "C", "--setenv", "HOME", "/work", "--setenv", "TMPDIR", "/tmp", "--setenv", "PGHOST", "/work/socket", "--setenv", "PGDATABASE", "postgres", "--setenv", "PGUSER", role, "/usr/bin/sh", "-c", script, "schema-backup-restore", role}
	child, cancel := context.WithTimeout(ctx, 40*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(child, "/usr/bin/bwrap", args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C"}
	cmd.Dir = scratch
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if cmd.Run() != nil {
		return errors.New("schema backup isolated actual PostgreSQL restore failed")
	}
	return nil
}
