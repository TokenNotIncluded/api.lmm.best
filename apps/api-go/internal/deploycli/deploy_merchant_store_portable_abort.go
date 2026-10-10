package deploycli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// These are expectations from the original, pre-apply capture, not permission
// to clear an owner. The same physical holder still performs the release CAS.
type merchantStorePortableAbortOptions struct {
	StateSHA256   string
	OwnerSHA256   string
	OldPID        int
	OldInvocation string
	OldBootID     string
}

func (runtime *productionRuntime) portableTransactionRoot() string {
	if runtime.portableWorkRoot != "" {
		return runtime.portableWorkRoot // Local component tests only.
	}
	return "/var/lib/lmm-api-deploy-systemd"
}

func (runtime *productionRuntime) portableAbortLocks() ([]*os.File, error) {
	var files []*os.File
	fail := func() ([]*os.File, error) {
		for _, file := range files {
			_ = file.Close()
		}
		return nil, errors.New("portable pre-apply abort requires all original ordinary locks; no guardian lease is adopted")
	}
	if _, err := os.Lstat(runtime.paths.TransactionLock); !errors.Is(err, os.ErrNotExist) {
		return fail()
	}
	for _, path := range []string{runtime.paths.GlobalLock, filepath.Join(runtime.portableTransactionRoot(), "lock"), filepath.Join(runtime.paths.FrontendRoot, ".release.lock")} {
		for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
			info, err := os.Lstat(parent)
			if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return fail()
			}
			uid, _, ok := deploymentFileOwnership(info)
			sticky := uid == 0 && info.Mode()&os.ModeSticky != 0
			if !ok || uid != 0 && uid != runtime.requiredOwnerUID || info.Mode().Perm()&0022 != 0 && !sticky {
				return fail()
			}
			if parent == filepath.Dir(parent) {
				break
			}
		}
		resolved, err := filepath.EvalSymlinks(path)
		info, statErr := os.Lstat(path)
		if err != nil || statErr != nil || resolved != path || !info.Mode().IsRegular() {
			return fail()
		}
		uid, links, ok := deploymentFileOwnership(info)
		if !ok || uid != runtime.requiredOwnerUID || links != 1 || info.Mode().Perm()&0022 != 0 {
			return fail()
		}
		file, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			return fail()
		}
		files = append(files, file)
		opened, err := file.Stat()
		if err != nil || !os.SameFile(info, opened) {
			return fail()
		}
		locked, err := tryDeploymentFileLock(file)
		if err != nil || !locked {
			return fail()
		}
		named, err := os.Lstat(path)
		if err != nil || !os.SameFile(opened, named) {
			return fail()
		}
	}
	if _, err := os.Lstat(runtime.paths.TransactionLock); !errors.Is(err, os.ErrNotExist) {
		return fail()
	}
	return files, nil
}

func (runtime *productionRuntime) portableAbortRecheckLocks(files []*os.File) error {
	if _, err := os.Lstat(runtime.paths.TransactionLock); !errors.Is(err, os.ErrNotExist) {
		return errors.New("another native transaction lease appeared")
	}
	for _, file := range files {
		opened, err := file.Stat()
		named, namedErr := os.Lstat(file.Name())
		if err != nil || namedErr != nil || !os.SameFile(opened, named) || !named.Mode().IsRegular() {
			return errors.New("portable held lock inode changed")
		}
		uid, links, ok := deploymentFileOwnership(named)
		if !ok || uid != runtime.requiredOwnerUID || links != 1 || named.Mode().Perm()&0022 != 0 {
			return errors.New("portable held lock ownership changed")
		}
		resolved, err := filepath.EvalSymlinks(file.Name())
		if err != nil || resolved != file.Name() {
			return errors.New("portable held lock ancestor changed")
		}
		for parent := filepath.Dir(file.Name()); ; parent = filepath.Dir(parent) {
			info, err := os.Lstat(parent)
			if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return errors.New("portable held lock ancestor is unsafe")
			}
			uid, _, ok := deploymentFileOwnership(info)
			sticky := uid == 0 && info.Mode()&os.ModeSticky != 0
			if !ok || uid != 0 && uid != runtime.requiredOwnerUID || info.Mode().Perm()&0022 != 0 && !sticky {
				return errors.New("portable held lock ancestor is replaceable")
			}
			if parent == filepath.Dir(parent) {
				break
			}
		}
	}
	return nil
}

