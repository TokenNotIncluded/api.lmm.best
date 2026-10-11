//go:build linux

package deploycli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const schemaMaintenanceGuardianBindingFormat = "lmm-schema-maintenance-lock-binding-v1"

type schemaMaintenanceGuardianBinding struct {
	Format         string `json:"format"`
	TransitionID   string `json:"transition_id"`
	PlanPath       string `json:"plan_path"`
	PlanSHA256     string `json:"plan_sha256"`
	Host           string `json:"host"`
	Workspace      string `json:"workspace"`
	GuardianSocket string `json:"guardian_socket"`
}

type schemaMaintenanceGuardianLock struct {
	Path   string `json:"path"`
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
	Held   bool   `json:"held"`
}

type schemaMaintenanceGuardianReply struct {
	Protocol         string                          `json:"protocol"`
	BindingSHA256    string                          `json:"binding_sha256"`
	TransitionID     string                          `json:"transition_id"`
	PlanSHA256       string                          `json:"plan_sha256"`
	GuardianPID      int                             `json:"guardian_pid"`
	TransferredPaths []string                        `json:"transferred_paths"`
	Locks            []schemaMaintenanceGuardianLock `json:"locks"`
}

type schemaMaintenanceGuardianFinishReply struct {
	Protocol      string   `json:"protocol"`
	Operation     string   `json:"operation"`
	BindingPath   string   `json:"binding_path"`
	BindingSHA256 string   `json:"binding_sha256"`
	TransitionID  string   `json:"transition_id"`
	PlanSHA256    string   `json:"plan_sha256"`
	GuardianPID   int      `json:"guardian_pid"`
	Accepted      bool     `json:"accepted"`
	ClosingPaths  []string `json:"closing_paths"`
}

// An uncertain terminal exchange must retain its adopted descriptors even if
// a caller drops the lease after its deferred Close is refused. Otherwise the
// os.File/net.Conn finalizers could silently release the last lock copies.
var schemaMaintenanceUncertainHostLeases = struct {
	sync.Mutex
	leases map[*schemaMaintenanceHostLockLease]struct{}
}{leases: make(map[*schemaMaintenanceHostLockLease]struct{})}

type schemaMaintenanceHostLockLease struct {
	mu              sync.Mutex
	connection      *net.UnixConn
	files           []*os.File
	binding         productionSchemaMaintenanceHostBinding
	bindingInfo     os.FileInfo
	socketInfo      os.FileInfo
	owner           uint32
	pid             int
	startTicks      uint64
	nativePath      string
	locks           []schemaMaintenanceGuardianLock
	closed          bool
	finishUncertain bool
}

func receiveSchemaMaintenanceHostLocks(ctx context.Context, binding productionSchemaMaintenanceHostBinding, ownerUID uint32) (productionSchemaMaintenanceHostLease, error) {
	return receiveSchemaMaintenanceHostLocksForPaths(ctx, binding, ownerUID, []string{
		"/run/lock/lmm-api-go-deploy.lock",
		"/var/lib/lmm-api-deploy-systemd/lock",
		"/srv/lmm-api-frontend/.release.lock",
	})
}

