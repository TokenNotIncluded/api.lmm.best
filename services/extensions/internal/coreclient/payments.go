package coreclient

import (
	"context"

	pb "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/corepb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// These wrappers use the existing bounded Unix-socket connection and replace
// caller metadata with the dedicated service credential. They do NOT register a
// Rust service or grant a payment capability. Unimplemented stays an error.
func (c *Client) PrepareTopup(ctx context.Context, user string, request *pb.PrepareTopupRequest) (*pb.PaymentIntent, error) {
	if !validUserCredential(user) {
		return nil, status.Error(codes.Unauthenticated, "invalid user credential")
	}
	return pb.NewCorePaymentsClient(c.connection).PrepareTopup(c.context(ctx, user), request)
}
func (c *Client) HoldPaymentRefund(ctx context.Context, user string, request *pb.HoldRefundRequest) (*pb.PaymentReceipt, error) {
	if !validUserCredential(user) {
		return nil, status.Error(codes.Unauthenticated, "invalid user credential")
	}
	return pb.NewCorePaymentsClient(c.connection).HoldRefund(c.context(ctx, user), request)
}
func (c *Client) ReadPaymentReceipt(ctx context.Context, user string, request *pb.GetPaymentReceiptRequest) (*pb.PaymentReceipt, error) {
	if !validUserCredential(user) {
		return nil, status.Error(codes.Unauthenticated, "invalid user credential")
	}
	return pb.NewCorePaymentsClient(c.connection).GetPaymentReceipt(c.context(ctx, user), request)
}

// ReconcilePaymentReceipt requires a separately scoped reconcile authority on
// Rust. The service token alone must not grant access to every payment intent.
func (c *Client) ReconcilePaymentReceipt(ctx context.Context, request *pb.GetPaymentReceiptRequest) (*pb.PaymentReceipt, error) {
	return pb.NewCorePaymentsClient(c.connection).GetPaymentReceipt(c.context(ctx, ""), request)
}
func (c *Client) CreditPaymentTopup(ctx context.Context, request *pb.CreditTopupRequest) (*pb.PaymentReceipt, error) {
	return pb.NewCorePaymentsClient(c.connection).CreditTopup(c.context(ctx, ""), request)
}
func (c *Client) CommitPaymentRefund(ctx context.Context, request *pb.ResolveRefundRequest) (*pb.PaymentReceipt, error) {
	return pb.NewCorePaymentsClient(c.connection).CommitRefund(c.context(ctx, ""), request)
}
func (c *Client) ReleasePaymentRefund(ctx context.Context, request *pb.ResolveRefundRequest) (*pb.PaymentReceipt, error) {
	return pb.NewCorePaymentsClient(c.connection).ReleaseRefund(c.context(ctx, ""), request)
}
func (c *Client) ApplyExternalPaymentRefund(ctx context.Context, request *pb.ExternalRefundRequest) (*pb.PaymentReceipt, error) {
	return pb.NewCorePaymentsClient(c.connection).ApplyExternalRefund(c.context(ctx, ""), request)
}
