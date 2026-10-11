package rustbridge

import (
	"context"
	"crypto/sha256"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"sync"
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
	for _, state := range []pb.PaymentReceiptState{pb.PaymentReceiptState_PAYMENT_RECEIPT_STATE_UNSPECIFIED, pb.PaymentReceiptState_PAYMENT_RECEIPT_STATE_PREPARED, pb.PaymentReceiptState(999)} {
		if _, err := convert(&pb.PaymentReceipt{State: state}, nil); !errors.Is(err, p.ErrUnavailable) {
			t.Fatal("unknown receipt accepted", err)
		}
	}
	if _, err := convert(nil, status.Error(codes.Unimplemented, "not registered")); !errors.Is(err, p.ErrUnavailable) {
		t.Fatal("missing Rust service became success", err)
	}
	e := p.Evidence{Provider: "stripe", Merchant: "acct_fixture", Environment: "test", Transaction: "pi_fixture", EventID: "evt_fixture", Source: "webhook", Payload: []byte("exact bytes"), Signature: []byte("exact signature"), AcceptedProofID: "proof_fixture"}
	v, err := evidence(e)
	if err != nil || v.AcceptedProofId != "proof_fixture" || string(v.SignedPayload) != "exact bytes" || string(v.Signature) != "exact signature" || v.Payment.Merchant != "acct_fixture" {
		t.Fatal(v, err)
	}
	e.Payload[0] = 'X'
	if string(v.SignedPayload) != "exact bytes" {
		t.Fatal("proof aliases caller buffer")
	}
}

type readServer struct {
	pb.UnimplementedCorePaymentsServer
	mu      sync.Mutex
	receipt *pb.PaymentReceipt
}

func (s *readServer) GetPaymentReceipt(_ context.Context, _ *pb.GetPaymentReceiptRequest) (*pb.PaymentReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.receipt, nil
}

func TestPreparedReceiptAuthorizesReadButNeverBecomesMoneySuccess(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "core.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	wire := &readServer{receipt: &pb.PaymentReceipt{ReceiptId: "pay_prepared", IntentId: 1, IdempotencyKey: "prepare_fixture_0001", RequestSha256: make([]byte, 32), State: pb.PaymentReceiptState_PAYMENT_RECEIPT_STATE_PREPARED}}
	server := grpc.NewServer()
	pb.RegisterCorePaymentsServer(server, wire)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	client, err := coreclient.New(socket, []byte(strings.Repeat("s", 32)))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	bridge, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	intent := p.Intent{ID: 1, Key: "prepare_fixture_0001"}
	if err = bridge.Read(context.Background(), strings.Repeat("u", 32), intent); err != nil {
		t.Fatal("durable intent read denied", err)
	}
	if _, err = convert(wire.receipt, nil); !errors.Is(err, p.ErrUnavailable) {
		t.Fatal("prepared intent became money success", err)
	}
	wire.mu.Lock()
	wire.receipt = &pb.PaymentReceipt{ReceiptId: "pay_prepared", IntentId: 1, IdempotencyKey: intent.Key, RequestSha256: make([]byte, 32), State: pb.PaymentReceiptState_PAYMENT_RECEIPT_STATE_PREPARED, JournalId: 10}
	wire.mu.Unlock()
	if err = bridge.Read(context.Background(), strings.Repeat("u", 32), intent); !errors.Is(err, p.ErrUnavailable) {
		t.Fatal("prepared intent invented journal", err)
	}
}

type wireServer struct {
	pb.UnimplementedCorePaymentsServer
	t *testing.T
}

