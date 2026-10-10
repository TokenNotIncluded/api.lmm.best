// These tests require a disposable PostgreSQL instance. No production database
// is read. Core identity/rewards are controlled fixtures, not live Rust funds.
package pgtest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/access"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/storage"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/testkit"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/promotions"
	_ "github.com/lib/pq"
)

func must(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}
func database(t *testing.T, admin *sql.DB, base *url.URL, kind string) (*sql.DB, *sql.DB, string) {
	t.Helper()
	name := fmt.Sprintf("mk09_%s_%d", kind, time.Now().UnixNano())
	_, e := admin.Exec(`CREATE DATABASE ` + name)
	must(t, e)
	u := *base
	u.Path = "/" + name
	first, e := sql.Open("postgres", u.String())
	must(t, e)
	second, e := sql.Open("postgres", u.String())
	must(t, e)
	first.SetMaxOpenConns(8)
	second.SetMaxOpenConns(8)
	t.Cleanup(func() {
		first.Close()
		second.Close()
		_, e := admin.Exec(`DROP DATABASE ` + name + ` WITH (FORCE)`)
		if e != nil {
			t.Error(e)
		}
	})
	return first, second, name
}
func TestPostgresIsolationRollbackConcurrentClaimsAndRestart(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv("LMM_MK09_TEST_DSN")
	if dsn == "" {
		t.Fatal("LMM_MK09_TEST_DSN must name a disposable test PostgreSQL instance")
	}
	base, e := url.Parse(dsn)
	must(t, e)
	if base.Hostname() != "127.0.0.1" && base.Hostname() != "localhost" {
		t.Fatal("test database must be local")
	}
	admin, e := sql.Open("postgres", dsn)
	must(t, e)
	t.Cleanup(func() { admin.Close() })
	must(t, admin.PingContext(ctx))
	a1, a2, aname := database(t, admin, base, "assistant")
	p1, p2, pname := database(t, admin, base, "promotions")
	_, e = a1.Exec(assistant.Schema)
	must(t, e)
	_, e = p1.Exec(promotions.Schema)
	must(t, e)
	if _, e = a1.Exec(assistant.Schema); e == nil {
		t.Fatal("fresh schema unexpectedly silently reinstalled")
	}
	ar1, e := assistant.NewPostgres(ctx, a1, aname)
	must(t, e)
	ar2, e := assistant.NewPostgres(ctx, a2, aname)
	must(t, e)
	pr1, e := promotions.NewPostgres(ctx, p1, pname)
	must(t, e)
	pr2, e := promotions.NewPostgres(ctx, p2, pname)
	must(t, e)
	if _, e := assistant.NewPostgres(ctx, a1, pname); !errors.Is(e, access.ErrForbidden) {
		t.Fatal("wrong database accepted", e)
	}
	if _, e := promotions.NewPostgres(ctx, a1, aname); !errors.Is(e, access.ErrUnavailable) {
		t.Fatal("cross module schema accepted", e)
	}
	_, e = a1.Exec(`CREATE SCHEMA core_identity`)
	must(t, e)
	if _, e := assistant.NewPostgres(ctx, a1, aname); !errors.Is(e, access.ErrForbidden) {
		t.Fatal("core database accepted", e)
	}
	_, e = a1.Exec(`DROP SCHEMA core_identity`)
	must(t, e)
	sentinel := errors.New("rollback")
	if e := ar1.Within(ctx, "rollback", func(tx storage.Tx) error {
		if e := tx.Put("entries", "one", map[string]string{"x": "one"}); e != nil {
			return e
		}
		return sentinel
	}); !errors.Is(e, sentinel) {
		t.Fatal(e)
	}
	e = ar2.Within(ctx, "rollback", func(tx storage.Tx) error { var v map[string]string; return tx.Get("entries", "one", &v) })
	if !errors.Is(e, access.ErrNotFound) {
		t.Fatal("rollback committed", e)
	}
	must(t, ar1.Within(ctx, "left", func(tx storage.Tx) error { return tx.Put("entries", "one", map[string]string{"x": "one"}) }))
	e = ar2.Within(ctx, "right", func(tx storage.Tx) error { var v map[string]string; return tx.Get("entries", "one", &v) })
	if !errors.Is(e, access.ErrNotFound) {
		t.Fatal("cross-account row", e)
	}
	identity := testkit.NewIdentity()
	token := testkit.Token("u")
	identity.Set(token, 1, 1)
	auth, e := access.New(identity)
	must(t, e)
	s1, e := assistant.New(ar1, auth, nil, assistant.Options{})
	must(t, e)
	s2, e := assistant.New(ar2, auth, nil, assistant.Options{})
	must(t, e)
	session, e := s1.Start(ctx, token, access.Account{})
	must(t, e)
	const args = `{"reference":"drawing-1","status":"completed"}`
	_, e = s1.Invoke(ctx, token, access.Account{}, session.ID, "drawing", "logs.drawing", json.RawMessage(args))
	must(t, e)
	replay, e := s2.Invoke(ctx, token, access.Account{}, session.ID, "drawing", "logs.drawing", json.RawMessage(args))
	must(t, e)
	if !replay.Duplicate {
		t.Fatal("restart repeated side effect")
	}
	entries, e := s2.Entries(ctx, token, access.Account{}, "", 100)
	must(t, e)
	if len(entries) != 1 {
		t.Fatal(entries)
	}
	policy := promotions.Policy{Limits: promotions.DefaultLimits(), Campaigns: []promotions.Campaign{{ID: "welcome", Revision: 1, Kind: promotions.Coupon, Enabled: true, StartsAt: time.Now().Add(-time.Hour), EndsAt: time.Now().Add(time.Hour), Units: 10, BudgetUnits: 10, MaxClaims: 1}}}
	rewards := &coreRewards{receipts: map[string]promotions.Receipt{}}
	ps1, e := promotions.New(pr1, auth, rewards, policy)
	must(t, e)
	ps2, e := promotions.New(pr2, auth, rewards, policy)
	must(t, e)
	var wg sync.WaitGroup
	failures := make(chan error, 16)
	for n := 0; n < 16; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			svc := ps1
			if n%2 != 0 {
				svc = ps2
			}
			_, e := svc.Apply(ctx, token, "welcome", "", promotions.Coupon)
			failures <- e
		}(n)
	}
	wg.Wait()
	close(failures)
	for e := range failures {
		must(t, e)
	}
	if rewards.effects != 1 {
		t.Fatal("duplicate monetary effect", rewards.effects)
	}
	var count int
	must(t, p1.QueryRow(`SELECT count(*) FROM lmm_promotions.records WHERE bucket='claims'`).Scan(&count))
	if count != 1 {
		t.Fatal(count)
	}
	var body []byte
	must(t, p1.QueryRow(`SELECT body FROM lmm_promotions.records WHERE bucket='user_totals' AND id='1'`).Scan(&body))
	var total struct{ Units, Claims int64 }
	must(t, json.Unmarshal(body, &total))
	if total.Units != 10 || total.Claims != 1 {
		t.Fatal(string(body))
	}
	// A second core-confirmed user cannot bypass the campaign's total budget.
	identity.Set(testkit.Token("v"), 2, 6)
	rewards.userID = 2
	if _, e := ps2.Apply(ctx, testkit.Token("v"), "welcome", "", promotions.Coupon); !errors.Is(e, access.ErrLimit) {
		t.Fatal("campaign cap", e)
	}
	// Simulate a process failure after core commit but before extension completion.
	policy.Campaigns[0].ID = "recovery"
	policy.Campaigns[0].BudgetUnits = 20
	policy.Campaigns[0].MaxClaims = 2
	rewards.userID = 1
	rewards.lose = true
	recovery, e := promotions.New(pr1, auth, rewards, policy)
	must(t, e)
	pending, e := recovery.Apply(ctx, token, "recovery", "", promotions.Coupon)
	if !errors.Is(e, access.ErrUnavailable) || pending.State != promotions.Pending {
		t.Fatal(pending, e)
	}
	restarted, e := promotions.New(pr2, auth, rewards, policy)
	must(t, e)
	settled, e := restarted.Reconcile(ctx, token, pending.Request.Key)
	must(t, e)
	if settled.State != promotions.Granted || rewards.effects != 2 {
		t.Fatal(settled, rewards.effects)
	}
	// Explicit module-only DB roles can use their own records, not another DB/schema.
	_, e = a1.Exec(`CREATE ROLE mk09_assistant_role NOLOGIN; GRANT USAGE ON SCHEMA lmm_assistant TO mk09_assistant_role; GRANT SELECT ON lmm_assistant.contract TO mk09_assistant_role; GRANT SELECT,INSERT,UPDATE ON lmm_assistant.records TO mk09_assistant_role`)
	must(t, e)
	t.Cleanup(func() {
		if _, err := a1.Exec(`DROP OWNED BY mk09_assistant_role`); err != nil {
			t.Error(err)
		}
		if _, err := admin.Exec(`DROP ROLE mk09_assistant_role`); err != nil {
			t.Error(err)
		}
	})
	conn, e := a1.Conn(ctx)
	must(t, e)
	defer conn.Close()
	_, e = conn.ExecContext(ctx, `SET ROLE mk09_assistant_role`)
	must(t, e)
	var version int
	must(t, conn.QueryRowContext(ctx, `SELECT version FROM lmm_assistant.contract`).Scan(&version))
	if version != 1 {
		t.Fatal(version)
	}
	if _, e = conn.ExecContext(ctx, `CREATE TABLE public.not_allowed(x int)`); e == nil {
		t.Fatal("module role created unrelated table")
	}
	_, e = conn.ExecContext(ctx, `RESET ROLE`)
	must(t, e)
	// No chat or user credential is retained in either module database.
	rows, e := a1.Query(`SELECT body::text FROM lmm_assistant.records`)
	must(t, e)
	defer rows.Close()
	for rows.Next() {
		var text string
		must(t, rows.Scan(&text))
		if strings.Contains(text, token) {
			t.Fatal("credential persisted")
		}
	}
	must(t, rows.Err())
}

