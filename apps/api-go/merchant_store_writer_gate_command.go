package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/internal/appcli"
	"github.com/LIghtJUNction/api.lmm.best/model"
)

func runMerchantStoreWriterGateCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return appcli.ExitUsage
	}
	set := flag.NewFlagSet("merchant-store-writer-gate "+args[0], flag.ContinueOnError)
	set.SetOutput(stderr)
	requireWritable := set.Bool("require-writable", false, "status: fail when this binary cannot create new shop writes")
	expected := set.Int("expected-current", 0, "activate: exact current required writer capability")
	ready := set.Bool("reviewed-variants-ready", false, "activate: operator confirms reviewed schema and all serving writers are ready")
	lifecycleReady := set.Bool("reviewed-lifecycle-ready", false, "activate-lifecycle: operator confirms every serving writer supports retained product retirement")
	if err := set.Parse(args[1:]); err != nil || set.NArg() != 0 {
		return appcli.ExitUsage
	}
	if args[0] == "activate" && (!*ready || *lifecycleReady || (*expected != 1 && *expected != 2) || *requireWritable) {
		return appcli.ExitUsage
	}
	if args[0] == "activate-lifecycle" && (!*lifecycleReady || *ready || (*expected != 2 && *expected != 3) || *requireWritable) {
		return appcli.ExitUsage
	}
	if args[0] != "activate" && args[0] != "activate-lifecycle" && (*expected != 0 || *ready || *lifecycleReady) {
		return appcli.ExitUsage
	}
	if args[0] == "bootstrap" && *requireWritable {
		return appcli.ExitUsage
	}
	if args[0] != "status" && args[0] != "bootstrap" && args[0] != "activate" && args[0] != "activate-lifecycle" {
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
