//go:build linux

package deploycli

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

type schemaGuardianFixture struct {
	binding    productionSchemaMaintenanceHostBinding
	paths      []string
	files      []*os.File
	reply      schemaMaintenanceGuardianReply
	listener   *net.UnixListener
	mu         sync.Mutex
	connection *net.UnixConn
	ready      chan struct{}
	done       chan error
}

func newSchemaGuardianFixture(t *testing.T) *schemaGuardianFixture {
	t.Helper()
	root, err := os.MkdirTemp(maintenanceFixtureCache(t), "schema-scm-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	f := &schemaGuardianFixture{ready: make(chan struct{}), done: make(chan error, 1)}
	for _, name := range []string{"native", "systemd", "frontend"} {
		path := filepath.Join(root, name+".lock")
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		f.files = append(f.files, file)
		f.paths = append(f.paths, path)
		if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
			t.Fatal(err)
		}
		info, err := file.Stat()
		if err != nil {
			t.Fatal(err)
		}
		stat := info.Sys().(*syscall.Stat_t)
		f.reply.Locks = append(f.reply.Locks, schemaMaintenanceGuardianLock{path, uint64(stat.Dev), stat.Ino, true})
	}
	if err := os.Mkdir(filepath.Join(root, "state"), 0o700); err != nil {
		t.Fatal(err)
	}
	f.binding = productionSchemaMaintenanceHostBinding{
		TransitionID: "fixture", PlanPath: filepath.Join(root, "schema-maintenance-plan.json"),
		Host: "fixture-host", Workspace: root, GuardianProtocol: schemaMaintenanceGuardianProtocol,
		GuardianSocket: filepath.Join(root, "state", "guardian.sock"), GuardianBindingPath: filepath.Join(root, "state", "guardian-binding.json"),
	}
	plan := []byte(`{"transition_id":"fixture"}`)
	if err := os.WriteFile(f.binding.PlanPath, plan, 0o600); err != nil {
		t.Fatal(err)
	}
	f.binding.PlanSHA256 = fmt.Sprintf("%x", sha256.Sum256(plan))
	bound, err := json.Marshal(schemaMaintenanceGuardianBinding{schemaMaintenanceGuardianBindingFormat, f.binding.TransitionID, f.binding.PlanPath, f.binding.PlanSHA256, f.binding.Host, root, f.binding.GuardianSocket})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.binding.GuardianBindingPath, bound, 0o600); err != nil {
		t.Fatal(err)
	}
	f.binding.GuardianBindingSHA256 = fmt.Sprintf("%x", sha256.Sum256(bound))
	f.listener, err = net.ListenUnix("unix", &net.UnixAddr{Name: f.binding.GuardianSocket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(f.binding.GuardianSocket, 0o600); err != nil {
		t.Fatal(err)
	}
	f.reply.Protocol, f.reply.BindingSHA256, f.reply.TransitionID, f.reply.PlanSHA256 = schemaMaintenanceGuardianProtocol, f.binding.GuardianBindingSHA256, f.binding.TransitionID, f.binding.PlanSHA256
	f.reply.GuardianPID, f.reply.TransferredPaths = os.Getpid(), append([]string(nil), f.paths...)
	t.Cleanup(func() {
		_ = f.listener.Close()
		f.mu.Lock()
		if f.connection != nil {
			_ = f.connection.Close()
		}
		f.mu.Unlock()
		for _, file := range f.files {
			_ = file.Close()
		}
	})
	return f
}

func (f *schemaGuardianFixture) serve(t *testing.T, descriptors []*os.File, fragment bool) {
	f.serveWithFinish(t, descriptors, fragment, nil)
}

func (f *schemaGuardianFixture) serveWithFinish(t *testing.T, descriptors []*os.File, fragment bool, finish func(*net.UnixConn) error) {
	t.Helper()
	go func() {
		connection, err := f.listener.AcceptUnix()
		if err != nil {
			f.done <- err
			return
		}
		f.mu.Lock()
		f.connection = connection
		f.mu.Unlock()
		close(f.ready)
		defer connection.Close()
		_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
		request, err := bufio.NewReader(connection).ReadBytes('\n')
		if err != nil {
			f.done <- err
			return
		}
		var actual map[string]any
		expected := map[string]any{"protocol": schemaMaintenanceGuardianProtocol, "operation": "adopt-all", "binding_path": f.binding.GuardianBindingPath, "binding_sha256": f.binding.GuardianBindingSHA256, "transition_id": f.binding.TransitionID, "plan_sha256": f.binding.PlanSHA256}
		if json.Unmarshal(request, &actual) != nil || !reflect.DeepEqual(actual, expected) {
			f.done <- fmt.Errorf("unexpected schema guardian request: %s", request)
			return
		}
		body, err := json.Marshal(f.reply)
		if err != nil {
			f.done <- err
			return
		}
		body = append(body, '\n')
		fds := []int{}
		for _, file := range descriptors {
			fds = append(fds, int(file.Fd()))
		}
		first := body
		if fragment {
			first = body[:len(body)/2]
		}
		if _, _, err := connection.WriteMsgUnix(first, unix.UnixRights(fds...), nil); err != nil {
			f.done <- err
			return
		}
		if fragment {
			if _, err := connection.Write(body[len(first):]); err != nil {
				f.done <- err
				return
			}
		}
		if finish != nil {
			f.done <- finish(connection)
			return
		}
		_, err = connection.Read(make([]byte, 1))
		if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
			err = nil
		}
		f.done <- err
	}()
}

