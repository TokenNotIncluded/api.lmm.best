package deploycli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/internal/deploymentfence"
)

func schemaMaintenanceHolderUnit(id string) string {
	return "lmm-schema-maintenance-" + id + ".service"
}

// This type is deliberately separate from an ordinary capsule. The artifact
// capsule only authenticates the signed binaries and scripts; its no-DDL policy
// is never used as permission to migrate. The dedicated controller constructs
// this authority after validating its immutable maintenance plan and observing
// the fixed two origins. A caller boolean is not a closure or recovery proof.
type productionSchemaMaintenanceAuthority struct {
	OwnerContext         context.Context
	TransitionID         string
	PlanSHA256           string
	Workspace            string
	Artifacts            productionMerchantStoreCapsule
	OldCapsule           productionMerchantStoreCapsule
	OldCapsuleSHA256     string
	CheckStoppedOrigins  func(context.Context) error
	CheckRecoveryOrigins func(context.Context) error
	CheckPlan            func(context.Context) error
	CheckBackup          func(context.Context) error
}

type productionSchemaMaintenanceFence struct {
	runtime    *productionRuntime
	authority  productionSchemaMaintenanceAuthority
	lease      *productionMerchantStoreFence
	owner      productionMerchantStoreFenceOwner
	ownerRaw   []byte
	localLease productionSchemaMaintenanceHostLease
	released   bool
}

