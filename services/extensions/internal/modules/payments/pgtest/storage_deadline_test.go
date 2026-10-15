package pgtest

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules"
	p "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/payments"
)

// No Core method may run: these probes exercise blocked storage or missing
// orders, never fake a successful authorization or money operation.
type unavailableDeadlineCore struct{ p.Core }

// @mark payments.storage-deadline role=test
func TestPostgresStorageDeadlines(t *testing.T) {
	dsn := os.Getenv("PAYMENT_DEADLINE_TEST_DSN")
	if dsn == "" {
		t.Skip("PAYMENT_DEADLINE_TEST_DSN must identify a fresh isolated deadline fixture")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(4)
	var name string
	setup, cancelSetup := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelSetup()
	if err := db.QueryRowContext(setup, "SELECT current_database()").Scan(&name); err != nil || name != "lmm_payments_deadline_test" {
		t.Fatal("refuse non-fixture database", name, err)
	}
	// No DROP or truncation: schema creation must fail on an already used fixture.
	if _, err := db.ExecContext(setup, p.Schema); err != nil {
		t.Fatal(err)
	}
	store, err := p.NewPostgres(setup, db, name)
	if err != nil {
		t.Fatal(err)
	}
	order := p.Order{ID: "deadline_order", ChannelID: 1, Namespace: "stripe/deadline/test", Intent: p.Intent{ID: 1, AmountMinor: 100}, Jobs: map[string]p.Job{}, Refunds: map[string]p.Refund{}}
	if err := store.Insert(setup, order); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := db.QueryRowContext(setup, "SELECT snapshot::text FROM payment_orders WHERE id=$1", order.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	locker, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Close()
	lock, err := locker.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback()
	if _, err := lock.Exec("LOCK TABLE payment_orders,payment_events IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	svc, err := p.New(store, unavailableDeadlineCore{})
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("h", 32)
	host, err := modules.NewHostWithLimit([]modules.Module{svc}, []byte(token), 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.MarkReady(); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(host)
	defer server.Close()
	client := &http.Client{Timeout: 5 * time.Second}
	request, err := http.NewRequest(http.MethodGet, server.URL+"/extensions/v1/payments/orders/missing_order", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-LMM-User-Credential", strings.Repeat("u", 32))
	started := time.Now()
	response, err := client.Do(request)
	if err != nil {
		t.Fatal("storage wait outlived local budget", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable || time.Since(started) > 4500*time.Millisecond {
		t.Fatal("locked storage response", response.StatusCode, time.Since(started))
	}
	release, err := host.TryAcquire()
	if err != nil {
		t.Fatal("timed-out HTTP retained host permit", err)
	}
	release()
	entered := false
	for operation, call := range map[string]func(context.Context) error{
		"Mutate": func(ctx context.Context) error {
			return store.Mutate(ctx, order.ID, func(*p.Order) error { entered = true; return nil })
		},
		"PutEvent": func(ctx context.Context) error {
			return store.PutEvent(ctx, p.Event{Key: "deadline_event", Digest: "digest", NextAttempt: time.Now()})
		},
	} {
		ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		started := time.Now()
		err := call(ctx)
		stop()
		if !errors.Is(err, p.ErrUnavailable) || time.Since(started) > 4500*time.Millisecond {
			t.Fatalf("%s lock deadline: %v %s", operation, err, time.Since(started))
		}
	}
	if entered {
		t.Fatal("locked transaction executed mutation callback")
	}
	if err := lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	var after string
	if err := db.QueryRow("SELECT snapshot::text FROM payment_orders WHERE id=$1", order.ID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	var events int
	if err := db.QueryRow("SELECT count(*) FROM payment_events").Scan(&events); err != nil {
		t.Fatal(err)
	}
	if before != after || events != 0 {
		t.Fatal("timed-out operations changed persisted data")
	}
	db.SetMaxOpenConns(1)
	held, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	calls := map[string]func(context.Context) error{
		"NewPostgres": func(ctx context.Context) error { _, err := p.NewPostgres(ctx, db, name); return err },
		"Insert":      func(ctx context.Context) error { return store.Insert(ctx, order) },
		"Get":         func(ctx context.Context) error { _, err := store.Get(ctx, order.ID); return err },
		"FindPayment": func(ctx context.Context) error {
			_, err := store.FindPayment(ctx, order.Namespace, "missing_payment")
			return err
		},
		"Mutate": func(ctx context.Context) error {
			return store.Mutate(ctx, order.ID, func(*p.Order) error { t.Error("pool-wait callback ran"); return nil })
		},
		"PutEvent": func(ctx context.Context) error {
			return store.PutEvent(ctx, p.Event{Key: "deadline_pool_event", NextAttempt: time.Now()})
		},
		"Events": func(ctx context.Context) error { _, err := store.Events(ctx, time.Now(), 1); return err },
		"FinishEvent": func(ctx context.Context) error {
			return store.FinishEvent(ctx, "missing_event", "pending", "", time.Now())
		},
		"DueOrders": func(ctx context.Context) error { _, err := store.DueOrders(ctx, time.Now(), 1); return err },
	}
	// Parallel waits test every entry's local 3s budget in one interval. The 5s
	// parent is a safety bound for old-code counterexamples, not the expected limit.
	for _, budget := range []time.Duration{5 * time.Second, 40 * time.Millisecond} {
		var wg sync.WaitGroup
		for operation, call := range calls {
			wg.Add(1)
			go func(operation string, call func(context.Context) error) {
				defer wg.Done()
				ctx, stop := context.WithTimeout(context.Background(), budget)
				defer stop()
				started := time.Now()
				err := call(ctx)
				limit := 4500 * time.Millisecond
				if budget < time.Second {
					limit = time.Second
				}
				if !errors.Is(err, p.ErrUnavailable) || time.Since(started) > limit {
					t.Errorf("%s pool budget %s: %v elapsed %s", operation, budget, err, time.Since(started))
				}
			}(operation, call)
		}
		wg.Wait()
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	host.BeginDrain()
	joined, stopJoin := context.WithTimeout(context.Background(), time.Second)
	defer stopJoin()
	if err := host.Wait(joined); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for db.Stats().InUse != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if db.Stats().InUse != 0 {
		t.Fatal("storage permits not drained", db.Stats())
	}
}
