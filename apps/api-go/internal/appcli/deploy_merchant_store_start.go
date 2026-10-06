package appcli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"
)

func (runtime *productionRuntime) merchantStoreWriterStartCheck(ctx context.Context, workspace productionWorkspace) error {
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
	if err := runtime.requestMerchantStoreFence(ctx, workspace, manifest, false); err != nil {
		return err
	}
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
	return runtime.requestMerchantStoreFence(ctx, workspace, manifest, false)
}

// This is a held-owner startup hook, not a standalone permission token. The
// durable ACTIVE row continues blocking activation between this process's
// exit and ExecStart, even if the holder crashes in that interval. A normal
// post-confirmation restart needs a freshly reviewed holder binding; silently
// admitting it from a stale completed receipt would recreate the race.
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
