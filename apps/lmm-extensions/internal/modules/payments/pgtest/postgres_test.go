package pgtest

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	p "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/payments"
	_ "github.com/lib/pq"
)

func TestPostgresDurabilityConcurrencyAndIsolation(t *testing.T) {
	dsn := os.Getenv("PAYMENT_TEST_DSN")
	if dsn == "" {
		t.Skip("PAYMENT_TEST_DSN must name a disposable payment test database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(16)
	var name string
	if err = db.QueryRowContext(ctx, "SELECT current_database()").Scan(&name); err != nil || name != "lmm_payments_test" {
		t.Fatal("refuse non-test database", name, err)
	}
	// This is an explicitly disposable, named CI database, never application startup.
	if _, err = db.ExecContext(ctx, `DROP TABLE IF EXISTS payment_events,payment_orders,payment_storage_guard; DROP SCHEMA IF EXISTS core_probe CASCADE`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, p.Schema); err != nil {
		t.Fatal(err)
	}
	store, err := p.NewPostgres(ctx, db, name)
	if err != nil {
		t.Fatal(err)
	}
	o := p.Order{ID: "top_1", ChannelID: 1, Namespace: "stripe/acct_test/test", Intent: p.Intent{ID: 1, AccountID: 42, CreatedBy: 7, Provider: "stripe", Merchant: "acct_test", Environment: "test", Currency: "USD", AmountMinor: 1000, Key: "create_fixture_0001", RequestHash: make([]byte, 32)}, CreditState: "pending", Refunds: map[string]p.Refund{}, Jobs: map[string]p.Job{}}
	if err = store.Insert(ctx, o); err != nil {
		t.Fatal(err)
	}
	if err = store.Insert(ctx, o); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := store.Mutate(ctx, o.ID, func(o *p.Order) error { j := o.Jobs["counter"]; j.Attempts++; o.Jobs["counter"] = j; return nil }); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	got, err := store.Get(ctx, o.ID)
	if err != nil || got.Version != 33 || got.Jobs["counter"].Attempts != 32 {
		t.Fatal("lost concurrent updates", got, err)
	}
	if err = store.Mutate(ctx, o.ID, func(o *p.Order) error { o.Intent.AccountID = 999; return nil }); !errors.Is(err, p.ErrConflict) {
		t.Fatal("ownership mutation accepted", err)
	}
	if err = store.Mutate(ctx, o.ID, func(o *p.Order) error { o.ProviderPaid = true; return p.ErrConflict }); !errors.Is(err, p.ErrConflict) {
		t.Fatal(err)
	}
	got, _ = store.Get(ctx, o.ID)
	if got.ProviderPaid {
		t.Fatal("failed transaction was committed")
	}
	if err = store.Mutate(ctx, o.ID, func(o *p.Order) error { o.PaymentID = "pi_unique"; return nil }); err != nil {
		t.Fatal(err)
	}
	other := o
	other.ID = "top_2"
	other.Intent.ID = 2
	if err = store.Insert(ctx, other); err != nil {
		t.Fatal(err)
	}
	if err = store.Mutate(ctx, other.ID, func(o *p.Order) error { o.PaymentID = "pi_unique"; return nil }); !errors.Is(err, p.ErrConflict) {
		t.Fatal("provider transaction credited two orders", err)
	}
	event := p.Event{Key: "evt_once", Digest: "digest_A", Status: "pending", NextAttempt: time.Now().Add(-time.Minute), Evidence: p.Evidence{Source: "webhook", Payload: []byte("original_signed_bytes"), Signature: []byte("signature")}}
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := store.PutEvent(ctx, event); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	events, err := store.Events(ctx, time.Now(), 100)
	if err != nil || len(events) != 1 {
		t.Fatal("event dedup failed", len(events), err)
	}
	event.Digest = "digest_B"
	if err = store.PutEvent(ctx, event); !errors.Is(err, p.ErrConflict) {
		t.Fatal("event ID changed meaning", err)
	}
	if err = store.FinishEvent(ctx, event.Key, "done", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = store.FinishEvent(ctx, event.Key, "pending", "old_worker", time.Now()); err != nil {
		t.Fatal(err)
	}
	events, err = store.Events(ctx, time.Now(), 100)
	if err != nil || len(events) != 0 {
		t.Fatal("completed event regressed", err)
	}
	// Reopen through a separate pool, simulating a stopped/restarted Go process.
	db2, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	restored, err := p.NewPostgres(ctx, db2, name)
	if err != nil {
		t.Fatal(err)
	}
	found, err := restored.FindPayment(ctx, o.Namespace, "pi_unique")
	if err != nil || found.ID != o.ID || found.Jobs["counter"].Attempts != 32 {
		t.Fatal("restart lost persisted state", err)
	}
	if _, err = p.NewPostgres(ctx, db, "some_other_database"); !errors.Is(err, p.ErrDenied) {
		t.Fatal("wrong database accepted", err)
	}
	if _, err = db.ExecContext(ctx, "CREATE SCHEMA core_probe"); err != nil {
		t.Fatal(err)
	}
	if _, err = p.NewPostgres(ctx, db, name); !errors.Is(err, p.ErrDenied) {
		t.Fatal("core schema accepted", err)
	}
}