func (f *schemaGuardianFixture) receive(ctx context.Context) (productionSchemaMaintenanceHostLease, error) {
	lease, err := receiveSchemaMaintenanceHostLocksForPaths(ctx, f.binding, uint32(os.Getuid()), f.paths)
	if err == nil {
		<-f.ready
	}
	return lease, err
}

func (f *schemaGuardianFixture) assertIndependentlyLocked(t *testing.T) {
	t.Helper()
	for _, path := range f.paths {
		fd, err := unix.Open(path, unix.O_RDWR|unix.O_CLOEXEC, 0)
		if err != nil {
			t.Fatal(err)
		}
		err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		_ = unix.Close(fd)
		if err != unix.EWOULDBLOCK && err != unix.EAGAIN {
			t.Fatalf("guardian lock %s became acquirable: %v", path, err)
		}
	}
}

func TestSchemaMaintenanceGuardianSCMRightsOwnsCompleteLockSet(t *testing.T) {
	for _, fragment := range []bool{false, true} {
		t.Run(fmt.Sprintf("fragment=%v", fragment), func(t *testing.T) {
			f := newSchemaGuardianFixture(t)
			f.serve(t, f.files, fragment)
			lease, err := f.receive(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if err := lease.Check(context.Background()); err != nil {
				t.Fatal(err)
			}
			for _, file := range lease.(*schemaMaintenanceHostLockLease).files {
				flags, err := unix.FcntlInt(file.Fd(), unix.F_GETFD, 0)
				if err != nil || flags&unix.FD_CLOEXEC == 0 {
					t.Fatalf("adopted fd missing atomic close-on-exec: flags=%d err=%v", flags, err)
				}
			}
			if err := lease.Close(); err != nil {
				t.Fatal(err)
			}
			if err := lease.Close(); err != nil {
				t.Fatal(err)
			}
			if err := <-f.done; err != nil {
				t.Fatal(err)
			}
			f.assertIndependentlyLocked(t)
			if err := lease.Check(context.Background()); err == nil {
				t.Fatal("closed lease passed Check")
			}
		})
	}
}

func TestSchemaMaintenanceGuardianRejectsForgedReceiptOrDescriptors(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*schemaGuardianFixture) []*os.File
	}{
		{"old-protocol", func(f *schemaGuardianFixture) []*os.File {
			f.reply.Protocol = productionMaintenanceLockProtocol
			return f.files
		}},
		{"wrong-plan", func(f *schemaGuardianFixture) []*os.File {
			f.reply.PlanSHA256 = f.binding.GuardianBindingSHA256
			return f.files
		}},
		{"wrong-binding", func(f *schemaGuardianFixture) []*os.File {
			f.reply.BindingSHA256 = f.binding.PlanSHA256
			return f.files
		}},
		{"wrong-transition", func(f *schemaGuardianFixture) []*os.File { f.reply.TransitionID = "other"; return f.files }},
		{"wrong-actual-peer-pid", func(f *schemaGuardianFixture) []*os.File { f.reply.GuardianPID++; return f.files }},
		{"wrong-path-order", func(f *schemaGuardianFixture) []*os.File {
			f.reply.TransferredPaths[0], f.reply.TransferredPaths[1] = f.reply.TransferredPaths[1], f.reply.TransferredPaths[0]
			return f.files
		}},
		{"wrong-metadata-inode", func(f *schemaGuardianFixture) []*os.File { f.reply.Locks[0].Inode++; return f.files }},
		{"not-held", func(f *schemaGuardianFixture) []*os.File { f.reply.Locks[0].Held = false; return f.files }},
		{"incomplete-lock-set", func(f *schemaGuardianFixture) []*os.File { return f.files[:2] }},
		{"extra-lock", func(f *schemaGuardianFixture) []*os.File {
			return append(append([]*os.File(nil), f.files...), f.files[0])
		}},
		{"wrong-descriptor-order", func(f *schemaGuardianFixture) []*os.File { return []*os.File{f.files[1], f.files[0], f.files[2]} }},
		{"unlocked-ofd-same-inode", func(f *schemaGuardianFixture) []*os.File {
			other, err := os.OpenFile(f.paths[0], os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = other.Close() })
			return []*os.File{other, f.files[1], f.files[2]}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSchemaGuardianFixture(t)
			fds := tc.mutate(f)
			f.serve(t, fds, false)
			lease, err := f.receive(context.Background())
			if lease != nil || err == nil {
				t.Fatalf("forged guardian accepted: lease=%v err=%v", lease, err)
			}
			if err := <-f.done; err != nil {
				t.Fatal(err)
			}
			f.assertIndependentlyLocked(t)
		})
	}
}

