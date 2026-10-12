package deploycli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// This is a projection, not evidence that the target hook is installed. The
// predecessor is an actual canonical capsule and the PID/invocation are checked
// again after the snapshot. Only the target command's self digest is normalized.
type productionMerchantStoreStartupProjection struct {
	Format           string                    `json:"format"`
	Host             string                    `json:"host"`
	Predecessor      productionReleaseFilePlan `json:"predecessor"`
	TargetPath       string                    `json:"target_path"`
	PID              int                       `json:"pid"`
	InvocationID     string                    `json:"invocation_id"`
	OldStartupSHA256 string                    `json:"old_startup_sha256"`
	StartupSHA256    string                    `json:"startup_sha256"`
	SignedUnitSHA256 string                    `json:"signed_unit_sha256"`
	OldHookSHA256    string                    `json:"old_hook_sha256"`
}

func merchantStoreCanonicalCapsulePath(path string, paths productionPaths) bool {
	id := filepath.Base(filepath.Dir(path))
	return productionIDPattern.MatchString(id) && path == filepath.Join(merchantStoreCapsuleRoot(paths, id), "capsule.json")
}

func merchantStoreCapsulePathGrammar(path string) bool {
	root := filepath.Dir(path)
	return filepath.IsAbs(path) && filepath.Clean(path) == path && filepath.Base(path) == "capsule.json" &&
		productionIDPattern.MatchString(filepath.Base(root)) && filepath.Base(filepath.Dir(root)) == "merchant-capsules"
}

func validateMerchantStoreStartupTransitionContract(writer *productionMerchantStoreWriterContract) error {
	if writer == nil {
		return errors.New("ordinary startup transition lacks its writer contract")
	}
	if writer.StartupPredecessorPath == "" && writer.StartupPredecessorSHA256 == "" && writer.StartupTargetPath == "" {
		return nil
	}
	if !merchantStoreCapsulePathGrammar(writer.StartupPredecessorPath) ||
		!productionSHA256Pattern.MatchString(writer.StartupPredecessorSHA256) ||
		!merchantStoreCapsulePathGrammar(writer.StartupTargetPath) || writer.StartupPredecessorPath == writer.StartupTargetPath ||
		filepath.Dir(filepath.Dir(writer.StartupPredecessorPath)) != filepath.Dir(filepath.Dir(writer.StartupTargetPath)) {
		return errors.New("ordinary startup transition requires distinct canonical predecessor/target capsules and exact predecessor digest")
	}
	return nil
}

func validateMerchantStoreStartupTransitionPaths(writer *productionMerchantStoreWriterContract, paths productionPaths) error {
	if err := validateMerchantStoreStartupTransitionContract(writer); err != nil {
		return err
	}
	if writer.StartupPredecessorPath != "" && (!merchantStoreCanonicalCapsulePath(writer.StartupPredecessorPath, paths) || !merchantStoreCanonicalCapsulePath(writer.StartupTargetPath, paths)) {
		return errors.New("ordinary startup transition capsule roots differ from the actual native deployment paths")
	}
	return nil
}

func validateMerchantStoreStartupTransitionBinding(writer *productionMerchantStoreWriterContract, schema *productionExistingSchemaContract, old productionMerchantStoreCapsule) error {
	if writer == nil || schema == nil || old.Format != 1 || old.StartupPolicy != "per-start" || old.Writer == nil || old.ExistingSchemaContract == nil ||
		writer.StartupPredecessorPath != filepath.Join(old.Root, "capsule.json") || writer.Rollback != old.Writer.Candidate || writer.RequiredCapability != old.Writer.RequiredCapability ||
		writer.SystemIdentifier != old.Writer.SystemIdentifier || writer.Database != old.Writer.Database || writer.DatabaseOID != old.Writer.DatabaseOID ||
		writer.Schema != old.Writer.Schema || writer.SchemaOID != old.Writer.SchemaOID || writer.Role != old.Writer.Role || writer.SignedUnitSHA256 != old.Writer.SignedUnitSHA256 {
		return errors.New("ordinary startup predecessor differs from the exact retained writer/physical identity")
	}
	before, target := *old.ExistingSchemaContract, *schema
	before.StartupSHA256, target.StartupSHA256 = "", ""
	if before != target || schema.StartupSHA256 != writer.StartupSHA256 || schema.SignedUnitSHA256 != writer.SignedUnitSHA256 {
		return errors.New("ordinary startup transition changed schema/catalog or ordered startup identity")
	}
	return nil
}

