//go:build linux

package appcli

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testMerchantSocketRuntime(t *testing.T) *productionRuntime {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "lmf-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return &productionRuntime{requiredOwnerUID: uint32(os.Getuid()), merchantStoreSocketDirectory: directory}
}

func testMerchantSocketOwner() productionMerchantStoreFenceOwner {
	id, inv := "go90-lab-firstconversion-20261007", strings.Repeat("a", 32)
	return productionMerchantStoreFenceOwner{Format: 1, State: "ACTIVE", DeploymentID: id, Host: "arch-dmit", PlanSHA256: strings.Repeat("b", 64), ContractSHA256: strings.Repeat("c", 64), ProviderSHA256: strings.Repeat("d", 64), Nonce: strings.Repeat("e", 32), HolderPID: os.Getpid(), HolderUnit: merchantStoreStartUnit(id, inv), HolderInvocationID: strings.Repeat("f", 32), BackendPID: 100, SystemIdentifier: "7648633982160478129", Database: "lmm_api", DatabaseOID: 1, Schema: "lmm_prod", SchemaOID: 2, Role: "lmm_api", Purpose: "start", Service: "lmm-api.service", StartInvocationID: inv}
}

func TestMerchantStoreShortSocketLongCapsuleRealUnix(t *testing.T) {
	runtime := testMerchantSocketRuntime(t)
	owner := testMerchantSocketOwner()
	root := "/var/lib/lmm-api-go-deploy/merchant-capsules/" + owner.DeploymentID
	c := productionMerchantStoreCapsule{Root: root}
	ownerPath := merchantStorePortableOwnerPath(c, owner.StartInvocationID)
	if ownerPath != filepath.Join(root, "state", "start-"+owner.StartInvocationID, "portable-owner.json") {
		t.Fatal("persistent owner path moved")
	}
	if len(filepath.Join(filepath.Dir(ownerPath), "portable-fence.sock")) < 108 {
		t.Fatal("fixture does not reproduce Linux path limit")
	}
	// An arbitrarily long complete authority root stays in the hash, not sun_path.
	path, err := runtime.merchantStoreSocketPath(root+strings.Repeat("x", 400), owner, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(path) >= 108 {
		t.Fatal("short socket exceeds Linux limit")
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	defer listener.Close()
	if err = os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	uid, _, ok := deploymentFileOwnership(info)
	if !ok || uid != uint32(os.Getuid()) || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0600 {
		t.Fatal("socket ownership/type/mode differs")
	}
	checked := make(chan error, 1)
	go func() {
		conn, e := listener.AcceptUnix()
		if e != nil {
			checked <- e
			return
		}
		defer conn.Close()
		checked <- merchantStoreFencePeer(conn, uint32(os.Getuid()), os.Getpid())
	}()
	conn, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err = merchantStoreFencePeer(conn, uint32(os.Getuid()), os.Getpid()); err != nil {
		t.Fatal(err)
	}
	if err = merchantStoreFencePeer(conn, uint32(os.Getuid()), os.Getpid()+1); err == nil {
		t.Fatal("wrong sealed PID accepted")
	}
	_ = conn.Close()
	if err = <-checked; err != nil {
		t.Fatal(err)
	}
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Lstat(path); err != nil {
		t.Fatal("failed/closed holder evidence removed")
	}
	// Neither closed sockets nor missing /run transport create recovery authority.
	if duplicate, e := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"}); e == nil {
		duplicate.Close()
		t.Fatal("existing socket replaced")
	}
	if err = os.RemoveAll(runtime.merchantStoreSocketDirectory); err != nil {
		t.Fatal(err)
	}
	if _, err = runtime.merchantStoreSocketPath(root, owner, false); err == nil {
		t.Fatal("request recreated missing runtime state after reboot")
	}
}

func TestMerchantStoreShortSocketAuthorityIsolation(t *testing.T) {
	runtime := testMerchantSocketRuntime(t)
	base := testMerchantSocketOwner()
	root := "/long/capsule/" + base.DeploymentID
	original, err := runtime.merchantStoreSocketPath(root, base, true)
	if err != nil {
		t.Fatal(err)
	}
	same, err := runtime.merchantStoreSocketPath(root, base, false)
	if err != nil || same != original {
		t.Fatal("canonical owner path is not deterministic")
	}
	variants := []struct {
		name   string
		change func(*productionMerchantStoreFenceOwner)
		root   string
	}{
		{"ID", func(o *productionMerchantStoreFenceOwner) {
			o.DeploymentID = "other-release"
			o.HolderUnit = merchantStoreStartUnit(o.DeploymentID, o.StartInvocationID)
		}, root},
		{"start invocation", func(o *productionMerchantStoreFenceOwner) {
			o.StartInvocationID = strings.Repeat("1", 32)
			o.HolderUnit = merchantStoreStartUnit(o.DeploymentID, o.StartInvocationID)
		}, root},
		{"holder invocation", func(o *productionMerchantStoreFenceOwner) { o.HolderInvocationID = strings.Repeat("2", 32) }, root},
		{"authority", func(o *productionMerchantStoreFenceOwner) { o.PlanSHA256 = strings.Repeat("3", 64) }, root},
		{"host", func(o *productionMerchantStoreFenceOwner) { o.Host = "dmit-ubuntu" }, root},
		{"root", func(*productionMerchantStoreFenceOwner) {}, "/another/capsule/" + base.DeploymentID},
		{"ordinary guardian", func(o *productionMerchantStoreFenceOwner) {
			o.Purpose = ""
			o.Service = ""
			o.StartInvocationID = ""
			o.HolderUnit = merchantStoreFenceUnit(o.DeploymentID)
		}, root},
	}
	seen := map[string]bool{original: true}
	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			o := base
			v.change(&o)
			path, e := runtime.merchantStoreSocketPath(v.root, o, false)
			if e != nil {
				t.Fatal(e)
			}
			if seen[path] {
				t.Fatal("different authority/generation shares socket")
			}
			seen[path] = true
		})
	}
}

