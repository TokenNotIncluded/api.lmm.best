// Package rustfunds is the only store money port. It has no SQL connection,
// wallet implementation, caller-selected amount or provider signing secret.
package rustfunds

import (
	"context"
	"encoding/json"
	pb "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/corepb"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/store"
)

type Core interface {
	ExecuteCommerce(context.Context, string, *pb.ExecuteCommerceRequest) (*pb.CommerceReceipt, error)
	QueryCommerce(context.Context, string, *pb.QueryCommerceRequest) (*pb.QueryCommerceResponse, error)
	CompleteCommerceDelivery(context.Context, *pb.CompleteDeliveryRequest) (*pb.CommerceReceipt, error)
	ReconcileCommerce(context.Context, *pb.ExecuteCommerceRequest) (*pb.QueryCommerceResponse, error)
}
type Adapter struct {
	core           Core
	completion     bool
	reconciliation bool
}

func New(core Core, features []string) (*Adapter, error) {
	if store.Missing(core) {
		return nil, store.ErrInvalid
	}
	set := map[string]bool{}
	for _, f := range features {
		set[f] = true
	}
	for _, f := range []string{"commerce.pay.v1", "commerce.settle.v1", "commerce.refund.v1", "commerce.receipt.read.v1", "commerce.delivery.complete.v1", "commerce.receipt.reconcile.v1"} {
		if !set[f] {
			return nil, store.ErrPaymentUnavailable
		}
	}
	return &Adapter{core: core, completion: set["commerce.delivery.complete.v1"], reconciliation: set["commerce.receipt.reconcile.v1"]}, nil
}
func (a *Adapter) Available() bool { return a != nil && !store.Missing(a.core) }
func (a *Adapter) Supports(price store.Price) bool {
	return price.Unit == "credits" && price.Currency == "" && price.Amount >= 0 || price.Unit == "" && price.Currency == "USD" && price.Amount >= 0 && price.Amount <= 9223372036854775807/5000
}
func (a *Adapter) DeliveryAvailable() bool { return a.Available() && a.completion }
func (a *Adapter) command(c store.MoneyCommand) ([]byte, error) {
	if !a.Available() || !a.Supports(c.Total) || c.OrderSnapshot == nil || !a.Supports(c.OrderSnapshot.UnitPrice) || c.OrderSnapshot.Quantity <= 0 || c.OrderSnapshot.CreatedBy <= 0 {
		return nil, store.ErrInvalid
	}
	if c.OrderSnapshot.UnitPrice.Unit != c.Total.Unit || c.OrderSnapshot.UnitPrice.Currency != c.Total.Currency || c.OrderSnapshot.UnitPrice.Amount > 9223372036854775807/c.OrderSnapshot.Quantity || c.OrderSnapshot.UnitPrice.Amount*c.OrderSnapshot.Quantity != c.Total.Amount {
		return nil, store.ErrInvalid
	}
	raw, err := json.Marshal([]any{c})
	if err != nil || len(raw) > 8192 {
		return nil, store.ErrInvalid
	}
	return raw, nil
}
func receipt(r *pb.CommerceReceipt, c store.MoneyCommand) (store.Receipt, error) {
	if r == nil || r.GetKey() != c.Key || r.GetCommandHash() != store.ID(c) || !store.Text(r.GetReference(), 256) || (r.GetOutcome() != "succeeded" && r.GetOutcome() != "rejected") {
		return store.Receipt{}, store.ErrPending
	}
	return store.Receipt{Key: r.GetKey(), CommandHash: r.GetCommandHash(), Reference: r.GetReference(), Outcome: r.GetOutcome()}, nil
}
func (a *Adapter) Execute(ctx context.Context, user string, c store.MoneyCommand) (store.Receipt, error) {
	raw, err := a.command(c)
	if err != nil {
		return store.Receipt{}, err
	}
	r, err := a.core.ExecuteCommerce(ctx, user, &pb.ExecuteCommerceRequest{ProtocolMajor: 1, CommandJson: raw})
	if err != nil {
		return store.Receipt{}, store.ErrPending
	}
	return receipt(r, c)
}
func (a *Adapter) Query(ctx context.Context, user string, c store.MoneyCommand) (store.Receipt, error) {
	if _, err := a.command(c); err != nil {
		return store.Receipt{}, err
	}
	r, err := a.core.QueryCommerce(ctx, user, &pb.QueryCommerceRequest{ProtocolMajor: 1, ShopId: c.ShopID, OrderId: c.OrderID, OperationKey: c.Key})
	if err != nil || r == nil || r.GetReceipt() == nil {
		return store.Receipt{}, store.ErrPending
	}
	return receipt(r.GetReceipt(), c)
}
func (a *Adapter) CompleteDelivery(ctx context.Context, c store.MoneyCommand, payHash, deliveryRef string) (store.Receipt, error) {
	if !a.DeliveryAvailable() {
		return store.Receipt{}, store.ErrPaymentUnavailable
	}
	if c.Kind != "settle" || len(payHash) != 64 || len(deliveryRef) != 64 {
		return store.Receipt{}, store.ErrInvalid
	}
	raw, err := a.command(c)
	if err != nil {
		return store.Receipt{}, err
	}
	r, err := a.core.CompleteCommerceDelivery(ctx, &pb.CompleteDeliveryRequest{ProtocolMajor: 1, CommandJson: raw, PaymentCommandHash: payHash, DeliveryReference: deliveryRef})
	if err != nil {
		return store.Receipt{}, store.ErrPending
	}
	return receipt(r, c)
}
func (a *Adapter) ReconcileAvailable() bool { return a.Available() && a.reconciliation }
func (a *Adapter) Reconcile(ctx context.Context, c store.MoneyCommand) (store.Receipt, error) {
	if !a.ReconcileAvailable() {
		return store.Receipt{}, store.ErrPaymentUnavailable
	}
	raw, err := a.command(c)
	if err != nil {
		return store.Receipt{}, err
	}
	r, err := a.core.ReconcileCommerce(ctx, &pb.ExecuteCommerceRequest{ProtocolMajor: 1, CommandJson: raw})
	if err != nil || r == nil || r.GetReceipt() == nil {
		return store.Receipt{}, store.ErrPending
	}
	return receipt(r.GetReceipt(), c)
}
