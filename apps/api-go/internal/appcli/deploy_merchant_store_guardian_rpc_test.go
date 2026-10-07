package appcli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

type merchantStoreHolderUnitFixture struct {
	owner             productionMerchantStoreFenceOwner
	changedInvocation bool
}

func (fixture *merchantStoreHolderUnitFixture) Run(_ context.Context, command productionCommand) ([]byte, error) {
	if command.Name != commandSystemctl || len(command.Args) < 3 || command.Args[0] != "show" || command.Args[1] != fixture.owner.HolderUnit {
		return nil, errors.New("unexpected holder fixture command; no systemd mutation is implemented")
	}
	invocation := fixture.owner.HolderInvocationID
	if fixture.changedInvocation {
		invocation = "00000000000000000000000000000000"
	}
	return []byte(fmt.Sprintf("MainPID=%d\nExecMainPID=%d\nExecMainCode=0\nExecMainStatus=0\nActiveState=active\nSubState=running\nResult=success\nControlGroup=/test-holder\nRestart=no\nInvocationID=%s\n", fixture.owner.HolderPID, fixture.owner.HolderPID, invocation)), nil
}

// The unit inventory is a component fixture. Socket peer credentials, actual
// executable hash, dedicated PG session/lock, and durable Options row below are
// real local resources; this is not an installed systemd-unit qualification.
func testMerchantStoreFenceLiveRPC(t *testing.T, ctx context.Context, lease *productionMerchantStoreFence, owner productionMerchantStoreFenceOwner, contract *productionMerchantStoreWriterContract) {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "lmm-fence-rpc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	state := filepath.Join(directory, "state")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	workspace := productionWorkspace{root: directory, id: owner.DeploymentID, stateDir: state, manifestPath: filepath.Join(state, productionManifestFilename), statusPath: filepath.Join(state, productionStatusFilename)}
	manifest := productionManifest{Go: productionPackageTransition{Changed: true}, SchemaPlanSHA256: owner.PlanSHA256, MerchantStoreWriter: contract}
	unit := &merchantStoreHolderUnitFixture{owner: owner}
	runtime := &productionRuntime{runner: unit, requiredOwnerUID: uint32(os.Getuid()), merchantStoreSocketDirectory: directory, hostname: func() (string, error) { return owner.Host, nil }}
	ownerPath := merchantStoreFenceOwnerPath(workspace)
	socketPath, err := runtime.merchantStoreSocketPath(workspace.root, owner, true)
	if err != nil {
		t.Fatal(err)
	}
	canonical, _ := canonicalMerchantStoreFenceOwner(owner)
	if err := os.WriteFile(ownerPath, canonical, 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := os.Chmod(socketPath, 0600); err != nil {
		t.Fatal(err)
	}
	serveOnce := func() <-chan bool {
		result := make(chan bool, 1)
		go func() {
			connection, err := listener.AcceptUnix()
			if err != nil {
				result <- false
				return
			}
			result <- runtime.handleMerchantStoreFenceConnection(ctx, workspace, manifest, owner, canonical, lease, connection)
		}()
		return result
	}
	result := serveOnce()
	if err := runtime.requestMerchantStoreFence(ctx, workspace, manifest, false); err != nil {
		t.Fatal(err)
	}
	if <-result {
		t.Fatal("check RPC released ACTIVE owner")
	}
	if err := lease.CheckOwner(ctx); err != nil {
		t.Fatal(err)
	}
	// A copied private receipt cannot bless a later systemd invocation.
	unit.changedInvocation = true
	if err := runtime.requestMerchantStoreFence(ctx, workspace, manifest, false); err == nil {
		t.Fatal("stale holder invocation accepted")
	}
	unit.changedInvocation = false
	// The same live owner still cannot be cleared merely by requesting release.
	// There is no terminal manifest/status/health evidence in this fixture.
	result = serveOnce()
	if err := runtime.requestMerchantStoreFence(ctx, workspace, manifest, true); err == nil {
		t.Fatal("RPC released nonterminal owner")
	}
	if <-result {
		t.Fatal("nonterminal guardian returned released")
	}
	if err := lease.CheckOwner(ctx); err != nil {
		t.Fatal("nonterminal RPC changed durable owner")
	}
	connection, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if err := merchantStoreFencePeer(connection, uint32(os.Getuid()), os.Getpid()+1); err == nil {
		t.Fatal("wrong SO_PEERCRED PID accepted: " + strconv.Itoa(os.Getpid()))
	}
}
