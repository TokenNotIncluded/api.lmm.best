package rustfunds

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	pb "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/corepb"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/store"
)

type coreStub struct {
	raw     []byte
	user    string
	execute int
	known   bool
	wrong   bool
}

func (c *coreStub) ExecuteCommerce(_ context.Context, user string, r *pb.ExecuteCommerceRequest) (*pb.CommerceReceipt, error) {
	c.raw = append([]byte{}, r.CommandJson...)
	c.user = user
	c.execute++
	return c.answer(), nil
}
func (c *coreStub) QueryCommerce(context.Context, string, *pb.QueryCommerceRequest) (*pb.QueryCommerceResponse, error) {
	return &pb.QueryCommerceResponse{}, nil
}
func (c *coreStub) CompleteCommerceDelivery(_ context.Context, r *pb.CompleteDeliveryRequest) (*pb.CommerceReceipt, error) {
	c.raw = append([]byte{}, r.CommandJson...)
	return c.answer(), nil
}
func (c *coreStub) ReconcileCommerce(_ context.Context, r *pb.ExecuteCommerceRequest) (*pb.QueryCommerceResponse, error) {
	c.raw = append([]byte{}, r.CommandJson...)
	if !c.known {
		return &pb.QueryCommerceResponse{}, nil
	}
	return &pb.QueryCommerceResponse{Receipt: c.answer()}, nil
}
func (c *coreStub) answer() *pb.CommerceReceipt {
	var commands []store.MoneyCommand
	_ = json.Unmarshal(c.raw, &commands)
	r := &pb.CommerceReceipt{Key: commands[0].Key, CommandHash: store.ID(commands[0]), Reference: "core-test-receipt", Outcome: "succeeded"}
	if c.wrong {
		r.CommandHash = "different"
	}
	return r
}
func command(price store.Price) store.MoneyCommand {
	return store.MoneyCommand{Key: "operation", Kind: "pay", ShopID: "shop", OrderID: "order", Buyer: store.Account{Kind: "personal", ID: 2}, Seller: store.Account{Kind: "personal", ID: 1}, Total: price, OrderSnapshot: &store.MoneyOrderSnapshot{VariantID: "variant", Quantity: 1, UnitPrice: price, CreatedBy: 2}}
}

var features = []string{"commerce.pay.v1", "commerce.settle.v1", "commerce.refund.v1", "commerce.receipt.read.v1", "commerce.delivery.complete.v1", "commerce.receipt.reconcile.v1"}

func TestExactFrozenMoneyAndUnknownRecovery(t *testing.T) {
	core := &coreStub{}
	a, err := New(core, features)
	if err != nil {
		t.Fatal(err)
	}
	for _, price := range []store.Price{{Amount: 1, Unit: "credits"}, {Amount: 1, Currency: "USD"}} {
		c := command(price)
		r, err := a.Execute(context.Background(), "opaque-user", c)
		if err != nil || r.CommandHash != store.ID(c) || core.user != "opaque-user" {
			t.Fatal(r, err)
		}
		want, _ := json.Marshal([]any{c})
		if string(want) != string(core.raw) {
			t.Fatal("frozen integer command was reencoded differently")
		}
	}
	before := core.execute
	c := command(store.Price{Amount: 500001, Unit: "credits"})
	if _, err := a.Reconcile(context.Background(), c); !errors.Is(err, store.ErrPending) || core.execute != before {
		t.Fatal("UNKNOWN recovery executed pay", err)
	}
	core.known = true
	if _, err := a.Reconcile(context.Background(), c); err != nil || core.execute != before {
		t.Fatal("known read recovery mutated pay", err)
	}
	core.wrong = true
	if _, err := a.Reconcile(context.Background(), c); !errors.Is(err, store.ErrPending) {
		t.Fatal("wrong receipt accepted", err)
	}
}
func TestMoneyRejectsUnsupportedOrChangedUnits(t *testing.T) {
	core := &coreStub{}
	a, _ := New(core, features)
	for _, price := range []store.Price{{Amount: 1, Currency: "CNY"}, {Amount: 9223372036854775807, Currency: "USD"}, {Amount: -1, Unit: "credits"}, {Amount: 1, Unit: "credits", Currency: "USD"}} {
		if _, err := a.Execute(context.Background(), "opaque", command(price)); !errors.Is(err, store.ErrInvalid) {
			t.Fatalf("accepted invalid price %#v: %v", price, err)
		}
	}
	c := command(store.Price{Amount: 1, Unit: "credits"})
	c.OrderSnapshot.UnitPrice = store.Price{Amount: 1, Currency: "USD"}
	if _, err := a.Execute(context.Background(), "opaque", c); !errors.Is(err, store.ErrInvalid) {
		t.Fatal("cross-unit total accepted", err)
	}
	if core.execute != 0 {
		t.Fatal("invalid command reached Core")
	}
}
