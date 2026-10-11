package deploycli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSchemaMaintenanceWorkerRefusesForeignOrMissingSameProcessOwner(t *testing.T) {
	runtime := &productionRuntime{}
	for _, fence := range []*productionSchemaMaintenanceFence{nil, {runtime: &productionRuntime{}}} {
		if _, err := runtime.runSchemaMaintenanceWorker(context.Background(), fence); err == nil {
			t.Fatal("worker accepted no live same-process owner")
		}
	}
}

func TestSchemaMaintenanceWorkerResultRequiresExactOriginalIntent(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	runtime := &productionRuntime{requiredOwnerUID: uint32(os.Geteuid())}
	owner := testSchemaMaintenanceOwner()
	ownerBytes, err := canonicalMerchantStoreFenceOwner(owner)
	if err != nil {
		t.Fatal(err)
	}
	ownerSHA := startupContentSHA256(ownerBytes)
	started := time.Now().UTC()
	output := []byte("PostgreSQL schema-only maintenance transaction committed; application data migrations were not run\n")
	result := productionSchemaMaintenanceWorkerResult{Format: "lmm-schema-maintenance-worker-v1", TransitionID: owner.DeploymentID, PlanSHA256: owner.PlanSHA256, OwnerSHA256: ownerSHA, ProviderSHA256: owner.ProviderSHA256, Phase: "WORKER_SUCCEEDED", OutputSHA256: startupContentSHA256(output), StartedUTC: started, FinishedUTC: started.Add(time.Second)}
	write := func(name string, value productionSchemaMaintenanceWorkerResult) {
		t.Helper()
		raw, _ := json.MarshalIndent(value, "", "  ")
		if err := os.WriteFile(filepath.Join(root, "state", name), append(raw, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("schema-worker-result.json", result)
	if _, err := runtime.readSchemaMaintenanceWorkerResult(root, owner, ownerSHA); err == nil {
		t.Fatal("success receipt without original dispatch was accepted")
	}
	intent := result
	intent.Phase, intent.OutputSHA256, intent.FinishedUTC = "WORKER_INTENT", "", time.Time{}
	write("schema-worker-intent.json", intent)
	if err := os.WriteFile(filepath.Join(root, "state", "schema-worker-output.log"), output, 0600); err != nil {
		t.Fatal(err)
	}
	if actual, err := runtime.readSchemaMaintenanceWorkerResult(root, owner, ownerSHA); err != nil || actual != result {
		t.Fatalf("exact result/intent rejected: %v", err)
	}
	for name, mutate := range map[string]func(*productionSchemaMaintenanceWorkerResult){
		"different invocation": func(r *productionSchemaMaintenanceWorkerResult) { r.StartedUTC = r.StartedUTC.Add(time.Second) },
		"different owner":      func(r *productionSchemaMaintenanceWorkerResult) { r.OwnerSHA256 = owner.PlanSHA256 },
		"uncertain worker":     func(r *productionSchemaMaintenanceWorkerResult) { r.Phase = "WORKER_RESULT_UNCERTAIN" },
		"backwards time":       func(r *productionSchemaMaintenanceWorkerResult) { r.FinishedUTC = r.StartedUTC.Add(-time.Second) },
	} {
		t.Run(name, func(t *testing.T) {
			changed := result
			mutate(&changed)
			write("schema-worker-result.json", changed)
			if _, err := runtime.readSchemaMaintenanceWorkerResult(root, owner, ownerSHA); err == nil {
				t.Fatal("changed result accepted")
			}
		})
	}
	write("schema-worker-result.json", result)
	intent.ProviderSHA256 = owner.PlanSHA256
	write("schema-worker-intent.json", intent)
	if _, err := runtime.readSchemaMaintenanceWorkerResult(root, owner, ownerSHA); err == nil {
		t.Fatal("result accepted unrelated intent")
	}
}