// Only this private receiver accepts fixture paths. Production always uses all
// three fixed normal-owner locks above, in native/systemd/frontend order.
func receiveSchemaMaintenanceHostLocksForPaths(ctx context.Context, binding productionSchemaMaintenanceHostBinding, ownerUID uint32, paths []string) (productionSchemaMaintenanceHostLease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(paths) != 3 || paths[0] == paths[1] || paths[0] == paths[2] || paths[1] == paths[2] {
		return nil, errors.New("schema guardian requires three distinct ordered owner locks")
	}
	bindingInfo, err := schemaMaintenanceCheckGuardianBinding(binding, ownerUID)
	if err != nil {
		return nil, err
	}
	socketInfo, err := schemaMaintenanceCheckGuardianSocket(binding.GuardianSocket, ownerUID)
	if err != nil {
		return nil, err
	}
	connection, err := (&net.Dialer{Timeout: 125 * time.Second}).DialContext(ctx, "unix", binding.GuardianSocket)
	if err != nil {
		return nil, err
	}
	stream, ok := connection.(*net.UnixConn)
	if !ok {
		_ = connection.Close()
		return nil, errors.New("schema guardian requires a Unix connection")
	}
	lease := &schemaMaintenanceHostLockLease{connection: stream, binding: binding, bindingInfo: bindingInfo, socketInfo: socketInfo, owner: ownerUID, nativePath: paths[0]}
	good := false
	defer func() {
		if !good {
			_ = lease.Close()
		}
	}()
	peer, err := schemaMaintenanceGuardianPeer(stream, ownerUID, 0)
	if err != nil {
		return nil, err
	}
	lease.pid = int(peer.Pid)
	lease.startTicks, err = schemaMaintenanceGuardianProcess(lease.pid)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(125 * time.Second)
	if limit, ok := ctx.Deadline(); ok && limit.Before(deadline) {
		deadline = limit
	}
	if err := stream.SetDeadline(deadline); err != nil {
		return nil, err
	}
	stopCancellation := context.AfterFunc(ctx, func() { _ = stream.SetDeadline(time.Now()) })
	defer stopCancellation()
	request, err := json.Marshal(struct {
		Protocol      string `json:"protocol"`
		Operation     string `json:"operation"`
		BindingPath   string `json:"binding_path"`
		BindingSHA256 string `json:"binding_sha256"`
		TransitionID  string `json:"transition_id"`
		PlanSHA256    string `json:"plan_sha256"`
	}{schemaMaintenanceGuardianProtocol, "adopt-all", binding.GuardianBindingPath, binding.GuardianBindingSHA256, binding.TransitionID, binding.PlanSHA256})
	if err != nil {
		return nil, err
	}
	if _, err := stream.Write(append(request, '\n')); err != nil {
		return nil, err
	}
	body, files, err := schemaMaintenanceReceiveGuardianMessage(stream, 3)
	lease.files = files
	if err != nil {
		return nil, err
	}
	var reply schemaMaintenanceGuardianReply
	if err := schemaMaintenanceDecodeGuardianJSON(body, &reply); err != nil {
		return nil, fmt.Errorf("schema guardian receipt: %w", err)
	}
	if reply.Protocol != schemaMaintenanceGuardianProtocol || reply.BindingSHA256 != binding.GuardianBindingSHA256 || reply.TransitionID != binding.TransitionID || reply.PlanSHA256 != binding.PlanSHA256 || reply.GuardianPID != lease.pid || lease.pid <= 0 {
		return nil, errors.New("schema guardian receipt differs from immutable binding or actual peer")
	}
	if len(reply.TransferredPaths) != 3 || len(reply.Locks) != 3 {
		return nil, errors.New("schema guardian receipt must contain all three owner locks")
	}
	for index, path := range paths {
		if reply.TransferredPaths[index] != path || reply.Locks[index].Path != path || !reply.Locks[index].Held {
			return nil, errors.New("schema guardian lock paths, order or held receipt differ")
		}
	}
	lease.locks = reply.Locks
	if err := stream.SetDeadline(time.Time{}); err != nil {
		return nil, err
	}
	if err := lease.Check(ctx); err != nil {
		return nil, err
	}
	good = true
	return lease, nil
}

func schemaMaintenanceDecodeGuardianJSON(body []byte, result any) error {
	// The shared structural decoder rejects duplicate keys and incorrectly
	// cased tags too; DisallowUnknownFields alone accepts both in encoding/json.
	if err := decodeControllerBackupJSON(body, result); err != nil {
		return fmt.Errorf("schema guardian JSON is not an exact typed object: %w", err)
	}
	return nil
}

func schemaMaintenanceSafeGuardianParents(path string, owner uint32) error {
	return schemaMaintenanceSafeGuardianParentsForNative(path, owner, "")
}

func schemaMaintenanceSafeGuardianParentsForNative(path string, owner uint32, nativePath string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return errors.New("schema guardian path must be clean and absolute")
	}
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		info, err := os.Lstat(parent)
		if err != nil {
			return err
		}
		uid, _, ok := deploymentFileOwnership(info)
		stickyNative := path == nativePath && parent == filepath.Dir(nativePath) && uid == owner && info.Mode().Perm() == 0o777 && info.Mode()&os.ModeSticky != 0
		if !ok || uid != owner && uid != 0 || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 && !stickyNative {
			return errors.New("schema guardian path ancestor is unsafe")
		}
		if parent == "/" {
			return nil
		}
	}
}

