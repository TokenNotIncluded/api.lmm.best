package coreclient

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"math/rand/v2"
	"time"

	pb "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/corepb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const MaxEventPayload = 16 * 1024

var eventFeatures = []string{"events.pull.v1", "events.ack.v1", "events.retry.v1"}

func eventVersion() *pb.EventClientVersion {
	return &pb.EventClientVersion{ProtocolMajor: 1, ProtocolMinor: 1, RequiredFeatures: append([]string(nil), eventFeatures...)}
}
func eventName(s string, max int) bool {
	if len(s) == 0 || len(s) > max {
		return false
	}
	for i := 0; i < len(s); i++ {
		b := s[i]
		if !((b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '.' || b == '_' || b == '-') {
			return false
		}
	}
	return true
}
func eventKey(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 33 || s[i] > 126 {
			return false
		}
	}
	return true
}

// EventFingerprint is shared with Rust NewEvent::fingerprint. It hashes the
// original payload bytes, not an implementation-specific protobuf re-encoding.
func EventFingerprint(e *pb.EventEnvelope) ([32]byte, error) {
	if e == nil || e.EventId <= 0 || !eventKey(e.EventKey) || !eventName(e.EventType, 96) || e.SchemaVersion == 0 || e.SchemaVersion > 2147483647 || e.ResourceId <= 0 || e.ResourceVersion < 0 || len(e.Payload) > MaxEventPayload {
		return [32]byte{}, status.Error(codes.InvalidArgument, "invalid event envelope")
	}
	h := sha256.New()
	_, _ = h.Write([]byte("lmm.event.v1\x00"))
	field := func(b []byte) {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(b)))
		_, _ = h.Write(n[:])
		_, _ = h.Write(b)
	}
	field([]byte(e.EventKey))
	field([]byte(e.EventType))
	var n [8]byte
	binary.BigEndian.PutUint32(n[:4], e.SchemaVersion)
	_, _ = h.Write(n[:4])
	binary.BigEndian.PutUint64(n[:], uint64(e.ResourceId))
	_, _ = h.Write(n[:])
	binary.BigEndian.PutUint64(n[:], uint64(e.ResourceVersion))
	_, _ = h.Write(n[:])
	field(e.Payload)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out, nil
}
func (c *Client) NegotiateEvents(ctx context.Context, consumer string) (*pb.NegotiateEventsResponse, error) {
	if !eventName(consumer, 64) {
		return nil, status.Error(codes.InvalidArgument, "invalid consumer")
	}
	r, err := c.events.Negotiate(c.context(ctx, ""), &pb.NegotiateEventsRequest{ConsumerId: consumer, Version: eventVersion()})
	if err != nil {
		return nil, err
	}
	if r.GetProtocolMajor() != 1 || r.GetProtocolMinor() < 1 || r.GetMaxBatch() == 0 || r.GetMaxBatch() > 8 || r.GetMaxPayloadBytes() > MaxEventPayload || r.GetMaxPayloadBytes() == 0 || r.GetLeaseSeconds() == 0 || r.GetLeaseSeconds() > 300 {
		return nil, status.Error(codes.FailedPrecondition, "unsupported event server limits or version")
	}
	for _, want := range eventFeatures {
		found := false
		for _, got := range r.Features {
			if want == got {
				found = true
				break
			}
		}
		if !found {
			return nil, status.Error(codes.FailedPrecondition, "required event feature unavailable")
		}
	}
	return r, nil
}
func (c *Client) PullEvents(ctx context.Context, consumer string, maximum uint32) (*pb.PullEventsResponse, error) {
	if !eventName(consumer, 64) || maximum == 0 || maximum > 8 {
		return nil, status.Error(codes.InvalidArgument, "invalid event pull")
	}
	return c.events.Pull(c.context(ctx, ""), &pb.PullEventsRequest{ConsumerId: consumer, Version: eventVersion(), MaxEvents: maximum})
}
func (c *Client) AcknowledgeEvent(ctx context.Context, consumer string, id int64, token []byte) (*pb.AcknowledgeEventResponse, error) {
	if !eventName(consumer, 64) || id <= 0 || len(token) != 32 {
		return nil, status.Error(codes.InvalidArgument, "invalid acknowledgement")
	}
	return c.events.Acknowledge(c.context(ctx, ""), &pb.AcknowledgeEventRequest{ConsumerId: consumer, EventId: id, LeaseToken: token})
}
func (c *Client) RetryEvent(ctx context.Context, consumer string, id int64, token []byte, reason pb.EventRetryReason) (*pb.RetryEventResponse, error) {
	if !eventName(consumer, 64) || id <= 0 || len(token) != 32 || reason < 1 || reason > 3 {
		return nil, status.Error(codes.InvalidArgument, "invalid event retry")
	}
	return c.events.Retry(c.context(ctx, ""), &pb.RetryEventRequest{ConsumerId: consumer, EventId: id, LeaseToken: token, Reason: reason})
}

