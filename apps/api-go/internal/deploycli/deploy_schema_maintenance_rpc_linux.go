//go:build linux

package deploycli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/sys/unix"
)

const schemaMaintenanceRPCMaxBytes = 64 << 10
const schemaMaintenanceRPCIOTimeout = 10 * time.Second
const schemaMaintenanceRPCMaxRequests = 4096

func schemaMaintenanceRPCSelfBinding(transitionID, planSHA256, expectedEngineSHA256 string) (schemaMaintenanceRPCBinding, error) {
	binding := schemaMaintenanceRPCBinding{TransitionID: transitionID, PlanSHA256: planSHA256,
		DaemonPID: os.Getpid(), DaemonExeSHA256: expectedEngineSHA256}
	if os.Geteuid() != 0 {
		return binding, errors.New("schema maintenance controller RPC requires root")
	}
	var err error
	binding.DaemonStartTicks, err = schemaMaintenanceGuardianProcess(binding.DaemonPID)
	if err != nil {
		return binding, err
	}
	binding.DaemonInvocationID, err = schemaMaintenanceRPCInvocation(binding.DaemonPID)
	if err != nil {
		return binding, err
	}
	if err := validateSchemaMaintenanceRPCBinding(binding); err != nil {
		return binding, err
	}
	return binding, schemaMaintenanceRPCProcess(binding)
}

// The controller remains the sole holder of its adopted locks. No descriptor,
// command, path, approval boolean or arbitrary operation is transported.
func serveSchemaMaintenanceRPC(ctx context.Context, workspace string, binding schemaMaintenanceRPCBinding, handler schemaMaintenanceRPCHandler) error {
	if os.Geteuid() != 0 || handler == nil {
		return errors.New("schema maintenance controller RPC requires root and an in-process handler")
	}
	if err := validateSchemaMaintenanceRPCBinding(binding); err != nil {
		return err
	}
	if binding.DaemonPID != os.Getpid() {
		return errors.New("schema maintenance RPC daemon is not this actual process")
	}
	if err := schemaMaintenanceRPCProcess(binding); err != nil {
		return err
	}
	path, err := schemaMaintenanceRPCSocketPath(workspace, binding)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return errors.New("schema maintenance controller socket already exists or cannot be inspected")
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return err
	}
	// A stopped daemon leaves its socket as evidence. A new process cannot
	// overwrite it and silently assume the old daemon's generation or locks.
	listener.SetUnlinkOnClose(false)
	defer listener.Close()
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	socketInfo, err := schemaMaintenanceCheckGuardianSocket(path, 0)
	if err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stop()
	seen := make(map[string]struct{})
	for {
		connection, err := listener.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		// Deliberately serial: the handler uses the one live host/owner instance
		// and the original three adopted FDs. Per-request errors do not retry it.
		schemaMaintenanceRPCHandle(ctx, connection, path, socketInfo, binding, seen, handler)
	}
}

func schemaMaintenanceRPCSocketPath(workspace string, binding schemaMaintenanceRPCBinding) (string, error) {
	if !filepath.IsAbs(workspace) || filepath.Clean(workspace) != workspace || filepath.Base(workspace) != binding.TransitionID {
		return "", errors.New("schema maintenance RPC workspace differs from its transition")
	}
	state := filepath.Join(workspace, "state")
	for _, directory := range []string{workspace, state} {
		info, err := os.Lstat(directory)
		if err != nil {
			return "", err
		}
		uid, _, ok := deploymentFileOwnership(info)
		if !ok || uid != 0 || !info.IsDir() || info.Mode().Perm() != 0o700 {
			return "", errors.New("schema maintenance RPC workspace must be a private root-owned directory")
		}
	}
	path := filepath.Join(state, "controller.sock")
	if err := schemaMaintenanceSafeGuardianParents(path, 0); err != nil {
		return "", err
	}
	return path, nil
}

