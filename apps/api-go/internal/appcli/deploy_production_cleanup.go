package appcli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

const productionWorkspaceCleanupRetention = 24 * time.Hour

type productionWorkspaceCleanupOptions struct {
	OlderThan                    time.Duration
	Execute                      bool
	SupersededBy                 string
	RetainRollback               string
	FinancialBackup              string
	FinancialBackupSHA256        string
	FinancialBackupReceipt       string
	FinancialBackupReceiptSHA256 string
}

type productionWorkspaceCleanupEntry struct {
	HistoricalBackup string   `json:"historical_backup,omitempty"`
	DeploymentID     string   `json:"deployment_id"`
	Workspace        string   `json:"workspace"`
	Phase            string   `json:"phase,omitempty"`
	Protected        bool     `json:"protected"`
	Reason           string   `json:"reason,omitempty"`
	Removed          []string `json:"removed,omitempty"`
}

type productionWorkspaceCleanupResult struct {
	SupersededBy                 string                            `json:"superseded_by,omitempty"`
	RetainRollback               string                            `json:"retain_rollback,omitempty"`
	FinancialBackup              string                            `json:"financial_backup,omitempty"`
	FinancialBackupSHA256        string                            `json:"financial_backup_sha256,omitempty"`
	FinancialBackupReceipt       string                            `json:"financial_backup_receipt,omitempty"`
	FinancialBackupReceiptSHA256 string                            `json:"financial_backup_receipt_sha256,omitempty"`
	WorkRoot                     string                            `json:"work_root"`
	DryRun                       bool                              `json:"dry_run"`
	OlderThan                    string                            `json:"older_than"`
	Entries                      []productionWorkspaceCleanupEntry `json:"entries"`
	RemovedBytes                 int64                             `json:"removed_bytes"`
}

