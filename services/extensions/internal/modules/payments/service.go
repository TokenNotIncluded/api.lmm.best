package payments

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"
)

type Service struct {
	store    Store
	core     Core
	adapters map[int64]Adapter
	now      func() time.Time
}

func New(store Store, core Core, adapters ...Adapter) (*Service, error) {
	if store == nil || core == nil {
		return nil, ErrInvalid
	}
	s := &Service{store: store, core: core, adapters: make(map[int64]Adapter), now: time.Now}
	namespaces := map[string]bool{}
	for _, a := range adapters {
		if a == nil {
			return nil, ErrInvalid
		}
		c := a.Channel()
		if !c.valid() || s.adapters[c.ID] != nil || namespaces[c.namespace()] {
			return nil, ErrInvalid
		}
		s.adapters[c.ID] = a
		namespaces[c.namespace()] = true
	}
	return s, nil
}
func (s *Service) Name() string { return "payments" }

// orderAdapter binds recovery to the original Core intent. A reused channel ID
// must never redirect an old checkout, lookup or refund to another merchant.
// Enabled gates new orders only; disabled channels can still finish old work.
func (s *Service) orderAdapter(o Order) (Adapter, error) {
	a := s.adapters[o.ChannelID]
	if a == nil {
		return nil, ErrUnavailable
	}
	c := a.Channel()
	if c.ID != o.ChannelID || c.namespace() != o.Namespace ||
		c.Provider != o.Intent.Provider || c.Merchant != o.Intent.Merchant ||
		c.Environment != o.Intent.Environment || c.Currency != o.Intent.Currency {
		return nil, ErrConflict
	}
	return a, nil
}