func (runtime *productionRuntime) portableAbortTree(root string) (string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		uid, links, ok := deploymentFileOwnership(info)
		if !ok || uid != runtime.requiredOwnerUID || info.Mode().Perm()&0022 != 0 || (!info.IsDir() && (!info.Mode().IsRegular() || links != 1)) {
			return errors.New("portable STAGED tree has unsafe ownership or links")
		}
		if !info.IsDir() {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	// Python sorts pathlib.Path objects by components, not the whole string
	// (a/x sorts before a.z). Match deploy-systemd.py's exact tree identity.
	sort.Slice(paths, func(i, j int) bool {
		left, right := strings.Split(paths[i], string(filepath.Separator)), strings.Split(paths[j], string(filepath.Separator))
		for n := 0; n < len(left) && n < len(right); n++ {
			if left[n] != right[n] {
				return left[n] < right[n]
			}
		}
		return len(left) < len(right)
	})
	hash := sha256.New()
	for _, path := range paths {
		digest, err := sha256File(path)
		if err != nil {
			return "", err
		}
		relative, _ := filepath.Rel(root, path)
		_, _ = hash.Write([]byte(filepath.ToSlash(relative) + "\x00" + digest + "\x00"))
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (runtime *productionRuntime) portableAbortStaged(work string, c productionMerchantStoreCapsule, expected string) ([]byte, map[string]json.RawMessage, error) {
	return runtime.portableAbortStagedEvidence(work, c, expected, false)
}

func (runtime *productionRuntime) portableAbortStagedEvidence(work string, c productionMerchantStoreCapsule, expected string, prepared bool) ([]byte, map[string]json.RawMessage, error) {
	if runtime.merchantStorePrivateDirectory(work, false) != nil {
		return nil, nil, errors.New("portable original STAGED workspace is unsafe")
	}
	raw, err := runtime.readExistingSchemaSealedFile(filepath.Join(work, "state.json"), true)
	var state map[string]json.RawMessage
	if err != nil || startupContentSHA256(raw) != expected || !merchantStoreUniqueJSON(raw) || json.Unmarshal(raw, &state) != nil || len(state) != 7 {
		return nil, nil, errors.New("portable original STAGED bytes changed")
	}
	var value struct {
		Release     string   `json:"release"`
		Version     string   `json:"version"`
		SHA         string   `json:"sha256"`
		FrontendSHA string   `json:"frontend_sha256"`
		Migrate     bool     `json:"migrate"`
		Excludes    []string `json:"backup_exclude_tables"`
		Phase       string   `json:"phase"`
	}
	for _, key := range []string{"release", "version", "sha256", "frontend_sha256", "migrate", "backup_exclude_tables", "phase"} {
		if _, ok := state[key]; !ok {
			return nil, nil, errors.New("portable STAGED fields are not the exact ordinary preparation")
		}
	}
	candidateVersion, versionErr := packageReleaseVersion(c.Candidate.Version)
	if json.Unmarshal(raw, &value) != nil || versionErr != nil || value.Release != c.DeploymentID || value.Phase != "STAGED" || value.Version != candidateVersion || value.SHA != c.Writer.Candidate.PayloadSHA256 || !productionSHA256Pattern.MatchString(value.FrontendSHA) || string(state["migrate"]) != "false" || value.Excludes == nil || len(value.Excludes) != 0 {
		return nil, nil, errors.New("portable abort only accepts an ordinary unmutated STAGED transaction")
	}
	entries, err := os.ReadDir(work)
	if err != nil {
		return nil, nil, err
	}
	allowed := map[string]bool{"state.json": true, "lmm-api": true, "lmm-api-go": true, "frontend": true, "verify-stage.log": true}
	if prepared {
		for _, name := range []string{"pre-apply-abort.original-state.json", "pre-apply-abort.original-owner.json", "pre-apply-abort.receipt.json"} {
			allowed[name] = true
		}
	}
	for _, entry := range entries {
		if !allowed[entry.Name()] {
			return nil, nil, errors.New("portable STAGED workspace contains mutation or unknown evidence")
		}
		if entry.Name() == "lmm-api" {
			continue
		}
		if entry.Name() != "frontend" {
			info, err := entry.Info()
			if err != nil {
				return nil, nil, err
			}
			uid, links, ok := deploymentFileOwnership(info)
			if !ok || uid != runtime.requiredOwnerUID || links != 1 || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
				return nil, nil, errors.New("portable STAGED file is unsafe")
			}
		}
	}
	link := filepath.Join(work, "lmm-api")
	info, err := os.Lstat(link)
	if err != nil {
		return nil, nil, err
	}
	uid, _, ok := deploymentFileOwnership(info)
	target, err := os.Readlink(link)
	if !ok || uid != runtime.requiredOwnerUID || info.Mode()&os.ModeSymlink == 0 || err != nil || target != backendGoName || sha256MustEqual(filepath.Join(work, backendGoName), value.SHA) != nil {
		return nil, nil, errors.New("portable STAGED provider changed")
	}
	if info, err := os.Lstat(filepath.Join(work, "frontend", "index.html")); err != nil || !info.Mode().IsRegular() {
		return nil, nil, errors.New("portable STAGED frontend has no regular index")
	}
	tree, err := runtime.portableAbortTree(filepath.Join(work, "frontend"))
	if err != nil || tree != value.FrontendSHA {
		return nil, nil, errors.New("portable STAGED frontend changed")
	}
	return raw, state, nil
}

func (runtime *productionRuntime) portableAbortOldGeneration(ctx context.Context, c productionMerchantStoreCapsule, options merchantStorePortableAbortOptions) error {
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil || strings.TrimSpace(string(boot)) != options.OldBootID {
		return errors.New("portable pre-apply boot generation changed")
	}
	unit, err := runtime.billingUnitState(ctx, c.Service)
	if err != nil {
		return err
	}
	restarts, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"show", c.Service, "--property=NRestarts", "--value"}, Timeout: 15 * time.Second})
	pid := strconv.Itoa(options.OldPID)
	if err != nil || strings.TrimSpace(string(restarts)) != "0" || unit["MainPID"] != pid || unit["ExecMainPID"] != pid || unit["InvocationID"] != options.OldInvocation || unit["ActiveState"] != "active" || unit["SubState"] != "running" || sha256MustEqual(filepath.Join("/proc", pid, "exe"), c.Writer.Rollback.PayloadSHA256) != nil || runtime.verifyMerchantStorePortableInstalled(c, c.Writer.Rollback.PayloadSHA256) != nil {
		return errors.New("portable original rollback writer generation changed")
	}
	return nil
}

