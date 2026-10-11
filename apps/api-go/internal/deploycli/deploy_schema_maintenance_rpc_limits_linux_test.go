//go:build linux

package deploycli

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func schemaMaintenanceRPCTestPair(t *testing.T) (*net.UnixConn, *net.UnixConn) {
	t.Helper()
	descriptors, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	streams := make([]*net.UnixConn, 0, 2)
	for _, descriptor := range descriptors {
		file := os.NewFile(uintptr(descriptor), "schema-rpc-limit-fixture")
		connection, err := net.FileConn(file)
		_ = file.Close()
		if err != nil {
			t.Fatal(err)
		}
		stream := connection.(*net.UnixConn)
		streams = append(streams, stream)
		t.Cleanup(func() { _ = stream.Close() })
	}
	return streams[0], streams[1]
}

func TestSchemaMaintenanceRPCReplyLimitsAreActionAndDirectionBound(t *testing.T) {
	for action, expected := range map[string]int{
		"backup-export": (16 << 20) + 4096, "capture-backup-source": (8 << 20) + 4096,
		"worker": 64 << 10, "status": 64 << 10, "capture-ready": 64 << 10,
		"backup-export --path /tmp": 64 << 10, "capture-backup-source-extra": 64 << 10,
	} {
		if actual := schemaMaintenanceRPCReplyLimit(action); actual != expected {
			t.Fatalf("%q reply cap = %d, want %d", action, actual, expected)
		}
	}
	writer, _ := schemaMaintenanceRPCTestPair(t)
	for _, action := range []string{"backup-export", "capture-backup-source"} {
		request := schemaMaintenanceRPCRequest{Action: action, Nonce: strings.Repeat("a", schemaMaintenanceRPCMaxBytes)}
		if err := schemaMaintenanceRPCWrite(writer, request); err == nil {
			t.Fatalf("large-result action %q expanded the request cap", action)
		}
	}
	ordinary := schemaMaintenanceRPCReply{Action: "status", OK: true, Result: json.RawMessage(`{"padding":"` + strings.Repeat("x", schemaMaintenanceRPCMaxBytes) + `"}`)}
	if err := schemaMaintenanceRPCWrite(writer, ordinary); err == nil {
		t.Fatal("ordinary action expanded its reply cap")
	}
}

func TestSchemaMaintenanceRPCLargeRepliesUseExactFramedBounds(t *testing.T) {
	for _, action := range []string{"capture-backup-source", "backup-export"} {
		t.Run(action, func(t *testing.T) {
			limit := schemaMaintenanceRPCReplyLimit(action)
			reply := schemaMaintenanceRPCReply{Action: action, OK: true, Result: json.RawMessage(`{"padding":""}`)}
			empty, err := schemaMaintenanceRPCMarshal(reply)
			if err != nil {
				t.Fatal(err)
			}
			// The cap includes both the envelope and its final newline.
			reply.Result = json.RawMessage(`{"padding":"` + strings.Repeat("x", limit-len(empty)-1) + `"}`)
			writer, reader := schemaMaintenanceRPCTestPair(t)
			written := make(chan error, 1)
			go func() {
				err := schemaMaintenanceRPCWrite(writer, reply)
				_ = writer.CloseWrite()
				written <- err
			}()
			body, err := schemaMaintenanceRPCRead(reader, 10*time.Second, limit)
			if err != nil {
				t.Fatal(err)
			}
			if err := <-written; err != nil {
				t.Fatal(err)
			}
			if len(body)+1 != limit {
				t.Fatalf("framed reply size %d, want %d", len(body)+1, limit)
			}
			var actual schemaMaintenanceRPCReply
			if err := schemaMaintenanceRPCDecode(body, &actual); err != nil || actual.Action != action || !bytes.Equal(actual.Result, reply.Result) {
				t.Fatalf("large typed canonical reply was not preserved: %v", err)
			}
			reply.Result = json.RawMessage(`{"padding":"` + strings.Repeat("x", limit-len(empty)) + `"}`)
			if err := schemaMaintenanceRPCWrite(writer, reply); err == nil {
				t.Fatal("one-byte oversized reply was accepted")
			}
		})
	}
}

func TestSchemaMaintenanceRPCReadRejectsOversizeRequestsAndArbitraryBounds(t *testing.T) {
	writer, reader := schemaMaintenanceRPCTestPair(t)
	if _, err := schemaMaintenanceRPCRead(reader, time.Second, (32<<20)+4096); err == nil {
		t.Fatal("caller-selected message limit was accepted")
	}
	written := make(chan error, 1)
	go func() {
		_, err := io.Copy(writer, strings.NewReader(strings.Repeat("x", schemaMaintenanceRPCMaxBytes)+"\n"))
		_ = writer.CloseWrite()
		written <- err
	}()
	if _, err := schemaMaintenanceRPCRead(reader, time.Second, schemaMaintenanceRPCMaxBytes); err == nil {
		t.Fatal("server request reader accepted an oversized body")
	}
	_ = reader.Close()
	<-written
}

func TestSchemaMaintenanceRPCNestedEmptyCollectionsRoundTrip(t *testing.T) {
	for _, result := range []string{
		`[]`, `{}`,
		`{"array":[],"nested":[[],{},null,{"indexes":[],"sequences":[]}],"null":null,"object":{}}`,
	} {
		t.Run(result, func(t *testing.T) {
			reply := schemaMaintenanceRPCReply{Action: "capture-backup-source", OK: true, Result: json.RawMessage(result)}
			body, err := schemaMaintenanceRPCMarshal(reply)
			if err != nil {
				t.Fatal(err)
			}
			var actual schemaMaintenanceRPCReply
			if err := schemaMaintenanceRPCDecode(body, &actual); err != nil {
				t.Fatalf("canonical reply roundtrip failed: %v", err)
			}
			if string(actual.Result) != result {
				t.Fatalf("empty collection or null changed: got %s, want %s", actual.Result, result)
			}
			second, err := schemaMaintenanceRPCMarshal(actual)
			if err != nil || !bytes.Equal(second, body) {
				t.Fatalf("canonical result was not stable: %v", err)
			}
		})
	}
}
