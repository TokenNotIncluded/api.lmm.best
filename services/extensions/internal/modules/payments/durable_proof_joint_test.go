package payments_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/coreclient"
	p "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/payments"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/payments/rustbridge"
	_ "github.com/lib/pq"
)

type proofJointFixture struct {
	Socket          string    `json:"socket"`
	Token           string    `json:"token"`
	User            string    `json:"user"`
	Channel         p.Channel `json:"channel"`
	Account         int64     `json:"account"`
	IntentID        int64     `json:"intent_id"`
	PrepareKey      string    `json:"prepare_key"`
	Currency        string    `json:"currency"`
	AmountMinor     int64     `json:"amount_minor"`
	DatabaseURLFile string    `json:"go_database_url_file"`
	DatabaseName    string    `json:"go_database_name"`
	Events          []struct {
		Payload   string `json:"payload"`
		Signature string `json:"signature"`
	} `json:"events"`
}

type proofJointTransport struct{ fixture proofJointFixture }

func (tr proofJointTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f := tr.fixture
	var body []byte
	var err error
	switch r.URL.Path {
	case "/v1/account":
		body, err = json.Marshal(map[string]string{"id": f.Channel.Merchant})
	case "/v1/checkout/sessions":
		var e struct {
			Data struct {
				Object struct {
					ID string `json:"id"`
				} `json:"object"`
			} `json:"data"`
		}
		if json.Unmarshal([]byte(f.Events[0].Payload), &e) != nil {
			return nil, fmt.Errorf("invalid local event fixture")
		}
		order := "top_" + strconv.FormatInt(f.IntentID, 10)
		body, err = json.Marshal(map[string]any{"id": "cs_joint_fixture", "object": "checkout.session", "mode": "payment", "client_reference_id": order, "metadata": map[string]string{"lmm_order_id": order, "lmm_core_intent_id": strconv.FormatInt(f.IntentID, 10), "lmm_merchant": f.Channel.Merchant}, "amount_total": f.AmountMinor, "currency": strings.ToLower(f.Currency), "livemode": false, "payment_intent": e.Data.Object.ID, "url": "https://checkout.stripe.com/c/pay/joint-fixture"})
	default:
		if strings.HasPrefix(r.URL.Path, "/v1/events/") {
			id := strings.TrimPrefix(r.URL.Path, "/v1/events/")
			for _, event := range f.Events {
				var e struct {
					ID string `json:"id"`
				}
				if json.Unmarshal([]byte(event.Payload), &e) == nil && e.ID == id {
					body = []byte(event.Payload)
					break
				}
			}
		}
		if len(body) == 0 {
			return nil, fmt.Errorf("unexpected local provider API route")
		}
	}
	if err != nil {
		return nil, fmt.Errorf("invalid local provider fixture")
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
}

// The orchestrator creates fresh, isolated Rust and Go PostgreSQL databases and
// signs the raw payload outside Go. Run recv and settle in separate Go processes,
// with a real >5 minute pause and a rebuilt Rust UDS service between phases.
// This test receives no Stripe webhook secret and never contacts Stripe.
func TestRustDurableProofAcrossGoRestart(t *testing.T) {
	path := os.Getenv("PAYMENT_CORE_PROOF_FIXTURE")
	if path == "" {
		t.Skip("PAYMENT_CORE_PROOF_FIXTURE is not configured; real joint test NOT RUN")
	}
	phase := os.Getenv("PAYMENT_CORE_PROOF_PHASE")
	if phase != "recv" && phase != "recv-lost" && phase != "offline" && phase != "settle" {
		t.Fatal("joint phase must be recv, recv-lost, offline or settle")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm()&0077 != 0 {
		t.Fatal("joint fixture must be a private file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("cannot read private joint fixture")
	}
	var f proofJointFixture
	if json.Unmarshal(raw, &f) != nil || f.IntentID <= 0 || f.Account <= 0 || f.Channel.Provider != "stripe" || f.Channel.Environment != "test" || len(f.Events) < 2 || !strings.HasPrefix(f.DatabaseName, "lmm_") || !strings.Contains(f.DatabaseName, "payment") {
		t.Fatal("invalid isolated joint fixture")
	}
	info, err = os.Stat(f.DatabaseURLFile)
	if err != nil || info.Mode().Perm()&0077 != 0 {
		t.Fatal("Go database URL must be a private file")
	}
	dsn, err := os.ReadFile(f.DatabaseURLFile)
	if err != nil {
		t.Fatal("cannot read Go database URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db, err := sql.Open("postgres", strings.TrimSpace(string(dsn)))
	if err != nil {
		t.Fatal("cannot open isolated payment database")
	}
	defer db.Close()
	db.SetMaxOpenConns(16)
	store, err := p.NewPostgres(ctx, db, f.DatabaseName)
	if err != nil {
		t.Fatal("isolated payment storage guard rejected fixture")
	}
	client, err := coreclient.New(f.Socket, []byte(f.Token))
	if err != nil {
		t.Fatal("cannot open isolated core client")
	}
	defer client.Close()
	bridge, err := rustbridge.New(client)
	if err != nil {
		t.Fatal("cannot construct bridge")
	}
	stripe, err := p.NewStripe(p.StripeConfig{Channel: f.Channel, APIKey: "sk_test_joint_fixture_only", APIVersion: "joint-fixture-version", SuccessURL: "https://joint.example.invalid/paid", CancelURL: "https://joint.example.invalid/canceled"})
	if err != nil {
		t.Fatal("cannot construct isolated Stripe parser")
	}
	p.ReplaceStripeTransportForTest(stripe, proofJointTransport{fixture: f})
	service, err := p.New(store, bridge, stripe)
	if err != nil {
		t.Fatal("cannot construct payment service")
	}
	// Exercise the Go callback over real loopback HTTP. This does not traverse
	// Caddy or claim proxy/browser acceptance.
	callbackServer := httptest.NewServer(service.WebhookHandler())
	defer callbackServer.Close()
	httpClient := callbackServer.Client()
	httpClient.Timeout = 12 * time.Second
	orderID := "top_" + strconv.FormatInt(f.IntentID, 10)
	deliver := func(start, end, want int) {
		for _, event := range f.Events[start:end] {
			r, err := http.NewRequestWithContext(ctx, "POST", callbackServer.URL+"/callbacks/"+strconv.FormatInt(f.Channel.ID, 10), strings.NewReader(event.Payload))
			if err != nil {
				t.Fatal("cannot construct local callback request")
			}
			r.Header.Set("Stripe-Signature", event.Signature)
			response, err := httpClient.Do(r)
			if err != nil {
				t.Fatal("local callback HTTP transport failed")
			}
			_, _ = io.CopyN(io.Discard, response.Body, 4096)
			_ = response.Body.Close()
			if response.StatusCode != want {
				t.Fatalf("durable webhook ingress rejected fixture: HTTP %d", response.StatusCode)
			}
		}
	}
	if phase == "recv" {
		o, err := service.Create(ctx, f.User, p.PrepareRequest{Key: f.PrepareKey, AccountID: f.Account, ChannelID: f.Channel.ID, Currency: f.Currency, AmountMinor: f.AmountMinor})
		if err != nil || o.ID != orderID {
			t.Fatal("real Rust prepare did not match frozen joint intent")
		}
		deliver(0, 1, 200)
		o, err = store.Get(ctx, orderID)
		if err != nil || o.Credited() {
			t.Fatal("proof ACK became a false credit")
		}
		events, err := store.Events(ctx, time.Now().Add(time.Second), 100)
		if err != nil || len(events) != 1 {
			t.Fatal("durable Go inbox missing accepted evidence")
		}
		for _, e := range events {
			if e.Evidence.AcceptedProofID == "" || len(e.Evidence.Payload) == 0 || len(e.Evidence.Signature) == 0 {
				t.Fatal("Go inbox omitted Rust proof binding")
			}
		}
		// Drain creates the durable job without calling the money worker. The
		// parent can now construct a legacy done-inbox/review-job without a
		// proof reference, while retaining the real Rust accepted proof.
		if err = service.Drain(ctx, 100); err != nil {
			t.Fatal("accepted inbox did not enqueue durable job")
		}
		o, err = store.Get(ctx, orderID)
		if err != nil || o.Credited() {
			t.Fatal("draining inbox claimed money")
		}
		credits := 0
		for _, j := range o.Jobs {
			if j.Kind == "credit" {
				credits++
				if j.State != "pending" || j.Evidence.AcceptedProofID == "" || j.Receipt != nil {
					t.Fatal("accepted credit job missing or falsely settled")
				}
			}
		}
		if credits != 1 {
			t.Fatal("accepted inbox did not create one durable credit job")
		}
		t.Log("recv: real Rust proof and Go inbox persisted; inbox drained to one durable credit job, no credit applied")
		return
	}
	if phase == "recv-lost" || phase == "offline" {
		index := 1
		if phase == "offline" {
			index = 0
		}
		deliver(index, index+1, 503)
		o, err := store.Get(ctx, orderID)
		if err != nil || o.Credited() {
			t.Fatal("failed ingress claimed credit")
		}
		var count int
		if err = db.QueryRowContext(ctx, `SELECT count(*) FROM payment_events`).Scan(&count); err != nil || count != 1 {
			t.Fatal("failed ingress changed persisted inbox")
		}
		t.Log("failed ingress returned non-2xx and did not claim settlement")
		return
	}
	deliver(0, len(f.Events), 200)
	if err = service.Work(ctx, 100); err != nil {
		t.Fatal("aged durable proof did not drain")
	}
	o, err := store.Get(ctx, orderID)
	if err != nil || !o.Credited() {
		t.Fatal("aged accepted proof failed real Rust settlement")
	}
	creditReceipt := o.CreditReceipt
	deliver(0, len(f.Events), 200)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := service.Work(ctx, 100); e != nil {
				t.Error("concurrent durable replay failed")
			}
		}()
	}
	wg.Wait()
	if err = service.Work(ctx, 100); err != nil {
		t.Fatal("final durable drain failed")
	}
	o, err = store.Get(ctx, orderID)
	if err != nil || !o.Credited() || o.CreditReceipt != creditReceipt {
		t.Fatal("duplicate replay altered credit receipt")
	}
	credits := 0
	for _, j := range o.Jobs {
		if j.Kind == "credit" {
			credits++
			if j.State != "done" || j.Receipt == nil || j.Receipt.ID != creditReceipt || j.Evidence.AcceptedProofID == "" {
				t.Fatal("credit job lost accepted proof or durable receipt")
			}
		}
	}
	if credits != 1 {
		t.Fatal("duplicate callbacks created more than one credit job")
	}
	t.Log("settle: restarted Go recovered aged proof, real Rust credit receipt stable under duplicate concurrent replay")
}
