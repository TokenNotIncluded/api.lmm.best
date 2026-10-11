//go:build linux

package deploycli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestSchemaMaintenanceRPCProtocolIsClosedAndCanonical(t *testing.T) {
	binding := schemaMaintenanceRPCBinding{PlanSHA256: strings.Repeat("a", 64), TransitionID: "test-transition", DaemonPID: 123, DaemonInvocationID: strings.Repeat("b", 32), DaemonExeSHA256: strings.Repeat("c", 64), DaemonStartTicks: 1}
	request := schemaMaintenanceRPCRequest{Protocol: schemaMaintenanceRPCProtocol, Binding: binding, Action: "status", Nonce: strings.Repeat("d", 64)}
	canonical, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var decoded schemaMaintenanceRPCRequest
	if err := schemaMaintenanceRPCDecode(canonical, &decoded); err != nil || decoded != request {
		t.Fatalf("canonical request: %v", err)
	}
	for _, invalid := range [][]byte{
		append([]byte(" "), canonical...),
		append(append([]byte{}, canonical...), '\n'),
		bytes.Replace(canonical, []byte(`"action":"status"`), []byte(`"action":"status","action":"close"`), 1),
		bytes.Replace(canonical, []byte(`"action":"status"`), []byte(`"action":"status","command":"rm"`), 1),
		bytes.Replace(canonical, []byte(`"daemon_pid":123`), []byte(`"daemon_pid":123,"daemon_pid":123`), 1),
		bytes.Replace(canonical, []byte(`"daemon_pid":123`), []byte(`"daemon_pid":1.23e2`), 1),
		append(append([]byte{}, canonical...), []byte(`{}`)...),
	} {
		if err := schemaMaintenanceRPCDecode(invalid, &decoded); err == nil {
			t.Fatalf("accepted noncanonical or expanded request: %s", invalid)
		}
	}
	for _, action := range []string{"capture-backup-source", "finish"} {
		if timeout, err := schemaMaintenanceRPCActionTimeout(action); err != nil || timeout != 30*time.Second {
			t.Fatalf("fixed action %q did not retain its bounded timeout: %v, %v", action, timeout, err)
		}
	}
	for _, action := range []string{"shell", "adopt-all", "migrate", "", "worker --execute", "finish --execute", "capture-ready /tmp", "capture-backup-source; command"} {
		if _, err := schemaMaintenanceRPCActionTimeout(action); err == nil {
			t.Fatalf("accepted arbitrary action %q", action)
		}
	}
	reply := schemaMaintenanceRPCReply{Result: json.RawMessage(`{"z":1,"a":{"z":2,"a":3}}`)}
	normalized, err := schemaMaintenanceRPCMarshal(reply)
	if err != nil || !bytes.Contains(normalized, []byte(`"result":{"a":{"a":3,"z":2},"z":1}`)) {
		t.Fatalf("result is not canonical: %s, %v", normalized, err)
	}
	reply.Result = json.RawMessage(`{"a":{"duplicate":1,"duplicate":2}}`)
	if _, err := schemaMaintenanceRPCMarshal(reply); err == nil {
		t.Fatal("nested duplicate result member accepted")
	}
}

func TestSchemaMaintenanceRPCPhaseBudgetsCoverNativeWork(t *testing.T) {
	for _, action := range []string{"close", "claim", "reopen", "capture-recovery", "capture-ready"} {
		budget, err := schemaMaintenanceRPCActionTimeout(action)
		if err != nil || budget != 5*time.Minute {
			t.Fatalf("%s phase must allow capsule verification: %v, %v", action, budget, err)
		}
		if budget < 2*(2*time.Minute) {
			t.Fatalf("%s phase cannot cover both two-minute Sigstore verifiers", action)
		}
	}
	stop, err := schemaMaintenanceRPCActionTimeout("stop")
	if err != nil || stop != 90*time.Second || stop < 45*time.Second {
		t.Fatalf("stop phase cannot cover native TimeoutStopSec=45s: %v, %v", stop, err)
	}
	for _, action := range []string{"capture-closed", "capture-stopped", "capture-backup-source", "status", "release", "finish"} {
		if budget, err := schemaMaintenanceRPCActionTimeout(action); err != nil || budget != 30*time.Second {
			t.Fatalf("%s phase lost its short bounded budget: %v, %v", action, budget, err)
		}
	}
	if budget, err := schemaMaintenanceRPCActionTimeout("worker"); err != nil || budget != 10*time.Minute {
		t.Fatalf("worker phase lost its bounded budget: %v, %v", budget, err)
	}
}

func TestSchemaMaintenanceRPCRequiresRealRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("this assertion exercises the actual non-root process")
	}
	if _, err := callSchemaMaintenanceRPC(context.Background(), "/unreached", schemaMaintenanceRPCBinding{}, "status"); err == nil || !strings.Contains(err.Error(), "requires root") {
		t.Fatalf("non-root client was accepted: %v", err)
	}
	if err := serveSchemaMaintenanceRPC(context.Background(), "/unreached", schemaMaintenanceRPCBinding{}, func(context.Context, schemaMaintenanceRPCRequest) (schemaMaintenanceRPCReply, error) {
		return schemaMaintenanceRPCReply{}, nil
	}); err == nil || !strings.Contains(err.Error(), "requires root") {
		t.Fatalf("non-root daemon was accepted: %v", err)
	}
}