func (runtime *productionRuntime) cleanupWorkspaces(ctx context.Context, options productionWorkspaceCleanupOptions) (productionWorkspaceCleanupResult, error) {
	if err := runtime.refuseUnstoppedPostMutation(); err != nil {
		return productionWorkspaceCleanupResult{}, err
	}
	if err := runtime.assertProductionMutation(); err != nil {
		return productionWorkspaceCleanupResult{}, err
	}
	if options.OlderThan <= 0 {
		return productionWorkspaceCleanupResult{}, errors.New("workspace cleanup retention must be positive")
	}
	if (options.SupersededBy == "") != (options.RetainRollback == "") {
		return productionWorkspaceCleanupResult{}, errors.New("superseded cleanup requires current and retained rollback workspace proofs together")
	}
	if options.SupersededBy != "" && options.OlderThan < productionWorkspaceCleanupRetention {
		return productionWorkspaceCleanupResult{}, errors.New("superseded cleanup requires at least 24 hours retention")
	}
	if options.SupersededBy != "" && runtime.maintenanceHandoff == nil {
		return productionWorkspaceCleanupResult{}, errors.New("superseded cleanup requires the bound three-lock maintenance guardian")
	}
	if options.SupersededBy != "" {
		if !filepath.IsAbs(options.FinancialBackup) || pathWithinRoot(runtime.paths.WorkRoot, options.FinancialBackup) || !productionSHA256Pattern.MatchString(options.FinancialBackupSHA256) {
			return productionWorkspaceCleanupResult{}, errors.New("superseded cleanup requires an external sealed full financial backup")
		}
		if err := validateMaintenanceFinancialBackup(options.FinancialBackup, options.FinancialBackupSHA256, runtime.requiredOwnerUID); err != nil {
			return productionWorkspaceCleanupResult{}, errors.New("sealed financial backup is missing, unsafe or changed")
		}
		if err := runtime.validateMaintenanceFinancialReceipt(options); err != nil {
			return productionWorkspaceCleanupResult{}, err
		}
	}

	var result productionWorkspaceCleanupResult
	if options.SupersededBy != "" {
		runtime.guardianAdoptAll = true
		defer func() { runtime.guardianAdoptAll = false }()
	}
	err := runtime.withGlobalLock(ctx, func() error {
		if err := requireRealDirectory(runtime.paths.WorkRoot); err != nil {
			return fmt.Errorf("inspect production work root: %w", err)
		}
		activeID, lockPresent, err := runtime.activeWorkspaceID()
		if err != nil {
			return err
		}
		proofProtected := map[string]string{}
		if options.SupersededBy != "" {
			if lockPresent {
				return errors.New("superseded cleanup requires no active deployment transaction")
			}
			proofProtected, err = runtime.verifySupersededCleanupProofs(ctx, options)
			if err != nil {
				return err
			}
		}
		currentRelease, err := currentFrontendRelease(runtime.paths.FrontendRoot)
		if err != nil {
			return fmt.Errorf("resolve current frontend release before cleanup: %w", err)
		}

		entries, err := os.ReadDir(runtime.paths.WorkRoot)
		if err != nil {
			return fmt.Errorf("list production workspaces: %w", err)
		}
		type candidate struct {
			workspace productionWorkspace
			status    productionStatus
		}
		candidates := make([]candidate, 0, len(entries))
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			root := filepath.Join(runtime.paths.WorkRoot, entry.Name())
			info, err := os.Lstat(root)
			if err != nil {
				return fmt.Errorf("inspect workspace %s: %w", entry.Name(), err)
			}
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				continue
			}
			workspace, err := runtime.openWorkspaceForInspection(root)
			if err != nil {
				// Unknown or damaged directories are never cleanup targets.
				result.Entries = append(result.Entries, productionWorkspaceCleanupEntry{
					DeploymentID: entry.Name(), Workspace: root, Protected: true,
					Reason: "workspace marker or layout is invalid; manual inspection required",
				})
				continue
			}
			status, err := runtime.readStatus(workspace)
			if err != nil {
				result.Entries = append(result.Entries, productionWorkspaceCleanupEntry{
					DeploymentID: workspace.id, Workspace: root, Protected: true,
					Reason: "workspace has no valid terminal status",
				})
				continue
			}
			if status.UpdatedUTC.IsZero() {
				if options.SupersededBy != "" {
					result.Entries = append(result.Entries, productionWorkspaceCleanupEntry{DeploymentID: workspace.id, Workspace: root, Phase: status.Phase, Protected: true, Reason: "no immutable terminal timestamp; manual inspection required"})
					continue
				}
				status.UpdatedUTC = info.ModTime().UTC()
			}
			switch status.Phase {
			case "CONFIRMED", "ROLLED_BACK", "ABORTED", "FAILED_PREARM":
				candidates = append(candidates, candidate{workspace: workspace, status: status})
			default:
				result.Entries = append(result.Entries, productionWorkspaceCleanupEntry{
					DeploymentID: workspace.id, Workspace: root, Phase: status.Phase,
					Protected: true, Reason: "transaction is not terminal",
				})
			}
		}

		// Keep the current release and one fallback point. A successful rollback
		// is preferred; when none exists, retain the newest confirmed workspace.
		protected := make(map[string]string)
		for id, reason := range proofProtected {
			protected[id] = reason
		}
		fallbackID := ""
		fallbackTime := time.Time{}
		for _, item := range candidates {
			if options.SupersededBy != "" {
				continue
			}
			if item.status.Phase == "CONFIRMED" && item.status.Version == currentRelease {
				protected[item.workspace.id] = "current published release"
			}
			if item.status.Phase == "ROLLED_BACK" && item.status.UpdatedUTC.After(fallbackTime) {
				fallbackID, fallbackTime = item.workspace.id, item.status.UpdatedUTC
			}
		}
		if fallbackID == "" && options.SupersededBy == "" {
			for _, item := range candidates {
				if item.status.Phase == "CONFIRMED" && item.status.UpdatedUTC.After(fallbackTime) {
					fallbackID, fallbackTime = item.workspace.id, item.status.UpdatedUTC
				}
			}
		}
		if fallbackID != "" {
			protected[fallbackID] = "most recent successful rollback point"
		}

		now := runtime.now().UTC()
		if options.SupersededBy != "" && options.Execute {
			if _, err := runtime.verifySupersededCleanupProofs(ctx, options); err != nil {
				return err
			}
			if err := validateMaintenanceFinancialBackup(options.FinancialBackup, options.FinancialBackupSHA256, runtime.requiredOwnerUID); err != nil {
				return err
			}
			if err := runtime.validateMaintenanceFinancialReceipt(options); err != nil {
				return err
			}
		}
		for _, item := range candidates {
			entry := productionWorkspaceCleanupEntry{
				DeploymentID: item.workspace.id, Workspace: item.workspace.root, Phase: item.status.Phase,
			}
			if lockPresent && item.workspace.id == activeID {
				entry.Protected, entry.Reason = true, "active deployment transaction lock"
				result.Entries = append(result.Entries, entry)
				continue
			}
			preservationReason := protected[item.workspace.id]
			if options.SupersededBy != "" && preservationReason != "" {
				entry.Protected, entry.Reason = true, preservationReason+"; complete workspace retained"
				result.Entries = append(result.Entries, entry)
				continue
			}
			if now.Sub(item.status.UpdatedUTC) < options.OlderThan {
				entry.Protected = true
				if preservationReason != "" {
					entry.Reason = preservationReason + "; terminal workspace is within retention window"
				} else {
					entry.Reason = "terminal workspace is within retention window"
				}
				result.Entries = append(result.Entries, entry)
				continue
			}

			// Confirmed and rolled-back workspaces may contain the only local
			// recovery material. Require a checksum-verified target backup first.
			if item.status.Phase == "CONFIRMED" || item.status.Phase == "ROLLED_BACK" {
				backupDir := filepath.Join(runtime.paths.BackupRoot, item.workspace.id)
				backupErr := verifyWorkspaceBackup(backupDir, item.workspace.id)
				if backupErr == nil {
					entry.HistoricalBackup = "verified"
				} else {
					entry.HistoricalBackup = "missing-or-unverified; no replacement evidence created"
				}
				if backupErr != nil && options.SupersededBy == "" {
					entry.Protected, entry.Reason = true, "durable rollback backup is not verified"
					result.Entries = append(result.Entries, entry)
					continue
				}
			}
			if options.SupersededBy != "" {
				processReference := runtime.cleanupProcessReferences
				if processReference == nil {
					processReference = workspaceProcessReferenced
				}
				held, err := processReference(item.workspace.root)
				if err != nil {
					return err
				}
				if held {
					entry.Protected, entry.Reason = true, "a live process retains workspace executable, cwd or descriptor"
					result.Entries = append(result.Entries, entry)
					continue
				}
				referenced, err := runtime.cleanupWorkspaceReferenced(item.workspace, entries)
				if err != nil {
					return err
				}
				if referenced {
					entry.Protected, entry.Reason = true, "another normal owner workspace retains a reference"
					result.Entries = append(result.Entries, entry)
					continue
				}
				entry.Reason = "historical backup not used as proof; current/bridge N-1 and sealed financial backup verified"
			}
			if !options.Execute {
				entry.Protected = preservationReason != ""
				entry.Reason = "eligible; rerun with --execute to remove disposable children"
				if preservationReason != "" {
					entry.Reason = preservationReason + "; " + entry.Reason
				}
				result.Entries = append(result.Entries, entry)
				continue
			}
			var removed []string
			var bytesRemoved int64
			if options.SupersededBy != "" {
				proof := map[string]any{"format": "lmm-credit-superseded-cleanup-v1", "deployment_id": item.workspace.id, "terminal_phase": item.status.Phase, "terminal_updated_utc": item.status.UpdatedUTC, "superseded_by": options.SupersededBy, "retained_rollback": options.RetainRollback, "financial_backup": options.FinancialBackup, "financial_backup_sha256": options.FinancialBackupSHA256, "financial_backup_receipt": options.FinancialBackupReceipt, "financial_backup_receipt_sha256": options.FinancialBackupReceiptSHA256, "historical_backup": entry.HistoricalBackup, "only_disposable_children": []string{"staging", "tmp", "cache", "caches"}}
				content, err := json.MarshalIndent(proof, "", "  ")
				if err != nil {
					return err
				}
				evidence := filepath.Join(item.workspace.stateDir, "maintenance-cleanup-proof.json")
				if existing, err := os.ReadFile(evidence); err == nil {
					if string(existing) != string(content)+"\n" {
						return errors.New("existing cleanup evidence differs from frozen replacement proofs")
					}
				} else if errors.Is(err, os.ErrNotExist) {
					if err := writeAtomicRegularFile(evidence, append(content, '\n'), 0600); err != nil {
						return err
					}
				} else {
					return err
				}
				removed, bytesRemoved, err = removeWorkspaceChildren(item.workspace, []string{"staging", "tmp", "cache", "caches"})
			} else {
				removed, bytesRemoved, err = removeDisposableWorkspaceChildren(item.workspace)
			}
			if err != nil {
				return fmt.Errorf("clean workspace %s: %w", item.workspace.id, err)
			}
			entry.Removed = removed
			entry.Protected = preservationReason != ""
			entry.Reason = "terminal disposable children removed; marker and status retained"
			if preservationReason != "" {
				entry.Reason = preservationReason + "; " + entry.Reason
			}
			result.RemovedBytes += bytesRemoved
			result.Entries = append(result.Entries, entry)
		}
		return nil
	})
	if err != nil {
		return productionWorkspaceCleanupResult{}, err
	}
	result.WorkRoot = runtime.paths.WorkRoot
	result.DryRun = !options.Execute
	result.OlderThan = options.OlderThan.String()
	result.SupersededBy = options.SupersededBy
	result.RetainRollback = options.RetainRollback
	result.FinancialBackup = options.FinancialBackup
	result.FinancialBackupSHA256 = options.FinancialBackupSHA256
	result.FinancialBackupReceipt = options.FinancialBackupReceipt
	result.FinancialBackupReceiptSHA256 = options.FinancialBackupReceiptSHA256
	return result, nil
}

