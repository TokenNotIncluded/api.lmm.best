//go:build linux

package appcli

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type portableReadinessRunner struct {
	merchantStoreHolderUnitFixture
	starts int
}

func (r *portableReadinessRunner) Run(ctx context.Context, command productionCommand) ([]byte, error) {
	if command.Name == "/usr/bin/systemd-run" {
		r.starts++
		return nil, nil // No systemd unit is created by this component fixture.
	}
	return r.merchantStoreHolderUnitFixture.Run(ctx, command)
}

// The clock and unit inventory are fixtures. The executable copy and Unix peer
// are real local resources; no signature, PostgreSQL or production proof is claimed.
func TestProductionMerchantStorePortableOrdinaryHolderCanBecomeReadyAfter60Seconds(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "lmm-portable-ready-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	providerDir := filepath.Join(root, "tmp", "migrations", "merchant-store-candidate")
	for _, directory := range []string{providerDir, filepath.Join(root, "state")} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	elf, err := os.ReadFile("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(providerDir, backendGoName), elf, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(backendGoName, filepath.Join(providerDir, productionCandidateLinkName)); err != nil {
		t.Fatal(err)
	}
	writer := testMerchantWriterContract()
	writer.Candidate.PayloadSHA256 = startupContentSHA256(elf)
	c := productionMerchantStoreCapsule{Format: 1, DeploymentID: "portable-ready-test", Root: root, Host: "arch-dmit", Service: "lmm-api.service", Writer: writer}
	digest := strings.Repeat("a", 64)
	owner := productionMerchantStoreFenceOwner{Format: 1, State: "ACTIVE", DeploymentID: c.DeploymentID, Host: c.Host,
		PlanSHA256: digest, ContractSHA256: merchantStoreFenceContractSHA(writer), ProviderSHA256: writer.Candidate.PayloadSHA256,
		Nonce: strings.Repeat("b", 32), HolderPID: os.Getpid(), HolderUnit: merchantStorePortableUnit(c.DeploymentID), HolderInvocationID: strings.Repeat("c", 32),
		BackendPID: 123, SystemIdentifier: writer.SystemIdentifier, Database: writer.Database, DatabaseOID: writer.DatabaseOID,
		Schema: writer.Schema, SchemaOID: writer.SchemaOID, Role: writer.Role, Purpose: "portable-deploy", Service: c.Service}
	body, err := canonicalMerchantStoreFenceOwner(owner)
	if err != nil {
		t.Fatal(err)
	}
	runner := &portableReadinessRunner{merchantStoreHolderUnitFixture: merchantStoreHolderUnitFixture{owner: owner}}
	start := time.Now()
	now := start
	runtime := &productionRuntime{runner: runner, requiredOwnerUID: uint32(os.Getuid()), merchantStoreSocketDirectory: root, now: func() time.Time { return now }}
	served := make(chan error, 1)
	ready := false
	runtime.sleep = func(delay time.Duration) {
		now = now.Add(delay)
		if ready || now.Sub(start) < 75*time.Second {
			return
		}
		ready = true
		socket, err := runtime.merchantStoreSocketPath(root, owner, true)
		if err != nil {
			t.Fatal(err)
		}
		listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = listener.Close() })
		if err := os.Chmod(socket, 0600); err != nil {
			t.Fatal(err)
		}
		go func() {
			connection, err := listener.AcceptUnix()
			if err != nil {
				served <- err
				return
			}
			defer connection.Close()
			request, err := readMerchantStoreFenceRequest(connection, owner)
			if err == nil && request.Operation != "check" {
				err = errors.New("readiness attempted to release the owner")
			}
			if err == nil {
				err = json.NewEncoder(connection).Encode(merchantStoreFenceReply{Protocol: merchantStoreFenceProtocol, Owner: string(body), Held: true})
			}
			served <- err
		}()
		if err := os.WriteFile(merchantStorePortableOwnerPath(c, ""), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.ensureMerchantStorePortableFence(context.Background(), c, digest, ""); err != nil {
		t.Fatalf("original portable holder was rejected after %s: %v", now.Sub(start), err)
	}
	if !ready || now.Sub(start) != 75*time.Second || runner.starts != 1 {
		t.Fatalf("late holder was not reused: ready=%t elapsed=%s starts=%d", ready, now.Sub(start), runner.starts)
	}
	if err := <-served; err != nil {
		t.Fatal(err)
	}
}