func (s *Service) Create(ctx context.Context, credential string, req PrepareRequest) (Order, error) {
	a := s.adapters[req.ChannelID]
	if a == nil || !a.Channel().Enabled || !validKey(req.Key) || req.AccountID <= 0 || !validAmount(req.AmountMinor) || req.Currency != a.Channel().Currency {
		return Order{}, ErrInvalid
	}
	// Prepare is repeated even on retries, so revoked permissions cannot reuse an
	// old local order as an authorization cache. Go does not calculate credit.
	intent, err := s.core.Prepare(ctx, credential, req)
	if err != nil {
		return Order{}, err
	}
	c := a.Channel()
	if intent.ID <= 0 || intent.CreatedBy <= 0 || intent.AccountID != req.AccountID || intent.AmountMinor != req.AmountMinor || intent.Currency != req.Currency || intent.Key != req.Key ||
		intent.Provider != c.Provider || intent.Merchant != c.Merchant || intent.Environment != c.Environment || len(intent.RequestHash) != 32 || intent.CreditUnits <= 0 || intent.CreditUnit != "credit_500k_usd" {
		return Order{}, ErrConflict
	}
	id := "top_" + strconv.FormatInt(intent.ID, 10)
	o := Order{ID: id, ChannelID: c.ID, Namespace: c.namespace(), Intent: intent, CreditState: "pending", Refunds: map[string]Refund{}, Jobs: map[string]Job{}}
	enqueue(&o, Job{Key: stableKey("checkout", id), Kind: "checkout"}, s.now())
	if err = s.store.Insert(ctx, o); err != nil {
		return Order{}, err
	}
	stored, err := s.store.Get(ctx, id)
	if err != nil {
		return Order{}, err
	}
	if stored.ChannelID != o.ChannelID || stored.Intent.Key != intent.Key || stored.Intent.AccountID != intent.AccountID || stored.Intent.AmountMinor != intent.AmountMinor || eventHash(stored.Intent.RequestHash) != eventHash(intent.RequestHash) {
		return Order{}, ErrConflict
	}
	return stored, nil
}
func (s *Service) Get(ctx context.Context, credential, id string) (Order, error) {
	o, err := s.store.Get(ctx, id)
	if err != nil {
		return Order{}, err
	}
	if err = s.core.Read(ctx, credential, o.Intent); err != nil {
		return Order{}, err
	}
	return o, nil
}
func (s *Service) RequestRefund(ctx context.Context, credential, id, key string, amount int64) (Refund, error) {
	if !validKey(key) || !validAmount(amount) {
		return Refund{}, ErrInvalid
	}
	o, err := s.Get(ctx, credential, id)
	if err != nil {
		return Refund{}, err
	}
	a, err := s.orderAdapter(o)
	if err != nil {
		return Refund{}, err
	}
	if !a.RefundEnabled() || amount != o.Intent.AmountMinor && !a.PartialRefunds() {
		return Refund{}, ErrUnsupported
	}
	rid := stableKey("refund", id, key)
	holdKey := stableKey("hold", rid)
	err = s.store.Mutate(ctx, id, func(o *Order) error {
		if old, ok := o.Refunds[rid]; ok {
			if old.AmountMinor != amount {
				return ErrConflict
			}
			return nil
		}
		if !o.Credited() || o.RefundFence || o.ErrorCode != "" {
			return ErrConflict
		}
		remaining := o.Intent.AmountMinor
		for _, r := range o.Refunds {
			if r.State != "released" && r.State != "rejected" {
				remaining -= r.AmountMinor
			}
		}
		if amount > remaining {
			return ErrConflict
		}
		o.Refunds[rid] = Refund{ID: rid, AmountMinor: amount, HoldKey: holdKey, State: "hold_pending"}
		return nil
	})
	if err != nil {
		return Refund{}, err
	}
	// The current user credential is never persisted. A timeout is recovered by a
	// receipt lookup, or by the user repeating this same request with fresh auth.
	receipt, callErr := s.core.Hold(ctx, credential, o.Intent.ID, holdKey, amount)
	err = s.store.Mutate(ctx, id, func(o *Order) error {
		r := o.Refunds[rid]
		if r.HoldReceipt != "" || r.State == "rejected" {
			return nil
		}
		if callErr != nil || !receiptMatches(receipt, o.Intent.ID, holdKey, RefundHeld) {
			enqueue(o, Job{Key: stableKey("lookup-hold", rid), Kind: "lookup_hold", RefundID: rid}, s.now())
			return nil
		}
		acceptHold(o, &r, receipt, s.now())
		o.Refunds[rid] = r
		return nil
	})
	if err != nil {
		return Refund{}, err
	}
	o, err = s.store.Get(ctx, id)
	if err != nil {
		return Refund{}, err
	}
	if errors.Is(callErr, ErrDenied) {
		return Refund{}, ErrDenied
	}
	return o.Refunds[rid], nil
}
func acceptHold(o *Order, r *Refund, receipt Receipt, now time.Time) {
	if receipt.State == Rejected {
		r.State = "rejected"
		return
	}
	r.HoldReceipt = receipt.ID
	r.State = "held"
	enqueue(o, Job{Key: stableKey("submit", r.ID), Kind: "refund_submit", RefundID: r.ID}, now)
}
func enqueue(o *Order, j Job, now time.Time) {
	if old, ok := o.Jobs[j.Key]; ok {
		if old.State != "done" && old.State != "rejected" && old.State != "fenced" && strongerProof(old.Evidence, j.Evidence) {
			old.Evidence = j.Evidence
			if old.State != "running" {
				old.State = "pending"
			}
			old.ErrorCode = ""
			old.NextAttempt = now
			o.Jobs[j.Key] = old
		}
		return
	}
	j.State = "pending"
	j.NextAttempt = now
	o.Jobs[j.Key] = j
}

func strongerProof(old, next Evidence) bool {
	if old.AcceptedProofID != "" || next.AcceptedProofID == "" || next.Source != "webhook" || !samePayment(old, next) {
		return false
	}
	// A semantic job may be repaired by a second independently accepted event.
	// The same provider event can never change its original signed payload.
	return old.EventID != next.EventID || eventHash(old.Payload) == eventHash(next.Payload)
}

// Receive acknowledges only after Rust durably accepts the provider proof and
// Go persists the inbox event. It does not claim credit.
// proofGatedCallback separates parsing from credentialed provider observation.
// No Go flag authenticates evidence: Core returns the durable proof receipt.
// Adapters without credentialed callback I/O keep their existing Verify path.
type proofGatedCallback interface {
	parseCallback(*http.Request) (Event, error)
	proofOrderID(Event) (string, error)
	observeCallback(context.Context, Event) (Event, error)
}

