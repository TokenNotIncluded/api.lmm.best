// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package store

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// Compare Core's fixture to the actual Go payment port, not a separately
// invented Rust encoding. rustfunds sends this same json.Marshal([]any{c}).
func TestCommerceUUIDFixtureMatchesGoPaymentCommand(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "core", "src", "commerce", "testdata", "go_uuid_command.json"))
	must(t, err)
	var commands []MoneyCommand
	must(t, json.Unmarshal(raw, &commands))
	if len(commands) != 1 || commands[0].OrderSnapshot == nil {
		t.Fatal("expected one frozen command")
	}
	c := commands[0]
	o := Order{ID: c.OrderID, Buyer: c.Buyer, Seller: c.Seller, Total: c.Total,
		VariantID: c.OrderSnapshot.VariantID, Quantity: c.OrderSnapshot.Quantity,
		UnitPrice: c.OrderSnapshot.UnitPrice, CreatedBy: c.OrderSnapshot.CreatedBy, State: AwaitingPayment}
	r := newRepo()
	must(t, r.Within(context.Background(), c.ShopID, func(tx Tx) error { return tx.Put("orders", o.ID, o) }))
	f := &a06StateFunds{execute: func(actual MoneyCommand) (Receipt, error) {
		encoded, err := json.Marshal([]any{actual})
		must(t, err)
		if !bytes.Equal(encoded, raw) {
			t.Fatal("Core fixture differs from the actual Go command bytes")
		}
		return a06StateReceipt(actual), nil
	}}
	s, err := New(r, &authority{}, f)
	must(t, err)
	paid, err := s.ChangeOrder(context.Background(), buyerToken, OrderChange{ShopID: c.ShopID, OrderID: o.ID, Action: "pay"})
	must(t, err)
	if paid.State != Paid || f.execCalls.Load() != 1 {
		t.Fatal("fixture did not reach the Go payment port")
	}
}

func TestLegacyRandomIDMatchesCoreCanonicalUUIDContract(t *testing.T) {
	canonical := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	for i := 0; i < 32; i++ {
		id, err := legacyRandomID()
		must(t, err)
		if !canonical.MatchString(id) {
			t.Fatal("Go variant generator violates Core's canonical UUIDv4 contract")
		}
	}
}
