// Package promotions owns campaign policy and application records. Rust alone
// verifies eligibility, issues usable coupons and posts monetary rewards.
package promotions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/access"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/storage"
)

const Unit = "credit_500k_usd"
const maxUnits int64 = 1_000_000_000_000
const (
	Coupon   = "coupon"
	Referral = "referral"
	Pending  = "pending"
	Granted  = "granted"
	Rejected = "rejected"
)

type Limit struct {
	CouponUnits   int64 `json:"coupon_units"`
	ReferralUnits int64 `json:"referral_units"`
	DiscountBPS   int64 `json:"discount_bps"`
	TotalUnits    int64 `json:"total_units"`
	Claims        int64 `json:"claims"`
}

// DefaultLimits are lifetime limits for this policy, including pending claims.
// Administrators do not receive an unlimited budget. No campaign is enabled by
// default; Rust must separately approve the same campaign and its treasury.
func DefaultLimits() [7]Limit {
	var limits [7]Limit
	for i, n := range []int64{25000, 50000, 100000, 150000, 200000, 200000, 200000} {
		referral := n
		if i == 0 {
			referral = 0
		}
		limits[i] = Limit{n, referral, 500 + int64(min(i, 3))*500, 3 * n, 3}
	}
	return limits
}

type Campaign struct {
	ID          string    `json:"id"`
	Revision    int64     `json:"revision"`
	Kind        string    `json:"kind"`
	Enabled     bool      `json:"enabled"`
	StartsAt    time.Time `json:"starts_at"`
	EndsAt      time.Time `json:"ends_at"`
	Units       int64     `json:"units"` // Maximum coupon value or referral reward, never float.
	DiscountBPS int64     `json:"discount_bps"`
	BudgetUnits int64     `json:"budget_units"`
	MaxClaims   int64     `json:"max_claims"`
}
type Policy struct {
	Limits    [7]Limit
	Campaigns []Campaign
}

func (p Policy) valid() bool {
	if len(p.Campaigns) > 64 {
		return false
	}
	for _, l := range p.Limits {
		if l.CouponUnits < 0 || l.ReferralUnits < 0 || l.TotalUnits < 0 || l.TotalUnits > maxUnits || l.CouponUnits > l.TotalUnits || l.ReferralUnits > l.TotalUnits || l.DiscountBPS < 0 || l.DiscountBPS > 10000 || l.Claims < 0 || l.Claims > 1000 {
			return false
		}
	}
	seen := map[string]bool{}
	for _, c := range p.Campaigns {
		if !access.ID(c.ID) || seen[c.ID] || c.Revision < 1 || (c.Kind != Coupon && c.Kind != Referral) || c.Units < 1 || c.Units > maxUnits || c.DiscountBPS < 0 || c.DiscountBPS > 10000 || (c.Kind == Referral && c.DiscountBPS != 0) || c.BudgetUnits < c.Units || c.BudgetUnits > maxUnits || c.MaxClaims < 1 || c.MaxClaims > 1_000_000 || c.StartsAt.IsZero() || !c.EndsAt.After(c.StartsAt) {
			return false
		}
		seen[c.ID] = true
	}
	return true
}

type EligibilityRequest struct {
	CampaignID  string
	Revision    int64
	Kind        string
	EvidenceRef string
}

// An eligibility proof is a core-owned single-use entitlement. A referral proof
// must bind the verified invitee/event to the current referrer, not user claims.
type Eligibility struct {
	ProofID     string         `json:"proof_id"`
	CampaignID  string         `json:"campaign_id"`
	Revision    int64          `json:"revision"`
	Kind        string         `json:"kind"`
	UserID      int64          `json:"user_id"`
	SubjectID   int64          `json:"subject_id"`
	Account     access.Account `json:"account"`
	Units       int64          `json:"units"`
	DiscountBPS int64          `json:"discount_bps"`
	ExpiresAt   time.Time      `json:"expires_at"`
}
type GrantRequest struct {
	Key         string      `json:"key"`
	Unit        string      `json:"unit"`
	Eligibility Eligibility `json:"eligibility"`
}
type Receipt struct {
	ID          string `json:"id"`
	Key         string `json:"key"`
	RequestHash string `json:"request_hash"`
	State       string `json:"state"`
	JournalID   int64  `json:"journal_id,omitempty"`
	CouponID    string `json:"coupon_id,omitempty"`
}