func (s *Service) Receive(ctx context.Context, channelID int64, req *http.Request) error {
	a := s.adapters[channelID]
	if a == nil {
		return ErrNotFound
	}
	if gated, ok := a.(proofGatedCallback); ok {
		e, err := gated.parseCallback(req)
		if err != nil {
			return err
		}
		e, err = s.admitWebhookProof(ctx, a, e)
		if err != nil {
			return err
		}
		observed, err := gated.observeCallback(ctx, e)
		if err != nil {
			return err
		}
		// Provider observation may resolve a missing order reference, but must
		// not replace the Core-accepted event, payload or payment identity.
		expected := e
		if expected.OrderID == "" {
			expected.OrderID, err = gated.proofOrderID(e)
			if err != nil {
				return err
			}
		}
		if eventDigest(expected) != eventDigest(observed) || !samePayment(e.Evidence, observed.Evidence) ||
			e.Evidence.EventID != observed.Evidence.EventID || e.Evidence.Source != observed.Evidence.Source ||
			e.Evidence.AcceptedProofID != observed.Evidence.AcceptedProofID ||
			!bytes.Equal(e.Evidence.Payload, observed.Evidence.Payload) || !bytes.Equal(e.Evidence.Signature, observed.Evidence.Signature) {
			return ErrConflict
		}
		return s.receiveEvent(ctx, a, observed)
	}
	e, err := a.Verify(req, s.now())
	if err != nil {
		return err
	}
	return s.receiveEvent(ctx, a, e)
}
func validateReceivedEvent(a Adapter, e Event) error {
	c := a.Channel()
	if !sameChannel(e.Evidence, c) || !validID(e.Evidence.EventID) || !validID(e.Evidence.Transaction) || e.Currency != c.Currency || !validAmount(e.AmountMinor) || len(e.Evidence.Payload) > MaxEvidence {
		return ErrInvalid
	}
	if e.Type != Paid && e.Type != PaymentFailed && e.Type != RefundSucceeded && e.Type != RefundFailed && e.Type != RefundPending {
		return ErrUnsupported
	}
	if e.Type == RefundSucceeded || e.Type == RefundFailed || e.Type == RefundPending {
		if !validID(e.RefundID) {
			return ErrInvalid
		}
	}
	if e.OrderID != "" && !validID(e.OrderID) {
		return ErrInvalid
	}
	return nil
}

// admitWebhookProof persists authenticity/evidence only. It never posts credit,
// creates an inbox item or dispatches provider requests.
func (s *Service) admitWebhookProof(ctx context.Context, a Adapter, e Event) (Event, error) {
	if err := validateReceivedEvent(a, e); err != nil {
		return Event{}, err
	}
	if e.Evidence.Source != "webhook" || len(e.Evidence.Payload) == 0 || len(e.Evidence.Signature) == 0 {
		return Event{}, ErrEvidence
	}
	o, err := s.eventOrder(ctx, a, e)
	if errors.Is(err, ErrNotFound) && e.OrderID == "" {
		if gated, ok := a.(proofGatedCallback); ok {
			// A lost checkout response may leave PaymentID unbound. The signed
			// core intent reference provides only a local lookup candidate;
			// Core still verifies its original bytes/signature and intent.
			candidate := e
			candidate.OrderID, err = gated.proofOrderID(e)
			if err == nil {
				o, err = s.eventOrder(ctx, a, candidate)
			}
		}
	}
	if err != nil {
		return Event{}, err
	}
	// Recover durable acceptance before checking delivery freshness again.
	proof, err := s.core.LookupProof(ctx, o.Intent.ID, e.Evidence)
	if errors.Is(err, ErrNotFound) {
		proof, err = s.core.AcceptProof(ctx, o.Intent.ID, e.Evidence)
	}
	if err != nil {
		return Event{}, err
	}
	if !proofMatches(proof, o.Intent.ID, e.Evidence) {
		return Event{}, ErrConflict
	}
	e.Evidence.AcceptedProofID = proof.ID
	return e, nil
}
func (s *Service) receiveEvent(ctx context.Context, a Adapter, e Event) error {
	c := a.Channel()
	if err := validateReceivedEvent(a, e); err != nil {
		return err
	}
	if e.Evidence.Source == "webhook" {
		var err error
		e, err = s.admitWebhookProof(ctx, a, e)
		if err != nil {
			return err
		}
	} else if e.Evidence.Source != "api" || e.Evidence.AcceptedProofID != "" {
		return ErrEvidence
	}
	e.ChannelID = c.ID
	e.Key = eventKey(e)
	e.Digest = eventDigest(e)
	e.Status = "pending"
	e.NextAttempt = s.now()
	return s.store.PutEvent(ctx, e)
}

