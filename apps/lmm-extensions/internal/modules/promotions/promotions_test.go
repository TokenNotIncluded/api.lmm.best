package promotions

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/access"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/storage"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/testkit"
)

var testNow = time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

type rewardCore struct {
	mu       sync.Mutex
	receipts map[string]Receipt
	effects  int
	deny     bool
	lose     bool
	invalid  bool
	proof    func(*Eligibility)
}

func newRewardCore() *rewardCore { return &rewardCore{receipts: map[string]Receipt{}} }
func (r *rewardCore) Evaluate(_ context.Context, token string, q EligibilityRequest) (Eligibility, error) {
	uid := int64(1)
	if token == testkit.Token("b") {
		uid = 2
	}
	subject := uid
	if q.Kind == Referral {
		n, e := strconv.ParseInt(q.EvidenceRef, 10, 64)
		if e != nil {
			return Eligibility{}, access.ErrForbidden
		}
		subject = n
	}
	p := Eligibility{ProofID: "proof_" + fmt.Sprint(subject), CampaignID: q.CampaignID, Revision: q.Revision, Kind: q.Kind, UserID: uid, SubjectID: subject, Account: access.Account{Kind: "personal", ID: uid}, Units: 10, ExpiresAt: testNow.Add(30 * time.Minute)}
	if r.proof != nil {
		r.proof(&p)
	}
	return p, nil
}
func (r *rewardCore) Grant(_ context.Context, _ string, q GrantRequest) (Receipt, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.invalid {
		return Receipt{ID: "mismatched", Key: q.Key, State: Granted}, nil
	}
	if v, ok := r.receipts[q.Key]; ok {
		if v.RequestHash != RequestHash(q) {
			return Receipt{}, access.ErrConflict
		}
		return v, nil
	}
	v := Receipt{ID: "receipt_" + q.Key, Key: q.Key, RequestHash: RequestHash(q), State: Granted}
	if r.deny {
		v.State = Rejected
	} else if q.Eligibility.Kind == Coupon {
		v.CouponID = "coupon_" + q.Key
		r.effects++
	} else {
		r.effects++
		v.JournalID = int64(r.effects)
	}
	r.receipts[q.Key] = v
	if r.lose {
		r.lose = false
		return Receipt{}, access.ErrUnavailable
	}
	return v, nil
}
func policy() Policy {
	p := Policy{Limits: DefaultLimits()}
	for i := range p.Limits {
		p.Limits[i] = Limit{CouponUnits: 10, ReferralUnits: 10, DiscountBPS: 1000, TotalUnits: 20, Claims: 2}
	}
	for _, kind := range []string{Coupon, Referral} {
		p.Campaigns = append(p.Campaigns, Campaign{ID: kind, Revision: 1, Kind: kind, Enabled: true, StartsAt: testNow.Add(-time.Hour), EndsAt: testNow.Add(time.Hour), Units: 10, BudgetUnits: 100, MaxClaims: 10})
	}
	return p
}
func fixture(t *testing.T, p Policy) (*Service, *rewardCore, *testkit.Store, *testkit.Identity) {
	t.Helper()
	store := testkit.NewStore()
	id := testkit.NewIdentity()
	id.Set(testkit.Token("a"), 1, 1)
	id.Set(testkit.Token("b"), 2, 1)
	auth, _ := access.New(id)
	core := newRewardCore()
	s, e := New(store, auth, core, p)
	if e != nil {
		t.Fatal(e)
	}
	s.now = func() time.Time { return testNow }
	return s, core, store, id
}
func TestCouponIdempotencyAcrossConcurrentServices(t *testing.T) {
	s, core, store, _ := fixture(t, policy())
	other, _ := New(store, s.auth, core, policy())
	other.now = s.now
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			svc := s
			if i%2 == 0 {
				svc = other
			}
			c, e := svc.Apply(context.Background(), testkit.Token("a"), Coupon, "", Coupon)
			if e == nil && c.State != Granted {
				e = errors.New("not granted")
			}
			errs <- e
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if core.effects != 1 {
		t.Fatal("duplicate reward", core.effects)
	}
	var totals total
	_ = store.Within(context.Background(), "global", func(tx storage.Tx) error { return tx.Get("user_totals", "1", &totals) })
	if totals != (total{10, 1}) {
		t.Fatal(totals)
	}
}
func TestLifetimeAndCampaignCaps(t *testing.T) {
	s, core, _, _ := fixture(t, policy())
	ctx := context.Background()
	token := testkit.Token("a")
	for _, subject := range []string{"10", "11"} {
		if _, e := s.Apply(ctx, token, Referral, subject, Referral); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := s.Apply(ctx, token, Referral, "12", Referral); !errors.Is(e, access.ErrLimit) {
		t.Fatal(e)
	}
	if core.effects != 2 {
		t.Fatal(core.effects)
	}
	p := policy()
	p.Campaigns[0].BudgetUnits = 10
	p.Campaigns[0].MaxClaims = 1
	s, core, _, _ = fixture(t, p)
	if _, e := s.Apply(ctx, token, Coupon, "", Coupon); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Apply(ctx, testkit.Token("b"), Coupon, "", Coupon); !errors.Is(e, access.ErrLimit) {
		t.Fatal(e)
	}
	if core.effects != 1 {
		t.Fatal(core.effects)
	}
}
func TestEveryLevelHasFiniteEnforcedLimits(t *testing.T) {
	for level := 0; level <= 6; level++ {
		t.Run(fmt.Sprint(level), func(t *testing.T) {
			p := policy()
			p.Limits[level].CouponUnits = 9
			s, core, _, id := fixture(t, p)
			id.Set(testkit.Token("a"), 1, int32(level))
			if _, e := s.Apply(context.Background(), testkit.Token("a"), Coupon, "", Coupon); !errors.Is(e, access.ErrLimit) {
				t.Fatal(e)
			}
			if core.effects != 0 {
				t.Fatal("limit bypass")
			}
		})
	}
	limits := DefaultLimits()
	if limits[5].TotalUnits > limits[4].TotalUnits || limits[6].TotalUnits > limits[4].TotalUnits {
		t.Fatal("admin bonus escalation")
	}
}
func TestUnknownCommitRetainsHoldAndReconcilesOnce(t *testing.T) {
	p := policy()
	p.Limits[1].TotalUnits = 10
	p.Limits[1].Claims = 1
	s, core, store, _ := fixture(t, p)
	core.lose = true
	ctx := context.Background()
	token := testkit.Token("a")
	claim, e := s.Apply(ctx, token, Coupon, "", Coupon)
	if !errors.Is(e, access.ErrUnavailable) || claim.State != Pending {
		t.Fatal(claim, e)
	}
	// A second campaign cannot consume the same reserved budget while unknown.
	if _, e = s.Apply(ctx, token, Referral, "10", Referral); !errors.Is(e, access.ErrLimit) {
		t.Fatal(e)
	}
	reopened, _ := New(store, s.auth, core, p)
	reopened.now = s.now
	claim, e = reopened.Reconcile(ctx, token, claim.Request.Key)
	if e != nil || claim.State != Granted || core.effects != 1 {
		t.Fatal(claim, e, core.effects)
	}
}
func TestRejectedClaimReleasesOnlyOnce(t *testing.T) {
	s, core, store, _ := fixture(t, policy())
	core.deny = true
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		c, e := s.Apply(ctx, testkit.Token("a"), Coupon, "", Coupon)
		if e != nil || c.State != Rejected {
			t.Fatal(c, e)
		}
	}
	var v total
	_ = store.Within(ctx, "global", func(tx storage.Tx) error { return tx.Get("user_totals", "1", &v) })
	if v != (total{}) {
		t.Fatal(v)
	}
	core.deny = false // A repeated permanent rejection never becomes a fresh grant.
	c, e := s.Apply(ctx, testkit.Token("a"), Coupon, "", Coupon)
	if e != nil || c.State != Rejected || core.effects != 0 {
		t.Fatal(c, e)
	}
}
func TestProofAndReceiptBinding(t *testing.T) {
	for _, mutate := range []func(*Eligibility){func(p *Eligibility) { p.UserID = 2 }, func(p *Eligibility) { p.Account = access.Account{Kind: "team", ID: 1} }, func(p *Eligibility) { p.Units = 11 }, func(p *Eligibility) { p.DiscountBPS = 10001 }, func(p *Eligibility) { p.SubjectID = 1 }, func(p *Eligibility) { p.Revision++ }, func(p *Eligibility) { p.ExpiresAt = testNow }} {
		s, core, _, _ := fixture(t, policy())
		core.proof = mutate
		if _, e := s.Apply(context.Background(), testkit.Token("a"), Referral, "10", Referral); !errors.Is(e, access.ErrUnavailable) {
			t.Fatal(e)
		}
		if core.effects != 0 {
			t.Fatal("forged proof granted")
		}
	}
	s, core, _, _ := fixture(t, policy())
	core.invalid = true
	c, e := s.Apply(context.Background(), testkit.Token("a"), Coupon, "", Coupon)
	if !errors.Is(e, access.ErrUnavailable) || c.State != Pending {
		t.Fatal(c, e)
	}
}
func TestAccountIsolationRevocationAndUnavailableCore(t *testing.T) {
	s, _, _, id := fixture(t, policy())
	ctx := context.Background()
	c, e := s.Apply(ctx, testkit.Token("a"), Coupon, "", Coupon)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Read(ctx, testkit.Token("b"), c.Request.Key); !errors.Is(e, access.ErrNotFound) {
		t.Fatal(e)
	}
	if _, e = s.Reconcile(ctx, testkit.Token("b"), c.Request.Key); !errors.Is(e, access.ErrNotFound) {
		t.Fatal(e)
	}
	id.Revoke(testkit.Token("a"))
	if _, e = s.Reconcile(ctx, testkit.Token("a"), c.Request.Key); !errors.Is(e, access.ErrUnauthorized) {
		t.Fatal(e)
	}
	s, _, store, _ := fixture(t, policy())
	unavailable, _ := New(store, s.auth, UnavailableRewards{}, policy())
	unavailable.now = s.now
	if _, e = unavailable.Apply(ctx, testkit.Token("a"), Coupon, "", Coupon); !errors.Is(e, access.ErrUnavailable) {
		t.Fatal(e)
	}
	if len(store.Data) != 0 {
		t.Fatal("unavailable core wrote a local reward")
	}
}
func TestPolicySnapshotAndInvalidConfiguration(t *testing.T) {
	p := policy()
	s, _, _, _ := fixture(t, p)
	p.Campaigns[0].Units = 999
	p.Limits[1].CouponUnits = 0
	if _, e := s.Apply(context.Background(), testkit.Token("a"), Coupon, "", Coupon); e != nil {
		t.Fatal("policy aliases caller", e)
	}
	for _, mutate := range []func(*Policy){func(p *Policy) { p.Limits[0].TotalUnits = -1 }, func(p *Policy) { p.Campaigns[0].DiscountBPS = 10001 }, func(p *Policy) { p.Campaigns[0].BudgetUnits = maxUnits + 1 }, func(p *Policy) { p.Campaigns = append(p.Campaigns, p.Campaigns[0]) }} {
		p := policy()
		mutate(&p)
		if _, e := New(s.repo, s.auth, s.rewards, p); !errors.Is(e, access.ErrInvalid) {
			t.Fatal(e)
		}
	}
}
func TestStorageFailurePrecedesAnyGrant(t *testing.T) {
	s, core, store, _ := fixture(t, policy())
	store.Fail = true
	if _, e := s.Apply(context.Background(), testkit.Token("a"), Coupon, "", Coupon); e == nil || core.effects != 0 {
		t.Fatal(e, core.effects)
	}
}

