package rustbridge

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/coreclient"
	pb "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/corepb"
	p "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/payments"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestUnsignedEvidenceAndUnknownReceiptsFailClosed(t *testing.T) {
	for _, e := range []p.Evidence{{Source: "api", Payload: []byte(`{"paid":true}`)}, {Source: "webhook", Payload: []byte("body")}, {Source: "webhook", Signature: []byte("signature")}} {
		if _, err := evidence(e); !errors.Is(err, p.ErrEvidence) {
			t.Fatal("unsigned evidence accepted", err)
		}
	}
	for _, state := range []pb.PaymentReceiptState{pb.PaymentReceiptState_PAYMENT_RECEIPT_STATE_UNSPECIFIED, pb.PaymentReceiptState(999)} {
		if _, err := convert(&pb.PaymentReceipt{State: state}, nil); !errors.Is(err, p.ErrUnavailable) {
			t.Fatal("unknown receipt accepted", err)
		}
	}
	if _, err := convert(nil, status.Error(codes.Unimplemented, "not registered")); !errors.Is(err, p.ErrUnavailable) {
		t.Fatal("missing Rust service became success", err)
	}
	e := p.Evidence{Provider: "stripe", Merchant: "acct_fixture", Environment: "test", Transaction: "pi_fixture", EventID: "evt_fixture", Source: "webhook", Payload: []byte("exact bytes"), Signature: []byte("exact signature")}
	v, err := evidence(e)
	if err != nil || string(v.SignedPayload) != "exact bytes" || string(v.Signature) != "exact signature" || v.Payment.Merchant != "acct_fixture" {
		t.Fatal(v, err)
	}
	e.Payload[0] = 'X'
	if string(v.SignedPayload) != "exact bytes" {
		t.Fatal("proof aliases caller buffer")
	}
}

type wireServer struct {
	pb.UnimplementedCorePaymentsServer
	t *testing.T
}

func (s *wireServer) PrepareTopup(ctx context.Context, r *pb.PrepareTopupRequest) (*pb.PaymentIntent, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	if len(md.Get("authorization")) != 1 || md.Get("authorization")[0] != "Bearer "+strings.Repeat("s", 32) || len(md.Get("x-lmm-user-credential")) != 1 || md.Get("x-lmm-user-credential")[0] != strings.Repeat("u", 32) || md.Get("x-lmm-protocol")[0] != "1" || len(md.Get("attacker")) != 0 {
		s.t.Error("caller metadata was forwarded", md)
	}
	return &pb.PaymentIntent{IntentId: 1, AccountId: r.AccountId, CreatedByUserId: 7, Provider: "stripe", Merchant: "acct_fixture", Environment: "test", Currency: r.Currency, AmountMinor: r.AmountMinor, CreditUnits: 500000, CreditUnit: "credit_500k_usd", IdempotencyKey: r.Mutation.IdempotencyKey, RequestSha256: make([]byte, 32)}, nil
}
func (s *wireServer) CreditTopup(ctx context.Context, r *pb.CreditTopupRequest) (*pb.PaymentReceipt, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	if len(md.Get("x-lmm-user-credential")) != 0 || len(md.Get("attacker")) != 0 {
		s.t.Error("provider worker forwarded user authority", md)
	}
	return nil, status.Error(codes.Unimplemented, "task02/task06 pending")
}
func TestControlledUnixRPCAndUnimplementedFailClosed(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "core.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	pb.RegisterCorePaymentsServer(server, &wireServer{t: t})
	go func() { _ = server.Serve(l) }()
	defer server.Stop()
	client, err := coreclient.New(socket, []byte(strings.Repeat("s", 32)))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	b, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "attacker", "x-lmm-user-credential", "wrong", "x-lmm-protocol", "99", "attacker", "value"))
	intent, err := b.Prepare(ctx, strings.Repeat("u", 32), p.PrepareRequest{Key: "create_fixture_0001", AccountID: 9007199254740993, ChannelID: 1, Currency: "USD", AmountMinor: 1000})
	if err != nil || intent.AccountID != 9007199254740993 {
		t.Fatal("account precision/authority lost", intent, err)
	}
	proof := p.Evidence{Source: "webhook", Payload: []byte("body"), Signature: []byte("signature")}
	if _, err = b.Credit(ctx, intent.ID, "credit_fixture_0001", proof); !errors.Is(err, p.ErrUnavailable) {
		t.Fatal("unimplemented RPC succeeded", err)
	}
	if _, err = b.Prepare(ctx, "short", p.PrepareRequest{}); !errors.Is(err, p.ErrDenied) {
		t.Fatal("bad credential reached RPC", err)
	}
}