func (runtime *productionRuntime) validateMaintenanceFinancialReceipt(options productionWorkspaceCleanupOptions) error {
	content, err := readMaintenanceBoundFile(options.FinancialBackupReceipt, options.FinancialBackupReceiptSHA256, runtime.requiredOwnerUID)
	if err != nil {
		return fmt.Errorf("verify sealed full financial backup receipt: %w", err)
	}
	var receipt struct {
		Format                       string         `json:"format"`
		TransitionID                 string         `json:"transition_id"`
		TransitionIntentSHA256       string         `json:"transition_intent_sha256"`
		ProviderSHA256               string         `json:"provider_sha256"`
		SourceSHA                    string         `json:"source_sha"`
		Target                       map[string]any `json:"target"`
		FullDatabase                 bool           `json:"full_database"`
		ArchiveFormat                string         `json:"archive_format"`
		PreserveOwnership            bool           `json:"preserve_ownership"`
		BackupSHA256                 string         `json:"backup_sha256"`
		SizeBytes                    int64          `json:"size_bytes"`
		FrozenGuardianBindingsSHA256 string         `json:"frozen_guardian_bindings_sha256"`
	}
	if err := json.Unmarshal(content, &receipt); err != nil {
		return err
	}
	h := runtime.maintenanceHandoff
	if h == nil || receipt.Format != "lmm-credit-financial-backup-v1" || receipt.TransitionID != h.TransitionID || receipt.TransitionIntentSHA256 != h.TransitionIntentSHA256 || receipt.ProviderSHA256 != h.ProviderSHA256 || !receipt.FullDatabase || receipt.ArchiveFormat != "custom" || !receipt.PreserveOwnership || receipt.BackupSHA256 != options.FinancialBackupSHA256 || len(receipt.SourceSHA) != 40 || !productionRevisionPattern.MatchString(receipt.SourceSHA) || !productionSHA256Pattern.MatchString(receipt.FrozenGuardianBindingsSHA256) {
		return errors.New("financial backup receipt differs from the frozen full-database transition")
	}
	archive, err := os.Stat(options.FinancialBackup)
	if err != nil || archive.Size() != receipt.SizeBytes || receipt.SizeBytes <= 5 {
		return errors.New("financial backup receipt size differs from the actual archive")
	}
	preparedBytes, err := readMaintenanceBoundFile(h.PrepareConfigPath, h.PrepareConfigSHA256, runtime.requiredOwnerUID)
	if err != nil {
		return err
	}
	var prepared productionMaintenancePrepareConfig
	if err := json.Unmarshal(preparedBytes, &prepared); err != nil {
		return err
	}
	if len(receipt.Target) != 5 {
		return errors.New("financial backup target identity has unknown or missing fields")
	}
	for _, key := range []string{"system_identifier", "database", "schema"} {
		if !reflect.DeepEqual(receipt.Target[key], prepared.Database[key]) {
			return errors.New("financial full backup target differs from sealed prepare database identity")
		}
	}
	databaseOID, ok := maintenanceFinancialReceiptOID(receipt.Target["database_oid"])
	preparedDatabaseOID, preparedOK := prepared.Database["database_oid"].(float64)
	if !ok || !preparedOK || preparedDatabaseOID <= 0 || preparedDatabaseOID > 4294967295 || preparedDatabaseOID != math.Trunc(preparedDatabaseOID) || uint32(preparedDatabaseOID) != databaseOID {
		return errors.New("financial full backup target differs from sealed prepare database identity")
	}
	if _, ok := maintenanceFinancialReceiptOID(receipt.Target["schema_oid"]); !ok {
		return errors.New("financial full backup schema oid is invalid")
	}
	return nil
}

