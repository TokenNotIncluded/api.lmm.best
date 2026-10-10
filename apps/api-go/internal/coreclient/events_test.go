package coreclient

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	pb "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/corepb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// These unit tests exercise client guards. Process/database acceptance is in
// contracts/proto/tests/process_recovery.py, not this fake server.
type guardEvents struct {
	pb.UnimplementedCoreEventsServer
	event *pb.EventEnvelope
	ack   atomic.Int32
	retry atomic.Int32
}

func (s *guardEvents) Negotiate(context.Context, *pb.NegotiateEventsRequest) (*pb.NegotiateEventsResponse, error) {
	return &pb.NegotiateEventsResponse{ProtocolMajor: 1, ProtocolMinor: 1, Features: eventFeatures, MaxBatch: 8, MaxPayloadBytes: MaxEventPayload, LeaseSeconds: 30, EventTypes: []string{"test.counter.v1"}}, nil
}
func (s *guardEvents) Pull(context.Context, *pb.PullEventsRequest) (*pb.PullEventsResponse, error) {
	return &pb.PullEventsResponse{Deliveries: []*pb.EventDelivery{{Event: s.event, LeaseToken: make([]byte, 32), Attempt: 1}}}, nil
}
func (s *guardEvents) Acknowledge(context.Context, *pb.AcknowledgeEventRequest) (*pb.AcknowledgeEventResponse, error) {
	s.ack.Add(1)
	return &pb.AcknowledgeEventResponse{}, nil
}
func (s *guardEvents) Retry(context.Context, *pb.RetryEventRequest) (*pb.RetryEventResponse, error) {
	s.retry.Add(1)
	return &pb.RetryEventResponse{}, nil
}
func eventClient(t *testing.T, s *guardEvents) *Client {
	t.Helper()
	dir, err := os.MkdirTemp("", "mk06-client-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "core.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	pb.RegisterCoreEventsServer(server, s)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	c, err := New(socket, []byte(testToken))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}
func fixtureEnvelope(t *testing.T) *pb.EventEnvelope {
	t.Helper()
	e := &pb.EventEnvelope{EventId: 9007199254740993, EventKey: "test-key", EventType: "test.counter.v1", SchemaVersion: 1, ResourceId: 9223372036854775807, ResourceVersion: 1, Payload: []byte{0x98, 0x06, 0x01}}
	h, err := EventFingerprint(e)
	if err != nil {
		t.Fatal(err)
	}
	e.ContentSha256 = h[:]
	return e
}
func TestEventProcessorGuardsNeverAcknowledgeUncommittedWork(t *testing.T) {
	for _, mode := range []string{"bad-fingerprint", "unsupported-version", "handler-failure", "success"} {
		t.Run(mode, func(t *testing.T) {
			s := &guardEvents{event: fixtureEnvelope(t)}
			if mode == "bad-fingerprint" {
				s.event.ContentSha256[0] ^= 1
			}
			if mode == "unsupported-version" {
				s.event.SchemaVersion = 2
				h, err := EventFingerprint(s.event)
				if err != nil {
					t.Fatal(err)
				}
				s.event.ContentSha256 = h[:]
			}
			c := eventClient(t, s)
			calls := 0
			n, err := c.DrainEvents(context.Background(), "fixture.consumer", map[string]uint32{"test.counter.v1": 1}, func(ctx context.Context, _ *pb.EventEnvelope) error {
				calls++
				if _, ok := ctx.Deadline(); !ok {
					t.Error("handler has no deadline")
				}
				if mode == "handler-failure" {
					return errors.New("injected rollback")
				}
				return nil
			})
			switch mode {
			case "bad-fingerprint":
				if status.Code(err) != codes.DataLoss || calls != 0 || s.retry.Load() != 0 {
					t.Fatalf("guard failed: %v", err)
				}
			case "unsupported-version":
				if status.Code(err) != codes.FailedPrecondition || calls != 0 || s.retry.Load() != 1 {
					t.Fatalf("version guard failed: %v", err)
				}
			case "handler-failure":
				if err == nil || calls != 1 || s.retry.Load() != 1 {
					t.Fatalf("failed handler guard: %v", err)
				}
			case "success":
				if err != nil || calls != 1 || n != 1 || s.ack.Load() != 1 {
					t.Fatalf("success failed: %v", err)
				}
			}
			if mode != "success" && (n != 0 || s.ack.Load() != 0) {
				t.Fatal("uncommitted/unsupported/corrupt event was acknowledged")
			}
		})
	}
}
func TestEventInputAndOpaquePayloadRoundTrip(t *testing.T) {
	e := fixtureEnvelope(t)
	wire, err := proto.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	wire = append(wire, 0x98, 0x06, 0x01)
	var decoded pb.EventEnvelope
	if err = proto.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.EventId != e.EventId || decoded.ResourceId != e.ResourceId || !bytes.Equal(decoded.Payload, e.Payload) || len(decoded.ProtoReflect().GetUnknown()) == 0 {
		t.Fatal("wire values changed")
	}
	h, err := EventFingerprint(&decoded)
	if err != nil || !bytes.Equal(h[:], e.ContentSha256) {
		t.Fatal("opaque fingerprint changed")
	}
	for _, bad := range []*pb.EventEnvelope{nil, {}, {EventId: -1}} {
		if _, err = EventFingerprint(bad); status.Code(err) != codes.InvalidArgument {
			t.Fatal("accepted invalid envelope")
		}
	}
	c := eventClient(t, &guardEvents{event: e})
	if _, err = c.DrainEvents(context.Background(), "fixture.consumer", nil, nil); status.Code(err) != codes.InvalidArgument {
		t.Fatal("accepted nil processor")
	}
	if _, err = c.AcknowledgeEvent(context.Background(), "fixture.consumer", e.EventId, make([]byte, 31)); status.Code(err) != codes.InvalidArgument {
		t.Fatal("accepted short receipt")
	}
	if _, err = c.RetryEvent(context.Background(), "fixture.consumer", e.EventId, make([]byte, 32), pb.EventRetryReason(99)); status.Code(err) != codes.InvalidArgument {
		t.Fatal("accepted unknown retry reason")
	}
}