// The child is the actual native daemon generation. Tests never substitute a
// receipt for SO_PEERCRED, /proc/exe, /proc/environ or /proc/stat observations.
func TestSchemaMaintenanceRPCHelper(t *testing.T) {
	workspace := os.Getenv("LMM_SCHEMA_RPC_TEST_WORKSPACE")
	if workspace == "" {
		return
	}
	exe, err := sha256File("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	binding, err := schemaMaintenanceRPCSelfBinding(filepath.Base(workspace), strings.Repeat("a", 64), exe)
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan error, 1)
	go func() {
		ready <- serveSchemaMaintenanceRPC(context.Background(), workspace, binding, func(ctx context.Context, request schemaMaintenanceRPCRequest) (schemaMaintenanceRPCReply, error) {
			file, err := os.OpenFile(filepath.Join(workspace, "calls"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			if err != nil {
				return schemaMaintenanceRPCReply{}, err
			}
			_, err = fmt.Fprintln(file, request.Action)
			_ = file.Close()
			if err != nil {
				return schemaMaintenanceRPCReply{}, err
			}
			if request.Action == "claim" {
				return schemaMaintenanceRPCReply{}, errors.New("fixture owner not ready")
			}
			if request.Action == "worker" {
				select {
				case <-time.After(11 * time.Second):
				case <-ctx.Done():
					return schemaMaintenanceRPCReply{}, ctx.Err()
				}
			}
			return schemaMaintenanceRPCReply{Result: json.RawMessage(`{"state":"unchanged","safe":true}`)}, nil
		})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-ready:
			t.Fatal(err)
		default:
		}
		if info, err := os.Lstat(filepath.Join(workspace, "state", "controller.sock")); err == nil && info.Mode().Perm() == 0o600 {
			// A second server in even the very same permitted process must not
			// replace the first socket or make another handler own this channel.
			if err := serveSchemaMaintenanceRPC(context.Background(), workspace, binding, func(context.Context, schemaMaintenanceRPCRequest) (schemaMaintenanceRPCReply, error) {
				return schemaMaintenanceRPCReply{}, nil
			}); err == nil || !strings.Contains(err.Error(), "already exists") {
				t.Fatalf("existing socket not refused by server: %v", err)
			}
			after, err := os.Lstat(filepath.Join(workspace, "state", "controller.sock"))
			if err != nil || !os.SameFile(info, after) {
				t.Fatal("second serve changed the original socket")
			}
			_ = json.NewEncoder(os.Stdout).Encode(binding)
			if err := <-ready; err != nil {
				t.Fatal(err)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("test daemon did not bind")
}

func schemaMaintenanceRPCRunning(t *testing.T) (string, schemaMaintenanceRPCBinding) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("real root-only RPC tests require running the compiled test binary with sudo")
	}
	// /run is root-owned and nonsticky; no production workspace or lock path is
	// involved. Every resource is confined to this unique local fixture.
	workspace, err := os.MkdirTemp("/run", "lmm-schema-rpc-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workspace) })
	if err := os.Mkdir(filepath.Join(workspace, "state"), 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestSchemaMaintenanceRPCHelper$", "-test.v=false")
	command.Env = append(os.Environ(), "LMM_SCHEMA_RPC_TEST_WORKSPACE="+workspace, "INVOCATION_ID="+strings.Repeat("b", 32))
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
		if stderr.Len() != 0 {
			t.Log(stderr.String())
		}
	})
	var binding schemaMaintenanceRPCBinding
	if err := json.NewDecoder(bufio.NewReader(stdout)).Decode(&binding); err != nil {
		t.Fatalf("native daemon startup: %v, stderr: %s", err, stderr.String())
	}
	return workspace, binding
}

func schemaMaintenanceRPCRaw(t *testing.T, workspace string, raw []byte) []byte {
	t.Helper()
	connection, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: filepath.Join(workspace, "state", "controller.sock"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := connection.Write(raw); err != nil {
		t.Fatal(err)
	}
	_ = connection.CloseWrite()
	body, err := io.ReadAll(connection)
	if err != nil && !errors.Is(err, syscall.ECONNRESET) {
		t.Fatal(err)
	}
	return body
}

func TestSchemaMaintenanceRPCRealPeerReplyAndNoRetry(t *testing.T) {
	workspace, binding := schemaMaintenanceRPCRunning(t)
	reply, err := callSchemaMaintenanceRPC(context.Background(), workspace, binding, "status")
	if err != nil || !reply.OK || reply.Binding != binding || reply.Action != "status" || len(reply.Nonce) != 64 || string(reply.Result) != `{"safe":true,"state":"unchanged"}` {
		t.Fatalf("real native RPC reply: %+v, %v", reply, err)
	}
	if reply, err := callSchemaMaintenanceRPC(context.Background(), workspace, binding, "claim"); err == nil || reply.OK || reply.Error != "fixture owner not ready" {
		t.Fatalf("handler error not preserved: %+v, %v", reply, err)
	}
	calls, err := os.ReadFile(filepath.Join(workspace, "calls"))
	if err != nil || string(calls) != "status\nclaim\n" {
		t.Fatalf("handler repeated after failure: %s, %v", calls, err)
	}
	for _, change := range []func(*schemaMaintenanceRPCBinding){
		func(b *schemaMaintenanceRPCBinding) { b.DaemonPID++ },
		func(b *schemaMaintenanceRPCBinding) { b.DaemonStartTicks++ },
		func(b *schemaMaintenanceRPCBinding) { b.DaemonInvocationID = strings.Repeat("c", 32) },
		func(b *schemaMaintenanceRPCBinding) { b.DaemonExeSHA256 = strings.Repeat("d", 64) },
	} {
		wrong := binding
		change(&wrong)
		if _, err := callSchemaMaintenanceRPC(context.Background(), workspace, wrong, "status"); err == nil {
			t.Fatalf("accepted stale native daemon binding: %+v", wrong)
		}
	}
	if info, err := os.Lstat(filepath.Join(workspace, "state", "controller.sock")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("controller socket is not private: %v, %v", info, err)
	}
}

func TestSchemaMaintenanceRPCRejectsReplayAndExtraAuthority(t *testing.T) {
	workspace, binding := schemaMaintenanceRPCRunning(t)
	request := schemaMaintenanceRPCRequest{Protocol: schemaMaintenanceRPCProtocol, Binding: binding, Action: "close", Nonce: strings.Repeat("d", 64)}
	raw, _ := json.Marshal(request)
	if reply := schemaMaintenanceRPCRaw(t, workspace, append(raw, '\n')); len(reply) == 0 {
		t.Fatal("first canonical request was not accepted")
	}
	for _, invalid := range [][]byte{
		append(append([]byte{}, raw...), '\n'), // Same nonce cannot execute twice.
		bytes.Replace(raw, []byte(`"action":"close"`), []byte(`"action":"close","execute":true`), 1),
		bytes.Replace(raw, []byte(`"action":"close"`), []byte(`"action":"adopt-all"`), 1),
		bytes.Replace(raw, []byte(`"plan_sha256":"`+binding.PlanSHA256+`"`), []byte(`"plan_sha256":"`+strings.Repeat("e", 64)+`"`), 1),
		append(append([]byte{}, raw...), []byte("\n{}")...),
		bytes.Repeat([]byte("x"), schemaMaintenanceRPCMaxBytes+1),
	} {
		if !bytes.HasSuffix(invalid, []byte{'\n'}) {
			invalid = append(invalid, '\n')
		}
		if reply := schemaMaintenanceRPCRaw(t, workspace, invalid); len(reply) != 0 {
			t.Fatalf("invalid request got a result: %s", reply)
		}
	}
	connection, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: filepath.Join(workspace, "state", "controller.sock"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	descriptor, err := os.Open("/dev/null")
	if err != nil {
		t.Fatal(err)
	}
	defer descriptor.Close()
	request.Nonce = strings.Repeat("f", 64)
	raw, _ = json.Marshal(request)
	_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
	if _, _, err := connection.WriteMsgUnix(append(raw, '\n'), unix.UnixRights(int(descriptor.Fd())), nil); err != nil {
		t.Fatal(err)
	}
	_ = connection.CloseWrite()
	if body, err := io.ReadAll(connection); len(body) != 0 || err != nil && !errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("unexpected FD transport accepted: %s, %v", body, err)
	}
	calls, _ := os.ReadFile(filepath.Join(workspace, "calls"))
	if string(calls) != "close\n" {
		t.Fatalf("replay or extra authority reached handler: %s", calls)
	}
}

func TestSchemaMaintenanceRPCWorkerWaitExceedsIODeadline(t *testing.T) {
	workspace, binding := schemaMaintenanceRPCRunning(t)
	start := time.Now()
	if _, err := callSchemaMaintenanceRPC(context.Background(), workspace, binding, "worker"); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < schemaMaintenanceRPCIOTimeout {
		t.Fatal("fixture did not exercise the long-running worker reply")
	}
}

func TestSchemaMaintenanceRPCDoesNotOverwriteExistingSocket(t *testing.T) {
	workspace, binding := schemaMaintenanceRPCRunning(t)
	binding.DaemonPID = os.Getpid()
	start, _ := schemaMaintenanceGuardianProcess(os.Getpid())
	binding.DaemonStartTicks = start
	// Process verification precedes bind, so make this separate assertion at
	// the transport boundary without claiming this test process is the daemon.
	path, err := schemaMaintenanceRPCSocketPath(workspace, binding)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err == nil {
		listener.Close()
		t.Fatal("existing controller socket was overwritten")
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("existing controller socket was changed")
	}
}
