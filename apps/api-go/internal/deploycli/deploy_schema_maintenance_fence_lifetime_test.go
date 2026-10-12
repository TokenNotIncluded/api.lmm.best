//go:build !windows

package deploycli

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSchemaMaintenanceFenceLifetimeSurvivesClaimRPCActualPostgres(t *testing.T) {
	// Reuse the owned no-TCP cluster, never a configured application DSN.
	f := newSchemaMaintenanceDeadPreWorkerPGFixture(t)
	if err := f.lease.Close(); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{"SQL_DSN": strings.Replace(f.adminDSN, "lmm_dead_pre_admin", "lmm_dead_pre_business", 1)}
	daemonContext, cancelDaemon := context.WithCancel(f.ctx)
	defer cancelDaemon()
	var lease *productionMerchantStoreFence
	var owner productionMerchantStoreFenceOwner
	var claimContext context.Context
	// Match RPCHandle: cancel the bounded request immediately after its claim
	// handler returns, before the next request observes or uses the same owner.
	claimRPC := func() error {
		var cancel context.CancelFunc
		claimContext, cancel = context.WithTimeout(daemonContext, time.Minute)
		defer cancel()
		var err error
		lease, err = openMerchantStoreDeploymentFenceForOwner(claimContext, daemonContext, values, f.contract, false)
		if err != nil {
			return err
		}
		owner = testSchemaMaintenanceOwner()
		owner.HolderPID, owner.BackendPID = os.Getpid(), lease.backendPID
		owner.SystemIdentifier, owner.Database, owner.DatabaseOID = f.contract.SystemIdentifier, f.contract.Database, f.contract.DatabaseOID
		owner.Schema, owner.SchemaOID, owner.Role = f.contract.Schema, f.contract.SchemaOID, f.contract.Role
		owner.ContractSHA256, owner.ProviderSHA256 = merchantStoreFenceContractSHA(f.contract), f.contract.Candidate.PayloadSHA256
		toolSHA, err := sha256File("/proc/self/exe")
		if err != nil {
			return err
		}
		return lease.claimOwner(claimContext, owner, toolSHA, nil)
	}
	if err := claimRPC(); err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if claimContext.Err() != context.Canceled || daemonContext.Err() != nil {
		t.Fatal("fixture did not cancel only the completed claim RPC")
	}
	physicalSession := lease.backendPID
	fence := &productionSchemaMaintenanceFence{lease: lease, localLease: &schemaMaintenanceTestLease{}, authority: productionSchemaMaintenanceAuthority{
		OwnerContext: daemonContext, CheckPlan: func(context.Context) error { return nil }, CheckStoppedOrigins: func(context.Context) error { return nil },
	}}
	for _, name := range []string{"first worker check", "next status or worker check"} {
		t.Run(name, func(t *testing.T) {
			requestContext, cancel := context.WithTimeout(daemonContext, time.Minute)
			defer cancel()
			if err := fence.Check(requestContext); err != nil {
				t.Fatalf("completed claim RPC invalidated its durable owner: %v", err)
			}
			if lease.backendPID != physicalSession || lease.ownerKey != merchantStoreFenceOwnerKey(owner) {
				t.Fatal("check replaced the original physical session or owner")
			}
		})
	}
	// A cancelled operation still fails; the fix does not detach RPC budgets.
	cancelledRequest, cancelRequest := context.WithCancel(daemonContext)
	cancelRequest()
	operationLease, err := openMerchantStoreDeploymentFenceForOwner(daemonContext, daemonContext, values, f.contract, false)
	if err != nil {
		t.Fatal(err)
	}
	defer operationLease.Close()
	if err := operationLease.Check(cancelledRequest, f.contract); err == nil {
		t.Fatal("cancelled worker request passed its physical PG check")
	}
	if other, err := openMerchantStoreDeploymentFenceForOwner(cancelledRequest, daemonContext, values, f.contract, false); err == nil {
		_ = other.Close()
		t.Fatal("cancelled claim request acquired a new physical session")
	}
	for _, ownerContext := range []context.Context{nil, cancelledRequest} {
		if other, err := openMerchantStoreDeploymentFenceForOwner(f.ctx, ownerContext, values, f.contract, false); err == nil {
			_ = other.Close()
			t.Fatal("missing or cancelled controller lifetime acquired a fence")
		}
	}
	// Ordinary/start callers retain the existing single-context semantics.
	ordinaryContext, cancelOrdinary := context.WithCancel(f.ctx)
	ordinary, err := openMerchantStoreDeploymentFence(ordinaryContext, values, f.contract, false)
	if err != nil {
		cancelOrdinary()
		t.Fatal(err)
	}
	defer ordinary.Close()
	cancelOrdinary()
	if err := ordinary.Check(f.ctx, f.contract); err == nil {
		t.Fatal("ordinary fence stopped following its own lifetime")
	}
	cancelDaemon()
	if err := fence.Check(f.ctx); err == nil {
		t.Fatal("cancelled daemon lifetime still authorized a worker")
	}
	admin := f.admin()
	defer admin.Close(context.Background())
	var actual string
	if err := admin.QueryRow(f.ctx, `SELECT value FROM dead_pre.options WHERE key=$1`, lease.ownerKey).Scan(&actual); err != nil || actual != lease.ownerValue {
		t.Fatalf("lifetime cancellation deleted or changed the durable ACTIVE owner: %v", err)
	}
}
