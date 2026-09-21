package appcli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// The admission barrier is the only live edge mutation allowed before the
// writer stops. Verify all other package-managed edge files against the
// retained snapshot before reopening traffic or finalizing the transaction.
func (runtime *productionRuntime) verifyPreStopEdgeState(workspace productionWorkspace, manifest productionManifest) error {
	if manifest.NginxEdgeRestoreSHA256 == "" {
		return nil // Older transactions may have no edge-policy snapshot.
	}
	root := filepath.Join(workspace.configRestore, "nginx-edge")
	if err := runtime.validateEdgePolicyBackup(root, manifest.NginxEdgeRestoreSHA256); err != nil {
		return err
	}
	data, err := readPrivateRegularFile(filepath.Join(root, "manifest.json"), edgePolicyBackupLimit)
	if err != nil {
		return err
	}
	var snapshot edgePolicyBackupManifest
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return err
	}
	assets := map[string]edgePolicyAsset{}
	for _, asset := range runtime.allEdgePolicyAssets() {
		assets[asset.Key] = asset
	}
	locations := filepath.Join(runtime.paths.NginxRoot, "lmm-api-locations.conf")
	for _, entry := range snapshot.Entries {
		target := assets[entry.Key].Target
		if target == locations {
			if entry.State != "present" || manifest.BillingGate == nil || entry.SHA256 != manifest.BillingGate.OriginalSHA256 {
				return errors.New("pre-stop admission snapshot does not match the edge snapshot")
			}
			continue // reopenBillingAdmission accepts only original/barrier bytes.
		}
		info, err := os.Lstat(target)
		if entry.State == "absent" {
			if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("pre-stop edge file unexpectedly exists: %s", entry.Key)
			}
			continue
		}
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != entry.Mode {
			return fmt.Errorf("pre-stop edge file changed: %s", entry.Key)
		}
		owner, links, ok := deploymentFileOwnership(info)
		if !ok || owner != runtime.requiredOwnerUID || links != 1 {
			return fmt.Errorf("pre-stop edge file ownership changed: %s", entry.Key)
		}
		digest, err := sha256File(target)
		if err != nil || digest != entry.SHA256 {
			return fmt.Errorf("pre-stop edge file content changed: %s", entry.Key)
		}
	}
	return nil
}