func schemaMaintenanceCheckGuardianBinding(binding productionSchemaMaintenanceHostBinding, owner uint32) (os.FileInfo, error) {
	if binding.GuardianProtocol != schemaMaintenanceGuardianProtocol || binding.TransitionID == "" || binding.Host == "" || !productionSHA256Pattern.MatchString(binding.GuardianBindingSHA256) || !productionSHA256Pattern.MatchString(binding.PlanSHA256) {
		return nil, errors.New("schema guardian immutable binding is incomplete")
	}
	for _, path := range []string{binding.PlanPath, binding.Workspace, binding.GuardianSocket} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
			return nil, errors.New("schema guardian binding contains an invalid absolute path")
		}
	}
	if err := schemaMaintenanceSafeGuardianParents(binding.GuardianBindingPath, owner); err != nil {
		return nil, err
	}
	fd, err := unix.Open(binding.GuardianBindingPath, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), binding.GuardianBindingPath)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	uid, links, ok := deploymentFileOwnership(info)
	if !ok || uid != owner || links != 1 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() <= 0 || info.Size() > 1<<20 {
		return nil, errors.New("schema guardian binding must be private, regular and single-linked")
	}
	body, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil {
		return nil, err
	}
	current, err := os.Lstat(binding.GuardianBindingPath)
	if err != nil || !schemaMaintenanceSameGuardianFile(info, after) || !schemaMaintenanceSameGuardianFile(after, current) || fmt.Sprintf("%x", sha256.Sum256(body)) != binding.GuardianBindingSHA256 {
		return nil, errors.New("schema guardian binding inode or bytes changed")
	}
	var actual schemaMaintenanceGuardianBinding
	if err := schemaMaintenanceDecodeGuardianJSON(body, &actual); err != nil {
		return nil, err
	}
	if actual != (schemaMaintenanceGuardianBinding{schemaMaintenanceGuardianBindingFormat, binding.TransitionID, binding.PlanPath, binding.PlanSHA256, binding.Host, binding.Workspace, binding.GuardianSocket}) {
		return nil, errors.New("schema guardian binding file differs from the frozen host plan")
	}
	return info, nil
}

func schemaMaintenanceSameGuardianFile(before, after os.FileInfo) bool {
	if before == nil || after == nil {
		return false
	}
	a, okA := before.Sys().(*syscall.Stat_t)
	b, okB := after.Sys().(*syscall.Stat_t)
	return okA && okB && a.Dev == b.Dev && a.Ino == b.Ino && a.Mode == b.Mode && a.Uid == b.Uid && a.Gid == b.Gid && a.Nlink == b.Nlink && a.Size == b.Size && a.Mtim == b.Mtim && a.Ctim == b.Ctim
}

func schemaMaintenanceCheckGuardianSocket(path string, owner uint32) (os.FileInfo, error) {
	if err := schemaMaintenanceSafeGuardianParents(path, owner); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	uid, links, ok := deploymentFileOwnership(info)
	if !ok || uid != owner || links != 1 || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0o600 {
		return nil, errors.New("schema guardian socket must be private and owned by the deployment authority")
	}
	return info, nil
}

func schemaMaintenanceGuardianPeer(stream *net.UnixConn, owner uint32, pid int) (*unix.Ucred, error) {
	raw, err := stream.SyscallConn()
	if err != nil {
		return nil, err
	}
	var peer *unix.Ucred
	var peerErr error
	if err := raw.Control(func(fd uintptr) { peer, peerErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) }); err != nil {
		return nil, err
	}
	if peerErr != nil || peer == nil || peer.Uid != owner || peer.Pid <= 0 || pid != 0 && int(peer.Pid) != pid {
		return nil, errors.New("schema guardian actual Unix peer differs from the deployment authority")
	}
	return peer, nil
}

func schemaMaintenanceGuardianProcess(pid int) (uint64, error) {
	body, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	// comm may contain spaces and closing parentheses; the final ')' separates
	// it from the fixed stat fields. starttime is field 22, state is field 3.
	end := bytes.LastIndexByte(body, ')')
	if end < 0 {
		return 0, errors.New("schema guardian process identity is malformed")
	}
	fields := strings.Fields(string(body[end+1:]))
	if len(fields) < 20 || fields[0] == "Z" || fields[0] == "X" || fields[0] == "x" {
		return 0, errors.New("schema guardian original process is no longer live")
	}
	start, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil || start == 0 {
		return 0, errors.New("schema guardian process start identity is invalid")
	}
	return start, nil
}