func schemaMaintenanceRPCProcess(binding schemaMaintenanceRPCBinding) error {
	start, err := schemaMaintenanceGuardianProcess(binding.DaemonPID)
	if err != nil || start != binding.DaemonStartTicks {
		return errors.New("schema maintenance RPC daemon process generation changed")
	}
	root := filepath.Join("/proc", strconv.Itoa(binding.DaemonPID))
	if err := sha256MustEqual(filepath.Join(root, "exe"), binding.DaemonExeSHA256); err != nil {
		return errors.New("schema maintenance RPC daemon executable differs")
	}
	invocation, err := schemaMaintenanceRPCInvocation(binding.DaemonPID)
	if err != nil || invocation != binding.DaemonInvocationID {
		return errors.New("schema maintenance RPC daemon invocation differs")
	}
	after, err := schemaMaintenanceGuardianProcess(binding.DaemonPID)
	if err != nil || after != start {
		return errors.New("schema maintenance RPC daemon generation changed while inspected")
	}
	return nil
}

func schemaMaintenanceRPCInvocation(pid int) (string, error) {
	file, err := os.Open(filepath.Join("/proc", strconv.Itoa(pid), "environ"))
	if err != nil {
		return "", err
	}
	body, readErr := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(body) > 1<<20 {
		return "", errors.New("schema maintenance RPC daemon environment is unavailable or oversized")
	}
	prefix := []byte("INVOCATION_ID=")
	found := 0
	invocation := ""
	for _, entry := range bytes.Split(body, []byte{0}) {
		if bytes.HasPrefix(entry, prefix) {
			found++
			invocation = string(entry[len(prefix):])
		}
	}
	if found != 1 || !existingSchemaInvocationPattern.MatchString(invocation) {
		return "", errors.New("schema maintenance RPC daemon has no unique native invocation")
	}
	return invocation, nil
}

func schemaMaintenanceRPCRead(connection *net.UnixConn, timeout time.Duration) ([]byte, error) {
	if err := connection.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}
	var body []byte
	for {
		data := make([]byte, 4096)
		control := make([]byte, unix.CmsgSpace(8*4))
		n, on, flags, _, err := connection.ReadMsgUnix(data, control)
		if on > 0 {
			// RPC never transfers the guardian's lock FDs. Close unexpected
			// rights immediately, including the visible subset of truncated OOB.
			messages, _ := unix.ParseSocketControlMessage(control[:on])
			for _, message := range messages {
				if message.Header.Level == unix.SOL_SOCKET && message.Header.Type == unix.SCM_RIGHTS {
					descriptors, _ := unix.ParseUnixRights(&message)
					for _, descriptor := range descriptors {
						_ = unix.Close(descriptor)
					}
				}
			}
			return nil, errors.New("schema maintenance RPC forbids ancillary messages and lock adoption")
		}
		if flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 {
			return nil, errors.New("schema maintenance RPC message was truncated")
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		body = append(body, data[:n]...)
		if len(body) > schemaMaintenanceRPCMaxBytes {
			return nil, errors.New("schema maintenance RPC message is oversized")
		}
		if n == 0 || errors.Is(err, io.EOF) {
			break
		}
	}
	if len(body) > schemaMaintenanceRPCMaxBytes || len(body) < 2 || body[len(body)-1] != '\n' {
		return nil, errors.New("schema maintenance RPC message is incomplete or oversized")
	}
	return body[:len(body)-1], nil
}

func schemaMaintenanceRPCDecode(body []byte, result any) error {
	// RawMessage replies still require duplicate-member rejection inside the
	// result, rather than merely at the typed envelope and binding levels.
	if _, err := schemaMaintenanceRPCJSONValue(json.NewDecoder(bytes.NewReader(body))); err != nil {
		return err
	}
	if err := schemaMaintenanceDecodeGuardianJSON(body, result); err != nil {
		return err
	}
	canonical, err := schemaMaintenanceRPCMarshal(result)
	if err != nil || !bytes.Equal(body, canonical) {
		return errors.New("schema maintenance RPC JSON is not canonical or contains duplicate members")
	}
	return nil
}