func TestSchemaMaintenanceGuardianCheckRejectsLeaseDrift(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*testing.T, *schemaGuardianFixture)
	}{
		{"guardian-disconnect", func(t *testing.T, f *schemaGuardianFixture) { _ = f.connection.Close() }},
		{"guardian-unsolicited-data", func(t *testing.T, f *schemaGuardianFixture) {
			if _, err := f.connection.Write([]byte("unexpected")); err != nil {
				t.Fatal(err)
			}
		}},
		{"replaced-lock-inode", func(t *testing.T, f *schemaGuardianFixture) {
			if err := os.Rename(f.paths[0], f.paths[0]+".old"); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f.paths[0], nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"lock-now-writable", func(t *testing.T, f *schemaGuardianFixture) {
			if err := os.Chmod(f.paths[0], 0o666); err != nil {
				t.Fatal(err)
			}
		}},
		{"guardian-unlocked-shared-ofd", func(t *testing.T, f *schemaGuardianFixture) {
			if err := unix.Flock(int(f.files[1].Fd()), unix.LOCK_UN); err != nil {
				t.Fatal(err)
			}
		}},
		{"binding-inode-replaced-same-bytes", func(t *testing.T, f *schemaGuardianFixture) {
			body, err := os.ReadFile(f.binding.GuardianBindingPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(f.binding.GuardianBindingPath, f.binding.GuardianBindingPath+".old"); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f.binding.GuardianBindingPath, body, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"binding-bytes-changed", func(t *testing.T, f *schemaGuardianFixture) {
			if err := os.WriteFile(f.binding.GuardianBindingPath, []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"socket-path-replaced", func(t *testing.T, f *schemaGuardianFixture) {
			if err := os.Rename(f.binding.GuardianSocket, f.binding.GuardianSocket+".old"); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f.binding.GuardianSocket, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSchemaGuardianFixture(t)
			f.serve(t, f.files, false)
			lease, err := f.receive(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Close()
			tc.mutate(t, f)
			if err := lease.Check(context.Background()); err == nil {
				t.Fatal("changed guardian lease accepted")
			}
		})
	}
}

func TestSchemaMaintenanceGuardianRejectsUnsafeBindingAndAuthority(t *testing.T) {
	for _, name := range []string{"binding-mode", "binding-link", "binding-symlink", "socket-mode", "binding-fields", "peer-owner"} {
		t.Run(name, func(t *testing.T) {
			f := newSchemaGuardianFixture(t)
			owner := uint32(os.Getuid())
			switch name {
			case "binding-mode":
				if err := os.Chmod(f.binding.GuardianBindingPath, 0o644); err != nil {
					t.Fatal(err)
				}
			case "binding-link":
				if err := os.Link(f.binding.GuardianBindingPath, f.binding.GuardianBindingPath+".link"); err != nil {
					t.Fatal(err)
				}
			case "binding-symlink":
				if err := os.Rename(f.binding.GuardianBindingPath, f.binding.GuardianBindingPath+".real"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(f.binding.GuardianBindingPath+".real", f.binding.GuardianBindingPath); err != nil {
					t.Fatal(err)
				}
			case "socket-mode":
				if err := os.Chmod(f.binding.GuardianSocket, 0o666); err != nil {
					t.Fatal(err)
				}
			case "binding-fields":
				f.binding.Host = "another-host"
			case "peer-owner":
				// Validate the actual SO_PEERCRED directly; changing the expected
				// authority earlier also correctly rejects the owned binding file.
				connection, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: f.binding.GuardianSocket, Net: "unix"})
				if err != nil {
					t.Fatal(err)
				}
				defer connection.Close()
				if _, err := schemaMaintenanceGuardianPeer(connection, owner+1, 0); err == nil {
					t.Fatal("wrong peer owner accepted")
				}
				return
			}
			if lease, err := receiveSchemaMaintenanceHostLocksForPaths(context.Background(), f.binding, owner, f.paths); lease != nil || err == nil {
				t.Fatalf("unsafe guardian accepted: lease=%v err=%v", lease, err)
			}
		})
	}
}

func TestSchemaMaintenanceGuardianReceiveHonorsCancellation(t *testing.T) {
	f := newSchemaGuardianFixture(t)
	go func() {
		connection, err := f.listener.AcceptUnix()
		if err != nil {
			return
		}
		defer connection.Close()
		_ = connection.SetDeadline(time.Now().Add(time.Second))
		_, _ = io.Copy(io.Discard, connection)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	if lease, err := f.receive(ctx); lease != nil || err == nil {
		t.Fatalf("cancelled guardian receive accepted: lease=%v err=%v", lease, err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("guardian receive did not honor the short context deadline")
	}
}

func TestSchemaMaintenanceGuardianOnlyNativeLockAllowsExactStickyParent(t *testing.T) {
	f := newSchemaGuardianFixture(t)
	parent := filepath.Join(f.binding.Workspace, "sticky-locks")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o777|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	native := filepath.Join(parent, "lmm-api-go-deploy.lock")
	if err := os.Rename(f.paths[0], native); err != nil {
		t.Fatal(err)
	}
	f.paths[0], f.reply.TransferredPaths[0], f.reply.Locks[0].Path = native, native, native
	f.serve(t, f.files, false)
	lease, err := f.receive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if err := schemaMaintenanceSafeGuardianParents(native, uint32(os.Getuid())); err == nil {
		t.Fatal("generic binding/socket path check accepted a writable sticky ancestor")
	}
	if err := schemaMaintenanceSafeGuardianParentsForNative(filepath.Join(parent, "another.lock"), uint32(os.Getuid()), native); err == nil {
		t.Fatal("native sticky exception accepted a different leaf")
	}
	if err := os.Chmod(parent, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := lease.Check(context.Background()); err == nil {
		t.Fatal("native sticky exception accepted a non-sticky writable parent")
	}
}

func TestSchemaMaintenanceGuardianProcessGenerationIsRequired(t *testing.T) {
	f := newSchemaGuardianFixture(t)
	f.serve(t, f.files, false)
	lease, err := f.receive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	lease.(*schemaMaintenanceHostLockLease).startTicks++
	if err := lease.Check(context.Background()); err == nil {
		t.Fatal("different original guardian process generation accepted")
	}
	if _, err := schemaMaintenanceGuardianProcess(1 << 30); err == nil {
		t.Fatal("absent original guardian process accepted")
	}
}

func (f *schemaGuardianFixture) readFinish(connection *net.UnixConn) (schemaMaintenanceGuardianFinishReply, error) {
	body, err := bufio.NewReader(connection).ReadBytes('\n')
	if err != nil {
		return schemaMaintenanceGuardianFinishReply{}, err
	}
	var actual map[string]any
	expected := map[string]any{"protocol": schemaMaintenanceGuardianProtocol, "operation": "finish-all", "binding_path": f.binding.GuardianBindingPath, "binding_sha256": f.binding.GuardianBindingSHA256, "transition_id": f.binding.TransitionID, "plan_sha256": f.binding.PlanSHA256}
	if json.Unmarshal(body, &actual) != nil || !reflect.DeepEqual(actual, expected) {
		return schemaMaintenanceGuardianFinishReply{}, fmt.Errorf("unexpected same-session finish request: %s", body)
	}
	return schemaMaintenanceGuardianFinishReply{
		Protocol: schemaMaintenanceGuardianProtocol, Operation: "finish-all", BindingPath: f.binding.GuardianBindingPath,
		BindingSHA256: f.binding.GuardianBindingSHA256, TransitionID: f.binding.TransitionID, PlanSHA256: f.binding.PlanSHA256,
		GuardianPID: os.Getpid(), Accepted: true, ClosingPaths: append([]string(nil), f.paths...),
	}, nil
}

func TestSchemaMaintenanceGuardianFinishClosesOnlyAfterExactTerminalAck(t *testing.T) {
	f := newSchemaGuardianFixture(t)
	f.serveWithFinish(t, f.files, false, func(connection *net.UnixConn) error {
		ack, err := f.readFinish(connection)
		if err != nil {
			return err
		}
		// Close the fixture guardian's originals before its ACK to demonstrate
		// that the Go copies retain every lock until the validated terminal reply.
		for _, file := range f.files {
			if err := file.Close(); err != nil {
				return err
			}
		}
		for _, path := range f.paths {
			fd, err := unix.Open(path, unix.O_RDWR|unix.O_CLOEXEC, 0)
			if err != nil {
				return err
			}
			err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
			_ = unix.Close(fd)
			if err != unix.EAGAIN && err != unix.EWOULDBLOCK {
				return fmt.Errorf("client clone released before terminal ACK: %s: %v", path, err)
			}
		}
		body, err := json.Marshal(ack)
		if err != nil {
			return err
		}
		_, err = connection.Write(append(body, '\n'))
		return err
	})
	lease, err := f.receive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if err := finishSchemaMaintenanceHostLocks(context.Background(), lease); err != nil {
		t.Fatal(err)
	}
	if err := <-f.done; err != nil {
		t.Fatal(err)
	}
	for _, path := range f.paths {
		fd, err := unix.Open(path, unix.O_RDWR|unix.O_CLOEXEC, 0)
		if err != nil {
			t.Fatal(err)
		}
		err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		_ = unix.Close(fd)
		if err != nil {
			t.Fatalf("terminal ACK left an adopted lock held: %s: %v", path, err)
		}
	}
	if err := lease.Check(context.Background()); err == nil {
		t.Fatal("completed terminal lease still passed Check")
	}
}

func TestSchemaMaintenanceGuardianFinishUnknownRetainsLocksAndRejectsCleanup(t *testing.T) {
	for _, name := range []string{"wrong-binding", "wrong-path", "wrong-pid", "wrong-plan", "wrong-operation", "unknown-ack", "duplicate-ack", "case-alias-ack", "disconnect", "unexpected-rights", "binding-changed"} {
		t.Run(name, func(t *testing.T) {
			f := newSchemaGuardianFixture(t)
			f.serveWithFinish(t, f.files, false, func(connection *net.UnixConn) error {
				ack, err := f.readFinish(connection)
				if err != nil {
					return err
				}
				// After this point only the adopted client FDs keep these locks.
				// Even a disconnected guardian must not trigger client cleanup.
				for _, file := range f.files {
					if err := file.Close(); err != nil {
						return err
					}
				}
				switch name {
				case "wrong-binding":
					ack.BindingSHA256 = f.binding.PlanSHA256
				case "wrong-path":
					ack.ClosingPaths[0], ack.ClosingPaths[1] = ack.ClosingPaths[1], ack.ClosingPaths[0]
				case "wrong-pid":
					ack.GuardianPID++
				case "wrong-plan":
					ack.PlanSHA256 = f.binding.GuardianBindingSHA256
				case "wrong-operation":
					ack.Operation = "adopt-all"
				case "unknown-ack":
					_, err := connection.Write([]byte("{}\n"))
					return err
				case "disconnect":
					return nil
				case "binding-changed":
					if err := os.Chmod(f.binding.GuardianBindingPath, 0o644); err != nil {
						return err
					}
				}
				body, err := json.Marshal(ack)
				if err != nil {
					return err
				}
				if name == "duplicate-ack" {
					body = append([]byte(`{"accepted":false,`), body[1:]...)
				}
				if name == "case-alias-ack" {
					body = []byte(strings.Replace(string(body), `"accepted":`, `"Accepted":`, 1))
				}
				if name == "unexpected-rights" {
					other, err := os.Open(f.paths[0])
					if err != nil {
						return err
					}
					defer other.Close()
					_, _, err = connection.WriteMsgUnix(append(body, '\n'), unix.UnixRights(int(other.Fd())), nil)
					return err
				}
				_, err = connection.Write(append(body, '\n'))
				return err
			})
			lease, err := f.receive(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			concrete := lease.(*schemaMaintenanceHostLockLease)
			// This is an explicit fixture-only cleanup after the retained locks
			// have been inspected. Production has no uncertain-close authority.
			t.Cleanup(func() {
				concrete.mu.Lock()
				defer concrete.mu.Unlock()
				concrete.finishUncertain = false
				schemaMaintenanceUncertainHostLeases.Lock()
				delete(schemaMaintenanceUncertainHostLeases.leases, concrete)
				schemaMaintenanceUncertainHostLeases.Unlock()
				_ = concrete.closeLocked()
			})
			if err := finishSchemaMaintenanceHostLocks(context.Background(), lease); err == nil {
				t.Fatal("unknown or mismatched terminal ACK accepted")
			}
			if err := <-f.done; err != nil {
				t.Fatal(err)
			}
			f.assertIndependentlyLocked(t)
			if err := lease.Close(); err == nil {
				t.Fatal("uncertain terminal exchange allowed deferred Close")
			}
			if err := finishSchemaMaintenanceHostLocks(context.Background(), lease); err == nil {
				t.Fatal("uncertain terminal exchange allowed a repeated Finish")
			}
			f.assertIndependentlyLocked(t)
			schemaMaintenanceUncertainHostLeases.Lock()
			_, pinned := schemaMaintenanceUncertainHostLeases.leases[concrete]
			schemaMaintenanceUncertainHostLeases.Unlock()
			if !pinned {
				t.Fatal("uncertain adopted FDs were not retained against garbage collection")
			}
		})
	}
}

func TestSchemaMaintenanceGuardianFinishPrecheckFailureRetainsLocks(t *testing.T) {
	f := newSchemaGuardianFixture(t)
	f.serve(t, f.files, false)
	lease, err := f.receive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	concrete := lease.(*schemaMaintenanceHostLockLease)
	t.Cleanup(func() {
		concrete.mu.Lock()
		defer concrete.mu.Unlock()
		concrete.finishUncertain = false
		schemaMaintenanceUncertainHostLeases.Lock()
		delete(schemaMaintenanceUncertainHostLeases.leases, concrete)
		schemaMaintenanceUncertainHostLeases.Unlock()
		_ = concrete.closeLocked()
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := finishSchemaMaintenanceHostLocks(ctx, lease); err == nil {
		t.Fatal("cancelled precheck sent a terminal request")
	}
	if err := lease.Close(); err == nil {
		t.Fatal("failed terminal precheck allowed deferred Close")
	}
	f.assertIndependentlyLocked(t)
}

func TestSchemaMaintenanceGuardianPythonTerminalProtocol(t *testing.T) {
	for _, name := range []string{"finish-all", "guardian-loss-before-finish"} {
		t.Run(name, func(t *testing.T) {
			f := newSchemaGuardianFixture(t)
			// The actual Python guardian must acquire its own descriptions, so
			// release only the fixture's initial setup locks and unused listener.
			if err := f.listener.Close(); err != nil {
				t.Fatal(err)
			}
			for _, file := range f.files {
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
			}
			f.binding.TransitionID = filepath.Base(f.binding.Workspace)
			plan, err := json.Marshal(map[string]string{"transition_id": f.binding.TransitionID})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f.binding.PlanPath, plan, 0o600); err != nil {
				t.Fatal(err)
			}
			f.binding.PlanSHA256 = fmt.Sprintf("%x", sha256.Sum256(plan))
			bound, err := json.Marshal(schemaMaintenanceGuardianBinding{schemaMaintenanceGuardianBindingFormat, f.binding.TransitionID, f.binding.PlanPath, f.binding.PlanSHA256, f.binding.Host, f.binding.Workspace, f.binding.GuardianSocket})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f.binding.GuardianBindingPath, bound, 0o600); err != nil {
				t.Fatal(err)
			}
			f.binding.GuardianBindingSHA256 = fmt.Sprintf("%x", sha256.Sum256(bound))
			script, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "scripts", "schema-maintenance-lock-guardian.py"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(script); err != nil {
				t.Fatalf("actual schema guardian source is required for protocol qualification: %v", err)
			}
			// Import the real implementation with test-only paths. Its production
			// CLI still requires root and its fixed three native lock paths.
			harness := `import importlib.util, os, sys
from pathlib import Path
spec = importlib.util.spec_from_file_location('schema_guardian_fixture', sys.argv[1])
guardian = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guardian)
guardian.serve(Path(sys.argv[2]), sys.argv[3], uid=os.getuid(),
               lock_paths=dict(zip(('native', 'systemd', 'frontend'), sys.argv[4:])),
               observed_host='fixture-host')
`
			args := []string{"-I", "-B", "-c", harness, script, f.binding.GuardianBindingPath, f.binding.GuardianBindingSHA256}
			args = append(args, f.paths...)
			command := exec.Command("python3", args...)
			var output strings.Builder
			command.Stdout, command.Stderr = &output, &output
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- command.Wait() }()
			waited := false
			t.Cleanup(func() {
				if !waited {
					_ = command.Process.Kill()
					<-done
				}
			})
			deadline := time.Now().Add(5 * time.Second)
			for {
				if info, err := os.Lstat(f.binding.GuardianSocket); err == nil && info.Mode()&os.ModeSocket != 0 {
					break
				}
				select {
				case err := <-done:
					waited = true
					t.Fatalf("Python guardian exited before socket readiness: %v: %s", err, output.String())
				default:
				}
				if time.Now().After(deadline) {
					t.Fatal("Python guardian socket readiness timed out")
				}
				time.Sleep(10 * time.Millisecond)
			}
			lease, err := receiveSchemaMaintenanceHostLocksForPaths(context.Background(), f.binding, uint32(os.Getuid()), f.paths)
			if err != nil {
				t.Fatal(err)
			}
			concrete := lease.(*schemaMaintenanceHostLockLease)
			t.Cleanup(func() {
				concrete.mu.Lock()
				defer concrete.mu.Unlock()
				concrete.finishUncertain = false
				schemaMaintenanceUncertainHostLeases.Lock()
				delete(schemaMaintenanceUncertainHostLeases.leases, concrete)
				schemaMaintenanceUncertainHostLeases.Unlock()
				_ = concrete.closeLocked()
			})
			if concrete.pid != command.Process.Pid {
				t.Fatal("SCM receiver authenticated a different Python process")
			}
			if err := lease.Check(context.Background()); err != nil {
				t.Fatal(err)
			}
			f.assertIndependentlyLocked(t)
			if name == "guardian-loss-before-finish" {
				if err := command.Process.Kill(); err != nil {
					t.Fatal(err)
				}
				<-done
				waited = true
				if err := finishSchemaMaintenanceHostLocks(context.Background(), lease); err == nil {
					t.Fatal("terminal precheck accepted a lost original guardian")
				}
				if err := lease.Close(); err == nil {
					t.Fatal("lost guardian permitted cleanup of its last adopted copies")
				}
				if err := finishSchemaMaintenanceHostLocks(context.Background(), lease); err == nil {
					t.Fatal("lost guardian permitted repeated terminal exchange")
				}
				f.assertIndependentlyLocked(t)
				return
			}
			if err := finishSchemaMaintenanceHostLocks(context.Background(), lease); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				waited = true
				if err != nil {
					t.Fatalf("Python guardian terminal exit failed: %v: %s", err, output.String())
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Python guardian did not exit after valid finish-all")
			}
			for _, path := range f.paths {
				fd, err := unix.Open(path, unix.O_RDWR|unix.O_CLOEXEC, 0)
				if err != nil {
					t.Fatal(err)
				}
				err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
				_ = unix.Close(fd)
				if err != nil {
					t.Fatalf("real Python terminal exit left lock held: %s: %v", path, err)
				}
			}
		})
	}
}
