package pgtest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	p "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/payments"
	_ "github.com/lib/pq"
)

// This fixture proves Go/PostgreSQL recovery, not Rust signature or money correctness.
type replayCore struct {
	p.Core
	proof   p.ProofReceipt
	credits int
}

func (c *replayCore) AcceptProof(_ context.Context, id int64, e p.Evidence) (p.ProofReceipt, error) {
	h := sha256.Sum256(e.Payload)
	c.proof = p.ProofReceipt{ID: "proof_pg_recovery", IntentID: id, EventID: e.EventID, PayloadSHA256: h[:], Payment: e, AcceptedUnixSeconds: 1}
	return c.proof, nil
}
func (c *replayCore) LookupProof(_ context.Context, _ int64, _ p.Evidence) (p.ProofReceipt, error) {
	if c.proof.ID == "" {
		return p.ProofReceipt{}, p.ErrNotFound
	}
	return c.proof, nil
}
func (c *replayCore) Receipt(_ context.Context, _ int64, _ string) (p.Receipt, error) {
	return p.Receipt{}, p.ErrNotFound
}
func (c *replayCore) Credit(_ context.Context, id int64, key string, _ p.Evidence) (p.Receipt, error) {
	c.credits++
	return p.Receipt{ID: "receipt_pg_recovery", IntentID: id, Key: key, RequestHash: make([]byte, 32), State: p.CreditApplied, JournalID: 77}, nil
}

type replayAdapter struct {
	p.Adapter
	channel p.Channel
	event   p.Event
}

