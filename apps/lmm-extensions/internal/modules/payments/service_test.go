package payments

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCreateExplicitAccountAndIdempotency(t *testing.T) {
	h := setup(t)
	o := h.create("create_fixture_0001")
	again := h.create("create_fixture_0001")
	if o.ID != again.ID || o.Intent.AccountID != 99 || o.Intent.CreatedBy != 7 || o.Intent.CreditUnits != 12345 || o.Credited() {
		t.Fatal("intent was not preserved")
	}
	if h.a.checkoutCalls.Load() != 0 {
		t.Fatal("provider called before durable job")
	}
	h.work()
	h.work()
	if h.a.checkoutCalls.Load() != 1 {
		t.Fatal("duplicate checkout")
	}
	h.c.deny = true
	if _, err := h.s.Create(context.Background(), "user", PrepareRequest{Key: o.Intent.Key, AccountID: 99, ChannelID: 1, Currency: "USD", AmountMinor: 1000}); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	if _, err := h.s.Get(context.Background(), "user", o.ID); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
}
func TestChangedAmountUnderSameKeyRejected(t *testing.T) {
	h := setup(t)
	o := h.create("create_fixture_0001")
	_, err := h.s.Create(context.Background(), "user", PrepareRequest{Key: o.Intent.Key, AccountID: 99, ChannelID: 1, Currency: "USD", AmountMinor: 999})
	if !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}
func TestOfflinePrepareDoesNotCreateProviderOrder(t *testing.T) {
	h := setup(t)
	h.c.offline = true
	_, err := h.s.Create(context.Background(), "user", PrepareRequest{Key: "create_fixture_0001", AccountID: 99, ChannelID: 1, Currency: "USD", AmountMinor: 1000})
	if err == nil || len(h.m.orders) != 0 || h.a.checkoutCalls.Load() != 0 {
		t.Fatal("offline core granted payment authority")
	}
}
func TestConcurrentDuplicateCallbacksCreditOnce(t *testing.T) {
	h := setup(t)
	o := h.create("create_fixture_0001")
	h.work()
	e := h.event(o, "evt_same", Paid, 1000)
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := h.s.receiveEvent(context.Background(), h.a, e); err != nil {
				t.Error(err)
			}
			if err := h.s.Work(context.Background(), 100); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	h.work()
	if !h.get(o.ID).Credited() || h.c.count("credit") != 1 || len(h.m.events) != 1 {
		t.Fatal("duplicate ledger credit", h.c.count("credit"))
	}
	e.Evidence.EventID = "evt_second_paid"
	h.send(e)
	h.work()
	if h.c.count("credit") != 1 {
		t.Fatal("semantic duplicate credited twice")
	}
}
func TestRejectedEventBindings(t *testing.T) {
	for _, tc := range []string{"amount", "currency", "merchant", "environment", "order", "transaction"} {
		t.Run(tc, func(t *testing.T) {
			h := setup(t)
			o := h.create("create_fixture_0001")
			h.work()
			e := h.event(o, "evt_wrong", Paid, 1000)
			switch tc {
			case "amount":
				e.AmountMinor = 999
			case "currency":
				e.Currency = "CNY"
			case "merchant":
				e.Evidence.Merchant = "acct_wrong"
			case "environment":
				e.Evidence.Environment = "live"
			case "order":
				e.OrderID = "top_missing"
			case "transaction":
				if err := h.m.Mutate(context.Background(), o.ID, func(o *Order) error { o.PaymentID = "pi_other"; return nil }); err != nil {
					t.Fatal(err)
				}
			}
			_ = h.s.receiveEvent(context.Background(), h.a, e)
			h.work()
			if h.get(o.ID).Credited() || h.c.count("credit") != 0 {
				t.Fatal("unbound event credited")
			}
		})
	}
}
func TestProviderEventIDCannotChangeMeaning(t *testing.T) {
	h := setup(t)
	o := h.create("create_fixture_0001")
	e := h.event(o, "evt_same", Paid, 1000)
	h.send(e)
	e.AmountMinor = 999
	if err := h.s.receiveEvent(context.Background(), h.a, e); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}