func schemaMaintenanceReceiveGuardianMessage(stream *net.UnixConn, expectedCount int) ([]byte, []*os.File, error) {
	raw, err := stream.SyscallConn()
	if err != nil {
		return nil, nil, err
	}
	var body []byte
	files := make([]*os.File, 0, 3)
	fail := func(err error) ([]byte, []*os.File, error) { return nil, files, err }
	for len(body) < 16384 {
		data, control := make([]byte, 16384-len(body)), make([]byte, unix.CmsgSpace(4*16))
		var n, on, flags int
		var receiveErr error
		if err := raw.Read(func(fd uintptr) bool {
			n, on, flags, _, receiveErr = unix.Recvmsg(int(fd), data, control, unix.MSG_CMSG_CLOEXEC)
			return receiveErr != unix.EAGAIN && receiveErr != unix.EWOULDBLOCK
		}); err != nil {
			return fail(err)
		}
		if receiveErr != nil {
			return fail(receiveErr)
		}
		messages, err := unix.ParseSocketControlMessage(control[:on])
		if err != nil {
			return fail(err)
		}
		unexpectedControl := false
		for _, message := range messages {
			if message.Header.Level != unix.SOL_SOCKET || message.Header.Type != unix.SCM_RIGHTS {
				unexpectedControl = true
				continue
			}
			fds, err := unix.ParseUnixRights(&message)
			if err != nil {
				return fail(err)
			}
			for _, fd := range fds {
				files = append(files, os.NewFile(uintptr(fd), "schema-maintenance-adopted-lock"))
			}
		}
		if flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 || unexpectedControl || len(files) > expectedCount {
			return fail(errors.New("schema guardian must transfer the exact complete lock descriptor set"))
		}
		if n == 0 {
			return fail(io.ErrUnexpectedEOF)
		}
		body = append(body, data[:n]...)
		if end := bytes.IndexByte(body, '\n'); end >= 0 {
			if len(files) != expectedCount || len(bytes.TrimSpace(body[end+1:])) != 0 {
				return fail(errors.New("schema guardian receipt has incomplete descriptors or trailing data"))
			}
			return body[:end], files, nil
		}
	}
	return fail(errors.New("schema guardian receipt exceeds the bounded message size"))
}

func (lease *schemaMaintenanceHostLockLease) Check(ctx context.Context) error {
	lease.mu.Lock()
	defer lease.mu.Unlock()
	return lease.checkLocked(ctx)
}

func (lease *schemaMaintenanceHostLockLease) checkLocked(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if lease.closed || lease.connection == nil || len(lease.files) != 3 || len(lease.locks) != 3 {
		return errors.New("schema guardian adopted lock lease is closed or incomplete")
	}
	if lease.finishUncertain {
		return errors.New("schema guardian terminal exchange is uncertain; adopted locks remain held")
	}
	if _, err := schemaMaintenanceGuardianPeer(lease.connection, lease.owner, lease.pid); err != nil {
		return err
	}
	if start, err := schemaMaintenanceGuardianProcess(lease.pid); err != nil || start != lease.startTicks {
		return errors.New("schema guardian original process generation changed")
	}
	raw, err := lease.connection.SyscallConn()
	if err != nil {
		return err
	}
	var aliveErr error
	if err := raw.Control(func(fd uintptr) {
		var data [1]byte
		n, _, _, _, err := unix.Recvmsg(int(fd), data[:], nil, unix.MSG_PEEK|unix.MSG_DONTWAIT)
		if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
			return
		}
		if err != nil {
			aliveErr = err
		} else if n == 0 {
			aliveErr = errors.New("schema guardian connection closed")
		} else {
			aliveErr = errors.New("schema guardian sent unsolicited lease data")
		}
	}); err != nil {
		return err
	}
	if aliveErr != nil {
		return aliveErr
	}
	info, err := schemaMaintenanceCheckGuardianBinding(lease.binding, lease.owner)
	if err != nil || !os.SameFile(info, lease.bindingInfo) {
		return errors.New("schema guardian immutable binding changed during adoption")
	}
	socketInfo, err := schemaMaintenanceCheckGuardianSocket(lease.binding.GuardianSocket, lease.owner)
	if err != nil || !os.SameFile(socketInfo, lease.socketInfo) {
		return errors.New("schema guardian listening socket changed during adoption")
	}
	for index, file := range lease.files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := schemaMaintenanceCheckAdoptedLock(file, lease.locks[index], lease.owner, lease.pid, lease.nativePath); err != nil {
			return err
		}
	}
	return nil
}

