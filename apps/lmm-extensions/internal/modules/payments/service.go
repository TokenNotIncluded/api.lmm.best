package payments

import (
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
	a := s.adapters[o.ChannelID]
	if a == nil || !a.RefundEnabled() || amount != o.Intent.AmountMinor && !a.PartialRefunds() {
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
		if old.Evidence.Source == "api" && j.Evidence.Source == "webhook" && old.State != "done" {
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

// Receive returns after durable inbox storage only. It does not claim credit.
func (s *Service) Receive(ctx context.Context, channelID int64, req *http.Request) error {
	a := s.adapters[channelID]
	if a == nil {
		return ErrNotFound
	}
	e, err := a.Verify(req, s.now())
	if err != nil {
		return err
	}
	return s.receiveEvent(ctx, a, e)
}
func (s *Service) receiveEvent(ctx context.Context, a Adapter, e Event) error {
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
	e.ChannelID = c.ID
	e.Key = eventKey(e)
	e.Digest = eventDigest(e)
	e.Status = "pending"
	e.NextAttempt = s.now()
	return s.store.PutEvent(ctx, e)
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
func (s *Service) apply(ctx context.Context, e Event) error {
	id := e.OrderID
	if id == "" {
		c := s.adapters[e.ChannelID].Channel()
		o, err := s.store.FindPayment(ctx, c.namespace(), e.Evidence.Transaction)
		if err != nil {
			return err
		}
		id = o.ID
	}
	return s.store.Mutate(ctx, id, func(o *Order) error {
		a := s.adapters[o.ChannelID]
		if a == nil || o.ChannelID != e.ChannelID || !sameChannel(e.Evidence, a.Channel()) || e.Currency != o.Intent.Currency ||
			o.PaymentID != "" && o.PaymentID != e.Evidence.Transaction {
			return ErrConflict
		}
		if e.Type == Paid || e.Type == PaymentFailed {
			if e.AmountMinor != o.Intent.AmountMinor {
				return ErrConflict
			}
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
		if e.AmountMinor > o.Intent.AmountMinor {
			return ErrConflict
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
	a := s.adapters[o.ChannelID]
	if a == nil {
		return ErrUnavailable
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