func maintenanceFinancialReceiptOID(value any) (uint32, bool) {
	text, ok := value.(string)
	if !ok {
		return 0, false
	}
	parsed, err := strconv.ParseUint(text, 10, 32)
	if err != nil || parsed == 0 || strconv.FormatUint(parsed, 10) != text {
		return 0, false
	}
	return uint32(parsed), true
}

func validateMaintenanceFinancialBackup(path, digest string, owner uint32) error {
	clean, err := cleanAbsoluteNonRoot(path)
	if err != nil || clean != path || !productionSHA256Pattern.MatchString(digest) {
		return errors.New("invalid full financial backup binding")
	}
	var original os.FileInfo
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		uid, links, ok := deploymentFileOwnership(info)
		if !ok || (uid != owner && uid != 0) || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 {
			return errors.New("financial backup or ancestor is unsafe")
		}
		if current == path {
			if uid != owner || !info.Mode().IsRegular() || links != 1 || (info.Mode().Perm() != 0600 && info.Mode().Perm() != 0640) || info.Size() <= 5 {
				return errors.New("financial backup must be a private complete regular archive")
			}
			original = info
		}
		if current == string(filepath.Separator) {
			break
		}
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(original, opened) {
		return errors.New("financial backup was replaced")
	}
	header := make([]byte, 5)
	if _, err := io.ReadFull(file, header); err != nil || string(header) != "PGDMP" {
		return errors.New("financial backup is not a PostgreSQL custom archive")
	}
	if _, err := file.Seek(0, 0); err != nil {
		return err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != digest {
		return errors.New("financial backup hash changed")
	}
	after, err := file.Stat()
	if err != nil || after.Size() != original.Size() || !after.ModTime().Equal(original.ModTime()) {
		return errors.New("financial backup changed while reading")
	}
	return nil
}

func workspaceProcessReferenced(root string) (bool, error) {
	processes, err := os.ReadDir("/proc")
	if err != nil {
		return false, err
	}
	for _, process := range processes {
		if !process.IsDir() || process.Name() == "" || strings.Trim(process.Name(), "0123456789") != "" {
			continue
		}
		base := filepath.Join("/proc", process.Name())
		paths := []string{filepath.Join(base, "exe"), filepath.Join(base, "cwd")}
		fds, err := os.ReadDir(filepath.Join(base, "fd"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		for _, fd := range fds {
			paths = append(paths, filepath.Join(base, "fd", fd.Name()))
		}
		for _, path := range paths {
			target, err := os.Readlink(path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return false, err
			}
			target = strings.TrimSuffix(target, " (deleted)")
			if target == root || strings.HasPrefix(target, root+string(filepath.Separator)) {
				return true, nil
			}
		}
		maps, err := os.ReadFile(filepath.Join(base, "maps"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		if strings.Contains(string(maps), root+string(filepath.Separator)) {
			return true, nil
		}
	}
	return false, nil
}

func (runtime *productionRuntime) verifySupersededCleanupProofs(ctx context.Context, options productionWorkspaceCleanupOptions) (map[string]string, error) {
	current, err := runtime.openWorkspace(options.SupersededBy)
	if err != nil {
		return nil, err
	}
	retained, err := runtime.openWorkspace(options.RetainRollback)
	if err != nil || retained.id == current.id {
		return nil, errors.New("cleanup requires a distinct retained bridge workspace")
	}
	status, err := runtime.readStatus(current)
	if err != nil || status.Phase != "CONFIRMED" || !status.MaintenanceAdmissionReopened {
		return nil, errors.New("cleanup current proof requires strict release confirmation and reopened admission")
	}
	manifest, err := runtime.readManifest(current)
	if err != nil {
		return nil, err
	}
	if manifest.MaintenanceHandoff == nil || manifest.MaintenanceHandoff.Stage != "post" {
		return nil, errors.New("cleanup current proof is not the strict post transition release")
	}
	bridgeStatus, err := runtime.readStatus(retained)
	if err != nil || (bridgeStatus.Phase != productionMaintenanceConfirmedPhase && bridgeStatus.Phase != "FROZEN") || !bridgeStatus.MaintenanceConfirmation {
		return nil, errors.New("retained bridge lacks normal owner maintenance confirmation")
	}
	bridge, err := runtime.readManifest(retained)
	if err != nil {
		return nil, err
	}
	h, old := manifest.MaintenanceHandoff, bridge.MaintenanceHandoff
	if old == nil || old.Stage != "prebridge" || h.PreviousDeploymentID != retained.id || old.TransitionID != h.TransitionID || old.TransitionIntentSHA256 != h.TransitionIntentSHA256 || old.ProviderSHA256 != h.ProviderSHA256 || old.PrepareConfigSHA256 != h.PrepareConfigSHA256 || bridge.ProbeBinarySHA256 != h.ProviderSHA256 || manifest.Go.CandidateSHA256 != manifest.Go.RollbackSHA256 || bridge.Go.CandidateSHA256 != manifest.Go.RollbackSHA256 {
		return nil, errors.New("retained bridge is not the verified compatible N-1 package")
	}
	installed, err := sha256File(runtime.paths.InstalledBinary)
	if err != nil || installed != h.ProviderSHA256 {
		return nil, errors.New("installed strict provider differs from retained bridge")
	}
	if err := runtime.verifyManifestInstalled(ctx, manifest, false); err != nil {
		return nil, err
	}
	bound := runtime.maintenanceHandoff
	if runtime.guardianLease == nil || len(runtime.guardianExtraLocks) != 2 || bound == nil || bound.Stage != "post" || bound.SHA256 != h.SHA256 || bound.PrepareConfigSHA256 != h.PrepareConfigSHA256 || bound.TransitionID != h.TransitionID || bound.TransitionIntentSHA256 != h.TransitionIntentSHA256 || bound.ProviderSHA256 != h.ProviderSHA256 {
		return nil, errors.New("cleanup lease differs from current post transition")
	}
	runtime.maintenanceHandoff = nil
	defer func() { runtime.maintenanceHandoff = bound }()
	// Confirmation removes its temporary bearer copy. The handoff binds the
	// external root-private probe token, which remains available for this final
	// authenticated public gate without recreating owner state.
	if _, err := readMaintenanceBoundFile(bound.ProbeTokenPath, bound.ProbeTokenSHA256, runtime.requiredOwnerUID); err != nil {
		return nil, err
	}
	current.probeToken = bound.ProbeTokenPath
	if err := runtime.probeReleaseWithBinary(ctx, current, runtime.paths.InstalledBinary, manifest.ExpectedVersion, manifest.Frontend.NewIndexSHA256); err != nil {
		return nil, err
	}
	return map[string]string{current.id: "current strict release and N-1 payload proof", retained.id: "sole retained compatible bridge proof"}, nil
}

func (runtime *productionRuntime) cleanupWorkspaceReferenced(target productionWorkspace, entries []os.DirEntry) (bool, error) {
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == target.id {
			continue
		}
		workspace, err := runtime.openWorkspaceForInspection(filepath.Join(runtime.paths.WorkRoot, entry.Name()))
		if err != nil {
			return true, nil
		}
		status, err := runtime.readStatus(workspace)
		if err != nil {
			return true, nil
		}
		switch status.Phase {
		case "CONFIRMED", "ROLLED_BACK", "ABORTED", "FAILED_PREARM", "FROZEN":
		default:
			return true, nil
		}
		content, err := readPrivateRegularFile(workspace.manifestPath, 1<<20)
		if errors.Is(err, os.ErrNotExist) && (status.Phase == "FAILED_PREARM" || status.Phase == "ABORTED") {
			continue
		}
		if err != nil {
			return true, nil
		}
		var document any
		if err := json.Unmarshal(content, &document); err != nil {
			return true, nil
		}
		var references func(any) bool
		references = func(value any) bool {
			switch value := value.(type) {
			case string:
				for _, name := range []string{"staging", "tmp", "cache", "caches"} {
					root := filepath.Join(target.root, name)
					if value == root || strings.HasPrefix(value, root+string(filepath.Separator)) {
						return true
					}
				}
			case []any:
				for _, child := range value {
					if references(child) {
						return true
					}
				}
			case map[string]any:
				for _, child := range value {
					if references(child) {
						return true
					}
				}
			}
			return false
		}
		if references(document) {
			return true, nil
		}
	}
	return false, nil
}

func (runtime *productionRuntime) activeWorkspaceID() (string, bool, error) {
	info, err := os.Lstat(runtime.paths.TransactionLock)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("inspect deployment transaction lock: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", true, errors.New("deployment transaction lock is unsafe")
	}
	content, err := readPrivateRegularFile(filepath.Join(runtime.paths.TransactionLock, productionTransactionMarker), 16<<10)
	if err != nil {
		return "", true, fmt.Errorf("read deployment transaction lock: %w", err)
	}
	values, err := parseSimpleManifest(content)
	if err != nil || values["status"] != "ACTIVE" || !productionIDPattern.MatchString(values["deployment_id"]) {
		return "", true, errors.New("deployment transaction lock is invalid; refusing cleanup")
	}
	return values["deployment_id"], true, nil
}

func verifyWorkspaceBackup(root, deploymentID string) error {
	if filepath.Base(root) != deploymentID || !productionIDPattern.MatchString(deploymentID) {
		return errors.New("rollback backup identity is invalid")
	}
	if err := requireRealDirectory(root); err != nil {
		return err
	}
	for _, name := range []string{"application.archive", "frontend.archive", "configuration.archive", "database.archive", "manifest.env", "SHA256SUMS", "rollback.package"} {
		info, err := os.Lstat(filepath.Join(root, name))
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("backup entry %s is missing or unsafe", name)
		}
	}
	if err := verifyBackupChecksums(root); err != nil {
		return err
	}
	return validateBackupAttestation(root, deploymentID)
}

func removeDisposableWorkspaceChildren(workspace productionWorkspace) ([]string, int64, error) {
	return removeWorkspaceChildren(workspace, []string{"staging", "tmp", "cache", "caches", filepath.Join("state", productionConfigRestoreDirname)})
}

func removeWorkspaceChildren(workspace productionWorkspace, names []string) ([]string, int64, error) {
	removed := make([]string, 0)
	var removedBytes int64
	for _, name := range names {
		path := filepath.Join(workspace.root, name)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, 0, err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || !pathWithinRoot(workspace.root, path) {
			return nil, 0, fmt.Errorf("disposable child is not a real direct directory: %s", name)
		}
		size, err := directorySize(path)
		if err != nil {
			return nil, 0, err
		}
		if err := os.RemoveAll(path); err != nil {
			return nil, 0, fmt.Errorf("remove %s: %w", name, err)
		}
		removed = append(removed, name)
		removedBytes += size
	}
	sort.Strings(removed)
	return removed, removedBytes, nil
}

func directorySize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
}