func schemaMaintenanceCheckAdoptedLock(file *os.File, lock schemaMaintenanceGuardianLock, owner uint32, pid int, nativePath string) error {
	if file == nil || !lock.Held {
		return errors.New("schema guardian adopted descriptor is missing")
	}
	if err := schemaMaintenanceSafeGuardianParentsForNative(lock.Path, owner, nativePath); err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	current, err := os.Lstat(lock.Path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	uid, links, owned := deploymentFileOwnership(info)
	if !ok || !owned || uid != owner || links != 1 || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 || !schemaMaintenanceSameGuardianFile(info, current) || uint64(stat.Dev) != lock.Device || stat.Ino != lock.Inode {
		return errors.New("schema guardian adopted lock inode or ownership changed")
	}
	// fdinfo reports locks belonging to this open file description. Checking
	// only an independently blocked flock would also accept an unlocked FD
	// while an unrelated description held the same inode.
	fdinfo, err := os.ReadFile(fmt.Sprintf("/proc/self/fdinfo/%d", file.Fd()))
	if err != nil {
		return err
	}
	held := false
	for _, line := range strings.Split(string(fdinfo), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "lock:" {
			continue
		}
		if len(fields) != 9 || fields[2] != "FLOCK" || fields[3] != "ADVISORY" || fields[4] != "WRITE" || fields[5] != strconv.Itoa(pid) || fields[7] != "0" || fields[8] != "EOF" {
			return errors.New("schema guardian descriptor does not hold the guardian's exclusive whole-file flock")
		}
		parts := strings.Split(fields[6], ":")
		if len(parts) != 3 {
			return errors.New("schema guardian descriptor lock device is malformed")
		}
		_, e1 := strconv.ParseUint(parts[0], 16, 32)
		_, e2 := strconv.ParseUint(parts[1], 16, 32)
		inode, e3 := strconv.ParseUint(parts[2], 10, 64)
		// fdinfo's lock device is the kernel superblock device. Btrfs can
		// expose a different synthetic subvolume st_dev through fstat.
		// This record already belongs to this FD; its inode must still match.
		if e1 != nil || e2 != nil || e3 != nil || inode != stat.Ino || held {
			return errors.New("schema guardian descriptor lock identity differs")
		}
		held = true
	}
	if !held {
		return errors.New("schema guardian transferred an open file description without its held flock")
	}
	check, err := unix.Open(lock.Path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer unix.Close(check)
	var independent unix.Stat_t
	if err := unix.Fstat(check, &independent); err != nil || uint64(independent.Dev) != lock.Device || independent.Ino != lock.Inode {
		return errors.New("schema guardian independent lock inode changed")
	}
	if err := unix.Flock(check, unix.LOCK_EX|unix.LOCK_NB); err != unix.EWOULDBLOCK && err != unix.EAGAIN {
		if err == nil {
			return errors.New("schema guardian lock is independently acquirable")
		}
		return err
	}
	return nil
}

func (lease *schemaMaintenanceHostLockLease) Close() error {
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.finishUncertain {
		return errors.New("schema guardian terminal exchange is uncertain; refusing to close adopted locks")
	}
	return lease.closeLocked()
}

func (lease *schemaMaintenanceHostLockLease) closeLocked() error {
	if lease.closed {
		return nil
	}
	lease.closed = true
	var result error
	for _, file := range lease.files {
		if file != nil {
			result = errors.Join(result, file.Close())
		}
	}
	if lease.connection != nil {
		result = errors.Join(result, lease.connection.Close())
	}
	// Never LOCK_UN: SCM_RIGHTS duplicates the guardian's open file
	// description, and unlocking it would also release the guardian's lock.
	return result
}

// Finish is the optional terminal operation on the already adopted Unix
// session. It is never a reconnect/re-adopt or a normal Close. A valid terminal
// ACK commits the guardian to close its originals; only then may these copies
// close. The controller must still read back actual guardian exit/free locks.
func (lease *schemaMaintenanceHostLockLease) Finish(ctx context.Context) error {
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if err := lease.checkLocked(ctx); err != nil {
		if !lease.closed && len(lease.files) != 0 {
			lease.pinUncertainLocked()
		}
		return err
	}
	request, err := json.Marshal(struct {
		Protocol      string `json:"protocol"`
		Operation     string `json:"operation"`
		BindingPath   string `json:"binding_path"`
		BindingSHA256 string `json:"binding_sha256"`
		TransitionID  string `json:"transition_id"`
		PlanSHA256    string `json:"plan_sha256"`
	}{schemaMaintenanceGuardianProtocol, "finish-all", lease.binding.GuardianBindingPath, lease.binding.GuardianBindingSHA256, lease.binding.TransitionID, lease.binding.PlanSHA256})
	if err != nil {
		return err
	}
	deadline := time.Now().Add(125 * time.Second)
	if limit, ok := ctx.Deadline(); ok && limit.Before(deadline) {
		deadline = limit
	}
	// Once a terminal operation has been attempted, a concurrent guardian
	// loss (including a failed deadline update) must not release our copies.
	lease.pinUncertainLocked()
	if err := lease.connection.SetDeadline(deadline); err != nil {
		return err
	}
	stopCancellation := context.AfterFunc(ctx, func() { _ = lease.connection.SetDeadline(time.Now()) })
	defer stopCancellation()
	// Mark before the first write: even a failed write can have delivered an
	// accepted request. There is deliberately no error cleanup or retry below.
	payload := append(request, '\n')
	if written, err := lease.connection.Write(payload); err != nil || written != len(payload) {
		if err == nil {
			err = io.ErrShortWrite
		}
		return fmt.Errorf("schema guardian finish request outcome is uncertain; locks retained: %w", err)
	}
	body, unexpectedFiles, err := schemaMaintenanceReceiveGuardianMessage(lease.connection, 0)
	for _, file := range unexpectedFiles {
		_ = file.Close()
	}
	if err != nil {
		return fmt.Errorf("schema guardian terminal receipt is uncertain; locks retained: %w", err)
	}
	var reply schemaMaintenanceGuardianFinishReply
	if err := schemaMaintenanceDecodeGuardianJSON(body, &reply); err != nil {
		return fmt.Errorf("schema guardian terminal receipt is invalid; locks retained: %w", err)
	}
	if reply.Protocol != schemaMaintenanceGuardianProtocol || reply.Operation != "finish-all" || reply.BindingPath != lease.binding.GuardianBindingPath || reply.BindingSHA256 != lease.binding.GuardianBindingSHA256 || reply.TransitionID != lease.binding.TransitionID || reply.PlanSHA256 != lease.binding.PlanSHA256 || reply.GuardianPID != lease.pid || !reply.Accepted || len(reply.ClosingPaths) != len(lease.locks) {
		return errors.New("schema guardian terminal receipt differs from the frozen authority; locks retained")
	}
	for index, lock := range lease.locks {
		if reply.ClosingPaths[index] != lock.Path {
			return errors.New("schema guardian terminal receipt omitted or reordered an owner lock; locks retained")
		}
	}
	// SO_PEERCRED remains the authenticated original peer after its legitimate
	// terminal exit. Do not require that original process to remain alive now.
	if _, err := schemaMaintenanceGuardianPeer(lease.connection, lease.owner, lease.pid); err != nil {
		return fmt.Errorf("schema guardian terminal peer differs; locks retained: %w", err)
	}
	info, err := schemaMaintenanceCheckGuardianBinding(lease.binding, lease.owner)
	if err != nil || !os.SameFile(info, lease.bindingInfo) {
		return errors.New("schema guardian immutable binding changed before terminal acknowledgement; locks retained")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("schema guardian terminal observation was cancelled; locks retained: %w", err)
	}
	lease.finishUncertain = false
	schemaMaintenanceUncertainHostLeases.Lock()
	delete(schemaMaintenanceUncertainHostLeases.leases, lease)
	schemaMaintenanceUncertainHostLeases.Unlock()
	return lease.closeLocked()
}

func (lease *schemaMaintenanceHostLockLease) pinUncertainLocked() {
	lease.finishUncertain = true
	schemaMaintenanceUncertainHostLeases.Lock()
	schemaMaintenanceUncertainHostLeases.leases[lease] = struct{}{}
	schemaMaintenanceUncertainHostLeases.Unlock()
}

func finishSchemaMaintenanceHostLocks(ctx context.Context, lease productionSchemaMaintenanceHostLease) error {
	terminal, ok := lease.(interface{ Finish(context.Context) error })
	if !ok {
		return errors.New("schema maintenance lease has no dedicated terminal guardian protocol")
	}
	return terminal.Finish(ctx)
}