func TestProviderTransactionCannotCreditTwoOrders(t *testing.T) {
	h := setup(t)
	first := h.paid()
	second := h.create("create_fixture_0002")
	e := h.event(second, "evt_other", Paid, 1000)
	e.Evidence.Transaction = first.PaymentID
	h.send(e)
	h.work()
	if h.get(second.ID).Credited() || h.c.count("credit") != 1 {
		t.Fatal("transaction rebound")
	}
}
func TestCoreCommitWithLostResponseRecoveredByReceipt(t *testing.T) {
	h := setup(t)
	o := h.create("create_fixture_0001")
	h.work()
	h.c.lose["credit"] = true
	h.send(h.event(o, "evt_paid", Paid, 1000))
	h.work()
	if h.get(o.ID).Credited() {
		t.Fatal("timeout shown as success")
	}
	h.advance()
	h.work()
	if !h.get(o.ID).Credited() || h.c.count("credit") != 1 || h.c.count("lookup") != 1 {
		t.Fatal("lost response not reconciled")
	}
}
func TestCoreOutageAndUnspecifiedReceiptNeverCredit(t *testing.T) {
	for _, mode := range []string{"offline", "queued"} {
		t.Run(mode, func(t *testing.T) {
			h := setup(t)
			o := h.create("create_fixture_0001")
			h.work()
			h.c.offline = mode == "offline"
			h.c.invalidReceipt = mode == "queued"
			h.send(h.event(o, "evt_paid", Paid, 1000))
			h.work()
			if h.get(o.ID).Credited() {
				t.Fatal("uncertain result credited")
			}
			if !h.get(o.ID).ProviderPaid {
				t.Fatal("provider state lost")
			}
		})
	}
}
func TestLateFailureDoesNotErasePayment(t *testing.T) {
	h := setup(t)
	o := h.paid()
	h.send(h.event(o, "evt_late_fail", PaymentFailed, 1000))
	h.work()
	if !h.get(o.ID).Credited() {
		t.Fatal("late failure erased success")
	}
}
func TestRefundBeforeCreditFencesLatePayment(t *testing.T) {
	h := setup(t)
	o := h.create("create_fixture_0001")
	h.work()
	r := h.event(o, "evt_refund", RefundSucceeded, 1000)
	r.RefundID = "re_external"
	h.send(r)
	h.work()
	h.send(h.event(o, "evt_late_paid", Paid, 1000))
	h.work()
	o = h.get(o.ID)
	if o.Credited() || !o.RefundFence || h.c.count("credit") != 0 || h.c.count("external") != 1 || o.State() != "refunded" {
		t.Fatal("pre-credit refund was replayed as credit", o.State())
	}
}
func TestCallbackBeforeLocalOrderIsRetried(t *testing.T) {
	h := setup(t)
	e := h.event(Order{ID: "top_1"}, "evt_early", Paid, 1000)
	h.send(e)
	h.work()
	if h.c.count("credit") != 0 {
		t.Fatal("orphan credited")
	}
	o := h.create("create_fixture_0001")
	h.advance()
	h.work()
	if !h.get(o.ID).Credited() {
		t.Fatal("late order did not consume durable callback")
	}
}
func TestPartialRefundNeedsCoreHoldAndVerifiedSuccess(t *testing.T) {
	h := setup(t)
	o := h.paid()
	r, err := h.s.RequestRefund(context.Background(), "user", o.ID, "refund_fixture_001", 300)
	if err != nil || r.State != "held" {
		t.Fatal(r, err)
	}
	if h.a.refundCalls.Load() != 0 {
		t.Fatal("refund sent before durable worker")
	}
	h.work()
	o = h.get(o.ID)
	r = o.Refunds[r.ID]
	if r.State != "awaiting_evidence" || o.State() != "refund_pending" {
		t.Fatal("API response treated as settled refund")
	}
	e := h.event(o, "evt_refund", RefundSucceeded, 300)
	e.RefundID = r.ProviderID
	e.LocalRefundID = r.ID
	h.send(e)
	h.work()
	o = h.get(o.ID)
	if o.State() != "partially_refunded" || h.c.count("commit") != 1 {
		t.Fatal(o.State())
	}
	h.send(e)
	h.work()
	if h.c.count("commit") != 1 {
		t.Fatal("refund committed twice")
	}
	if _, err = h.s.RequestRefund(context.Background(), "user", o.ID, "refund_fixture_002", 701); !errors.Is(err, ErrConflict) {
		t.Fatal("over-refund accepted", err)
	}
}
func TestConcurrentRefundsCannotOverReserve(t *testing.T) {
	h := setup(t)
	o := h.paid()
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted := 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := h.s.RequestRefund(context.Background(), "user", o.ID, fmt.Sprintf("refund_parallel_%03d", i), 300)
			if err == nil {
				mu.Lock()
				accepted++
				mu.Unlock()
			} else if !errors.Is(err, ErrConflict) {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if accepted != 3 || h.c.count("hold") != 3 {
		t.Fatal("refund total not capped", accepted)
	}
}
func TestHoldTimeoutRecoveredWithoutPersistingUserCredential(t *testing.T) {
	h := setup(t)
	o := h.paid()
	h.c.lose["hold"] = true
	r, err := h.s.RequestRefund(context.Background(), "private_user_credential", o.ID, "refund_fixture_001", 500)
	if err != nil || r.State != "hold_pending" {
		t.Fatal(r, err)
	}
	h.work()
	o = h.get(o.ID)
	if o.Refunds[r.ID].State != "held" || h.c.count("hold") != 1 {
		t.Fatal("hold recovery failed")
	}
	b, _ := encodeOrder(o)
	if strings.Contains(string(b), "private_user_credential") {
		t.Fatal("credential persisted")
	}
}
func TestRefundTimeoutNeverReleasesHold(t *testing.T) {
	h := setup(t)
	o := h.paid()
	r, err := h.s.RequestRefund(context.Background(), "user", o.ID, "refund_fixture_001", 1000)
	if err != nil {
		t.Fatal(err)
	}
	h.a.refundErr = ErrUnavailable
	h.work()
	h.advance()
	h.work()
	o = h.get(o.ID)
	if o.Refunds[r.ID].HoldReceipt == "" || o.Refunds[r.ID].State == "released" || h.c.count("release") != 0 || h.a.refundCalls.Load() != 2 {
		t.Fatal("unknown refund released hold")
	}
}
func TestLegacyRefundAndExpiredIdempotencyNeverResubmitted(t *testing.T) {
	for _, window := range []time.Duration{0, 23 * time.Hour} {
		t.Run(fmt.Sprint(window), func(t *testing.T) {
			h := setup(t)
			o := h.paid()
			_, err := h.s.RequestRefund(context.Background(), "user", o.ID, "refund_fixture_001", 1000)
			if err != nil {
				t.Fatal(err)
			}
			h.a.window = window
			h.a.refundErr = ErrUnavailable
			h.work()
			h.clock.Add(int64((24 * time.Hour) / time.Second))
			h.work()
			if h.a.refundCalls.Load() != 1 || h.c.count("release") != 0 {
				t.Fatal("ambiguous provider write replayed")
			}
		})
	}
}
func TestVerifiedTerminalFailureReleasesButLateFailureCannotUndoSuccess(t *testing.T) {
	for _, successFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(successFirst), func(t *testing.T) {
			h := setup(t)
			o := h.paid()
			r, err := h.s.RequestRefund(context.Background(), "user", o.ID, "refund_fixture_001", 500)
			if err != nil {
				t.Fatal(err)
			}
			h.work()
			r = h.get(o.ID).Refunds[r.ID]
			e := h.event(o, "evt_failed", RefundFailed, 500)
			e.RefundID = r.ProviderID
			e.LocalRefundID = r.ID
			if successFirst {
				v := e
				v.Type = RefundSucceeded
				v.Evidence.EventID = "evt_success"
				h.send(v)
				h.work()
			}
			h.send(e)
			h.work()
			o = h.get(o.ID)
			if successFirst {
				if o.Refunds[r.ID].State != "committed" || h.c.count("release") != 0 {
					t.Fatal("late failed refund regressed")
				}
			} else if o.Refunds[r.ID].State != "released" || h.c.count("release") != 1 {
				t.Fatal("verified failure not released")
			}
		})
	}
}
func TestRefundCommitLostResponseReconciled(t *testing.T) {
	h := setup(t)
	o := h.paid()
	r, err := h.s.RequestRefund(context.Background(), "user", o.ID, "refund_fixture_001", 1000)
	if err != nil {
		t.Fatal(err)
	}
	h.work()
	r = h.get(o.ID).Refunds[r.ID]
	e := h.event(o, "evt_refund", RefundSucceeded, 1000)
	e.RefundID = r.ProviderID
	e.LocalRefundID = r.ID
	h.c.lose["commit"] = true
	h.send(e)
	h.work()
	if h.get(o.ID).State() == "refunded" {
		t.Fatal("unacknowledged refund shown as complete")
	}
	h.advance()
	h.work()
	if h.get(o.ID).State() != "refunded" || h.c.count("commit") != 1 {
		t.Fatal("refund receipt not reconciled")
	}
}
func TestSignedEvidenceUpgradesUnsignedObservation(t *testing.T) {
	h := setup(t)
	o := h.create("create_fixture_0001")
	h.work()
	e := h.event(o, "api_observed", Paid, 1000)
	e.Evidence.Source = "api"
	e.Evidence.Signature = nil
	h.a.observations = []Event{e}
	if err := h.s.Reconcile(context.Background(), o.ID); err != nil {
		t.Fatal(err)
	}
	h.work()
	if h.get(o.ID).Credited() {
		t.Fatal("unsigned observation credited")
	}
	e.Evidence.Source = "webhook"
	e.Evidence.Signature = []byte("signed")
	e.Evidence.EventID = "evt_verified"
	h.send(e)
	h.work()
	if !h.get(o.ID).Credited() {
		t.Fatal("signed evidence lost")
	}
}
func TestSignedEvidenceArrivingDuringAPIJobIsNotLost(t *testing.T) {
	h := setup(t)
	o := h.create("create_fixture_0001")
	h.work()
	e := h.event(o, "api_observed", Paid, 1000)
	e.Evidence.Source = "api"
	e.Evidence.Signature = nil
	h.send(e)
	if err := h.s.Drain(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	h.c.creditStarted = make(chan struct{}, 1)
	h.c.creditGate = make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- h.s.Step(context.Background(), o.ID) }()
	<-h.c.creditStarted
	e.Evidence.Source = "webhook"
	e.Evidence.Signature = []byte("signed")
	e.Evidence.EventID = "evt_verified"
	h.send(e)
	if err := h.s.Drain(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	close(h.c.creditGate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	h.c.creditStarted = nil
	h.c.creditGate = nil
	h.work()
	if !h.get(o.ID).Credited() {
		t.Fatal("concurrent signed evidence lost")
	}
}
func TestRefundDisabledBeforeHold(t *testing.T) {
	h := setup(t)
	o := h.paid()
	h.a.disabled = true
	if _, err := h.s.RequestRefund(context.Background(), "user", o.ID, "refund_fixture_001", 1000); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	if h.c.count("hold") != 0 {
		t.Fatal("unsupported refund froze funds")
	}
}
func TestFullRefundOnlyPolicy(t *testing.T) {
	h := setup(t)
	o := h.paid()
	h.a.fullOnly = true
	if _, err := h.s.RequestRefund(context.Background(), "user", o.ID, "refund_fixture_001", 500); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
}
func TestReceiptValidationRequiresIdentityStateAndJournal(t *testing.T) {
	valid := Receipt{ID: "r", IntentID: 1, Key: "key", RequestHash: make([]byte, 32), State: CreditApplied, JournalID: 5}
	for _, change := range []func(*Receipt){func(r *Receipt) { r.ID = "" }, func(r *Receipt) { r.IntentID = 2 }, func(r *Receipt) { r.Key = "other" }, func(r *Receipt) { r.RequestHash = nil }, func(r *Receipt) { r.State = "queued" }, func(r *Receipt) { r.JournalID = 0 }} {
		r := valid
		change(&r)
		if receiptMatches(r, 1, "key", CreditApplied) {
			t.Fatal("invalid receipt accepted")
		}
	}
}
func TestHTTPNeverExposesEvidenceOrInternalJobs(t *testing.T) {
	h := setup(t)
	o := h.paid()
	r := httptest.NewRequest("GET", "/orders/"+o.ID, nil)
	r.Header.Set("X-LMM-User-Credential", strings.Repeat("a", 32))
	w := httptest.NewRecorder()
	h.s.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, word := range []string{"signed_fixture", "jobs", "request_hash", "receipt_pay", "hold_receipt"} {
		if strings.Contains(w.Body.String(), word) {
			t.Fatal("internal data leaked", word)
		}
	}
	r = httptest.NewRequest("GET", "/orders/"+o.ID, nil)
	r.Header.Set("Authorization", "Bearer host-secret")
	w = httptest.NewRecorder()
	h.s.Handler().ServeHTTP(w, r)
	if w.Code < 400 {
		t.Fatal("host token authorized user")
	}
}
func TestHTTPRejectsDuplicateAndUnknownFields(t *testing.T) {
	for _, body := range []string{`{"account_id":99,"account_id":1,"channel_id":1,"currency":"USD","amount_minor":1000}`, `{"account_id":99,"channel_id":1,"currency":"USD","amount_minor":1000,"credit_units":1000000}`, `{"account_id":99,"channel_id":1,"currency":"USD","amount_minor":10.1}`, `{} {}`} {
		h := setup(t)
		r := httptest.NewRequest("POST", "/orders", strings.NewReader(body))
		r.Header.Set("X-LMM-User-Credential", strings.Repeat("a", 32))
		r.Header.Set("Idempotency-Key", "create_fixture_0001")
		w := httptest.NewRecorder()
		h.s.Handler().ServeHTTP(w, r)
		if w.Code < 400 || h.c.count("prepare") != 0 {
			t.Fatal("unsafe request reached core", w.Code, body)
		}
	}
}
func TestCanceledWorkerDoesNotSendMoney(t *testing.T) {
	h := setup(t)
	o := h.create("create_fixture_0001")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := h.s.Step(ctx, o.ID); err == nil {
		t.Fatal("canceled mutation ran")
	}
	if h.a.checkoutCalls.Load() != 0 {
		t.Fatal("canceled request called provider")
	}
}