func (runtime *productionRuntime) claimSchemaMaintenanceFence(ctx context.Context, a productionSchemaMaintenanceAuthority, localLease productionSchemaMaintenanceHostLease) (*productionSchemaMaintenanceFence, error) {
	if err := runtime.assertProductionMutation(); err != nil {
		return nil, err
	}
	if !productionIDPattern.MatchString(a.TransitionID) || !productionSHA256Pattern.MatchString(a.PlanSHA256) ||
		a.OwnerContext == nil || a.OwnerContext.Err() != nil ||
		a.Workspace != schemaMaintenanceHostWorkspace(runtime.paths, a.TransitionID) || localLease == nil ||
		a.CheckPlan == nil || a.CheckBackup == nil || a.CheckStoppedOrigins == nil || a.CheckRecoveryOrigins == nil ||
		a.Artifacts.Format != 1 || a.Artifacts.StartupPolicy != "held" || a.OldCapsule.Format != 1 || a.OldCapsule.StartupPolicy != "per-start" {
		return nil, errors.New("schema maintenance lacks its separate immutable controller authority")
	}
	if err := validateMerchantStoreCapsule(a.Artifacts, runtime.paths); err != nil {
		return nil, err
	}
	if err := validateMerchantStoreCapsule(a.OldCapsule, runtime.paths); err != nil {
		return nil, err
	}
	loadedOld, err := runtime.loadMerchantStoreCapsule(filepath.Join(a.OldCapsule.Root, "capsule.json"), a.OldCapsuleSHA256)
	if err != nil {
		return nil, err
	}
	oldRaw, _ := canonicalMerchantStoreCapsule(a.OldCapsule)
	loadedRaw, _ := canonicalMerchantStoreCapsule(loadedOld)
	if !bytes.Equal(oldRaw, loadedRaw) {
		return nil, errors.New("schema maintenance retained capsule differs from its exact sealed file")
	}
	if a.Artifacts.DeploymentID != a.TransitionID || a.Artifacts.Host != a.OldCapsule.Host || a.Artifacts.Rollback.PayloadSHA256 != a.OldCapsule.Candidate.PayloadSHA256 || a.Artifacts.Writer.RequiredCapability > 7 {
		return nil, errors.New("schema maintenance changed its actual N-1 provider or supported recovery floor")
	}
	pre, old := *a.Artifacts.ExistingSchemaContract, *a.OldCapsule.ExistingSchemaContract
	pre.StartupSHA256, old.StartupSHA256 = "", ""
	if pre != old {
		return nil, errors.New("schema maintenance signed artifact plan does not bind the original physical catalog")
	}
	for _, check := range []func(context.Context) error{a.CheckPlan, localLease.Check, a.CheckStoppedOrigins, a.CheckBackup} {
		if err := check(ctx); err != nil {
			return nil, err
		}
	}
	for _, rollback := range []bool{false, true} {
		if _, err := runtime.verifyMerchantStoreCapsuleArtifact(ctx, a.Artifacts, rollback); err != nil {
			return nil, err
		}
	}
	// Qualify current startup/configuration with the retained capsule while the
	// pre-DDL catalog is still exact. The current installed engine may be N-1;
	// this dedicated holder itself must be the authenticated new engine.
	child, err := runtime.merchantStoreCapsuleEnvironment(ctx, a.OldCapsule, a.OldCapsuleSHA256, "")
	if err != nil {
		return nil, err
	}
	if err := runtime.verifyMerchantStoreCapsuleSchema(ctx, a.OldCapsule, child); err != nil {
		return nil, err
	}
	values := map[string]string{}
	for _, row := range child {
		for i := range row {
			if row[i] == '=' {
				values[row[:i]] = row[i+1:]
				break
			}
		}
	}
	// The shared physical fence prevents activation. The sole durable typed
	// owner blocks ordinary/start claims, while the worker serializes owner
	// changes with ...0003 before taking ...0001 in its own DDL transaction.
	lease, err := openMerchantStoreDeploymentFenceForOwner(ctx, a.OwnerContext, values, a.Artifacts.Writer, false)
	if err != nil {
		return nil, err
	}
	failed := true
	defer func() {
		if failed {
			_ = lease.Close()
		}
	}()
	if err := lease.requireNoDurableOwner(ctx); err != nil {
		return nil, err
	}
	if err := lease.checkSchemaMaintenanceRecoveryBusiness(ctx); err != nil {
		return nil, err
	}
	invocation := os.Getenv("INVOCATION_ID")
	unitName := schemaMaintenanceHolderUnit(a.TransitionID)
	unit, err := runtime.billingUnitState(ctx, unitName)
	if err != nil || !existingSchemaInvocationPattern.MatchString(invocation) || unit["MainPID"] != strconv.Itoa(os.Getpid()) ||
		unit["ExecMainPID"] != unit["MainPID"] || unit["InvocationID"] != invocation || unit["Restart"] != "no" ||
		unit["ActiveState"] != "active" || unit["SubState"] != "running" ||
		sha256MustEqual("/proc/self/exe", a.Artifacts.deploymentSHA256()) != nil {
		return nil, errors.New("schema maintenance holder is not its actual signed systemd process")
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	w := a.Artifacts.Writer
	owner := productionMerchantStoreFenceOwner{Format: 1, State: "ACTIVE", DeploymentID: a.TransitionID, Host: a.Artifacts.Host,
		PlanSHA256: a.PlanSHA256, ContractSHA256: merchantStoreFenceContractSHA(w), ProviderSHA256: a.Artifacts.Candidate.PayloadSHA256,
		Nonce: hex.EncodeToString(nonce), HolderPID: os.Getpid(), HolderUnit: unitName, HolderInvocationID: invocation,
		BackendPID: lease.backendPID, SystemIdentifier: w.SystemIdentifier, Database: w.Database, DatabaseOID: w.DatabaseOID,
		Schema: w.Schema, SchemaOID: w.SchemaOID, Role: w.Role, Purpose: "schema-maintenance", Service: runtime.paths.Service}
	raw, err := canonicalMerchantStoreFenceOwner(owner)
	if err != nil {
		return nil, err
	}
	if _, err := deploymentfence.ParseSchemaMaintenanceOwner(raw); err != nil {
		return nil, errors.New("schema maintenance owner is not accepted by the exact native worker wire contract")
	}
	if err := lease.claimOwner(ctx, owner, a.Artifacts.deploymentSHA256(), func(ctx context.Context) error {
		if err := localLease.Check(ctx); err != nil {
			return err
		}
		if err := a.CheckPlan(ctx); err != nil {
			return err
		}
		return a.CheckStoppedOrigins(ctx)
	}); err != nil {
		return nil, err
	}
	path := filepath.Join(a.Workspace, "state", "schema-owner.json")
	if err := runtime.writeSchemaMaintenanceRawNewFile(path, raw); err != nil {
		return nil, errors.New("schema maintenance owner receipt failed; exact ACTIVE owner remains")
	}
	failed = false
	return &productionSchemaMaintenanceFence{runtime: runtime, authority: a, lease: lease, owner: owner, ownerRaw: raw, localLease: localLease}, nil
}

// CheckPhysical is the heartbeat safe while the worker owns its DDL TX. It
// uses only the original PG connection and never launches a third client.
func (f *productionSchemaMaintenanceFence) CheckPhysical(ctx context.Context) error {
	if f == nil || f.lease == nil || f.localLease == nil || f.released {
		return errors.New("schema maintenance physical owner is unavailable")
	}
	if err := f.authority.CheckPlan(ctx); err != nil {
		return err
	}
	if err := f.localLease.Check(ctx); err != nil {
		return err
	}
	return f.lease.CheckOwner(ctx)
}

func (f *productionSchemaMaintenanceFence) Check(ctx context.Context) error {
	if err := f.CheckPhysical(ctx); err != nil {
		return err
	}
	return f.authority.CheckStoppedOrigins(ctx)
}

// Release is allowed only with both origins still stopped/closed and actual
// new recovery capsules qualified for the resulting catalog. It never accepts
// an ordinary transaction terminal phase, an expiry or a caller's approval.
func (f *productionSchemaMaintenanceFence) Release(ctx context.Context) error {
	if err := f.CheckPhysical(ctx); err != nil {
		return err
	}
	// The recovery callback itself freshly proves both stopped origins, their
	// closed admission and new loaded post-catalog capsules. Repeating the same
	// full signature/provider/database qualification via CheckStoppedOrigins
	// would add no authority while substantially extending the closed window.
	if err := f.authority.CheckRecoveryOrigins(ctx); err != nil {
		return err
	}
	if err := f.lease.checkSchemaMaintenanceRecoveryBusiness(ctx); err != nil {
		return err
	}
	if err := f.CheckPhysical(ctx); err != nil {
		return err
	}
	if err := f.lease.ReleaseOwner(ctx); err != nil {
		return errors.New("schema maintenance exact CAS result is uncertain; reconcile without retry")
	}
	f.released = true
	completion := struct {
		Format       string    `json:"format"`
		TransitionID string    `json:"transition_id"`
		PlanSHA256   string    `json:"plan_sha256"`
		OwnerSHA256  string    `json:"owner_sha256"`
		FinishedUTC  time.Time `json:"finished_utc"`
	}{"lmm-schema-maintenance-owner-released-v1", f.authority.TransitionID, f.authority.PlanSHA256, startupContentSHA256(f.ownerRaw), f.runtime.now().UTC()}
	raw, err := json.MarshalIndent(completion, "", "  ")
	if err != nil {
		return errors.New("schema owner CAS completed but local receipt encoding failed")
	}
	if err := f.runtime.writeSchemaMaintenanceRawNewFile(filepath.Join(f.authority.Workspace, "state", "owner-released.json"), append(raw, '\n')); err != nil {
		return errors.New("schema owner CAS completed but local receipt failed; reconcile exact physical state")
	}
	return f.lease.Close()
}

// A process failure closes only this physical session. The original durable
// ACTIVE value is retained permanently until an explicit recovery authority
// proves the same plan and schema outcome; Close never deletes an owner.
func (f *productionSchemaMaintenanceFence) Close() error {
	if f == nil || f.lease == nil {
		return nil
	}
	return f.lease.Close()
}

// Preserve the exact compact owner bytes stored by the same-session CAS. An
// indented JSON receipt is not interchangeable with the reserved database row.
func (runtime *productionRuntime) writeSchemaMaintenanceRawNewFile(path string, raw []byte) error {
	if err := runtime.merchantStorePrivateDirectory(filepath.Dir(path), false); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = file.Write(raw)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
