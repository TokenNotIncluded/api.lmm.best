package appcli

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type legacyRefundRiskAcknowledgement struct {
	Format               int       `json:"format"`
	Purpose              string    `json:"purpose"`
	DeploymentID         string    `json:"deployment_id"`
	OldPackageIdentity   string    `json:"old_package_identity"`
	OldPackageSHA256     string    `json:"old_package_sha256"`
	WriterPID            int       `json:"writer_pid"`
	WriterInvocationID   string    `json:"writer_invocation_id"`
	BackupManifestSHA256 string    `json:"backup_manifest_sha256"`
	BackupCapturedUTC    time.Time `json:"backup_captured_utc"`
	AcceptedUTC          time.Time `json:"accepted_utc"`
	Outcome              string    `json:"outcome"`
}

type controllerBackupReference struct {
	SourceHost     string                                   `json:"source_host"`
	SourceDatabase string                                   `json:"source_database"`
	Purpose        string                                   `json:"purpose"`
	CapturedUTC    time.Time                                `json:"captured_utc"`
	Files          map[string]controllerBackupReferenceFile `json:"files"`
}

type controllerBackupReferenceFile struct {
	Bytes    int64  `json:"bytes"`
	SHA256   string `json:"sha256"`
	Verified bool   `json:"verified"`
}

// The reference is the controller's already-verified backup manifest, not a
// claim that this host decrypted the backups or that historical refunds succeeded.
func (runtime *productionRuntime) acceptedLegacyRefundRisk(manifest *productionManifest) (bool, error) {
	if manifest == nil || manifest.BillingGate == nil {
		return false, nil
	}
	state := filepath.Dir(manifest.ConfigRestorePath)
	if filepath.Base(manifest.ConfigRestorePath) != productionConfigRestoreDirname || filepath.Base(state) != "state" || filepath.Base(filepath.Dir(state)) != manifest.DeploymentID {
		return false, errors.New("legacy acknowledgement workspace mismatch")
	}
	path := filepath.Join(state, "legacy-refund-risk.json")
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	data, err := controllerBackupReadPrivate(path, int(runtime.requiredOwnerUID), 16<<10)
	if err != nil {
		return false, errors.New("unsafe legacy refund acknowledgement")
	}
	var ack legacyRefundRiskAcknowledgement
	if err := decodeControllerBackupJSON(data, &ack); err != nil {
		return false, errors.New("invalid legacy refund acknowledgement")
	}
	invocation, invocationErr := hex.DecodeString(ack.WriterInvocationID)
	if ack.Format != 1 || ack.Purpose != "user-accepted-legacy-refund-risk" || ack.Outcome != "unknown" ||
		ack.DeploymentID != manifest.DeploymentID || ack.OldPackageIdentity != "lmm-api-go-bin 0.2.17-1" ||
		ack.OldPackageIdentity != manifest.Go.RollbackIdentity || manifest.Go.RollbackPackageName != productionAURPackageName ||
		!productionSHA256Pattern.MatchString(ack.OldPackageSHA256) || ack.OldPackageSHA256 != manifest.Go.RollbackSHA256 ||
		ack.WriterPID <= 1 || ack.WriterPID != manifest.BillingGate.GoPID || invocationErr != nil || len(invocation) != 16 ||
		ack.WriterInvocationID != manifest.BillingGate.GoInvocationID || !productionSHA256Pattern.MatchString(ack.BackupManifestSHA256) {
		return false, errors.New("legacy refund acknowledgement does not match the transaction")
	}
	referenceBytes, err := controllerBackupReadPrivate(filepath.Join(state, "controller-backup-reference.json"), int(runtime.requiredOwnerUID), 16<<10)
	if err != nil || fmt.Sprintf("%x", sha256Bytes(referenceBytes)) != ack.BackupManifestSHA256 {
		return false, errors.New("controller backup reference mismatch")
	}
	var reference controllerBackupReference
	if err := decodeControllerBackupJSON(referenceBytes, &reference); err != nil || reference.SourceHost != "ArchDmit" || reference.SourceDatabase != "lmm_api" ||
		len(reference.Files) != 3 || !reference.CapturedUTC.Equal(ack.BackupCapturedUTC) {
		return false, errors.New("invalid controller backup reference")
	}
	for _, name := range []string{"database.dump.age", "roles.sql.age", "environment.tar.age"} {
		file, ok := reference.Files[name]
		if !ok || !file.Verified || file.Bytes <= 0 || !productionSHA256Pattern.MatchString(file.SHA256) {
			return false, errors.New("controller backup reference is incomplete")
		}
	}
	now := runtime.now().UTC()
	for _, captured := range []time.Time{ack.BackupCapturedUTC, ack.AcceptedUTC} {
		if captured.IsZero() || captured.After(now.Add(30*time.Second)) || captured.Before(now.Add(-24*time.Hour)) {
			return false, errors.New("legacy refund acknowledgement is expired or future-dated")
		}
	}
	return true, nil
}