// Rewards is a narrow core port, NOT a generic credit or transfer API. Rust
// must independently recheck current user authority, campaign limits, referral
// evidence and global uniqueness inside the same transaction as the ledger /
// single-use coupon and outbox. Every replay rechecks authority. Timeouts may
// mean committed: retry only the exact same request and key. See README.md.
type Rewards interface {
	Evaluate(context.Context, string, EligibilityRequest) (Eligibility, error)
	Grant(context.Context, string, GrantRequest) (Receipt, error)
}

// UnavailableRewards is the production default until the core reward contract
// is implemented. Existing payment-credit/transfer RPCs are NOT substitutes.
type UnavailableRewards struct{}

func (UnavailableRewards) Evaluate(context.Context, string, EligibilityRequest) (Eligibility, error) {
	return Eligibility{}, access.ErrUnavailable
}
func (UnavailableRewards) Grant(context.Context, string, GrantRequest) (Receipt, error) {
	return Receipt{}, access.ErrUnavailable
}

type Claim struct {
	Request   GrantRequest `json:"request"`
	State     string       `json:"state"`
	Receipt   Receipt      `json:"receipt"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
}
type total struct {
	Units  int64 `json:"units"`
	Claims int64 `json:"claims"`
}
type Service struct {
	repo      storage.Repository
	auth      access.Authority
	rewards   Rewards
	limits    [7]Limit
	campaigns map[string]Campaign
	now       func() time.Time
}

func New(repo storage.Repository, auth access.Authority, rewards Rewards, policy Policy) (*Service, error) {
	if access.Missing(repo) || access.Missing(auth) || access.Missing(rewards) || !policy.valid() {
		return nil, access.ErrInvalid
	}
	s := &Service{repo: repo, auth: auth, rewards: rewards, limits: policy.Limits, campaigns: map[string]Campaign{}, now: time.Now}
	for _, c := range policy.Campaigns {
		s.campaigns[c.ID] = c
	}
	return s, nil
}
func (s *Service) principal(ctx context.Context, token string) (access.Principal, error) {
	p, e := s.auth.Check(ctx, token, access.Account{}, access.Read)
	if e != nil {
		return p, e
	}
	if p.UserID <= 0 || p.Level < 0 || p.Level > 6 || p.Account != (access.Account{Kind: "personal", ID: p.UserID}) {
		return p, access.ErrForbidden
	}
	return p, nil
}
func (s *Service) Limits(ctx context.Context, token string) (Limit, error) {
	p, e := s.principal(ctx, token)
	if e != nil {
		return Limit{}, e
	}
	return s.limits[p.Level], nil
}
func (s *Service) evaluate(ctx context.Context, token, id, evidence, kind string) (access.Principal, Eligibility, Campaign, error) {
	p, err := s.principal(ctx, token)
	if err != nil {
		return p, Eligibility{}, Campaign{}, err
	}
	c, ok := s.campaigns[id]
	now := s.now()
	if !ok || c.Kind != kind || !c.Enabled || now.Before(c.StartsAt) || !now.Before(c.EndsAt) {
		return p, Eligibility{}, c, access.ErrForbidden
	}
	if (kind == Referral && !access.ID(evidence)) || (kind == Coupon && evidence != "") {
		return p, Eligibility{}, c, access.ErrInvalid
	}
	l := s.limits[p.Level]
	cap := l.CouponUnits
	if kind == Referral {
		cap = l.ReferralUnits
	}
	if cap == 0 || l.Claims == 0 || c.Units > cap || c.DiscountBPS > l.DiscountBPS {
		return p, Eligibility{}, c, access.ErrLimit
	}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	proof, err := s.rewards.Evaluate(bounded, token, EligibilityRequest{c.ID, c.Revision, c.Kind, evidence})
	if err != nil {
		return p, Eligibility{}, c, err
	}
	if !access.ID(proof.ProofID) || proof.CampaignID != c.ID || proof.Revision != c.Revision || proof.Kind != c.Kind || proof.UserID != p.UserID || proof.Account != p.Account || proof.SubjectID <= 0 || proof.Units <= 0 || proof.Units > c.Units || proof.DiscountBPS < 0 || proof.DiscountBPS > c.DiscountBPS || !proof.ExpiresAt.After(now) || proof.ExpiresAt.After(c.EndsAt) || (kind == Coupon && proof.SubjectID != p.UserID) || (kind == Referral && proof.SubjectID == p.UserID) {
		return p, Eligibility{}, c, access.ErrUnavailable
	}
	return p, proof, c, nil
}
func (s *Service) Eligibility(ctx context.Context, token, id, evidence, kind string) (Eligibility, error) {
	_, proof, _, e := s.evaluate(ctx, token, id, evidence, kind)
	return proof, e
}
func hash(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func RequestHash(r GrantRequest) string { return hash(r) }
func claimKey(e Eligibility) string {
	return "promo_" + hash([]any{"v1", e.CampaignID, e.Kind, e.SubjectID})
}
func getTotal(tx storage.Tx, bucket, id string) (total, error) {
	var v total
	e := tx.Get(bucket, id, &v)
	if errors.Is(e, access.ErrNotFound) {
		e = nil
	}
	if v.Units < 0 || v.Claims < 0 {
		return v, access.ErrUnavailable
	}
	return v, e
}
func reserve(v total, amount, cap, count int64) (total, error) {
	if amount <= 0 || v.Units > cap || amount > cap-v.Units || v.Claims >= count {
		return v, access.ErrLimit
	}
	v.Units += amount
	v.Claims++
	return v, nil
}

func (s *Service) Apply(ctx context.Context, token, id, evidence, kind string) (Claim, error) {
	// A saved application can be recovered even if eligibility has expired or
	// already been consumed. Replays still ask Rust to authorize the exact grant.
	p, authErr := s.principal(ctx, token)
	if authErr != nil {
		return Claim{}, authErr
	}
	if !access.ID(id) || (kind != Coupon && kind != Referral) || (kind == Coupon && evidence != "") || (kind == Referral && !access.ID(evidence)) {
		return Claim{}, access.ErrInvalid
	}
	application := hash([]any{p.UserID, id, kind, evidence})
	var prior Claim
	lookup := s.repo.Within(ctx, "global", func(tx storage.Tx) error {
		var link struct {
			Key string `json:"key"`
		}
		if err := tx.Get("applications", application, &link); err != nil {
			return err
		}
		if err := tx.Get("claims", link.Key, &prior); err != nil {
			return access.ErrUnavailable
		}
		if prior.Request.Eligibility.UserID != p.UserID || prior.Request.Eligibility.Account != p.Account {
			return access.ErrForbidden
		}
		return nil
	})
	if lookup == nil {
		return s.finish(ctx, token, prior)
	}
	if !errors.Is(lookup, access.ErrNotFound) {
		return Claim{}, lookup
	}
	p, proof, c, err := s.evaluate(ctx, token, id, evidence, kind)
	if err != nil {
		return Claim{}, err
	}
	r := GrantRequest{Key: claimKey(proof), Unit: Unit, Eligibility: proof}
	var claim Claim
	err = s.repo.Within(ctx, "global", func(tx storage.Tx) error {
		err := tx.Get("claims", r.Key, &claim)
		if err == nil {
			if claim.Request.Eligibility.UserID != p.UserID || claim.Request.Eligibility.Account != p.Account {
				return access.ErrForbidden
			}
			// The first accepted terms remain frozen across policy/level changes. Do
			// not replace an existing grant or refund its hold on an uncertain result.
			return nil
		}
		if !errors.Is(err, access.ErrNotFound) {
			return err
		}
		uid := fmt.Sprint(p.UserID)
		u, err := getTotal(tx, "user_totals", uid)
		if err != nil {
			return err
		}
		limit := s.limits[p.Level]
		u, err = reserve(u, proof.Units, limit.TotalUnits, limit.Claims)
		if err != nil {
			return err
		}
		campaign, err := getTotal(tx, "campaign_totals", c.ID)
		if err != nil {
			return err
		}
		campaign, err = reserve(campaign, proof.Units, c.BudgetUnits, c.MaxClaims)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		claim = Claim{Request: r, State: Pending, CreatedAt: now, UpdatedAt: now}
		if err = tx.Put("user_totals", uid, u); err != nil {
			return err
		}
		if err = tx.Put("campaign_totals", c.ID, campaign); err != nil {
			return err
		}
		if err = tx.Put("claims", r.Key, claim); err != nil {
			return err
		}
		if err = tx.Put("applications", application, struct {
			Key string `json:"key"`
		}{r.Key}); err != nil {
			return err
		}
		return audit(tx, claim)
	})
	if err != nil {
		return Claim{}, err
	}
	return s.finish(ctx, token, claim)
}
func audit(tx storage.Tx, c Claim) error {
	return tx.Put("events", c.Request.Key+":"+c.State, struct {
		Key       string    `json:"key"`
		UserID    int64     `json:"user_id"`
		State     string    `json:"state"`
		ReceiptID string    `json:"receipt_id,omitempty"`
		At        time.Time `json:"at"`
	}{c.Request.Key, c.Request.Eligibility.UserID, c.State, c.Receipt.ID, c.UpdatedAt})
}
func (s *Service) Read(ctx context.Context, token, key string) (Claim, error) {
	p, e := s.principal(ctx, token)
	if e != nil {
		return Claim{}, e
	}
	if !access.ID(key) {
		return Claim{}, access.ErrInvalid
	}
	var c Claim
	e = s.repo.Within(ctx, "global", func(tx storage.Tx) error {
		if e := tx.Get("claims", key, &c); e != nil {
			return e
		}
		if c.Request.Eligibility.UserID != p.UserID || c.Request.Eligibility.Account != p.Account {
			return access.ErrNotFound
		}
		return nil
	})
	return c, e
}
func (s *Service) Reconcile(ctx context.Context, token, key string) (Claim, error) {
	c, e := s.Read(ctx, token, key)
	if e != nil {
		return Claim{}, e
	}
	return s.finish(ctx, token, c)
}
func validReceipt(r Receipt, c Claim) bool {
	if !access.ID(r.ID) || r.Key != c.Request.Key || r.RequestHash != RequestHash(c.Request) {
		return false
	}
	if r.State == Rejected {
		return r.JournalID == 0 && r.CouponID == ""
	}
	if r.State != Granted {
		return false
	}
	if c.Request.Eligibility.Kind == Referral {
		return r.JournalID > 0 && r.CouponID == ""
	}
	return access.ID(r.CouponID) && r.JournalID >= 0
}
func (s *Service) finish(ctx context.Context, token string, claim Claim) (Claim, error) {
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	receipt, err := s.rewards.Grant(bounded, token, claim.Request)
	if err != nil {
		return claim, err
	} // Pending reservation survives unknown outcomes.
	if !validReceipt(receipt, claim) {
		return claim, access.ErrUnavailable
	}
	err = s.repo.Within(ctx, "global", func(tx storage.Tx) error {
		var current Claim
		if e := tx.Get("claims", claim.Request.Key, &current); e != nil {
			return e
		}
		if RequestHash(current.Request) != RequestHash(claim.Request) {
			return access.ErrConflict
		}
		if current.State != Pending {
			if current.Receipt != receipt {
				return access.ErrUnavailable
			}
			claim = current
			return nil
		}
		if receipt.State == Rejected {
			for _, item := range []struct{ bucket, id string }{{"user_totals", fmt.Sprint(current.Request.Eligibility.UserID)}, {"campaign_totals", current.Request.Eligibility.CampaignID}} {
				v, e := getTotal(tx, item.bucket, item.id)
				if e != nil {
					return e
				}
				if v.Claims < 1 || v.Units < current.Request.Eligibility.Units {
					return access.ErrUnavailable
				}
				v.Claims--
				v.Units -= current.Request.Eligibility.Units
				if e = tx.Put(item.bucket, item.id, v); e != nil {
					return e
				}
			}
		}
		current.State = receipt.State
		current.Receipt = receipt
		current.UpdatedAt = s.now().UTC()
		if e := tx.Put("claims", current.Request.Key, current); e != nil {
			return e
		}
		if e := audit(tx, current); e != nil {
			return e
		}
		claim = current
		return nil
	})
	return claim, err
}

// PendingClaim is a recovery handle, never a spendable coupon or posted reward.
// It lets a caller resume a grant whose core result was lost or unavailable.
type PendingClaim struct {
	Key   string `json:"key"`
	State string `json:"state"`
}

func Result(c Claim, err error) (any, error) {
	if err != nil && c.State == Pending && access.ID(c.Request.Key) {
		return PendingClaim{c.Request.Key, Pending}, nil
	}
	return c, err
}