func (s *wireServer) AcceptProviderProof(ctx context.Context, r *pb.AcceptProviderProofRequest) (*pb.ProviderProofReceipt, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	if len(md.Get("x-lmm-user-credential")) != 0 || len(md.Get("attacker")) != 0 || r.Evidence.AcceptedProofId != "" || string(r.Evidence.SignedPayload) != "exact-original-bytes" || string(r.Evidence.Signature) != "original-signature" {
		s.t.Error("proof ingress changed evidence or forwarded user authority")
	}
	h := sha256.Sum256(r.Evidence.SignedPayload)
	return &pb.ProviderProofReceipt{ProofId: "proof_fixture", IntentId: r.IntentId, ProviderEventId: r.Evidence.ProviderEventId, PayloadSha256: h[:], Payment: r.Evidence.Payment, AcceptedUnixSeconds: 1}, nil
}
func (s *wireServer) GetProviderProof(ctx context.Context, r *pb.GetProviderProofRequest) (*pb.ProviderProofReceipt, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	h := sha256.Sum256([]byte("exact-original-bytes"))
	if len(md.Get("x-lmm-user-credential")) != 0 || len(md.Get("attacker")) != 0 || string(r.PayloadSha256) != string(h[:]) {
		s.t.Error("proof lookup omitted raw payload binding or forwarded caller authority")
	}
	return &pb.ProviderProofReceipt{ProofId: "proof_fixture", IntentId: r.IntentId, ProviderEventId: r.ProviderEventId, PayloadSha256: h[:], Payment: &pb.ExternalPaymentReference{Provider: "stripe", Merchant: "acct_fixture", Environment: "test", Transaction: "pi_fixture"}, AcceptedUnixSeconds: 1}, nil
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
	proof := p.Evidence{Source: "webhook", Payload: []byte("body"), Signature: []byte("signature"), AcceptedProofID: "proof_fixture"}
	if _, err = b.Credit(ctx, intent.ID, "credit_fixture_0001", proof); !errors.Is(err, p.ErrUnavailable) {
		t.Fatal("unimplemented RPC succeeded", err)
	}
	if _, err = b.Prepare(ctx, "short", p.PrepareRequest{}); !errors.Is(err, p.ErrDenied) {
		t.Fatal("bad credential reached RPC", err)
	}
	e := p.Evidence{Provider: "stripe", Merchant: "acct_fixture", Environment: "test", Transaction: "pi_fixture", EventID: "evt_fixture", Source: "webhook", Payload: []byte("exact-original-bytes"), Signature: []byte("original-signature")}
	accepted, err := b.AcceptProof(ctx, intent.ID, e)
	if err != nil || accepted.ID != "proof_fixture" || accepted.IntentID != intent.ID {
		t.Fatal("proof acceptance lost durable binding", err)
	}
	lookup, err := b.LookupProof(ctx, intent.ID, e)
	if err != nil || lookup.ID != accepted.ID {
		t.Fatal("proof recovery lost durable binding", err)
	}
	if _, err = evidence(e); !errors.Is(err, p.ErrEvidence) {
		t.Fatal("unaccepted raw evidence reached settlement", err)
	}
}

func TestProviderProofReceiptRejectsWrongBinding(t *testing.T) {
	e := p.Evidence{Provider: "stripe", Merchant: "acct_fixture", Environment: "test", Transaction: "pi_fixture", EventID: "evt_fixture", Source: "webhook", Payload: []byte("exact-original-bytes")}
	h := sha256.Sum256(e.Payload)
	for name, change := range map[string]func(*pb.ProviderProofReceipt){
		"id":          func(r *pb.ProviderProofReceipt) { r.ProofId = "" },
		"intent":      func(r *pb.ProviderProofReceipt) { r.IntentId++ },
		"event":       func(r *pb.ProviderProofReceipt) { r.ProviderEventId = "other" },
		"payload":     func(r *pb.ProviderProofReceipt) { r.PayloadSha256 = []byte("wrong") },
		"merchant":    func(r *pb.ProviderProofReceipt) { r.Payment.Merchant = "other" },
		"transaction": func(r *pb.ProviderProofReceipt) { r.Payment.Transaction = "other" },
		"time":        func(r *pb.ProviderProofReceipt) { r.AcceptedUnixSeconds = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			r := &pb.ProviderProofReceipt{ProofId: "proof_fixture", IntentId: 1, ProviderEventId: e.EventID, PayloadSha256: h[:], Payment: &pb.ExternalPaymentReference{Provider: e.Provider, Merchant: e.Merchant, Environment: e.Environment, Transaction: e.Transaction}, AcceptedUnixSeconds: 1}
			change(r)
			if _, err := convertProof(r, nil, 1, e); !errors.Is(err, p.ErrConflict) {
				t.Fatal("unbound durable proof accepted", err)
			}
		})
	}
}