func (a *replayAdapter) Channel() p.Channel                                   { return a.channel }
func (a *replayAdapter) Verify(_ *http.Request, _ time.Time) (p.Event, error) { return a.event, nil }

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
	event := p.Event{Key: "evt_once", ChannelID: 1, OrderID: o.ID, Type: p.Paid, Currency: "USD", AmountMinor: 1000, Digest: "digest_A", Status: "pending", NextAttempt: time.Now().Add(-time.Minute), Evidence: p.Evidence{Provider: "stripe", Merchant: "acct_test", Environment: "test", Transaction: "pi_unique", EventID: "evt_once", Source: "webhook", Payload: []byte("original_signed_bytes"), Signature: []byte("signature")}}
	type storedEvent struct {
		snapshot []byte
		event    p.Event
		status   string
		code     string
		next     time.Time
	}
	readEvent := func(t *testing.T, pool *sql.DB, key string) storedEvent {
		t.Helper()
		var saved storedEvent
		if err := pool.QueryRowContext(ctx, `SELECT snapshot,status,error_code,next_attempt FROM payment_events WHERE event_key=$1`, key).Scan(&saved.snapshot, &saved.status, &saved.code, &saved.next); err != nil {
			t.Fatal("read persisted event", err)
		}
		if err := json.Unmarshal(saved.snapshot, &saved.event); err != nil {
			t.Fatal("decode persisted event", err)
		}
		return saved
	}
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
	changedDigest := event
	changedDigest.Digest = "digest_B"
	if err = store.PutEvent(ctx, changedDigest); !errors.Is(err, p.ErrConflict) {
		t.Fatal("event ID changed meaning", err)
	}
	accepted := event
	accepted.Evidence.AcceptedProofID = "proof_accepted_fixture_1"
	if !t.Run("duplicate_promotes_and_persists_accepted_proof", func(t *testing.T) {
		before := readEvent(t, db, event.Key)
		if before.event.Evidence.AcceptedProofID != "" {
			t.Fatal("fixture already had accepted proof")
		}
		if err := store.PutEvent(ctx, accepted); err != nil {
			t.Fatal("same signed event could not receive accepted proof", err)
		}
		saved := readEvent(t, db, event.Key)
		if saved.status != "pending" || saved.event.Digest != event.Digest || !reflect.DeepEqual(saved.event.Evidence, accepted.Evidence) {
			t.Fatal("accepted proof or original signed evidence was not persisted")
		}
		pending, err := store.Events(ctx, time.Now(), 100)
		if err != nil || len(pending) != 1 || !reflect.DeepEqual(pending[0].Evidence, accepted.Evidence) {
			t.Fatal("recovery worker did not receive persisted accepted evidence", err)
		}
		// An older delivery must not erase a proof acquired by a newer worker.
		if err := store.PutEvent(ctx, event); err != nil {
			t.Fatal("original duplicate delivery failed", err)
		}
		if saved := readEvent(t, db, event.Key); !reflect.DeepEqual(saved.event.Evidence, accepted.Evidence) {
			t.Fatal("original duplicate erased accepted proof or signed evidence")
		}
	}) {
		return
	}
	if !t.Run("same_digest_rejects_changed_raw_payload", func(t *testing.T) {
		before := readEvent(t, db, event.Key)
		changedPayload := accepted
		changedPayload.Evidence.Payload = []byte("different_signed_bytes")
		if err := store.PutEvent(ctx, changedPayload); !errors.Is(err, p.ErrConflict) {
			t.Fatal("same normalized digest accepted a different signed payload", err)
		}
		after := readEvent(t, db, event.Key)
		if !bytes.Equal(after.snapshot, before.snapshot) || after.status != before.status || after.code != before.code || !after.next.Equal(before.next) {
			t.Fatal("rejected payload changed stored event or scheduling state")
		}
	}) {
		return
	}
	repaired := event
	repaired.Key = "evt_stale_proof"
	repaired.Digest = "digest_stale_proof"
	repaired.Evidence.EventID = repaired.Key
	if !t.Run("review_without_proof_requeues_after_acceptance", func(t *testing.T) {
		if err := store.PutEvent(ctx, repaired); err != nil {
			t.Fatal("insert stale proof fixture", err)
		}
		if err := store.FinishEvent(ctx, repaired.Key, "review", "proof_expired", time.Now().Add(time.Hour)); err != nil {
			t.Fatal("hold stale proof event for review", err)
		}
		repaired.Evidence.AcceptedProofID = "proof_accepted_fixture_repaired"
		if err := store.PutEvent(ctx, repaired); err != nil {
			t.Fatal("accept original evidence for review recovery", err)
		}
		saved := readEvent(t, db, repaired.Key)
		if saved.status != "pending" || saved.code != "" || !reflect.DeepEqual(saved.event.Evidence, repaired.Evidence) {
			t.Fatal("new accepted proof did not repair stale-proof review")
		}
		pending, err := store.Events(ctx, time.Now(), 100)
		if err != nil {
			t.Fatal("read repaired event for recovery worker", err)
		}
		var found bool
		for _, candidate := range pending {
			if candidate.Key == repaired.Key {
				found = reflect.DeepEqual(candidate.Evidence, repaired.Evidence)
			}
		}
		if !found {
			t.Fatal("repaired review did not become due with durable accepted proof")
		}
		if err := store.FinishEvent(ctx, repaired.Key, "done", "", time.Now()); err != nil {
			t.Fatal("finish recovered event", err)
		}
	}) {
		return
	}
	review := accepted
	review.Key = "evt_review"
	review.Digest = "digest_review"
	review.Evidence.EventID = review.Key
	review.Evidence.AcceptedProofID = "proof_accepted_fixture_2"
	if err := store.PutEvent(ctx, review); err != nil {
		t.Fatal("insert review fixture", err)
	}
	if !t.Run("duplicate_does_not_regress_done_or_accepted_review", func(t *testing.T) {
		for _, terminal := range []struct {
			event  p.Event
			status string
		}{{accepted, "done"}, {review, "review"}} {
			if err := store.FinishEvent(ctx, terminal.event.Key, terminal.status, "terminal_reason", time.Now().Add(time.Hour)); err != nil {
				t.Fatal("finish terminal event", err)
			}
			before := readEvent(t, db, terminal.event.Key)
			for _, proof := range []string{terminal.event.Evidence.AcceptedProofID, ""} {
				duplicate := terminal.event
				duplicate.Status = "pending"
				duplicate.ErrorCode = ""
				duplicate.NextAttempt = time.Now().Add(-time.Hour)
				duplicate.Evidence.AcceptedProofID = proof
				if err := store.PutEvent(ctx, duplicate); err != nil {
					t.Fatal("duplicate terminal delivery failed", err)
				}
				after := readEvent(t, db, terminal.event.Key)
				if !bytes.Equal(after.snapshot, before.snapshot) || after.status != terminal.status || after.code != before.code || !after.next.Equal(before.next) {
					t.Fatal("duplicate regressed terminal event, evidence, or scheduling state", terminal.status)
				}
			}
		}
	}) {
		return
	}
	if !t.Run("stale_finish_does_not_regress_done", func(t *testing.T) {
		before := readEvent(t, db, event.Key)
		if err := store.FinishEvent(ctx, event.Key, "pending", "old_worker", time.Now()); err != nil {
			t.Fatal(err)
		}
		after := readEvent(t, db, event.Key)
		if !bytes.Equal(after.snapshot, before.snapshot) || after.status != "done" || after.code != before.code || !after.next.Equal(before.next) {
			t.Fatal("stale worker changed completed event or scheduling state")
		}
	}) {
		return
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
	for _, terminal := range []struct {
		event  p.Event
		status string
	}{{accepted, "done"}, {review, "review"}, {repaired, "done"}} {
		saved := readEvent(t, db2, terminal.event.Key)
		if saved.status != terminal.status || !reflect.DeepEqual(saved.event.Evidence, terminal.event.Evidence) {
			t.Fatal("restart lost terminal state or accepted signed evidence", terminal.status)
		}
	}
	if _, err = p.NewPostgres(ctx, db, "some_other_database"); !errors.Is(err, p.ErrDenied) {
		t.Fatal("wrong database accepted", err)
	}
	t.Run("done_inbox_repairs_quarantined_job_only_with_durable_proof", func(t *testing.T) {
		order := o
		order.ID = "top_recovery"
		order.Intent.ID = 3
		order.PaymentID = "pi_recovery"
		order.Jobs = map[string]p.Job{}
		if err := store.Insert(ctx, order); err != nil {
			t.Fatal(err)
		}
		evidence := p.Evidence{Provider: "stripe", Merchant: "acct_test", Environment: "test", Transaction: order.PaymentID, EventID: "evt_pg_recovery", Payload: []byte("immutable signed payload"), Signature: []byte("original signature"), Source: "webhook"}
		adapter := &replayAdapter{channel: p.Channel{ID: 1, Provider: "stripe", Merchant: "acct_test", Environment: "test", Currency: "USD", Enabled: true}, event: p.Event{OrderID: order.ID, Type: p.Paid, Currency: "USD", AmountMinor: 1000, Evidence: evidence}}
		core := &replayCore{}
		service, err := p.New(store, core, adapter)
		if err != nil {
			t.Fatal(err)
		}
		request := func() *http.Request { return httptest.NewRequest("POST", "/callbacks/1", nil) }
		if err = service.Receive(ctx, 1, request()); err != nil {
			t.Fatal(err)
		}
		if err = service.Drain(ctx, 100); err != nil {
			t.Fatal(err)
		}
		// Construct a historical drained webhook queue without a proof ID.
		// The fixture retains the independently accepted core proof.
		if err = store.Mutate(ctx, order.ID, func(o *p.Order) error {
			for key, j := range o.Jobs {
				if j.Kind == "credit" {
					j.Evidence.AcceptedProofID = ""
					j.State = "review"
					j.Attempts = 1
					j.ErrorCode = p.ErrEvidence.Error()
					o.Jobs[key] = j
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if _, err = db.ExecContext(ctx, `UPDATE payment_events SET snapshot=snapshot #- '{evidence,accepted_proof_id}' WHERE snapshot->>'order_id'=$1`, order.ID); err != nil {
			t.Fatal(err)
		}
		before, err := store.Get(ctx, order.ID)
		if err != nil {
			t.Fatal(err)
		}
		var jobKey string
		for key, job := range before.Jobs {
			if job.Kind == "credit" {
				jobKey = key
				if job.State != "review" || job.Evidence.AcceptedProofID != "" {
					t.Fatal("missing proof was not quarantined")
				}
			}
		}
		if jobKey == "" {
			t.Fatal("legacy credit job missing")
		}
		var status string
		if err = db.QueryRowContext(ctx, `SELECT status FROM payment_events WHERE snapshot->>'order_id'=$1`, order.ID).Scan(&status); err != nil || status != "done" {
			t.Fatal("legacy inbox did not reach done", err)
		}
		adapter.event.Evidence.Source = "webhook"
		adapter.event.Evidence.Signature = []byte("fresh signature")
		if err = service.Receive(ctx, 1, request()); err != nil {
			t.Fatal("fresh accepted replay rejected", err)
		}
		if err = db.QueryRowContext(ctx, `SELECT status FROM payment_events WHERE snapshot->>'order_id'=$1`, order.ID).Scan(&status); err != nil || status != "pending" {
			t.Fatal("done inbox blocked proof repair", err)
		}
		if err = service.Drain(ctx, 100); err != nil {
			t.Fatal(err)
		}
		repaired, err := store.Get(ctx, order.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(repaired.Jobs) != 1 || repaired.Jobs[jobKey].State != "pending" || repaired.Jobs[jobKey].Evidence.AcceptedProofID != "proof_pg_recovery" {
			t.Fatal("same quarantined semantic job was not repaired")
		}
		if err = service.Step(ctx, order.ID); err != nil {
			t.Fatal(err)
		}
		final, err := store.Get(ctx, order.ID)
		if err != nil || !final.Credited() || core.credits != 1 {
			t.Fatal("repaired job did not settle", err)
		}
		if err = service.Receive(ctx, 1, request()); err != nil {
			t.Fatal(err)
		}
		if err = service.Work(ctx, 100); err != nil {
			t.Fatal(err)
		}
		final, err = store.Get(ctx, order.ID)
		if err != nil || len(final.Jobs) != 1 || final.Jobs[jobKey].State != "done" || core.credits != 1 {
			t.Fatal("final money job regressed", err)
		}
	})
	if _, err = db.ExecContext(ctx, "CREATE SCHEMA core_probe"); err != nil {
		t.Fatal(err)
	}
	if _, err = p.NewPostgres(ctx, db, name); !errors.Is(err, p.ErrDenied) {
		t.Fatal("core schema accepted", err)
	}
}