func (runtime *productionRuntime) abortMerchantStorePortablePreApply(ctx context.Context, c productionMerchantStoreCapsule, digest string, options merchantStorePortableAbortOptions) (string, error) {
	if c.Format != 1 || c.Writer == nil || !productionSHA256Pattern.MatchString(options.StateSHA256) || !productionSHA256Pattern.MatchString(options.OwnerSHA256) || options.OldPID <= 1 || !existingSchemaInvocationPattern.MatchString(options.OldInvocation) || len(options.OldBootID) != 36 {
		return "", errors.New("portable pre-apply abort requires original captured state/owner/generation expectations")
	}
	locks, err := runtime.portableAbortLocks()
	if err != nil {
		return "", err
	}
	defer func() {
		for _, file := range locks {
			_ = file.Close()
		}
	}()
	work := filepath.Join(runtime.portableTransactionRoot(), c.DeploymentID)
	original, state, err := runtime.portableAbortStaged(work, c, options.StateSHA256)
	if err != nil {
		return "", err
	}
	if err := runtime.portableAbortOldGeneration(ctx, c, options); err != nil {
		return "", err
	}
	owner, ownerRaw, err := runtime.portableOwner(ctx, c, digest, "")
	if err != nil || startupContentSHA256(ownerRaw) != options.OwnerSHA256 {
		return "", errors.New("portable original physical owner changed")
	}
	if err := runtime.requestMerchantStorePortableFence(ctx, c, digest, "", false); err != nil {
		return "", err
	}
	if err := runtime.qualifyMerchantStoreCapsule(ctx, c, digest, "", false); err != nil {
		return "", err
	}
	if err := runtime.verifyPortableRunningWriter(ctx, c, digest, "", true); err != nil {
		return "", err
	}
	current := filepath.Join(runtime.paths.FrontendRoot, "current")
	link, err := os.Lstat(current)
	if err != nil {
		return "", err
	}
	uid, _, ok := deploymentFileOwnership(link)
	frontend, err := os.Readlink(current)
	if !ok || uid != runtime.requiredOwnerUID || link.Mode()&os.ModeSymlink == 0 || err != nil || !strings.HasPrefix(frontend, "releases/") || filepath.Clean(frontend) != frontend || strings.Contains(strings.TrimPrefix(frontend, "releases/"), "/") {
		return "", errors.New("portable active frontend target is unsafe")
	}
	frontendSHA, err := runtime.portableAbortTree(filepath.Join(runtime.paths.FrontendRoot, frontend))
	if err != nil {
		return "", errors.New("portable active frontend is unsafe")
	}
	var stagedFrontendSHA string
	_ = json.Unmarshal(state["frontend_sha256"], &stagedFrontendSHA)
	if frontendSHA != stagedFrontendSHA {
		return "", errors.New("portable abort requires the unchanged staged/active frontend")
	}
	// Recheck after external signature/schema/readiness work, while all three
	// ordinary locks remain held. No service install/stop/start operation exists.
	after, _, err := runtime.portableAbortStaged(work, c, options.StateSHA256)
	if err != nil || !bytesEqual(original, after) {
		return "", errors.New("portable STAGED changed during pre-apply abort")
	}
	if err := runtime.portableAbortOldGeneration(ctx, c, options); err != nil {
		return "", err
	}
	_, ownerAfter, err := runtime.portableOwner(ctx, c, digest, "")
	if err != nil || !bytesEqual(ownerRaw, ownerAfter) {
		return "", errors.New("portable owner changed during pre-apply abort")
	}
	if err := runtime.requestMerchantStorePortableFence(ctx, c, digest, "", false); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	executorSHA, err := sha256File("/proc/self/exe")
	if err != nil {
		return "", err
	}
	guard := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := runtime.portableAbortRecheckLocks(locks); err != nil {
			return err
		}
		if err := runtime.portableAbortOldGeneration(ctx, c, options); err != nil {
			return err
		}
		if _, err := runtime.loadMerchantStoreCapsule(filepath.Join(c.Root, "capsule.json"), digest); err != nil {
			return err
		}
		if _, err := runtime.merchantStoreCapsuleEnvironment(ctx, c, digest, ""); err != nil {
			return err
		}
		_, actualOwner, err := runtime.portableOwner(ctx, c, digest, "")
		if err != nil || !bytesEqual(actualOwner, ownerRaw) {
			return errors.New("portable owner changed before terminal transition")
		}
		if err := runtime.requestMerchantStorePortableFence(ctx, c, digest, "", false); err != nil {
			return err
		}
		actualFrontend, err := os.Readlink(current)
		if err != nil || actualFrontend != frontend {
			return errors.New("portable active frontend changed before terminal transition")
		}
		actualTree, err := runtime.portableAbortTree(filepath.Join(runtime.paths.FrontendRoot, frontend))
		if err != nil || actualTree != frontendSHA {
			return errors.New("portable active frontend bytes changed before terminal transition")
		}
		if err := runtime.portableAbortRecheckLocks(locks); err != nil {
			return err
		}
		return ctx.Err()
	}
	return runtime.commitPortablePreApplyAbort(ctx, work, c, digest, options, original, state, ownerRaw, owner, frontend, frontendSHA, executorSHA, guard)
}