func (s *Service) eventOrder(ctx context.Context, a Adapter, e Event) (Order, error) {
	e.ChannelID = a.Channel().ID
	o, err := s.storedEventOrder(ctx, e)
	if err != nil {
		return Order{}, err
	}
	if _, err := s.orderAdapter(o); err != nil {
		return Order{}, err
	}
	return o, nil
}

// Drain persists a job before marking the inbox item done. A crash between the
// two writes replays the event safely through the same semantic job key.
func (s *Service) Drain(ctx context.Context, limit int) error {
	if limit < 1 || limit > 100 {
		return ErrInvalid
	}
	events, err := s.store.Events(ctx, s.now(), limit)
	if err != nil {
		return err
	}
	for _, e := range events {
		err = s.apply(ctx, e)
		status, code := "done", ""
		next := s.now()
		if err != nil {
			code = errorCode(err)
			status = "pending"
			next = next.Add(time.Minute)
			if errors.Is(err, ErrConflict) || errors.Is(err, ErrInvalid) {
				status = "review"
			}
		}
		if saveErr := s.store.FinishEvent(ctx, e.Key, status, code, next); saveErr != nil {
			return saveErr
		}
	}
	return nil
}

// storedEventOrder uses the immutable order binding, not today's provider
// adapter. Accepted inbox work must survive a crash before the job is created,
// even if the old channel is no longer configured on this Go host.
func (s *Service) storedEventOrder(ctx context.Context, e Event) (Order, error) {
	if !validID(e.Evidence.EventID) || !validID(e.Evidence.Transaction) ||
		!validAmount(e.AmountMinor) || len(e.Evidence.Payload) > MaxEvidence ||
		e.OrderID != "" && !validID(e.OrderID) {
		return Order{}, ErrInvalid
	}
	switch e.Type {
	case Paid, PaymentFailed:
	case RefundSucceeded, RefundFailed, RefundPending:
		if !validID(e.RefundID) {
			return Order{}, ErrInvalid
		}
	default:
		return Order{}, ErrInvalid
	}
	var o Order
	var err error
	if e.OrderID != "" {
		o, err = s.store.Get(ctx, e.OrderID)
	} else {
		namespace := e.Evidence.Provider + "/" + e.Evidence.Merchant + "/" + e.Evidence.Environment
		o, err = s.store.FindPayment(ctx, namespace, e.Evidence.Transaction)
	}
	if err != nil {
		return Order{}, err
	}
	if !storedEventMatches(o, e) {
		return Order{}, ErrConflict
	}
	return o, nil
}

func storedEventMatches(o Order, e Event) bool {
	c := Channel{ID: o.ChannelID, Provider: o.Intent.Provider, Merchant: o.Intent.Merchant, Environment: o.Intent.Environment, Currency: o.Intent.Currency}
	return c.valid() && o.Namespace == c.namespace() && o.ChannelID == e.ChannelID &&
		(e.OrderID == "" || e.OrderID == o.ID) && sameChannel(e.Evidence, c) && e.Currency == c.Currency &&
		(o.PaymentID == "" || o.PaymentID == e.Evidence.Transaction) && e.AmountMinor <= o.Intent.AmountMinor &&
		((e.Type != Paid && e.Type != PaymentFailed) || e.AmountMinor == o.Intent.AmountMinor)
}

