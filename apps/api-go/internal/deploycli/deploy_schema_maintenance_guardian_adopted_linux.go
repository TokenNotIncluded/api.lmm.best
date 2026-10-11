//go:build linux

package deploycli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
)

const schemaMaintenanceGuardianAdoptedFormat = "lmm-schema-maintenance-guardian-adopted-v1"

type schemaMaintenanceGuardianFileIdentity struct {
	Path   string `json:"path"`
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
}

// This record is made by the first native daemon, before accepting any RPC.
// A later recovery may compare it, but must never replace or synthesize it
// from the guardian that happens to be reachable at recovery time.
type productionSchemaMaintenanceGuardianAdopted struct {
	Format             string                                 `json:"format"`
	HostBinding        productionSchemaMaintenanceHostBinding `json:"host_binding"`
	DaemonGeneration   schemaMaintenanceRPCBinding            `json:"daemon_generation"`
	GuardianPID        int                                    `json:"guardian_pid"`
	GuardianStartTicks uint64                                 `json:"guardian_start_ticks"`
	BindingFile        schemaMaintenanceGuardianFileIdentity  `json:"binding_file"`
	GuardianSocket     schemaMaintenanceGuardianFileIdentity  `json:"guardian_socket"`
	Locks              []schemaMaintenanceGuardianLock        `json:"locks"`
}

func (runtime *productionRuntime) writeSchemaMaintenanceGuardianAdopted(host *productionSchemaMaintenanceHost, generation schemaMaintenanceRPCBinding) error {
	if runtime == nil || runtime.effectiveUID == nil || runtime.effectiveUID() != 0 || runtime.requiredOwnerUID != 0 || os.Geteuid() != 0 {
		return errors.New("schema guardian original adoption requires native root authority")
	}
	return runtime.writeSchemaMaintenanceGuardianAdoptedForPaths(host, generation, schemaMaintenanceNativeLocks())
}

// The original daemon's death and NEVER_CLAIMED authority are separate gates
// owned by the recovery entrypoint. This check grants neither permission. It
// only proves that an already re-adopted native lease is the original guardian
// generation and exact original file/lock set, using the original durable record.
func (runtime *productionRuntime) checkSchemaMaintenanceGuardianReadopted(host *productionSchemaMaintenanceHost, originalGeneration schemaMaintenanceRPCBinding) error {
	if runtime == nil || runtime.effectiveUID == nil || runtime.effectiveUID() != 0 || runtime.requiredOwnerUID != 0 || os.Geteuid() != 0 {
		return errors.New("schema guardian recovery comparison requires native root authority")
	}
	return runtime.checkSchemaMaintenanceGuardianReadoptedForPaths(host, originalGeneration, schemaMaintenanceNativeLocks())
}