// EventProcessor must durably commit its inbox and local business effects before
// returning nil. Use SQLInbox.Apply. Do not acknowledge external network effects
// as exactly-once: enqueue those in the extension's own transactional outbox.
type EventProcessor func(context.Context, *pb.EventEnvelope) error

// DrainEvents negotiates on every batch, including after a reconnect or upgrade.
// Unsupported versions are retained and reported, never silently acknowledged.
func (c *Client) DrainEvents(ctx context.Context, consumer string, supported map[string]uint32, process EventProcessor) (int, error) {
	if process == nil {
		return 0, status.Error(codes.InvalidArgument, "event processor is required")
	}
	caps, err := c.NegotiateEvents(ctx, consumer)
	if err != nil {
		return 0, err
	}
	for _, kind := range caps.EventTypes {
		if supported[kind] == 0 {
			return 0, status.Error(codes.FailedPrecondition, "consumer lacks a subscribed event handler")
		}
	}
	response, err := c.PullEvents(ctx, consumer, caps.MaxBatch)
	if err != nil {
		return 0, err
	}
	if len(response.GetDeliveries()) > int(caps.MaxBatch) {
		return 0, status.Error(codes.DataLoss, "event batch exceeds negotiated limit")
	}
	completed := 0
	for _, d := range response.Deliveries {
		if d == nil || len(d.LeaseToken) != 32 || d.Attempt == 0 {
			return completed, status.Error(codes.DataLoss, "invalid event delivery")
		}
		event := d.GetEvent()
		fingerprint, validation := EventFingerprint(event)
		if validation != nil || len(event.GetContentSha256()) != 32 || !equalDigest(fingerprint[:], event.GetContentSha256()) {
			return completed, status.Error(codes.DataLoss, "event fingerprint mismatch")
		}
		if event.SchemaVersion > supported[event.EventType] || supported[event.EventType] == 0 {
			_, _ = c.RetryEvent(ctx, consumer, event.EventId, d.LeaseToken, pb.EventRetryReason_EVENT_RETRY_REASON_UNSUPPORTED_EVENT)
			return completed, status.Error(codes.FailedPrecondition, "unsupported event payload version")
		}
		deadline := time.Duration(caps.LeaseSeconds) * time.Second / 2
		if deadline > 15*time.Second {
			deadline = 15 * time.Second
		}
		bounded, cancel := context.WithTimeout(ctx, deadline)
		err = process(bounded, event)
		cancel()
		if err != nil {
			_, _ = c.RetryEvent(ctx, consumer, event.EventId, d.LeaseToken, pb.EventRetryReason_EVENT_RETRY_REASON_HANDLER_FAILURE)
			return completed, err
		}
		if _, err = c.AcknowledgeEvent(ctx, consumer, event.EventId, d.LeaseToken); err != nil {
			return completed, err
		}
		completed++
	}
	return completed, nil
}

// RunEvents has one bounded batch in memory. Retries and leases live in core
// PostgreSQL, so exiting this loop or killing Go does not lose accepted events.
func (c *Client) RunEvents(ctx context.Context, consumer string, supported map[string]uint32, process EventProcessor) error {
	known := make(map[string]uint32, len(supported))
	for k, v := range supported {
		known[k] = v
	}
	backoff := 250 * time.Millisecond
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := c.DrainEvents(ctx, consumer, known, process)
		if err != nil {
			switch status.Code(err) {
			case codes.Unauthenticated, codes.PermissionDenied, codes.InvalidArgument, codes.FailedPrecondition, codes.Unimplemented, codes.NotFound, codes.AlreadyExists, codes.DataLoss:
				return err
			}
		} else {
			backoff = 250 * time.Millisecond
			if n > 0 {
				continue
			}
		}
		delay := backoff + time.Duration(rand.Int64N(int64(backoff/4)+1))
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if err != nil {
			backoff *= 2
			if backoff > 15*time.Second {
				backoff = 15 * time.Second
			}
		}
	}
}
func equalDigest(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var different byte
	for i := range a {
		different |= a[i] ^ b[i]
	}
	return different == 0
}
