//go:build linux

package appcli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// A failed pg_dump created a zero-byte file before any service stop or DDL.
// Retain that checkpoint unchanged; allow one fresh backup attempt only when
// both saved native records still exactly match the live, already-validated
// transaction. A completed backup, schema change, or previous retry forbids it.
func (runtime *productionRuntime) prepareIncident343Audit(workspace productionWorkspace, manifest productionManifest, status productionStatus) (string, error) {
	root := filepath.Join(workspace.stateDir, "incident-343-schema-recovery")
	before := make(map[string][]byte, 2)
	for name, value := range map[string]any{"before-manifest.json": manifest, "before-status.json": status} {
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return "", err
		}
		before[name] = append(data, '\n')
	}
	if err := os.Mkdir(root, 0700); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return "", errors.New("cannot create incident recovery audit directory")
		}
		if err := runtime.validateIncident343EmptyBackup(root, before); err != nil {
			return "", err
		}
		// Never rename or remove the original dump/audit and never overwrite a
		// retry. The global native deployment lock serializes this transition.
		root = filepath.Join(root, "backup-retry-1")
		if err := os.Mkdir(root, 0700); err != nil {
			return "", errors.New("incident backup retry already exists or cannot be created; inspect instead of replaying")
		}
		if err := writeAtomicRegularFile(filepath.Join(root, "resume.json"), []byte("{\"prior_checkpoint\":\"empty-backup-before-mutation\",\"prior_evidence_preserved\":true,\"fresh_complete_backup_required\":true}\n"), 0600); err != nil {
			return "", err
		}
	}
	for name, data := range before {
		if err := writeAtomicRegularFile(filepath.Join(root, name), data, 0600); err != nil {
			return "", err
		}
	}
	return root, nil
}

func (runtime *productionRuntime) validateIncident343EmptyBackup(root string, before map[string][]byte) error {
	if err := runtime.requireOwnedSafePath(root, true); err != nil {
		return errors.New("existing incident audit directory is unsafe")
	}
	info, err := os.Lstat(root)
	if err != nil || info.Mode().Perm() != 0700 {
		return errors.New("existing incident audit directory is not private")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 3 {
		return errors.New("existing recovery progressed beyond an empty backup; inspect instead of replaying")
	}
	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
		expected, isRecord := before[entry.Name()]
		isEmptyDump := entry.Name() == "before-schema.database.dump"
		if !isRecord && !isEmptyDump {
			return errors.New("existing incident checkpoint contains unexpected evidence")
		}
		if err := runtime.requireOwnedSafePath(path, false); err != nil {
			return errors.New("existing incident checkpoint file is unsafe")
		}
		info, err := os.Lstat(path)
		if err != nil || info.Mode().Perm() != 0600 || (isEmptyDump && info.Size() != 0) {
			return errors.New("only a private zero-byte failed backup may be retried")
		}
		if isRecord {
			data, err := readPrivateRegularFile(path, 1<<20)
			if err != nil || !bytes.Equal(data, expected) {
				return fmt.Errorf("recorded incident checkpoint %s does not match current native state", entry.Name())
			}
		}
	}
	return nil
}
