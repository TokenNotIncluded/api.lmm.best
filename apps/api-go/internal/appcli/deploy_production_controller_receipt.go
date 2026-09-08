package appcli

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"filippo.io/edwards25519"
)

const (
	controllerBackupReceiptName      = "controller-backup-receipt.json"
	controllerBackupConfirmationName = "controller-backup-confirmation.json"
	controllerBackupKeyName          = "controller-backup.key"
	controllerBackupEvidenceFormat   = 3
	controllerBackupReceiptMaxBytes  = 64 << 10
)

var controllerBackupKinds = []string{"application", "frontend", "configuration", "database"}

type controllerBackupBinding struct {
	PublicKey     string `json:"public_key"`
	PlanSHA256    string `json:"plan_sha256"`
	ReceiptPath   string `json:"receipt_path"`
	ReceiptSHA256 string `json:"receipt_sha256"`
}

type controllerBackupEnvelope struct {
	Format    int    `json:"format"`
	Payload   []byte `json:"payload"`
	Signature string `json:"signature"`
}

type controllerBackupReceipt struct {
	Format                   int               `json:"format"`
	Purpose                  string            `json:"purpose"`
	DeploymentID             string            `json:"deployment_id"`
	ExpectedHost             string            `json:"expected_host"`
	PlanSHA256               string            `json:"plan_sha256"`
	VerifiedUTC              time.Time         `json:"verified_utc"`
	CapturedUTC              time.Time         `json:"captured_utc"`
	DeploymentManifestSHA256 string            `json:"deployment_manifest_sha256"`
	BackupSetSHA256          string            `json:"backup_set_sha256"`
	DatabaseSchema           string            `json:"database_schema"`
	EnvironmentSHA256        string            `json:"environment_sha256"`
	GoCandidateSHA256        string            `json:"go_candidate_sha256"`
	GoRollbackSHA256         string            `json:"go_rollback_sha256"`
	WebCandidateSHA256       string            `json:"web_candidate_sha256"`
	WebRollbackSHA256        string            `json:"web_rollback_sha256"`
	GoRollbackPayloadSHA256  string            `json:"go_rollback_payload_sha256"`
	FrontendRollbackSHA256   string            `json:"frontend_rollback_sha256"`
	ArchiveCiphertexts       map[string]string `json:"archive_ciphertexts"`
	ArchivePlaintexts        map[string]string `json:"archive_plaintexts"`
}

// decodeControllerBackupJSON rejects duplicate keys before decoding typed fields.
// Exact tag casing avoids Go's otherwise case-insensitive field matching differing
// from the Rust recovery reader. No extra JSON values or fields are accepted.
func decodeControllerBackupJSON(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := rejectControllerBackupDuplicateKeys(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("backup JSON must contain one value")
	}
	if err := validateControllerBackupJSONFields(data, reflect.TypeOf(value)); err != nil {
		return err
	}
	decoder = json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return errors.New("backup JSON has invalid fields or values")
	}
	return nil
}

func rejectControllerBackupDuplicateKeys(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return errors.New("backup JSON is malformed")
	}
	delim, nested := token.(json.Delim)
	if !nested {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]bool)
		for decoder.More() {
			keyToken, err := decoder.Token()
			key, ok := keyToken.(string)
			if err != nil || !ok || seen[key] {
				return errors.New("backup JSON has invalid or duplicate object keys")
			}
			seen[key] = true
			if err := rejectControllerBackupDuplicateKeys(decoder); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := rejectControllerBackupDuplicateKeys(decoder); err != nil {
				return err
			}
		}
	default:
		return errors.New("backup JSON has an unexpected delimiter")
	}
	closing, err := decoder.Token()
	if err != nil || (delim == '{' && closing != json.Delim('}')) || (delim == '[' && closing != json.Delim(']')) {
		return errors.New("backup JSON is incomplete")
	}
	return nil
}

