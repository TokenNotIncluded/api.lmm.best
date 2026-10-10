// Package rustbridge maps the payment module to the existing CorePayments wire
// contract. It cannot bypass the controlled client or open a core database.
package rustbridge

import (
	"context"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/coreclient"
	pb "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/corepb"
	p "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/payments"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Bridge struct{ client *coreclient.Client }

func New(client *coreclient.Client) (*Bridge, error) {
	if client == nil {
		return nil, p.ErrInvalid
	}
	return &Bridge{client: client}, nil
}

var _ p.Core = (*Bridge)(nil)

func (b *Bridge) Prepare(ctx context.Context, user string, r p.PrepareRequest) (p.Intent, error) {
	v, err := b.client.PrepareTopup(ctx, user, &pb.PrepareTopupRequest{Mutation: mutation(r.Key), AccountId: r.AccountID, PaymentChannelId: r.ChannelID, Currency: r.Currency, AmountMinor: r.AmountMinor})
	if err != nil {
		return p.Intent{}, mapError(err)
	}
	if v == nil {
		return p.Intent{}, p.ErrUnavailable
	}
	return p.Intent{ID: v.IntentId, AccountID: v.AccountId, CreatedBy: v.CreatedByUserId, Provider: v.Provider, Merchant: v.Merchant, Environment: v.Environment, Currency: v.Currency, AmountMinor: v.AmountMinor, CreditUnits: v.CreditUnits, CreditUnit: v.CreditUnit, Key: v.IdempotencyKey, RequestHash: append([]byte(nil), v.RequestSha256...)}, nil
}
func (b *Bridge) Read(ctx context.Context, user string, intent p.Intent) error {
	// Read must be checked by Rust even before credit. Task 06 must expose the
	// prepared intent's receipt (or supply a read-intent RPC). NotFound is NOT an
	// authorization success; no local membership/creator fallback is permitted.
	r, err := b.client.ReadPaymentReceipt(ctx, user, &pb.GetPaymentReceiptRequest{IntentId: intent.ID, IdempotencyKey: intent.Key})
	if err != nil {
		return mapError(err)
	}
	if r == nil || r.IntentId != intent.ID {
		return p.ErrUnavailable
	}
	return nil
}
func (b *Bridge) Hold(ctx context.Context, user string, id int64, key string, amount int64) (p.Receipt, error) {
	return convert(b.client.HoldPaymentRefund(ctx, user, &pb.HoldRefundRequest{Mutation: mutation(key), IntentId: id, RefundAmountMinor: amount}))
}
func (b *Bridge) Credit(ctx context.Context, id int64, key string, e p.Evidence) (p.Receipt, error) {
	proof, err := evidence(e)
	if err != nil {
		return p.Receipt{}, err
	}
	return convert(b.client.CreditPaymentTopup(ctx, &pb.CreditTopupRequest{Mutation: mutation(key), IntentId: id, Evidence: proof}))
}
func (b *Bridge) Commit(ctx context.Context, id int64, key, hold string, e p.Evidence) (p.Receipt, error) {
	proof, err := evidence(e)
	if err != nil {
		return p.Receipt{}, err
	}
	return convert(b.client.CommitPaymentRefund(ctx, &pb.ResolveRefundRequest{Mutation: mutation(key), IntentId: id, HoldReceiptId: hold, Evidence: proof}))
}
func (b *Bridge) Release(ctx context.Context, id int64, key, hold string, e p.Evidence) (p.Receipt, error) {
	proof, err := evidence(e)
	if err != nil {
		return p.Receipt{}, err
	}
	return convert(b.client.ReleasePaymentRefund(ctx, &pb.ResolveRefundRequest{Mutation: mutation(key), IntentId: id, HoldReceiptId: hold, Evidence: proof}))
}
func (b *Bridge) ExternalRefund(ctx context.Context, id int64, key string, e p.Evidence) (p.Receipt, error) {
	proof, err := evidence(e)
	if err != nil {
		return p.Receipt{}, err
	}
	return convert(b.client.ApplyExternalPaymentRefund(ctx, &pb.ExternalRefundRequest{Mutation: mutation(key), IntentId: id, Evidence: proof}))
}
func (b *Bridge) Receipt(ctx context.Context, id int64, key string) (p.Receipt, error) {
	return convert(b.client.ReconcilePaymentReceipt(ctx, &pb.GetPaymentReceiptRequest{IntentId: id, IdempotencyKey: key}))
}
func mutation(key string) *pb.MutationContext { return &pb.MutationContext{IdempotencyKey: key} }
func evidence(e p.Evidence) (*pb.ProviderEvidence, error) {
	if e.Source != "webhook" || len(e.Signature) == 0 || len(e.Signature) > 2048 || len(e.Payload) == 0 || len(e.Payload) > p.MaxEvidence {
		return nil, p.ErrEvidence
	}
	return &pb.ProviderEvidence{Payment: &pb.ExternalPaymentReference{Provider: e.Provider, Merchant: e.Merchant, Environment: e.Environment, Transaction: e.Transaction}, ProviderEventId: e.EventID, SignedPayload: append([]byte(nil), e.Payload...), Signature: append([]byte(nil), e.Signature...)}, nil
}
func convert(r *pb.PaymentReceipt, err error) (p.Receipt, error) {
	if err != nil {
		return p.Receipt{}, mapError(err)
	}
	if r == nil {
		return p.Receipt{}, p.ErrUnavailable
	}
	states := map[pb.PaymentReceiptState]string{
		pb.PaymentReceiptState_PAYMENT_RECEIPT_STATE_CREDIT_APPLIED:          p.CreditApplied,
		pb.PaymentReceiptState_PAYMENT_RECEIPT_STATE_REFUND_HELD:             p.RefundHeld,
		pb.PaymentReceiptState_PAYMENT_RECEIPT_STATE_REFUND_COMMITTED:        p.RefundCommitted,
		pb.PaymentReceiptState_PAYMENT_RECEIPT_STATE_REFUND_RELEASED:         p.RefundReleased,
		pb.PaymentReceiptState_PAYMENT_RECEIPT_STATE_EXTERNAL_REFUND_APPLIED: p.ExternalRefundApplied,
		pb.PaymentReceiptState_PAYMENT_RECEIPT_STATE_REJECTED:                p.Rejected,
	}
	state, ok := states[r.State]
	if !ok {
		return p.Receipt{}, p.ErrUnavailable
	}
	return p.Receipt{ID: r.ReceiptId, IntentID: r.IntentId, Key: r.IdempotencyKey, RequestHash: append([]byte(nil), r.RequestSha256...), State: state, JournalID: r.JournalId, RejectionCode: r.RejectionCode}, nil
}
func mapError(err error) error {
	switch status.Code(err) {
	case codes.NotFound:
		return p.ErrNotFound
	case codes.PermissionDenied, codes.Unauthenticated:
		return p.ErrDenied
	case codes.InvalidArgument:
		return p.ErrInvalid
	case codes.AlreadyExists, codes.Aborted:
		return p.ErrConflict
	default:
		return p.ErrUnavailable
	}
}