// The old command and its persistent bytes have already been independently
// qualified. Build a separate snapshot; never rewrite loaded unit facts to make
// a prospective target appear installed, and never use schema-recovery authority.
func (runtime *productionRuntime) merchantStoreStartupTransitionSnapshot(ctx context.Context, loaded, commands map[string]string, operator, targetPath string) (string, string, error) {
	if !merchantStoreCanonicalCapsulePath(targetPath, runtime.paths) || operator == "" {
		return "", "", errors.New("ordinary startup projection target is not canonical")
	}
	projected := make(map[string]string, len(commands))
	for key, value := range commands {
		projected[key] = value
	}
	projected["ExecStartPre"] = operator + "\x00" + operator + " operator production writer-start --capsule " + targetPath + " --capsule-sha256 " + merchantStoreCapsuleHashPlaceholder + "\x00no\x00privileged"
	projected["ExecStartPreEx"] = "privileged"
	projected["PersistentPortableHookSHA256"] = startupContentSHA256(merchantStorePortableHookBytes(operator, targetPath, merchantStoreCapsuleHashPlaceholder))
	values, err := parseExistingSchemaLoadedEnvironment(loaded["Environment"])
	if err != nil || verifyExistingSchemaStartupEnvironment(values, true) != nil {
		return "", "", errors.New("ordinary startup projection is not verify-only")
	}
	_, startup, unit, err := runtime.existingSchemaSealedStartupCommands(ctx, loaded, values, projected)
	return startup, unit, err
}

func merchantStoreStartupLoadedEqual(before, after map[string]string) bool {
	for _, key := range strings.Split(existingSchemaUnitProperties, ",") {
		if before[key] != after[key] {
			return false
		}
	}
	return true
}

func (runtime *productionRuntime) inspectMerchantStoreStartupProjection(ctx context.Context, oldPath, oldSHA, targetPath string, loaded map[string]string, requireRunning bool) (productionMerchantStoreStartupProjection, productionMerchantStoreCapsule, map[string]string, error) {
	var projection productionMerchantStoreStartupProjection
	var old productionMerchantStoreCapsule
	if !merchantStoreCanonicalCapsulePath(oldPath, runtime.paths) || !merchantStoreCanonicalCapsulePath(targetPath, runtime.paths) || oldPath == targetPath || !productionSHA256Pattern.MatchString(oldSHA) {
		return projection, old, nil, errors.New("ordinary startup projection requires exact distinct predecessor and target references")
	}
	var err error
	old, err = runtime.loadMerchantStoreCapsule(oldPath, oldSHA)
	if err != nil {
		return projection, old, nil, err
	}
	if old.Format != 1 || old.StartupPolicy != "per-start" {
		return projection, old, nil, errors.New("ordinary startup projection requires an ordinary per-start predecessor")
	}
	actual, err := runtime.loadedExistingSchemaUnit(ctx)
	if err != nil || loaded != nil && !merchantStoreStartupLoadedEqual(loaded, actual) {
		return projection, old, nil, errors.New("ordinary startup predecessor loaded generation changed")
	}
	loaded = actual
	pid, parseErr := strconv.Atoi(loaded["MainPID"])
	active := parseErr == nil && pid > 1 && loaded["ActiveState"] == "active" && existingSchemaInvocationPattern.MatchString(loaded["InvocationID"])
	stopped := parseErr == nil && pid == 0 && loaded["ActiveState"] == "inactive"
	if !active && (requireRunning || !stopped) {
		return projection, old, nil, errors.New("ordinary startup projection requires an actual active or strictly stopped predecessor generation")
	}
	values, err := runtime.merchantStoreCapsuleStartup(ctx, old, oldSHA, loaded)
	if err != nil {
		return projection, old, nil, err
	}
	if active {
		if err := runtime.verifyPortableRunningWriter(ctx, old, oldSHA, loaded["InvocationID"], false); err != nil {
			return projection, old, nil, err
		}
	} else {
		if err := runtime.verifyMerchantStorePortableInstalled(old, old.Writer.Candidate.PayloadSHA256); err != nil {
			return projection, old, nil, err
		}
		child, err := runtime.merchantStoreCapsuleEnvironment(ctx, old, oldSHA, "")
		if err != nil {
			return projection, old, nil, err
		}
		if err := runtime.verifyMerchantStoreCapsuleSchema(ctx, old, child); err != nil {
			return projection, old, nil, err
		}
	}
	commands, err := runtime.merchantStorePortableStartupCommands(loaded, old.Binary, oldPath, oldSHA)
	if err != nil {
		return projection, old, nil, err
	}
	hookPath := filepath.Join(runtime.paths.DropInDir, "90-merchant-startup-baseline.conf")
	hook, err := runtime.readExistingSchemaSealedFile(hookPath, false)
	if err != nil {
		return projection, old, nil, err
	}
	startup, unit, err := runtime.merchantStoreStartupTransitionSnapshot(ctx, loaded, commands, deploymentInstalledCommand(old.Binary, old.Candidate), targetPath)
	if err != nil || unit != old.Writer.SignedUnitSHA256 {
		return projection, old, nil, errors.New("ordinary startup projection changed the signed unit")
	}
	after, err := runtime.loadedExistingSchemaUnit(ctx)
	if err != nil || !merchantStoreStartupLoadedEqual(loaded, after) {
		return projection, old, nil, errors.New("ordinary startup predecessor generation changed during projection")
	}
	if _, err := runtime.merchantStoreCapsuleStartup(ctx, old, oldSHA, after); err != nil {
		return projection, old, nil, err
	}
	if runtime.verifyMerchantStorePortableInstalled(old, old.Writer.Candidate.PayloadSHA256) != nil || active && sha256MustEqual(filepath.Join("/proc", strconv.Itoa(pid), "exe"), old.Writer.Candidate.PayloadSHA256) != nil {
		return projection, old, nil, errors.New("ordinary startup predecessor provider changed during projection")
	}
	afterHook, err := runtime.readExistingSchemaSealedFile(hookPath, false)
	if err != nil || !bytes.Equal(hook, afterHook) {
		return projection, old, nil, errors.New("ordinary startup predecessor hook changed during projection")
	}
	if _, err := runtime.loadMerchantStoreCapsule(oldPath, oldSHA); err != nil {
		return projection, old, nil, err
	}
	projection = productionMerchantStoreStartupProjection{"lmm-merchant-store-startup-projection-v1", old.Host,
		productionReleaseFilePlan{Path: oldPath, SHA256: oldSHA}, targetPath, pid, loaded["InvocationID"], old.Writer.StartupSHA256, startup, unit, startupContentSHA256(hook)}
	return projection, old, values, nil
}