func TestSavedApplicationReplaysAfterEligibilityExpires(t *testing.T) {
	s, core, _, _ := fixture(t, policy())
	token := testkit.Token("a")
	first, e := s.Apply(context.Background(), token, "coupon", "", Coupon)
	if e != nil {
		t.Fatal(e)
	}
	s.now = func() time.Time { return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) }
	again, e := s.Apply(context.Background(), token, "coupon", "", Coupon)
	if e != nil || again.Request.Key != first.Request.Key || core.effects != 1 {
		t.Fatal(again, e, core.effects)
	}
}

func TestPendingResultReturnsOnlyRecoveryHandle(t *testing.T) {
	s, core, _, _ := fixture(t, policy())
	core.lose = true
	claim, err := s.Apply(context.Background(), testkit.Token("a"), "coupon", "", Coupon)
	value, err := Result(claim, err)
	if err != nil {
		t.Fatal(err)
	}
	pending, ok := value.(PendingClaim)
	if !ok || pending.State != Pending || pending.Key != claim.Request.Key {
		t.Fatal(value)
	}
	recovered, err := s.Apply(context.Background(), testkit.Token("a"), "coupon", "", Coupon)
	if err != nil || recovered.State != Granted || core.effects != 1 {
		t.Fatal(recovered, err)
	}
}