func validateControllerBackupJSONFields(data json.RawMessage, target reflect.Type) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		if target.Kind() == reflect.Pointer {
			return nil
		}
		return errors.New("backup JSON does not permit null scalar or collection values")
	}
	for target.Kind() == reflect.Pointer {
		target = target.Elem()
	}
	if target == reflect.TypeOf(time.Time{}) {
		return nil
	}
	if target == reflect.TypeOf([]byte{}) {
		var encoded string
		if err := json.Unmarshal(data, &encoded); err != nil {
			return errors.New("backup JSON byte payload must be a base64 string, not a numeric array")
		}
		return nil
	}
	switch target.Kind() {
	case reflect.Struct:
		var object map[string]json.RawMessage
		if err := json.Unmarshal(data, &object); err != nil || object == nil {
			return errors.New("backup JSON requires an object")
		}
		fields := make(map[string]reflect.Type)
		for i := 0; i < target.NumField(); i++ {
			field := target.Field(i)
			tag := strings.Split(field.Tag.Get("json"), ",")[0]
			if tag != "" && tag != "-" {
				fields[tag] = field.Type
			}
		}
		if target == reflect.TypeOf(controllerBackupReceipt{}) || target == reflect.TypeOf(controllerBackupEnvelope{}) ||
			target == reflect.TypeOf(controllerBackupSet{}) || target == reflect.TypeOf(controllerBackupArchive{}) || target == reflect.TypeOf(controllerBackupBinding{}) {
			for key := range fields {
				if _, ok := object[key]; !ok {
					return errors.New("backup JSON has a missing required field")
				}
			}
		}
		for key, child := range object {
			kind, ok := fields[key]
			if !ok {
				return errors.New("backup JSON has an unknown or incorrectly cased field")
			}
			if err := validateControllerBackupJSONFields(child, kind); err != nil {
				return err
			}
		}
	case reflect.Map:
		var object map[string]json.RawMessage
		if err := json.Unmarshal(data, &object); err != nil || object == nil {
			return errors.New("backup JSON requires a non-null map")
		}
		for _, child := range object {
			if err := validateControllerBackupJSONFields(child, target.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateControllerBackupDigestMap(values map[string]string) error {
	if len(values) != len(controllerBackupKinds) {
		return errors.New("backup evidence requires exactly four archive kinds")
	}
	for _, kind := range controllerBackupKinds {
		if !productionSHA256Pattern.MatchString(values[kind]) {
			return errors.New("backup evidence has an invalid archive digest")
		}
	}
	return nil
}

func validateControllerBackupReceiptShape(receipt controllerBackupReceipt) error {
	if receipt.Format != 1 || !productionIDPattern.MatchString(receipt.DeploymentID) || receipt.ExpectedHost != productionExpectedHost {
		return errors.New("controller backup receipt identity or format is invalid")
	}
	if receipt.Purpose != "prepare" && receipt.Purpose != "confirm" {
		return errors.New("controller backup receipt purpose is invalid")
	}
	if receipt.Purpose == "prepare" && receipt.DeploymentManifestSHA256 != "" {
		return errors.New("prepare receipt cannot predeclare a deployment manifest hash")
	}
	if receipt.Purpose == "confirm" && !productionSHA256Pattern.MatchString(receipt.DeploymentManifestSHA256) {
		return errors.New("confirmation receipt requires a manifest hash")
	}
	for _, digest := range []string{receipt.PlanSHA256, receipt.BackupSetSHA256, receipt.EnvironmentSHA256, receipt.GoCandidateSHA256, receipt.GoRollbackSHA256, receipt.WebCandidateSHA256, receipt.WebRollbackSHA256, receipt.GoRollbackPayloadSHA256, receipt.FrontendRollbackSHA256} {
		if !productionSHA256Pattern.MatchString(digest) {
			return errors.New("controller backup receipt contains an invalid digest")
		}
	}
	_, verifiedOffset := receipt.VerifiedUTC.Zone()
	_, capturedOffset := receipt.CapturedUTC.Zone()
	if !isDatabaseSchema(receipt.DatabaseSchema) || receipt.VerifiedUTC.Unix() <= 0 || receipt.CapturedUTC.Unix() <= 0 || verifiedOffset != 0 || capturedOffset != 0 || receipt.CapturedUTC.After(receipt.VerifiedUTC.Add(30*time.Second)) || receipt.VerifiedUTC.Sub(receipt.CapturedUTC) > 24*time.Hour {
		return errors.New("controller backup receipt schema or snapshot time is invalid")
	}
	if err := validateControllerBackupDigestMap(receipt.ArchiveCiphertexts); err != nil {
		return err
	}
	return validateControllerBackupDigestMap(receipt.ArchivePlaintexts)
}

func validateControllerBackupFreshness(receipt controllerBackupReceipt, now time.Time) error {
	if receipt.VerifiedUTC.After(now.Add(30*time.Second)) || now.Sub(receipt.VerifiedUTC) >= 5*time.Minute {
		return errors.New("controller backup verification receipt is stale or future-dated")
	}
	return nil
}

func controllerBackupVerificationKey(value string) (ed25519.PublicKey, error) {
	if !productionSHA256Pattern.MatchString(value) {
		return nil, errors.New("controller backup public key is invalid")
	}
	key, err := hex.DecodeString(value)
	if err != nil || !controllerBackupStrongPoint(key) {
		return nil, errors.New("controller backup public key is invalid or weak")
	}
	return ed25519.PublicKey(key), nil
}

// Match the strict Rust reader: reject noncanonical or small-order points as
// well as invalid signatures. Use the vetted Edwards implementation, not a
// hand-maintained blacklist or custom curve arithmetic.
func controllerBackupStrongPoint(encoded []byte) bool {
	point, err := new(edwards25519.Point).SetBytes(encoded)
	return err == nil && bytes.Equal(point.Bytes(), encoded) && new(edwards25519.Point).MultByCofactor(point).Equal(edwards25519.NewIdentityPoint()) != 1
}

func decodeSignedControllerBackupReceipt(data []byte, publicKey string) (controllerBackupReceipt, error) {
	var receipt controllerBackupReceipt
	key, err := controllerBackupVerificationKey(publicKey)
	if err != nil {
		return receipt, err
	}
	var envelope controllerBackupEnvelope
	if len(data) > controllerBackupReceiptMaxBytes || decodeControllerBackupJSON(data, &envelope) != nil || envelope.Format != 1 || len(envelope.Payload) == 0 || len(envelope.Payload) > controllerBackupReceiptMaxBytes || len(envelope.Signature) != ed25519.SignatureSize*2 || envelope.Signature != strings.ToLower(envelope.Signature) {
		return receipt, errors.New("controller backup receipt envelope is invalid")
	}
	signature, err := hex.DecodeString(envelope.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize || !controllerBackupStrongPoint(signature[:32]) || !ed25519.Verify(key, envelope.Payload, signature) {
		return receipt, errors.New("controller backup receipt signature is invalid")
	}
	if err := decodeControllerBackupJSON(envelope.Payload, &receipt); err != nil {
		return receipt, err
	}
	return receipt, validateControllerBackupReceiptShape(receipt)
}

func readControllerBackupReceipt(path, publicKey, expectedSHA string, owner uint32) (controllerBackupReceipt, error) {
	var receipt controllerBackupReceipt
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&(os.ModeSymlink|os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || info.Mode().Perm() != 0o600 || info.Size() > controllerBackupReceiptMaxBytes || info.Size() == 0 {
		return receipt, errors.New("controller backup receipt file is missing or unsafe")
	}
	uid, links, ok := deploymentFileOwnership(info)
	canonical, canonicalErr := filepath.EvalSymlinks(path)
	if !ok || uid != owner || links != 1 || canonicalErr != nil || canonical != path {
		return receipt, errors.New("controller backup receipt ownership or path is unsafe")
	}
	data, err := readPrivateRegularFile(path, controllerBackupReceiptMaxBytes)
	if err != nil {
		return receipt, errors.New("controller backup receipt cannot be read")
	}
	actual := fmt.Sprintf("%x", sha256.Sum256(data))
	if expectedSHA != "" && actual != expectedSHA {
		return receipt, errors.New("controller backup receipt digest differs from immutable evidence")
	}
	return decodeSignedControllerBackupReceipt(data, publicKey)
}

func controllerBackupKey(plan productionReleasePlan) (ed25519.PrivateKey, error) {
	path := filepath.Join(plan.ControllerWorkspace, "state", controllerBackupKeyName)
	info, err := os.Lstat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		return nil, errors.New("controller backup signing key is unavailable")
	}
	uid, links, ok := deploymentFileOwnership(info)
	if !ok || uid != uint32(os.Geteuid()) || links != 1 {
		return nil, errors.New("controller backup signing key ownership is invalid")
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil || canonical != path {
		return nil, errors.New("controller backup signing key path is unsafe")
	}
	data, err := readPrivateRegularFile(path, ed25519.PrivateKeySize)
	if err != nil || len(data) != ed25519.PrivateKeySize {
		return nil, errors.New("controller backup signing key is invalid")
	}
	key := ed25519.PrivateKey(data)
	if hex.EncodeToString(key.Public().(ed25519.PublicKey)) != plan.ControllerBackupPublicKey {
		return nil, errors.New("controller backup signing key differs from frozen plan")
	}
	return key, nil
}

func initializeControllerBackupKey(workspace string) (string, error) {
	path := filepath.Join(workspace, "state", controllerBackupKeyName)
	if _, err := os.Lstat(path); err == nil {
		data, readErr := readPrivateRegularFile(path, ed25519.PrivateKeySize)
		if readErr != nil || len(data) != ed25519.PrivateKeySize {
			return "", errors.New("existing controller backup key is invalid")
		}
		public := hex.EncodeToString(ed25519.PrivateKey(data).Public().(ed25519.PublicKey))
		_, keyErr := controllerBackupKey(productionReleasePlan{ControllerWorkspace: workspace, ControllerBackupPublicKey: public})
		return public, keyErr
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", errors.New("could not create controller backup signing key")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	_, writeErr := file.Write(private)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return "", err
	}
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return "", err
	}
	return hex.EncodeToString(public), nil
}

func signedControllerBackupReceipt(plan productionReleasePlan, set controllerBackupSet, setSHA, planSHA, purpose, manifestSHA string, now time.Time) ([]byte, error) {
	key, err := controllerBackupKey(plan)
	if err != nil {
		return nil, err
	}
	receipt := controllerBackupReceipt{
		Format: 1, Purpose: purpose, DeploymentID: plan.DeploymentID, ExpectedHost: plan.ExpectedHost, PlanSHA256: planSHA,
		VerifiedUTC: utcSecond(now), CapturedUTC: set.CapturedUTC, DeploymentManifestSHA256: manifestSHA,
		BackupSetSHA256: setSHA, DatabaseSchema: set.DatabaseSchema, EnvironmentSHA256: set.EnvironmentSHA256,
		GoCandidateSHA256: plan.GoCandidate.PackageSHA256, GoRollbackSHA256: plan.GoRollback.PackageSHA256,
		WebCandidateSHA256: plan.WebCandidate.PackageSHA256, WebRollbackSHA256: plan.WebRollback.PackageSHA256,
		GoRollbackPayloadSHA256: plan.GoRollback.PayloadSHA256, FrontendRollbackSHA256: plan.WebRollback.PayloadSHA256,
		ArchiveCiphertexts: make(map[string]string), ArchivePlaintexts: make(map[string]string),
	}
	for _, kind := range controllerBackupKinds {
		receipt.ArchiveCiphertexts[kind] = set.Archives[kind].CiphertextSHA256
		receipt.ArchivePlaintexts[kind] = set.Archives[kind].PlaintextSHA256
	}
	if err := validateControllerBackupReceiptShape(receipt); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(receipt)
	if err != nil {
		return nil, err
	}
	envelope := controllerBackupEnvelope{Format: 1, Payload: payload, Signature: hex.EncodeToString(ed25519.Sign(key, payload))}
	return json.Marshal(envelope)
}

func validateControllerOnlyReleasePlanPolicy(plan productionReleasePlan) error {
	if plan.Format == 5 {
		if plan.BackupMode != "" || plan.ControllerBackupDir != "" || plan.ControllerBackupPublicKey != "" {
			return errors.New("legacy plans cannot contain controller-only backup fields")
		}
		return nil
	}
	if plan.AgeRecipient != (productionReleaseFilePlan{}) {
		return errors.New("controller-only plans cannot contain legacy target backup recipients")
	}
	if plan.BackupMode == "disabled" {
		if plan.WithBackups || plan.ControllerBackupDir != "" || plan.ControllerBackupPublicKey != "" {
			return errors.New("disabled backup plans cannot contain backup paths or keys")
		}
		return nil
	}
	if plan.BackupMode != "controller-only" || !plan.WithBackups || !productionSHA256Pattern.MatchString(plan.ControllerBackupPublicKey) {
		return errors.New("selected backups require controller-only mode and a frozen verification key")
	}
	if _, err := controllerBackupVerificationKey(plan.ControllerBackupPublicKey); err != nil {
		return err
	}
	clean, err := cleanAbsoluteNonRoot(plan.ControllerBackupDir)
	if err != nil || clean != plan.ControllerBackupDir {
		return errors.New("controller backup import path must be absolute and canonical")
	}
	return nil
}

func validateControllerBackupTransactionOptions(options productionTransactionOptions) error {
	binding := options.ControllerBackup
	hasBinding := binding != (controllerBackupBinding{})
	if !hasBinding {
		if options.WithBackups != (options.BackupDir != "") {
			return errors.New("selected legacy backups require --backup-dir; disabled backups forbid it")
		}
		return nil
	}
	if !options.WithBackups || options.BackupDir != "" {
		return errors.New("controller-only evidence requires selected backups without a target backup directory")
	}
	if binding.ReceiptPath != filepath.Join(options.Workspace, "state", controllerBackupReceiptName) {
		return errors.New("controller-only receipt must be at the exact workspace state path")
	}
	if _, err := controllerBackupVerificationKey(binding.PublicKey); err != nil {
		return err
	}
	for _, value := range []string{binding.PublicKey, binding.PlanSHA256, binding.ReceiptSHA256} {
		if !productionSHA256Pattern.MatchString(value) {
			return errors.New("all controller-only receipt bindings are required and must be lowercase 64-character hexadecimal")
		}
	}
	return nil
}

func validateControllerBackupBinding(workspace productionWorkspace, manifest productionManifest) error {
	binding := manifest.ControllerOnlyBackup
	if binding == nil || manifest.BackupEvidenceFormat != controllerBackupEvidenceFormat || !manifest.BackupsEnabled || manifest.BackupDir != "" || manifest.TargetBackupSHA256 != "" || manifest.OffhostBackupSHA256 != "" {
		return errors.New("controller-only backup evidence is missing or mixed with legacy copies")
	}
	if binding.ReceiptPath != filepath.Join(workspace.stateDir, controllerBackupReceiptName) {
		return errors.New("controller-only receipt must be in the exact retained state directory")
	}
	if !isDatabaseSchema(manifest.DatabaseSchema) {
		return errors.New("controller backup database schema is invalid")
	}
	if _, err := controllerBackupVerificationKey(binding.PublicKey); err != nil {
		return err
	}
	for _, digest := range []string{binding.PublicKey, binding.PlanSHA256, binding.ReceiptSHA256, manifest.ControllerBackupSHA256, manifest.DatabaseBackupSHA256} {
		if !productionSHA256Pattern.MatchString(digest) {
			return errors.New("controller-only backup binding has an invalid digest or key")
		}
	}
	return nil
}

func matchControllerBackupReceipt(receipt controllerBackupReceipt, manifest productionManifest) error {
	binding := manifest.ControllerOnlyBackup
	if binding == nil || receipt.DeploymentID != manifest.DeploymentID || receipt.PlanSHA256 != binding.PlanSHA256 || receipt.BackupSetSHA256 != manifest.ControllerBackupSHA256 || receipt.DatabaseSchema != manifest.DatabaseSchema || receipt.EnvironmentSHA256 != manifest.EnvironmentRestoreSHA256 || receipt.GoCandidateSHA256 != manifest.Go.CandidateSHA256 || receipt.GoRollbackSHA256 != manifest.Go.RollbackSHA256 || receipt.WebCandidateSHA256 != manifest.Web.CandidateSHA256 || receipt.WebRollbackSHA256 != manifest.Web.RollbackSHA256 || receipt.FrontendRollbackSHA256 != manifest.Frontend.OldIndexSHA256 || receipt.ArchivePlaintexts["database"] != manifest.DatabaseBackupSHA256 {
		return errors.New("controller backup receipt does not bind the exact deployment, source configuration and packages")
	}
	return nil
}

func (runtime *productionRuntime) verifyControllerBackupEvidence(workspace productionWorkspace, manifest productionManifest, fresh bool) error {
	if err := validateControllerBackupBinding(workspace, manifest); err != nil {
		return err
	}
	binding := manifest.ControllerOnlyBackup
	receipt, err := readControllerBackupReceipt(binding.ReceiptPath, binding.PublicKey, binding.ReceiptSHA256, runtime.requiredOwnerUID)
	if err != nil {
		return err
	}
	if receipt.Purpose != "prepare" {
		return errors.New("initial controller backup receipt must have prepare purpose")
	}
	if err := matchControllerBackupReceipt(receipt, manifest); err != nil {
		return err
	}
	if fresh {
		if err := validateControllerBackupFreshness(receipt, runtime.now()); err != nil {
			return err
		}
		actual, err := sha256File(runtime.paths.InstalledBinary)
		if err != nil || actual != receipt.GoRollbackPayloadSHA256 {
			return errors.New("controller backup application payload differs from the verified active Go baseline")
		}
	}
	return nil
}

func sameControllerBackupSnapshot(initial, confirmation controllerBackupReceipt) bool {
	return initial.CapturedUTC.Equal(confirmation.CapturedUTC) && initial.BackupSetSHA256 == confirmation.BackupSetSHA256 &&
		initial.GoRollbackPayloadSHA256 == confirmation.GoRollbackPayloadSHA256 &&
		reflect.DeepEqual(initial.ArchiveCiphertexts, confirmation.ArchiveCiphertexts) && reflect.DeepEqual(initial.ArchivePlaintexts, confirmation.ArchivePlaintexts)
}

func (runtime *productionRuntime) verifyControllerBackupConfirmation(workspace productionWorkspace, manifest productionManifest) error {
	if err := runtime.verifyControllerBackupEvidence(workspace, manifest, false); err != nil {
		return err
	}
	binding := manifest.ControllerOnlyBackup
	receipt, err := readControllerBackupReceipt(filepath.Join(workspace.stateDir, controllerBackupConfirmationName), binding.PublicKey, "", runtime.requiredOwnerUID)
	if err != nil {
		return err
	}
	if receipt.Purpose != "confirm" {
		return errors.New("fresh controller backup receipt must have confirm purpose")
	}
	if err := matchControllerBackupReceipt(receipt, manifest); err != nil {
		return err
	}
	initial, err := readControllerBackupReceipt(binding.ReceiptPath, binding.PublicKey, binding.ReceiptSHA256, runtime.requiredOwnerUID)
	if err != nil {
		return err
	}
	if !sameControllerBackupSnapshot(initial, receipt) {
		return errors.New("controller backup confirmation changed the original snapshot evidence")
	}
	manifestSHA, err := sha256File(workspace.manifestPath)
	if err != nil || receipt.DeploymentManifestSHA256 != manifestSHA {
		return errors.New("controller backup confirmation does not bind the immutable manifest")
	}
	return validateControllerBackupFreshness(receipt, runtime.now())
}
