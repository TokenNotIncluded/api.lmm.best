package deploycli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Only writer-start-check creates this process-local context after validating
// the immutable manifest and the real ExecStartPre process. It is never a CLI
// option, durable receipt, or permission for an ordinary lifecycle caller.
type merchantStoreHeldStartContextKey struct{}
type merchantStoreHeldStartContext struct {
	workspace   productionWorkspace
	manifest    productionManifest
	manifestSHA string
	invocation  string
}

func (runtime *productionRuntime) verifyMerchantStoreHeldStart(ctx context.Context, held *merchantStoreHeldStartContext, manifest productionManifest, loaded map[string]string) error {
	if held == nil || runtime.effectiveUID == nil || runtime.effectiveUID() != 0 ||
		!existingSchemaInvocationPattern.MatchString(held.invocation) || os.Getenv("INVOCATION_ID") != held.invocation ||
		manifest.MerchantStoreWriter == nil || held.manifest.MerchantStoreWriter == nil || *manifest.MerchantStoreWriter != *held.manifest.MerchantStoreWriter ||
		manifest.ExistingSchemaContract == nil || held.manifest.ExistingSchemaContract == nil || *manifest.ExistingSchemaContract != *held.manifest.ExistingSchemaContract ||
		manifest.SchemaPlanSHA256 != held.manifest.SchemaPlanSHA256 {
		return errors.New("merchant held startup lacks its process-local manifest/invocation binding")
	}
	if sha256MustEqual(held.workspace.manifestPath, held.manifestSHA) != nil {
		return errors.New("merchant held startup manifest changed during inspection")
	}
	entry := merchantStoreHeldEngineEntrypoint(held.workspace.root, manifest)
	expected := entry + "\x00" + entry + " operator production writer-start-check --workspace " + held.workspace.root + "\x00no"
	semantic, err := existingSchemaCommandSemantics(loaded["ExecStartPre"])
	if err != nil || semantic != expected || merchantStorePrivilegedStartCommand(semantic, loaded["ExecStartPreEx"]) != nil {
		return errors.New("merchant held startup is not its exact privileged candidate hook")
	}
	state, err := runtime.merchantStoreStartupState(ctx, runtime.paths.Service)
	if err != nil || !merchantStoreStartupActivating(state, held.invocation, os.Getpid()) ||
		loaded["InvocationID"] != held.invocation || loaded["ActiveState"] != "activating" || loaded["MainPID"] != "0" {
		return errors.New("merchant held startup is not the actual ExecStartPre control process")
	}
	provider := filepath.Join(filepath.Dir(entry), backendGoName)
	info, err := os.Lstat(entry)
	if err != nil {
		return errors.New("merchant held startup candidate entry is unavailable")
	}
	owner, _, ok := deploymentFileOwnership(info)
	if !ok || owner != runtime.requiredOwnerUID || runtime.merchantStoreCapsuleFile(provider, 0700) != nil || sha256MustEqual(provider, manifest.MerchantStoreWriter.Candidate.PayloadSHA256) != nil {
		return errors.New("merchant held startup candidate entry/payload is unsafe or changed")
	}
	if manifest.DeployEngineSHA256 != "" {
		if runtime.merchantStoreCapsuleFile(entry, 0700) != nil || sha256MustEqual(entry, manifest.DeployEngineSHA256) != nil {
			return errors.New("merchant held startup deployment tool is unsafe or changed")
		}
	} else if target, err := os.Readlink(entry); err != nil || target != backendGoName {
		return errors.New("merchant held startup candidate entry/payload is unsafe or changed")
	}
	hasher := runtime.billingExecutableSHA256
	if hasher == nil {
		hasher = func(pid int) (string, error) { return sha256File(filepath.Join("/proc", strconv.Itoa(pid), "exe")) }
	}
	if digest, err := hasher(os.Getpid()); err != nil || digest != manifest.deploymentSHA256() {
		return errors.New("merchant held startup checker is not the qualified candidate payload")
	}
	digest, err := sha256File(runtime.paths.InstalledBinary)
	if err != nil || digest != manifest.MerchantStoreWriter.Candidate.PayloadSHA256 && digest != manifest.MerchantStoreWriter.Rollback.PayloadSHA256 {
		return errors.New("merchant held startup installed writer is outside its candidate/retained artifact set")
	}
	if err := runtime.requestMerchantStoreFence(ctx, held.workspace, manifest, false); err != nil {
		return err
	}
	after, err := runtime.merchantStoreStartupState(ctx, runtime.paths.Service)
	if err != nil || !merchantStoreStartupActivating(after, held.invocation, os.Getpid()) || after["InactiveExitTimestampMonotonic"] != state["InactiveExitTimestampMonotonic"] {
		return errors.New("merchant held startup generation changed during holder inspection")
	}
	return nil
}

