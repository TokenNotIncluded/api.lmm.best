package payments

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"time"
)

// Work performs at most limit bounded calls. Scheduling belongs to the host;
// construction and HTTP requests do not spawn unbounded background goroutines.
func (s *Service) Work(ctx context.Context, limit int) error {
	if limit < 1 || limit > 100 {
		return ErrInvalid
	}
	if err := s.Drain(ctx, limit); err != nil {
		return err
	}
	ids, err := s.store.DueOrders(ctx, s.now(), limit)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = s.Step(ctx, id); err != nil {
			return err
		}
	}
	return nil
}
func priority(kind string) int {
	switch kind {
	case "external_refund":
		return 0
	case "commit", "release", "lookup_hold":
		return 1
	case "credit":
		return 2
	case "checkout":
		return 3
	default:
		return 4
	}
}
func (s *Service) Step(ctx context.Context, id string) error {
	var chosen Job
	var snapshot Order
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		return err
	}
	lease := hex.EncodeToString(token)
	now := s.now()
	err := s.store.Mutate(ctx, id, func(o *Order) error {
		keys := make([]string, 0, len(o.Jobs))
		for key, j := range o.Jobs {
			if j.State == "running" && j.LeaseUntil.After(now) {
				return nil
			}
			if (j.State == "pending" || j.State == "running") && !j.NextAttempt.After(now) {
				keys = append(keys, key)
			}
		}
		sort.Slice(keys, func(i, j int) bool {
			a, b := o.Jobs[keys[i]], o.Jobs[keys[j]]
			if priority(a.Kind) != priority(b.Kind) {
				return priority(a.Kind) < priority(b.Kind)
			}
			return keys[i] < keys[j]
		})
		for _, key := range keys {
			j := o.Jobs[key]
			if j.Kind == "credit" && o.RefundFence {
				j.State = "fenced"
				o.Jobs[key] = j
				continue
			}
			if j.Kind == "refund_submit" {
				r := o.Refunds[j.RefundID]
				if r.HoldReceipt == "" {
					return ErrConflict
				}
				if r.ProviderState == RefundSucceeded || r.ProviderState == RefundFailed {
					j.State = "done"
					o.Jobs[key] = j
					continue
				}
			}
			j.State = "running"
			j.Lease = lease
			j.LeaseUntil = now.Add(30 * time.Second)
			j.Attempts++
			if j.FirstAttempt.IsZero() {
				j.FirstAttempt = now
			}
			o.Jobs[key] = j
			chosen = j
			snapshot = *o
			return nil
		}
		return nil
	})
	if err != nil || chosen.Key == "" {
		return err
	}
	a := s.adapters[snapshot.ChannelID]
	if a == nil {
		return ErrUnavailable
	}
	bounded, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	var receipt Receipt
	var checkout Checkout
	var refund RefundResult
	expected := ""
	var callErr error
	switch chosen.Kind {
	case "checkout", "refund_submit":
		// Provider idempotency retention is finite. Never repeat an ambiguous write
		// after that window, and never automatically retry legacy ePay refunds.
		if chosen.Attempts > 1 && (a.ReplayWindow(chosen.Kind) <= 0 || now.Sub(chosen.FirstAttempt) >= a.ReplayWindow(chosen.Kind)) {
			callErr = ErrUnsupported
		} else if chosen.Kind == "checkout" {
			checkout, callErr = a.Checkout(bounded, snapshot, chosen.Key)
		} else {
			refund, callErr = a.Refund(bounded, snapshot, snapshot.Refunds[chosen.RefundID], chosen.Key)
		}
	case "lookup_hold":
		expected = RefundHeld
		receipt, callErr = s.core.Receipt(bounded, snapshot.Intent.ID, snapshot.Refunds[chosen.RefundID].HoldKey)
	default:
		switch chosen.Kind {
		case "credit":
			expected = CreditApplied
		case "commit":
			expected = RefundCommitted
		case "release":
			expected = RefundReleased
		case "external_refund":
			expected = ExternalRefundApplied
		default:
			callErr = ErrInvalid
		}
		if callErr == nil && chosen.Attempts > 1 {
			receipt, callErr = s.core.Receipt(bounded, snapshot.Intent.ID, chosen.Key)
		}
		if chosen.Attempts == 1 || errors.Is(callErr, ErrNotFound) {
			switch chosen.Kind {
			case "credit":
				receipt, callErr = s.core.Credit(bounded, snapshot.Intent.ID, chosen.Key, chosen.Evidence)
			case "commit":
				receipt, callErr = s.core.Commit(bounded, snapshot.Intent.ID, chosen.Key, snapshot.Refunds[chosen.RefundID].HoldReceipt, chosen.Evidence)
			case "release":
				receipt, callErr = s.core.Release(bounded, snapshot.Intent.ID, chosen.Key, snapshot.Refunds[chosen.RefundID].HoldReceipt, chosen.Evidence)
			case "external_refund":
				receipt, callErr = s.core.ExternalRefund(bounded, snapshot.Intent.ID, chosen.Key, chosen.Evidence)
			}
		}
	}
	key := chosen.Key
	if chosen.Kind == "lookup_hold" {
		key = snapshot.Refunds[chosen.RefundID].HoldKey
	}
	if callErr == nil && expected != "" && !receiptMatches(receipt, snapshot.Intent.ID, key, expected) {
		callErr = ErrConflict
	}
	if callErr == nil && chosen.Kind == "checkout" && (!validID(checkout.ID) || checkout.URL == "" || checkout.PaymentID != "" && !validID(checkout.PaymentID)) {
		callErr = ErrConflict
	}
	if callErr == nil && chosen.Kind == "refund_submit" && refund.ID != "" && !validID(refund.ID) {
		callErr = ErrConflict
	}
	return s.store.Mutate(ctx, id, func(o *Order) error {
		j := o.Jobs[chosen.Key]
		if j.Lease != lease {
			return nil
		} // An expired worker cannot overwrite a newer one.
		j.Lease = ""
		j.LeaseUntil = time.Time{}
		if callErr != nil {
			// A signed callback may arrive while an unsigned observation is in
			// flight. Keep the stronger evidence and retry; do not lose it when
			// the older observation fails closed at the core bridge.
			if chosen.Evidence.Source == "api" && j.Evidence.Source == "webhook" {
				j.State = "pending"
				j.ErrorCode = ""
				j.NextAttempt = s.now()
				o.Jobs[j.Key] = j
				return nil
			}
			j.State = "pending"
			j.ErrorCode = errorCode(callErr)
			delay := time.Second * time.Duration(1<<min(j.Attempts, 8))
			j.NextAttempt = s.now().Add(delay)
			if errors.Is(callErr, ErrConflict) || errors.Is(callErr, ErrInvalid) || errors.Is(callErr, ErrEvidence) || errors.Is(callErr, ErrUnsupported) {
				j.State = "review"
			}
			o.Jobs[j.Key] = j
			return nil
		}
		j.State = "done"
		j.ErrorCode = ""
		if expected != "" {
			j.Receipt = &receipt
			if receipt.State == Rejected {
				j.State = "rejected"
				if j.Kind == "credit" {
					o.CreditState = "rejected"
				}
				if j.Kind == "lookup_hold" {
					r := o.Refunds[j.RefundID]
					r.State = "rejected"
					o.Refunds[r.ID] = r
				}
				o.Jobs[j.Key] = j
				return nil
			}
		}
		switch j.Kind {
		case "checkout":
			if o.PaymentID != "" && checkout.PaymentID != "" && o.PaymentID != checkout.PaymentID {
				return ErrConflict
			}
			o.Checkout = checkout
			if checkout.PaymentID != "" {
				o.PaymentID = checkout.PaymentID
			}
		case "credit":
			o.CreditState = "confirmed"
			o.CreditReceipt = receipt.ID
		case "lookup_hold":
			r := o.Refunds[j.RefundID]
			if r.HoldReceipt == "" {
				acceptHold(o, &r, receipt, s.now())
				o.Refunds[r.ID] = r
			}
		case "refund_submit":
			r := o.Refunds[j.RefundID]
			if r.ProviderID != "" && refund.ID != "" && r.ProviderID != refund.ID {
				return ErrConflict
			}
			if refund.ID != "" {
				r.ProviderID = refund.ID
			}
			if r.State == "held" {
				r.State = "awaiting_evidence"
			}
			o.Refunds[r.ID] = r
		case "commit", "external_refund":
			r := o.Refunds[j.RefundID]
			r.State = "committed"
			o.Refunds[r.ID] = r
		case "release":
			r := o.Refunds[j.RefundID]
			r.State = "released"
			o.Refunds[r.ID] = r
		}
		o.Jobs[j.Key] = j
		return nil
	})
}
