package coreclient

import (
	"context"
	pb "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/corepb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Use a separate Client initialized with only the commerce service credential.
// User metadata is independent of that credential and is replaced, not merged.
func (c *Client) ExecuteCommerce(ctx context.Context, user string, r *pb.ExecuteCommerceRequest) (*pb.CommerceReceipt, error) {
	if !validUserCredential(user) {
		return nil, status.Error(codes.Unauthenticated, "invalid user credential")
	}
	return pb.NewCoreCommerceClient(c.connection).ExecuteCommerce(c.context(ctx, user), r)
}
func (c *Client) QueryCommerce(ctx context.Context, user string, r *pb.QueryCommerceRequest) (*pb.QueryCommerceResponse, error) {
	if !validUserCredential(user) {
		return nil, status.Error(codes.Unauthenticated, "invalid user credential")
	}
	return pb.NewCoreCommerceClient(c.connection).QueryCommerce(c.context(ctx, user), r)
}

// Completion consumes a previously user-authorized fixed capture grant. It
// cannot select a new payer, amount or recipient and never retains a user token.
func (c *Client) CompleteCommerceDelivery(ctx context.Context, r *pb.CompleteDeliveryRequest) (*pb.CommerceReceipt, error) {
	return pb.NewCoreCommerceClient(c.connection).CompleteDelivery(c.context(ctx, ""), r)
}

// Reconcile reads only the receipt of an already persisted command. The core
// binds this service identity to the original operation; UNKNOWN never executes.
func (c *Client) ReconcileCommerce(ctx context.Context, r *pb.ExecuteCommerceRequest) (*pb.QueryCommerceResponse, error) {
	return pb.NewCoreCommerceClient(c.connection).ReconcileCommerce(c.context(ctx, ""), r)
}