func (runtime *productionRuntime) merchantStoreWriterStartCheck(ctx context.Context, workspace productionWorkspace) error {
	manifestSHA, err := sha256File(workspace.manifestPath)
	if err != nil {
		return err
	}
	manifest, err := runtime.readManifest(workspace)
	if err != nil {
		return err
	}
	if err := validateMerchantStoreWriterContract(manifest.MerchantStoreWriter); err != nil {
		return err
	}
	if err := validateProductionExistingSchemaManifestPlan(workspace, manifest); err != nil {
		return err
	}
	held := &merchantStoreHeldStartContext{workspace: workspace, manifest: manifest, manifestSHA: manifestSHA, invocation: os.Getenv("INVOCATION_ID")}
	loaded, err := runtime.loadedExistingSchemaUnit(ctx)
	if err != nil {
		return err
	}
	if err := runtime.verifyMerchantStoreHeldStart(ctx, held, manifest, loaded); err != nil {
		return err
	}
	ctx = context.WithValue(ctx, merchantStoreHeldStartContextKey{}, held)
	digest, err := sha256File(runtime.paths.InstalledBinary)
	if err != nil {
		return errors.New("merchant startup provider cannot be identified")
	}
	rollback := false
	if digest != manifest.MerchantStoreWriter.Candidate.PayloadSHA256 {
		if digest != manifest.MerchantStoreWriter.Rollback.PayloadSHA256 {
			return errors.New("merchant startup provider is outside its immutable candidate/retained artifact set")
		}
		rollback = true
	}
	if err := runtime.checkMerchantStoreWriterLifecycle(ctx, workspace, manifest, true, rollback); err != nil {
		return err
	}
	if err := runtime.verifyExistingSchemaLifecycle(ctx, manifest); err != nil {
		return err
	}
	loaded, err = runtime.loadedExistingSchemaUnit(ctx)
	if err != nil {
		return err
	}
	return runtime.verifyMerchantStoreHeldStart(ctx, held, manifest, loaded)
}

// This is a held-owner startup hook, not a standalone permission token. The
// durable ACTIVE row continues blocking activation between this process's
// exit and ExecStart, even if the holder crashes in that interval. A normal
// post-confirmation restart needs a freshly reviewed holder binding; silently
// admitting it from a stale completed receipt would recreate the race.
// Both candidate and retained-writer starts use the workspace candidate checker,
// so an older installed provider need not understand the new held-start path.
// Keep that workspace and provider until rollback completes or a replacement
// portable startup baseline is confirmed.
func runProductionMerchantStoreStartCheck(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("production writer-start-check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("workspace", "", "manifest-owned workspace with an actual active durable holder")
	if flags.Parse(args) != nil || flags.NArg() != 0 || *path == "" {
		return ExitUsage
	}
	runtime := defaultProductionRuntime()
	if runtime.effectiveUID() != 0 {
		return ExitError
	}
	workspace, err := runtime.openWorkspace(*path)
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		err = runtime.merchantStoreWriterStartCheck(ctx, workspace)
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s production writer-start-check: %v\n", DeployProgramName, err)
		return ExitError
	}
	_, _ = fmt.Fprintln(stdout, "merchant_store_start=qualified")
	return ExitOK
}