func schemaMaintenanceGuardianIdentity(path string, info os.FileInfo) (schemaMaintenanceGuardianFileIdentity, error) {
	if info == nil {
		return schemaMaintenanceGuardianFileIdentity{}, errors.New("schema guardian original file identity is unavailable")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Ino == 0 {
		return schemaMaintenanceGuardianFileIdentity{}, errors.New("schema guardian original file identity is invalid")
	}
	return schemaMaintenanceGuardianFileIdentity{Path: path, Device: uint64(stat.Dev), Inode: stat.Ino}, nil
}

// Only these private helpers accept test lock paths/owners. Production entrypoints
// above require root and the three fixed native/systemd/frontend owner paths.
func (runtime *productionRuntime) schemaMaintenanceGuardianAdoptedSnapshot(host *productionSchemaMaintenanceHost, generation schemaMaintenanceRPCBinding, paths []string) (productionSchemaMaintenanceGuardianAdopted, error) {
	var profile productionSchemaMaintenanceGuardianAdopted
	if runtime == nil || host == nil || host.runtime != runtime || host.validate == nil || validateSchemaMaintenanceRPCBinding(generation) != nil || generation.PlanSHA256 != host.binding.PlanSHA256 || generation.TransitionID != host.binding.TransitionID || len(paths) != 3 {
		return profile, errors.New("schema guardian original adoption lacks its full native host/daemon authority")
	}
	lease, ok := host.lease.(*schemaMaintenanceHostLockLease)
	if !ok || lease == nil {
		return profile, errors.New("schema guardian original adoption requires an actual SCM_RIGHTS lease")
	}
	ctx := context.Background()
	if err := host.validate(ctx); err != nil {
		return profile, err
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.owner != runtime.requiredOwnerUID || lease.binding != host.binding || lease.pid <= 1 || lease.startTicks == 0 || lease.nativePath != paths[0] {
		return profile, errors.New("schema guardian original adoption differs from its full immutable host binding")
	}
	if err := lease.checkLocked(ctx); err != nil {
		return profile, err
	}
	for index, path := range paths {
		if lease.locks[index].Path != path || !lease.locks[index].Held || lease.locks[index].Inode == 0 {
			return profile, errors.New("schema guardian original adoption changed its ordered held owner locks")
		}
		for prior := 0; prior < index; prior++ {
			if path == paths[prior] || lease.locks[index].Device == lease.locks[prior].Device && lease.locks[index].Inode == lease.locks[prior].Inode {
				return profile, errors.New("schema guardian original adoption repeats an owner lock or inode")
			}
		}
	}
	bindingInfo, err := schemaMaintenanceCheckGuardianBinding(host.binding, lease.owner)
	if err != nil || !os.SameFile(bindingInfo, lease.bindingInfo) {
		return profile, errors.New("schema guardian original binding file changed before capture")
	}
	socketInfo, err := schemaMaintenanceCheckGuardianSocket(host.binding.GuardianSocket, lease.owner)
	if err != nil || !os.SameFile(socketInfo, lease.socketInfo) {
		return profile, errors.New("schema guardian original socket changed before capture")
	}
	profile = productionSchemaMaintenanceGuardianAdopted{
		Format: schemaMaintenanceGuardianAdoptedFormat, HostBinding: host.binding, DaemonGeneration: generation,
		GuardianPID: lease.pid, GuardianStartTicks: lease.startTicks,
		Locks: append([]schemaMaintenanceGuardianLock(nil), lease.locks...),
	}
	profile.BindingFile, err = schemaMaintenanceGuardianIdentity(host.binding.GuardianBindingPath, bindingInfo)
	if err != nil {
		return profile, err
	}
	profile.GuardianSocket, err = schemaMaintenanceGuardianIdentity(host.binding.GuardianSocket, socketInfo)
	if err != nil {
		return profile, err
	}
	// checkLocked re-reads peer credentials, process generation, binding/socket
	// and each SCM_RIGHTS FD's own held flock after collecting all references.
	if err := lease.checkLocked(ctx); err != nil {
		return profile, err
	}
	return profile, host.validate(ctx)
}

func (runtime *productionRuntime) schemaMaintenanceGuardianOriginalDaemonBytes(host *productionSchemaMaintenanceHost, generation schemaMaintenanceRPCBinding) error {
	if runtime == nil || host == nil || validateSchemaMaintenanceRPCBinding(generation) != nil {
		return errors.New("schema guardian original daemon generation is incomplete")
	}
	raw, err := runtime.readExistingSchemaSealedFile(filepath.Join(host.binding.Workspace, "state", "daemon-binding.json"), true)
	want, marshalErr := schemaMaintenanceRPCMarshal(generation)
	if err != nil || marshalErr != nil || !bytes.Equal(raw, append(want, '\n')) {
		return errors.New("schema guardian original daemon receipt differs from its exact original generation")
	}
	return nil
}

func (runtime *productionRuntime) writeSchemaMaintenanceGuardianAdoptedForPaths(host *productionSchemaMaintenanceHost, generation schemaMaintenanceRPCBinding, paths []string) error {
	if generation.DaemonPID != os.Getpid() || schemaMaintenanceRPCProcess(generation) != nil {
		return errors.New("schema guardian original adoption is not made by its actual native daemon generation")
	}
	if err := runtime.schemaMaintenanceGuardianOriginalDaemonBytes(host, generation); err != nil {
		return err
	}
	profile, err := runtime.schemaMaintenanceGuardianAdoptedSnapshot(host, generation, paths)
	if err != nil {
		return err
	}
	raw, err := schemaMaintenanceRPCMarshal(profile)
	if err != nil {
		return err
	}
	path := filepath.Join(host.binding.Workspace, "state", "guardian-adopted.json")
	if err := runtime.writeSchemaMaintenanceRawNewFile(path, append(raw, '\n')); err != nil {
		return errors.New("schema guardian original adoption record already exists or is uncertain; refusing to replace it")
	}
	if err := runtime.schemaMaintenanceGuardianOriginalDaemonBytes(host, generation); err != nil {
		return err
	}
	if schemaMaintenanceRPCProcess(generation) != nil {
		return errors.New("schema guardian original daemon generation changed while recording adoption")
	}
	// The just-written record is re-read and checked against a fresh native
	// capture; partial publication or any intervening drift leaves evidence.
	return runtime.checkSchemaMaintenanceGuardianReadoptedForPaths(host, generation, paths)
}

func (runtime *productionRuntime) checkSchemaMaintenanceGuardianReadoptedForPaths(host *productionSchemaMaintenanceHost, originalGeneration schemaMaintenanceRPCBinding, paths []string) error {
	if err := runtime.schemaMaintenanceGuardianOriginalDaemonBytes(host, originalGeneration); err != nil {
		return err
	}
	daemonPath := filepath.Join(host.binding.Workspace, "state", "daemon-binding.json")
	daemonInfo, err := os.Lstat(daemonPath)
	if err != nil {
		return errors.New("schema guardian original daemon receipt cannot be inspected")
	}
	path := filepath.Join(host.binding.Workspace, "state", "guardian-adopted.json")
	before, err := os.Lstat(path)
	if err != nil {
		return errors.New("schema guardian original durable adoption record cannot be inspected")
	}
	raw, err := runtime.readExistingSchemaSealedFile(path, true)
	if err != nil {
		return errors.New("schema guardian original durable adoption record is unavailable")
	}
	var original productionSchemaMaintenanceGuardianAdopted
	if len(raw) == 0 || raw[len(raw)-1] != '\n' || schemaMaintenanceRPCDecode(raw[:len(raw)-1], &original) != nil {
		return errors.New("schema guardian original adoption record is not exact canonical native evidence")
	}
	actual, err := runtime.schemaMaintenanceGuardianAdoptedSnapshot(host, originalGeneration, paths)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(original, actual) {
		return errors.New("schema guardian recovery differs from its original continuous guardian generation or exact held files")
	}
	after, err := runtime.readExistingSchemaSealedFile(path, true)
	afterInfo, statErr := os.Lstat(path)
	if err != nil || statErr != nil || !bytes.Equal(raw, after) || !schemaMaintenanceSameGuardianFile(before, afterInfo) {
		return errors.New("schema guardian original adoption record changed during recovery comparison")
	}
	if err := runtime.schemaMaintenanceGuardianOriginalDaemonBytes(host, originalGeneration); err != nil {
		return err
	}
	afterDaemonInfo, err := os.Lstat(daemonPath)
	if err != nil || !schemaMaintenanceSameGuardianFile(daemonInfo, afterDaemonInfo) {
		return errors.New("schema guardian original daemon receipt changed during recovery comparison")
	}
	return host.lease.Check(context.Background())
}