func schemaMaintenanceRPCMarshal(value any) ([]byte, error) {
	var reply *schemaMaintenanceRPCReply
	switch item := value.(type) {
	case schemaMaintenanceRPCReply:
		reply = &item
	case *schemaMaintenanceRPCReply:
		copy := *item
		reply = &copy
	}
	if reply == nil || len(reply.Result) == 0 {
		return json.Marshal(value)
	}
	decoder := json.NewDecoder(bytes.NewReader(reply.Result))
	result, err := schemaMaintenanceRPCJSONValue(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("schema maintenance RPC result contains trailing JSON")
	}
	reply.Result, err = json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return json.Marshal(reply)
}

func schemaMaintenanceRPCJSONValue(decoder *json.Decoder) (any, error) {
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return token, nil
	}
	switch delimiter {
	case '{':
		object := make(map[string]any)
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			name, ok := key.(string)
			if !ok {
				return nil, errors.New("schema maintenance RPC object member is invalid")
			}
			if _, duplicate := object[name]; duplicate {
				return nil, errors.New("schema maintenance RPC contains duplicate JSON members")
			}
			value, err := schemaMaintenanceRPCJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			object[name] = value
		}
		if end, err := decoder.Token(); err != nil || end != json.Delim('}') {
			return nil, errors.New("schema maintenance RPC object is incomplete")
		}
		return object, nil
	case '[':
		var array []any
		for decoder.More() {
			value, err := schemaMaintenanceRPCJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		if end, err := decoder.Token(); err != nil || end != json.Delim(']') {
			return nil, errors.New("schema maintenance RPC array is incomplete")
		}
		return array, nil
	default:
		return nil, errors.New("schema maintenance RPC JSON delimiter is invalid")
	}
}

func schemaMaintenanceRPCWrite(connection *net.UnixConn, value any) error {
	body, err := schemaMaintenanceRPCMarshal(value)
	if err != nil {
		return err
	}
	if len(body)+1 > schemaMaintenanceRPCMaxBytes {
		return errors.New("schema maintenance RPC reply is oversized")
	}
	if err := connection.SetWriteDeadline(time.Now().Add(schemaMaintenanceRPCIOTimeout)); err != nil {
		return err
	}
	_, err = io.Copy(connection, bytes.NewReader(append(body, '\n')))
	return err
}