func (runtime *productionRuntime) projectMerchantStoreStartup(ctx context.Context, oldPath, oldSHA, targetPath string) (productionMerchantStoreStartupProjection, error) {
	projection, _, _, err := runtime.inspectMerchantStoreStartupProjection(ctx, oldPath, oldSHA, targetPath, nil, true)
	return projection, err
}

func merchantStoreStartupPrebindIntentAbsent(targetPath string) error {
	intent := filepath.Join(filepath.Dir(targetPath), "state", "ordinary-startup-bind-intent.json")
	if _, err := os.Lstat(intent); !errors.Is(err, os.ErrNotExist) {
		return errors.New("ordinary startup bind intent exists or is unknown; pre-bind qualification cannot be reused")
	}
	return nil
}

// This authorizes only pre-bind qualification. It returns the actual old
// effective environment after matching the separately computed future seal.
// The ordinary mutation boundary must still install and verify the actual target.
func (runtime *productionRuntime) merchantStoreTransitionStartup(ctx context.Context, writer *productionMerchantStoreWriterContract, expectedSchema *productionExistingSchemaContract, loaded map[string]string) (map[string]string, error) {
	if err := validateMerchantStoreWriterContract(writer); err != nil {
		return nil, err
	}
	if writer.StartupPredecessorPath == "" || validateProductionExistingSchemaContract(expectedSchema) != nil {
		return nil, errors.New("ordinary pre-bind startup lacks its exact transition/schema binding")
	}
	if err := merchantStoreStartupPrebindIntentAbsent(writer.StartupTargetPath); err != nil {
		return nil, err
	}
	projection, old, values, err := runtime.inspectMerchantStoreStartupProjection(ctx, writer.StartupPredecessorPath, writer.StartupPredecessorSHA256, writer.StartupTargetPath, loaded, false)
	if err != nil {
		return nil, err
	}
	if err := validateMerchantStoreStartupTransitionBinding(writer, expectedSchema, old); err != nil {
		return nil, err
	}
	if projection.StartupSHA256 != writer.StartupSHA256 || projection.SignedUnitSHA256 != writer.SignedUnitSHA256 {
		return nil, errors.New("ordinary pre-bind startup projection differs from the immutable target seal")
	}
	if err := merchantStoreStartupPrebindIntentAbsent(writer.StartupTargetPath); err != nil {
		return nil, err
	}
	return values, nil
}
