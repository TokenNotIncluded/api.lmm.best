// Fixture only: actual Go gRPC client and extension-owned PostgreSQL inbox.
package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	client "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/coreclient"
	pb "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/corepb"
	_ "github.com/lib/pq"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const consumer = "fixture.consumer"
const large = int64(9007199254740993)

var known = map[string]uint32{"test.counter.v1": 1}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func check(ok bool, message string) {
	if !ok {
		panic(message)
	}
}
func expect(err error, code codes.Code) {
	check(status.Code(err) == code, fmt.Sprintf("expected %s, received %s", code, status.Code(err)))
}
func read(name string) []byte { b, err := os.ReadFile(os.Getenv(name)); must(err); return b }

func main() {
	check(os.Getenv("LMM_EVENTS_FIXTURE") == "1", "explicit fixture opt-in required")
	check(len(os.Args) == 2, "one fixture mode required")
	u, err := url.Parse(os.Getenv("MK06_EXTENSION_URL"))
	must(err)
	check(strings.HasPrefix(u.Path, "/mk06_ext_"), "only disposable extension database is accepted")
	db, err := sql.Open("postgres", u.String())
	must(err)
	defer db.Close()
	db.SetMaxOpenConns(2)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	token := read("MK06_SERVICE_FILE")
	c, err := client.New(os.Getenv("MK06_SOCKET"), token)
	must(err)
	defer c.Close()
	mode := os.Args[1]
	if mode == "setup" {
		_, err = db.ExecContext(ctx, client.SQLInboxSchema+"\nCREATE TABLE fixture_effects (singleton boolean PRIMARY KEY DEFAULT true, amount bigint NOT NULL); INSERT INTO fixture_effects VALUES(true,0);")
		must(err)
		fmt.Println("extension inbox initialized in separate database")
		return
	}
	inbox, err := client.NewSQLInbox(db, consumer)
	must(err)
	handler := func(ctx context.Context, tx *sql.Tx, e *pb.EventEnvelope) error {
		_, err := tx.ExecContext(ctx, "UPDATE fixture_effects SET amount=amount+1 WHERE singleton")
		return err
	}
	apply := func(ctx context.Context, e *pb.EventEnvelope) error {
		_, err := inbox.Apply(ctx, e, handler)
		return err
	}
	switch mode {
	case "auth-wire":
		user := string(read("MK06_USER_FILE"))
		p, err := c.Authorize(ctx, user)
		must(err)
		check(p.UserId == large, "large user ID changed")
		_, err = c.Authorize(ctx, strings.Repeat("x", 64))
		expect(err, codes.Unauthenticated)
		bad, err := client.New(os.Getenv("MK06_SOCKET"), []byte(strings.Repeat("z", 64)))
		must(err)
		_, err = bad.Authorize(ctx, user)
		expect(err, codes.Unauthenticated)
		_, err = bad.NegotiateEvents(ctx, consumer)
		expect(err, codes.Unauthenticated)
		must(bad.Close())
		_, err = c.NegotiateEvents(ctx, "other.consumer")
		expect(err, codes.PermissionDenied)
		_, err = c.PullEvents(ctx, consumer, 0)
		expect(err, codes.InvalidArgument)
		_, err = c.PullEvents(ctx, consumer, 9)
		expect(err, codes.InvalidArgument)
		// The high-level client strips forged caller credentials and versions.
		forged := metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer forged", "x-lmm-protocol", "2", "x-lmm-user-credential", user))
		_, err = c.NegotiateEvents(forged, consumer)
		must(err)
		conn, err := grpc.NewClient("passthrough:///fixture", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", os.Getenv("MK06_SOCKET"))
		}))
		must(err)
		defer conn.Close()
		rpc := pb.NewCoreEventsClient(conn)
		auth := metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+string(token), "x-lmm-protocol", "1"))
		req := &pb.NegotiateEventsRequest{ConsumerId: consumer, Version: &pb.EventClientVersion{ProtocolMajor: 1, ProtocolMinor: 999, RequiredFeatures: []string{"events.pull.v1"}}}
		// Go -> Rust: unknown field accepted, newer minor accepted for supported features.
		req.ProtoReflect().SetUnknown([]byte{0x98, 0x06, 0x01})
		wire, err := proto.Marshal(req)
		must(err)
		var roundtrip pb.NegotiateEventsRequest
		must(proto.Unmarshal(wire, &roundtrip))
		check(bytes.Equal(roundtrip.ProtoReflect().GetUnknown(), []byte{0x98, 0x06, 0x01}), "Go lost unknown fields")
		_, err = rpc.Negotiate(auth, &roundtrip)
		must(err)
		req.Version.ProtocolMajor = 2
		_, err = rpc.Negotiate(auth, req)
		expect(err, codes.FailedPrecondition)
		req.Version.ProtocolMajor = 1
		req.Version.RequiredFeatures = []string{"events.unknown.v9"}
		_, err = rpc.Negotiate(auth, req)
		expect(err, codes.FailedPrecondition)
		noService := metadata.NewOutgoingContext(ctx, metadata.Pairs("x-lmm-user-credential", user, "x-lmm-protocol", "1"))
		_, err = rpc.Negotiate(noService, req)
		expect(err, codes.Unauthenticated)
		fmt.Println("PASS service/user separation, subscription scope, versions, unknown request fields and large user ID")
	case "crash-during-handler", "commit-no-ack", "redeliver-ack":
		_, err = c.NegotiateEvents(ctx, consumer)
		must(err)
		var d *pb.EventDelivery
		for d == nil {
			batch, e := c.PullEvents(ctx, consumer, 1)
			must(e)
			if len(batch.Deliveries) > 0 {
				d = batch.Deliveries[0]
				break
			}
			select {
			case <-ctx.Done():
				panic("delivery timeout")
			case <-time.After(100 * time.Millisecond):
			}
		}
		check(d.Event.EventId >= large && d.Event.ResourceId == large && d.Event.ResourceVersion == math.MaxInt64, "large IDs or resource version changed")
		var account pb.Account
		must(proto.Unmarshal(d.Event.Payload, &account))
		check(account.Id == math.MaxInt64 && bytes.Equal(account.ProtoReflect().GetUnknown(), []byte{0x98, 0x06, 0x01}), "Rust -> Go opaque payload or unknown fields changed")
		digest, err := client.EventFingerprint(d.Event)
		must(err)
		check(bytes.Equal(digest[:], d.Event.ContentSha256), "cross-language digest mismatch")
		if mode == "crash-during-handler" {
			_, err = inbox.Apply(ctx, d.Event, func(ctx context.Context, tx *sql.Tx, e *pb.EventEnvelope) error {
				must(handler(ctx, tx, e))
				must(os.WriteFile(os.Getenv("MK06_MARKER"), []byte("uncommitted"), 0600))
				<-ctx.Done()
				return ctx.Err()
			})
			must(err)
			panic("fixture must be killed inside transaction")
		}
		if mode == "commit-no-ack" {
			_, err = inbox.Apply(ctx, d.Event, func(ctx context.Context, tx *sql.Tx, e *pb.EventEnvelope) error {
				must(handler(ctx, tx, e))
				return errors.New("injected local handler failure")
			})
			check(err != nil, "failed handler unexpectedly committed")
			duplicate, err := inbox.Apply(ctx, d.Event, handler)
			must(err)
			check(!duplicate, "first committed effect was incorrectly deduplicated")
			duplicate, err = inbox.Apply(ctx, d.Event, handler)
			must(err)
			check(duplicate, "duplicate reran side effect")
			changed := proto.Clone(d.Event).(*pb.EventEnvelope)
			changed.ResourceVersion--
			h, err := client.EventFingerprint(changed)
			must(err)
			changed.ContentSha256 = h[:]
			_, err = inbox.Apply(ctx, changed, handler)
			expect(err, codes.AlreadyExists)
			fmt.Println("PASS committed local effect, duplicate inbox and conflicting content; exit WITHOUT ACK")
			return
		}
		duplicate, err := inbox.Apply(ctx, d.Event, handler)
		must(err)
		check(duplicate, "crash replay reran committed side effect")
		wire, err := proto.Marshal(d)
		must(err)
		must(os.WriteFile(os.Getenv("MK06_RECEIPT"), wire, 0600))
		// Inject a lost application acknowledgement: do not preserve its response,
		// then terminate. The next process repeats this token after Rust restarts.
		_, err = c.AcknowledgeEvent(ctx, consumer, d.Event.EventId, d.LeaseToken)
		must(err)
		fmt.Println("PASS redelivery deduplicated; ACK response discarded")
	case "repeat-ack":
		var d pb.EventDelivery
		must(proto.Unmarshal(read("MK06_RECEIPT"), &d))
		reply, err := c.AcknowledgeEvent(ctx, consumer, d.Event.EventId, d.LeaseToken)
		must(err)
		check(reply.AlreadyAcknowledged, "ACK receipt did not survive Rust restart")
		fmt.Println("PASS lost ACK repeat after Rust restart")
	case "watch":
		_, err = c.NegotiateEvents(ctx, consumer)
		must(err)
		must(os.WriteFile(os.Getenv("MK06_MARKER"), []byte("watching"), 0600))
		must(c.RunEvents(ctx, consumer, known, apply))
	case "drain":
		n, err := c.DrainEvents(ctx, consumer, known, apply)
		must(err)
		check(n > 0, "expected offline backlog")
		fmt.Println("PASS offline backlog drained")
	default:
		panic("unknown fixture mode")
	}
}
