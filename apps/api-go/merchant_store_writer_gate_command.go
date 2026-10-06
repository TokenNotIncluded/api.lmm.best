package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/internal/appcli"
	"github.com/LIghtJUNction/api.lmm.best/model"
)

func runMerchantStoreWriterGateCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return appcli.ExitUsage
	}
	// Publication reads the compiled source capability without opening a
	// database, starting resources, or treating it as a writable-floor proof.
	if args[0] == "capability" {
		if len(args) != 1 {
			return appcli.ExitUsage
		}
		if _, err := fmt.Fprintf(stdout, "%d\n", model.MerchantStoreWriterCapability); err != nil {
			return appcli.ExitError
		}
		return appcli.ExitOK
	}
	set := flag.NewFlagSet("merchant-store-writer-gate "+args[0], flag.ContinueOnError)
	set.SetOutput(stderr)
	requireWritable := set.Bool("require-writable", false, "status: fail when this binary cannot create new shop writes")
	expected := set.Int("expected-current", 0, "activate: exact current required writer capability")
	ready := set.Bool("reviewed-variants-ready", false, "activate: operator confirms reviewed schema and all serving writers are ready")
	lifecycleReady := set.Bool("reviewed-lifecycle-ready", false, "activate-lifecycle: operator confirms every serving writer supports retained product retirement")
	refundsReady := set.Bool("reviewed-refunds-ready", false, "activate-refunds: operator confirms complete refund, provider, promotion and limit schema and all serving writers support terminal refund acknowledgments")
	schemaReady := set.Bool("reviewed-store-schema-ready", false, "prepare-schema: operator confirms reviewed shop-only DDL and clone preservation proof")
	accessReady := set.Bool("reviewed-access-ready", false, "activate-access: operator confirms full access/catalogue/guest-email schema and all serving and retained writers support capability five")
	if err := set.Parse(args[1:]); err != nil || set.NArg() != 0 {
		return appcli.ExitUsage
	}
	if args[0] == "activate" && (!*ready || *lifecycleReady || *refundsReady || *schemaReady || *accessReady || (*expected != 1 && *expected != 2) || *requireWritable) {
		return appcli.ExitUsage
	}
	if args[0] == "activate-lifecycle" && (!*lifecycleReady || *ready || *refundsReady || *schemaReady || *accessReady || (*expected != 2 && *expected != 3) || *requireWritable) {
		return appcli.ExitUsage
	}
	if args[0] == "activate-refunds" && (!*refundsReady || *ready || *lifecycleReady || *schemaReady || *accessReady || (*expected != 3 && *expected != 4) || *requireWritable) {
		return appcli.ExitUsage
	}
	if args[0] == "prepare-schema" && (!*schemaReady || *accessReady || *ready || *lifecycleReady || *refundsReady || *expected < 1 || *expected > model.MerchantStoreWriterCapability || *requireWritable) {
		return appcli.ExitUsage
	}
	if args[0] == "activate-access" && (!*accessReady || *schemaReady || *ready || *lifecycleReady || *refundsReady || (*expected != 4 && *expected != 5) || *requireWritable) {
		return appcli.ExitUsage
	}
	if args[0] != "activate" && args[0] != "activate-lifecycle" && args[0] != "activate-refunds" && args[0] != "prepare-schema" && args[0] != "activate-access" && (*expected != 0 || *ready || *lifecycleReady || *refundsReady || *schemaReady || *accessReady) {
		return appcli.ExitUsage
	}
	if args[0] == "bootstrap" && *requireWritable {
		return appcli.ExitUsage
	}
	if args[0] != "status" && args[0] != "bootstrap" && args[0] != "activate" && args[0] != "activate-lifecycle" && args[0] != "activate-refunds" && args[0] != "prepare-schema" && args[0] != "activate-access" {
		return appcli.ExitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := model.OpenMerchantStoreWriterGateDatabase(ctx)
	if err != nil {
		return appcli.ExitError
	}
	pool, err := db.DB()
	if err != nil {
		return appcli.ExitError
	}
	defer pool.Close()
	switch args[0] {
	case "bootstrap":
		err = model.BootstrapMerchantStoreWriterGate(db)
	case "activate":
		err = model.ActivateMerchantStoreVariants(db, *expected)
	case "activate-lifecycle":
		err = model.ActivateMerchantStoreProductLifecycle(db, *expected)
	case "activate-refunds":
		err = model.ActivateMerchantStoreRefunds(db, *expected)
	case "prepare-schema":
		err = model.PrepareMerchantStoreSchema(db, *expected)
	case "activate-access":
		err = model.ActivateMerchantStoreAccess(db, *expected)
	}
	if err != nil {
		return appcli.ExitError
	}
	status, err := model.GetMerchantStoreWriterGateStatus(db)
	if err := json.NewEncoder(stdout).Encode(status); err != nil {
		return appcli.ExitError
	}
	if err != nil {
		return appcli.ExitError
	}
	if *requireWritable && !status.NewWritesAllowed {
		return appcli.ExitError
	}
	return appcli.ExitOK
}
