package appcli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const productionWorkspaceCleanupRetention = 24 * time.Hour

type productionWorkspaceCleanupOptions struct {
	OlderThan time.Duration
	Execute   bool
}

type productionWorkspaceCleanupEntry struct {
	DeploymentID string   `json:"deployment_id"`
	Workspace    string   `json:"workspace"`
	Phase        string   `json:"phase,omitempty"`
	Protected    bool     `json:"protected"`
	Reason       string   `json:"reason,omitempty"`
	Removed      []string `json:"removed,omitempty"`
}

type productionWorkspaceCleanupResult struct {
	WorkRoot     string                            `json:"work_root"`
	DryRun       bool                              `json:"dry_run"`
	OlderThan    string                            `json:"older_than"`
	Entries      []productionWorkspaceCleanupEntry `json:"entries"`
	RemovedBytes int64                             `json:"removed_bytes"`
}

func (runtime *productionRuntime) cleanupWorkspaces(ctx context.Context, options productionWorkspaceCleanupOptions) (productionWorkspaceCleanupResult, error) {
	if err := runtime.assertProductionMutation(); err != nil {
		return productionWorkspaceCleanupResult{}, err
	}
	if options.OlderThan <= 0 {
		return productionWorkspaceCleanupResult{}, errors.New("workspace cleanup retention must be positive")
	}

	var result productionWorkspaceCleanupResult
	err := runtime.withGlobalLock(ctx, func() error {
		if err := requireRealDirectory(runtime.paths.WorkRoot); err != nil {
			return fmt.Errorf("inspect production work root: %w", err)
		}
		activeID, lockPresent, err := runtime.activeWorkspaceID()
		if err != nil {
			return err
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
		fallbackID := ""
		fallbackTime := time.Time{}
		for _, item := range candidates {
			if item.status.Phase == "CONFIRMED" && item.status.Version == currentRelease {
				protected[item.workspace.id] = "current published release"
			}
			if item.status.Phase == "ROLLED_BACK" && item.status.UpdatedUTC.After(fallbackTime) {
				fallbackID, fallbackTime = item.workspace.id, item.status.UpdatedUTC
			}
		}
		if fallbackID == "" {
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
				if err := verifyWorkspaceBackup(backupDir, item.workspace.id); err != nil {
					entry.Protected, entry.Reason = true, "durable rollback backup is not verified"
					result.Entries = append(result.Entries, entry)
					continue
				}
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
			removed, bytesRemoved, err := removeDisposableWorkspaceChildren(item.workspace)
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
	return result, nil
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
	removed := make([]string, 0)
	var removedBytes int64
	for _, name := range []string{"staging", "tmp", "cache", "caches", filepath.Join("state", productionConfigRestoreDirname)} {
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