type coreRewards struct {
	mu       sync.Mutex
	receipts map[string]promotions.Receipt
	effects  int
	userID   int64
	lose     bool
}

func (c *coreRewards) Evaluate(_ context.Context, _ string, r promotions.EligibilityRequest) (promotions.Eligibility, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	uid := c.userID
	if uid == 0 {
		uid = 1
	}
	return promotions.Eligibility{ProofID: "proof", CampaignID: r.CampaignID, Revision: r.Revision, Kind: r.Kind, UserID: uid, SubjectID: uid, Account: access.Account{Kind: "personal", ID: uid}, Units: 10, ExpiresAt: time.Now().Add(time.Minute)}, nil
}
func (c *coreRewards) Grant(_ context.Context, _ string, r promotions.GrantRequest) (promotions.Receipt, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if saved, ok := c.receipts[r.Key]; ok {
		if saved.RequestHash != promotions.RequestHash(r) {
			return promotions.Receipt{}, access.ErrConflict
		}
		return saved, nil
	}
	receipt := promotions.Receipt{ID: "receipt:" + r.Key, Key: r.Key, RequestHash: promotions.RequestHash(r), State: promotions.Granted, CouponID: "coupon:" + r.Key}
	c.receipts[r.Key] = receipt
	c.effects++
	if c.lose {
		c.lose = false
		return promotions.Receipt{}, access.ErrUnavailable
	}
	return receipt, nil
}