func (s *Service) apply(ctx context.Context, e Event) error {
	o, err := s.storedEventOrder(ctx, e)
	if err != nil {
		return err
	}
	if e.Evidence.Source == "webhook" {
		previous := e.Evidence.AcceptedProofID
		e.Evidence, err = s.acceptedEvidence(ctx, o.Intent.ID, e.Evidence)
		if err != nil {
			return err // Look up old acceptance only; never accept a stale new proof.
		}
		if previous != e.Evidence.AcceptedProofID {
			if err = s.store.PutEvent(ctx, e); err != nil {
				return err
			}
		}
	} else {
		if e.Evidence.Source != "api" || e.Evidence.AcceptedProofID != "" {
			return ErrEvidence
		}
		// Unsigned observations still require the configured original channel;
		// they do not gain settlement authority from this recovery path.
		if _, err = s.orderAdapter(o); err != nil {
			return err
		}
	}
	return s.store.Mutate(ctx, o.ID, func(o *Order) error {
		if !storedEventMatches(*o, e) {
			return ErrConflict
		}
		if e.Type == Paid || e.Type == PaymentFailed {
			if e.Type == PaymentFailed {
				return nil
			} // Failure cannot erase later payment.
			o.PaymentID = e.Evidence.Transaction
			o.ProviderPaid = true
			if o.RefundFence {
				return nil
			} // Rust must fence pre-credit refunds atomically.
			if !o.Credited() {
				enqueue(o, Job{Key: stableKey("credit", o.ID), Kind: "credit", Evidence: e.Evidence}, s.now())
			}
			return nil
		}
		// An external refund without an order ID stays in the inbox until its
		// payment reference is bound; it is never attributed by an account header.
		o.PaymentID = e.Evidence.Transaction
		rid := e.LocalRefundID
		if rid != "" {
			if _, ok := o.Refunds[rid]; !ok {
				return ErrConflict
			}
		}
		for key, r := range o.Refunds {
			if r.ProviderID == e.RefundID {
				if rid != "" && rid != key {
					return ErrConflict
				}
				rid = key
			}
		}
		external := rid == ""
		if external {
			rid = stableKey("external", o.Namespace, e.RefundID)
		}
		r, exists := o.Refunds[rid]
		if exists && (r.AmountMinor != e.AmountMinor || r.ProviderID != "" && r.ProviderID != e.RefundID) {
			return ErrConflict
		}
		if !exists {
			r = Refund{ID: rid, AmountMinor: e.AmountMinor, State: "external_pending"}
		}
		r.ProviderID = e.RefundID
		if r.ProviderState == RefundSucceeded && e.Type != RefundSucceeded {
			return nil
		} // No terminal regression.
		if r.ProviderState == RefundFailed && e.Type == RefundSucceeded {
			o.ErrorCode = "conflicting_refund_terminal_state"
			return nil
		}
		if e.Type == RefundPending {
			if r.ProviderState == RefundFailed {
				return nil
			}
			r.ProviderState = e.Type
			o.Refunds[rid] = r
			return nil
		}
		r.ProviderState = e.Type
		if e.Type == RefundFailed {
			if r.HoldReceipt != "" {
				enqueue(o, Job{Key: stableKey("release", rid), Kind: "release", RefundID: rid, Evidence: e.Evidence}, s.now())
			} else if r.HoldKey != "" {
				return ErrUnavailable
			} else {
				r.State = "released"
			}
		} else {
			if !o.Credited() {
				o.RefundFence = true
				o.CreditState = "refund_fenced"
			}
			kind := "external_refund"
			if r.HoldKey != "" {
				if r.HoldReceipt == "" {
					return ErrUnavailable
				}
				kind = "commit"
			}
			enqueue(o, Job{Key: stableKey(kind, o.Namespace, e.RefundID), Kind: kind, RefundID: rid, Evidence: e.Evidence}, s.now())
		}
		o.Refunds[rid] = r
		return nil
	})
}

// Reconcile uses authenticated provider reads only as observations. The core
// bridge must not turn an unsigned API response into signed webhook evidence.
func (s *Service) Reconcile(ctx context.Context, id string) error {
	o, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	a, err := s.orderAdapter(o)
	if err != nil {
		return err
	}
	events, err := a.Lookup(ctx, o)
	if err != nil {
		return err
	}
	for _, e := range events {
		if err = s.receiveEvent(ctx, a, e); err != nil {
			return err
		}
	}
	return nil
}