func TestMerchantStoreSocketDirectoryConcurrentAndUnsafe(t *testing.T) {
	runtime := testMerchantSocketRuntime(t)
	parent := runtime.merchantStoreSocketDirectory
	directory := filepath.Join(parent, "shared")
	const count = 16
	start := make(chan struct{})
	errors := make(chan error, count)
	var group sync.WaitGroup
	for i := 0; i < count; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			errors <- verifyMerchantStoreSocketDirectory(directory, uint32(os.Getuid()), true)
		}()
	}
	close(start)
	group.Wait()
	close(errors)
	for e := range errors {
		if e != nil {
			t.Fatal(e)
		}
	}
	info, e := os.Lstat(directory)
	if e != nil || info.Mode().Perm() != 0700 {
		t.Fatal("shared directory mode changed")
	}
	if e = verifyMerchantStoreSocketDirectory(directory, uint32(os.Getuid())+1, false); e == nil {
		t.Fatal("wrong UID accepted")
	}
	if e = os.Chmod(directory, 0770); e != nil {
		t.Fatal(e)
	}
	if e = verifyMerchantStoreSocketDirectory(directory, uint32(os.Getuid()), true); e == nil {
		t.Fatal("existing writable directory accepted or repaired")
	}
	if e = os.Remove(directory); e != nil {
		t.Fatal(e)
	}
	target := filepath.Join(parent, "target")
	if e = os.Mkdir(target, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink(target, directory); e != nil {
		t.Fatal(e)
	}
	if e = verifyMerchantStoreSocketDirectory(directory, uint32(os.Getuid()), true); e == nil {
		t.Fatal("existing symlink accepted")
	}
	if e = verifyMerchantStoreSocketDirectory(filepath.Join(directory, "child"), uint32(os.Getuid()), true); e == nil {
		t.Fatal("symlink ancestor accepted")
	}
	if _, e = os.Lstat(filepath.Join(target, "child")); !os.IsNotExist(e) {
		t.Fatal("symlink target mutated")
	}
}