func (runtime *productionRuntime) commitPortablePreApplyAbort(ctx context.Context, work string, c productionMerchantStoreCapsule, digest string, options merchantStorePortableAbortOptions, original []byte, state map[string]json.RawMessage, ownerRaw []byte, owner productionMerchantStoreFenceOwner, frontend, frontendSHA, executorSHA string, guard func() error) (string, error) {
	version, err := packageReleaseVersion(c.Rollback.Version)
	if err != nil {
		return "", err
	}
	if err := runtime.startupBaselineNewBytes(filepath.Join(work, "pre-apply-abort.original-state.json"), original); err != nil {
		return "", err
	}
	if err := runtime.startupBaselineNewBytes(filepath.Join(work, "pre-apply-abort.original-owner.json"), ownerRaw); err != nil {
		return "", err
	}
	receiptPath := filepath.Join(work, "pre-apply-abort.receipt.json")
	receipt := map[string]any{"format": "lmm-portable-pre-apply-abort-v1", "reason": "unchanged-before-mutation", "deployment_id": c.DeploymentID, "host": c.Host, "capsule_sha256": digest, "controller_plan_sha256": c.ControllerPlanSHA256, "original_state_sha256": options.StateSHA256, "original_owner_sha256": options.OwnerSHA256, "holder_pid": owner.HolderPID, "holder_invocation_id": owner.HolderInvocationID, "old_pid": options.OldPID, "old_invocation_id": options.OldInvocation, "old_boot_id": options.OldBootID, "rollback_payload_sha256": c.Writer.Rollback.PayloadSHA256, "frontend_target": frontend, "frontend_sha256": frontendSHA, "executor_sha256": executorSHA, "created_utc": runtime.now().UTC()}
	receiptRaw, err := merchantStartupJSON(receipt)
	if err != nil {
		return "", err
	}
	if err := runtime.startupBaselineNewBytes(receiptPath, receiptRaw); err != nil {
		return "", err
	}
	if err := syncDirectory(work); err != nil {
		return "", err
	}
	receiptSHA := startupContentSHA256(receiptRaw)
	set := func(key string, value any) { raw, _ := json.Marshal(value); state[key] = raw }
	set("phase", "ROLLED_BACK")
	set("previous_sha256", c.Writer.Rollback.PayloadSHA256)
	set("previous_version", version)
	set("previous_frontend", strings.TrimPrefix(frontend, "releases/"))
	set("terminal_at", float64(runtime.now().UnixNano())/1e9)
	set("pre_apply_abort", map[string]string{"receipt_path": receiptPath, "receipt_sha256": receiptSHA})
	terminal, err := merchantStartupJSON(state)
	if err != nil {
		return "", err
	}
	// A partially written receipt is retained for inspection. It never makes
	// STAGED terminal, and this operation is deliberately not replayable.
	if current, err := runtime.readExistingSchemaSealedFile(filepath.Join(work, "state.json"), true); err != nil || !bytesEqual(original, current) {
		return "", errors.New("portable original STAGED changed before terminal transition")
	}
	if err := guard(); err != nil {
		return "", err
	}
	current, _, err := runtime.portableAbortStagedEvidence(work, c, options.StateSHA256, true)
	if err != nil || !bytesEqual(current, original) {
		return "", errors.New("portable original STAGED changed during final physical guard")
	}
	for _, evidence := range []struct {
		name string
		raw  []byte
	}{{"pre-apply-abort.original-state.json", original}, {"pre-apply-abort.original-owner.json", ownerRaw}, {"pre-apply-abort.receipt.json", receiptRaw}} {
		actual, err := runtime.readExistingSchemaSealedFile(filepath.Join(work, evidence.name), true)
		if err != nil || !bytesEqual(actual, evidence.raw) {
			return "", errors.New("portable sealed abort evidence changed before terminal transition")
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := writeAtomicRegularFile(filepath.Join(work, "state.json"), terminal, 0600); err != nil {
		return "", err
	}
	if current, err := runtime.readExistingSchemaSealedFile(filepath.Join(work, "state.json"), true); err != nil || !bytesEqual(terminal, current) {
		return "", errors.New("portable pre-apply abort terminal readback failed; inspect retained evidence")
	}
	return receiptSHA, nil
}
