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
	"path/filepath"
	"reflect"
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