func schemaMaintenanceRPCHandle(ctx context.Context, connection *net.UnixConn, path string, socketInfo os.FileInfo, binding schemaMaintenanceRPCBinding, seen map[string]struct{}, handler schemaMaintenanceRPCHandler) {
	defer connection.Close()
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	if _, err := schemaMaintenanceGuardianPeer(connection, 0, 0); err != nil {
		return
	}
	current, err := schemaMaintenanceCheckGuardianSocket(path, 0)
	if err != nil || !os.SameFile(current, socketInfo) || schemaMaintenanceRPCProcess(binding) != nil {
		return
	}
	body, err := schemaMaintenanceRPCRead(connection, schemaMaintenanceRPCIOTimeout)
	if err != nil {
		return
	}
	var request schemaMaintenanceRPCRequest
	if err := schemaMaintenanceRPCDecode(body, &request); err != nil || request.Protocol != schemaMaintenanceRPCProtocol || request.Binding != binding || len(request.Nonce) != 64 || !productionSHA256Pattern.MatchString(request.Nonce) {
		return
	}
	timeout, err := schemaMaintenanceRPCActionTimeout(request.Action)
	if err != nil {
		return
	}
	if _, duplicate := seen[request.Nonce]; duplicate || len(seen) >= schemaMaintenanceRPCMaxRequests {
		return
	}
	seen[request.Nonce] = struct{}{} // Reserve before invoking a potentially mutating handler.
	if ctx.Err() != nil {
		return
	}
	current, err = schemaMaintenanceCheckGuardianSocket(path, 0)
	if err != nil || !os.SameFile(current, socketInfo) || schemaMaintenanceRPCProcess(binding) != nil {
		return
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	reply, handlerErr := handler(requestCtx, request)
	cancel()
	// Only the transport supplies correlation and success/error fields. A
	// handler cannot answer for a different plan, action, generation or nonce.
	reply.Protocol, reply.Binding, reply.Action, reply.Nonce = schemaMaintenanceRPCProtocol, binding, request.Action, request.Nonce
	reply.OK, reply.Error = handlerErr == nil, ""
	if handlerErr != nil {
		reply.Result, reply.Error = nil, handlerErr.Error()
	}
	// A lost response remains an unknown result: never repeat the handler here.
	_ = schemaMaintenanceRPCWrite(connection, reply)
}

func callSchemaMaintenanceRPC(ctx context.Context, workspace string, binding schemaMaintenanceRPCBinding, action string) (schemaMaintenanceRPCReply, error) {
	var reply schemaMaintenanceRPCReply
	if os.Geteuid() != 0 {
		return reply, errors.New("schema maintenance controller RPC requires root")
	}
	if err := validateSchemaMaintenanceRPCBinding(binding); err != nil {
		return reply, err
	}
	timeout, err := schemaMaintenanceRPCActionTimeout(action)
	if err != nil {
		return reply, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	path, err := schemaMaintenanceRPCSocketPath(workspace, binding)
	if err != nil {
		return reply, err
	}
	socketInfo, err := schemaMaintenanceCheckGuardianSocket(path, 0)
	if err != nil || schemaMaintenanceRPCProcess(binding) != nil {
		return reply, errors.New("schema maintenance RPC daemon socket or generation differs")
	}
	connection, err := (&net.Dialer{Timeout: schemaMaintenanceRPCIOTimeout}).DialContext(ctx, "unix", path)
	if err != nil {
		return reply, err
	}
	defer connection.Close()
	stream, ok := connection.(*net.UnixConn)
	if !ok {
		return reply, errors.New("schema maintenance RPC requires a Unix connection")
	}
	stop := context.AfterFunc(ctx, func() { _ = stream.Close() })
	defer stop()
	if _, err := schemaMaintenanceGuardianPeer(stream, 0, binding.DaemonPID); err != nil {
		return reply, err
	}
	current, err := schemaMaintenanceCheckGuardianSocket(path, 0)
	if err != nil || !os.SameFile(current, socketInfo) || schemaMaintenanceRPCProcess(binding) != nil {
		return reply, errors.New("schema maintenance RPC daemon changed before request")
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return reply, err
	}
	request := schemaMaintenanceRPCRequest{Protocol: schemaMaintenanceRPCProtocol, Binding: binding, Action: action, Nonce: hex.EncodeToString(nonce)}
	unknown := func(err error) (schemaMaintenanceRPCReply, error) {
		return schemaMaintenanceRPCReply{}, fmt.Errorf("schema maintenance RPC result is unknown; inspect status without retrying %s: %w", action, err)
	}
	if err := schemaMaintenanceRPCWrite(stream, request); err != nil {
		return unknown(err)
	}
	if err := stream.CloseWrite(); err != nil {
		return unknown(err)
	}
	// The peer may be executing worker for minutes. Its read/write calls stay
	// bounded, while this reply wait follows the caller's bounded action context.
	deadline, _ := ctx.Deadline()
	body, err := schemaMaintenanceRPCRead(stream, time.Until(deadline))
	if err != nil {
		return unknown(err)
	}
	if err := schemaMaintenanceRPCDecode(body, &reply); err != nil {
		return unknown(err)
	}
	if reply.Protocol != request.Protocol || reply.Binding != request.Binding || reply.Action != request.Action || reply.Nonce != request.Nonce || reply.OK && reply.Error != "" || !reply.OK && (reply.Error == "" || len(reply.Result) != 0 && string(reply.Result) != "null") {
		return unknown(errors.New("schema maintenance RPC reply correlation or result differs"))
	}
	if _, err := schemaMaintenanceGuardianPeer(stream, 0, binding.DaemonPID); err != nil || schemaMaintenanceRPCProcess(binding) != nil {
		return unknown(errors.New("schema maintenance RPC daemon generation changed before reply verification"))
	}
	current, err = schemaMaintenanceCheckGuardianSocket(path, 0)
	if err != nil || !os.SameFile(current, socketInfo) {
		return unknown(errors.New("schema maintenance RPC daemon socket changed before reply verification"))
	}
	if !reply.OK {
		return reply, fmt.Errorf("schema maintenance RPC %s failed without retry: %s", action, reply.Error)
	}
	return reply, nil
}
