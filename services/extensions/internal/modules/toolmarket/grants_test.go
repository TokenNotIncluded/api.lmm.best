// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package toolmarket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func grantPreviewFor(t *testing.T, m *Module, in ExecuteInput, calls, amount int64) GrantPreview {
	t.Helper()
	preview, err := m.previewGrant(context.Background(), alice, "alice", GrantPreviewInput{
		Installation: in.Installation, Name: in.Name, Handle: in.Handle, Quote: in.Quote, Arguments: in.Arguments,
		MaxCalls: calls, MaxAmount: amount, Expires: m.now().Add(30 * time.Minute).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return preview
}
func confirmFor(t *testing.T, m *Module, preview GrantPreview) ExecutionGrant {
	t.Helper()
	g, err := m.confirmGrant(context.Background(), alice, "alice", GrantConfirmInput{preview.Grant.ID, preview.Confirmation})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestA08LoadingDoesNotGrantExecution(t *testing.T) {
	mock := newMock(t)
	m := testModule(t, mock)
	_, i, _ := prepared(t, m, mock, "none", "free")
	in := invocation(t, m, alice, i.ID, "no-consent")
	in.Grant = ""
	if _, err := m.execute(context.Background(), alice, "alice", in); !errors.Is(err, ErrConsent) {
		t.Fatal(err)
	}
	preview := grantPreviewFor(t, m, in, 1, 0)
	in.Grant = preview.Grant.ID
	if _, err := m.execute(context.Background(), alice, "alice", in); !errors.Is(err, ErrConsent) {
		t.Fatal("preview became authority", err)
	}
	if mock.calls.Load() != 0 {
		t.Fatal("unconfirmed side effect")
	}
	for _, tool := range metaTools() {
		if strings.Contains(tool.Name, "grant") || strings.Contains(tool.Name, "confirm") {
			t.Fatal("model can grant itself consent")
		}
	}
	in.Grant = confirmFor(t, m, preview).ID
	if _, err := m.execute(context.Background(), alice, "alice", in); err != nil {
		t.Fatal(err)
	}
	if mock.calls.Load() != 1 {
		t.Fatal("confirmed call missing")
	}
}

func TestA08ConfirmationBindsOwnerPayloadAndSingleUse(t *testing.T) {
	mock := newMock(t)
	m := testModule(t, mock)
	_, i, _ := prepared(t, m, mock, "none", "free")
	in := invocation(t, m, alice, i.ID, "confirm")
	preview := grantPreviewFor(t, m, in, 1, 0)
	for _, attempt := range []struct {
		p                        Principal
		credential, confirmation string
		want                     error
	}{
		{bob, "bob", preview.Confirmation, ErrMissing},
		{alice, "read-only", preview.Confirmation, ErrDenied},
		{alice, "alice", strings.Repeat("x", 43), ErrDenied},
	} {
		_, err := m.confirmGrant(context.Background(), attempt.p, attempt.credential, GrantConfirmInput{preview.Grant.ID, attempt.confirmation})
		if !errors.Is(err, attempt.want) {
			t.Fatal(err)
		}
	}
	body, _ := json.Marshal(map[string]any{"grant": preview.Grant.ID, "confirmation": preview.Confirmation, "max_amount": 9999})
	if w := post(t, m, "/grants/confirm", "alice", string(body)); w.Code != 400 {
		t.Fatal(w.Code)
	}
	g := confirmFor(t, m, preview)
	if g.State != "active" || g.ConfirmationHash != "" || g.MaxCalls != 1 || g.MaxAmount != 0 {
		t.Fatal("confirmation changed scope")
	}
	if _, err := m.confirmGrant(context.Background(), alice, "alice", GrantConfirmInput{g.ID, preview.Confirmation}); !errors.Is(err, ErrConflict) {
		t.Fatal("confirmation replay", err)
	}
	if _, err := m.grantStatus(bob, g.ID); !errors.Is(err, ErrMissing) {
		t.Fatal("same-team grant leaked", err)
	}
	_ = m.store.view(func(d database) error {
		b, _ := json.Marshal(d)
		if strings.Contains(string(b), preview.Confirmation) {
			t.Fatal("raw confirmation persisted")
		}
		return nil
	})
}

func TestA08ConfirmationExpiryAndGenerationDrift(t *testing.T) {
	for _, mode := range []string{"expiry", "credentials", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			mock := newMock(t)
			m := testModule(t, mock)
			_, i, _ := prepared(t, m, mock, "api_key", "free")
			in := invocation(t, m, alice, i.ID, "change")
			preview := grantPreviewFor(t, m, in, 1, 0)
			switch mode {
			case "expiry":
				now := m.now().Add(6 * time.Minute)
				m.now = func() time.Time { return now }
			case "credentials":
				if err := m.setKey(alice, KeyInput{i.ID, "new-synthetic-key"}); err != nil {
					t.Fatal(err)
				}
			case "disabled":
				if err := m.enable(alice, EnableInput{i.ID, false}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := m.confirmGrant(context.Background(), alice, "alice", GrantConfirmInput{preview.Grant.ID, preview.Confirmation}); err == nil {
				t.Fatal("accepted stale confirmation")
			}
			if mock.calls.Load() != 0 {
				t.Fatal("preview called provider")
			}
		})
	}
}

func TestA08ConsentCapIsAtomicAndDuplicateDoesNotSpendItAgain(t *testing.T) {
	mock := newMock(t)
	m := testModule(t, mock)
	f := newFunds()
	m.funds = f
	_, i, _ := prepared(t, m, mock, "none", "fixed")
	in := invocation(t, m, alice, i.ID, "base")
	in.Grant = confirmFor(t, m, grantPreviewFor(t, m, in, 12, 127)).ID // Money consent, not call count, is the limiting factor.
	var wg sync.WaitGroup
	var successes atomic.Int32
	for n := 0; n < 12; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			call := in
			call.IdempotencyKey = fmt.Sprintf("cap-%d", n)
			_, err := m.execute(context.Background(), alice, "alice", call)
			if err == nil {
				successes.Add(1)
			} else if !errors.Is(err, ErrLimit) {
				t.Errorf("unexpected error: %v", err)
			}
		}(n)
	}
	wg.Wait()
	g, err := m.grantStatus(alice, in.Grant)
	if err != nil || g.UsedCalls != 1 || g.UsedAmount != 127 || mock.calls.Load() != 1 || successes.Load() != 1 || f.reserveCalls != 1 {
		t.Fatal("consent budget raced", g, err)
	}
	var key string
	_ = m.store.view(func(d database) error {
		for _, r := range d.Executions {
			if r.Grant == in.Grant {
				key = r.RequestHash
			}
		}
		return nil
	})
	if key == "" {
		t.Fatal("missing durable operation")
	}
}

func TestA08RevokeStopsNewCallsButPreservesReceipt(t *testing.T) {
	mock := newMock(t)
	m := testModule(t, mock)
	_, i, _ := prepared(t, m, mock, "none", "free")
	in := invocation(t, m, alice, i.ID, "first")
	if _, err := m.execute(context.Background(), alice, "alice", in); err != nil {
		t.Fatal(err)
	}
	if err := m.revokeGrant(context.Background(), alice, "alice", in.Grant); err != nil {
		t.Fatal(err)
	}
	if _, err := m.execute(context.Background(), alice, "alice", in); err != nil {
		t.Fatal("lost receipt", err)
	}
	in.IdempotencyKey = "second"
	if _, err := m.execute(context.Background(), alice, "alice", in); !errors.Is(err, ErrConsent) {
		t.Fatal("revoked grant usable", err)
	}
	g, _ := m.grantStatus(alice, in.Grant)
	if g.UsedCalls != 1 || mock.calls.Load() != 1 {
		t.Fatal("replay spent consent or called provider")
	}
}

func TestA08SettlementSurvivesPriceExpiryAndInstallationRevocation(t *testing.T) {
	mock := newMock(t)
	m := testModule(t, mock)
	f := newFunds()
	m.funds = f
	f.failCommit.Store(true)
	_, i, _ := prepared(t, m, mock, "api_key", "fixed")
	in := invocation(t, m, alice, i.ID, "settle-expired")
	if _, err := m.execute(context.Background(), alice, "alice", in); !errors.Is(err, ErrPending) {
		t.Fatal(err)
	}
	if err := m.enable(alice, EnableInput{i.ID, false}); err != nil {
		t.Fatal(err)
	}
	now := m.now().Add(2 * time.Hour)
	m.now = func() time.Time { return now }
	f.failCommit.Store(false)
	if _, err := m.execute(context.Background(), alice, "alice", in); err != nil {
		t.Fatal("original settlement blocked by changed discovery", err)
	}
	if f.reserveCalls != 1 || f.commitCalls != 2 || len(f.payments) != 1 || mock.calls.Load() != 1 {
		t.Fatal("repriced or repeated original operation")
	}
	in.Arguments = json.RawMessage(`{"q":"changed"}`)
	if _, err := m.execute(context.Background(), alice, "alice", in); !errors.Is(err, ErrConflict) {
		t.Fatal("different request reused receipt", err)
	}
}

type a08AuthorityFunc func(context.Context, string, string, string) (Principal, error)

func (f a08AuthorityFunc) Authorize(ctx context.Context, c, a, p string) (Principal, error) {
	return f(ctx, c, a, p)
}

type a08ReserveHook struct {
	*fakeFunds
	after func()
}

func (f a08ReserveHook) Reserve(ctx context.Context, c string, p Principal, charge Charge) error {
	err := f.fakeFunds.Reserve(ctx, c, p, charge)
	f.after()
	return err
}

func TestA08PermissionAndConsentRevocationDuringReserve(t *testing.T) {
	for _, mode := range []string{"permission", "grant", "identity-response"} {
		t.Run(mode, func(t *testing.T) {
			mock := newMock(t)
			m := testModule(t, mock)
			_, i, _ := prepared(t, m, mock, "none", "fixed")
			in := invocation(t, m, alice, i.ID, "reserve-revoke")
			var revoked atomic.Bool
			m.authority = a08AuthorityFunc(func(ctx context.Context, c, a, p string) (Principal, error) {
				if revoked.Load() {
					if mode == "identity-response" {
						return bob, nil
					}
					return Principal{}, ErrDenied
				}
				return (&testAuthority{}).Authorize(ctx, c, a, p)
			})
			f := newFunds()
			m.funds = a08ReserveHook{f, func() {
				if mode == "grant" {
					if err := m.revokeGrant(context.Background(), alice, "alice", in.Grant); err != nil {
						t.Error(err)
					}
				} else {
					revoked.Store(true)
				}
			}}
			if _, err := m.execute(context.Background(), alice, "alice", in); !errors.Is(err, ErrPending) {
				t.Fatal(err)
			}
			if mock.calls.Load() != 0 || f.reserveCalls != 1 || f.commitCalls != 0 {
				t.Fatal("revocation crossed dispatch boundary")
			}
		})
	}
}

func TestA08ToolResultsAreRedactedBeforeStorageAndReplay(t *testing.T) {
	mock := newMock(t)
	m := testModule(t, mock)
	mock.callResult = json.RawMessage(`{"content":[{"type":"text","text":"Authorization: Basic alphaFixture\nvalue secret-alice"}],"structuredContent":{"refresh_token":"betaFixture","exact":9007199254740993},"isError":true}`)
	_, i, _ := prepared(t, m, mock, "api_key", "free")
	in := invocation(t, m, alice, i.ID, "result-secrets")
	for n := 0; n < 2; n++ {
		out, err := m.execute(context.Background(), alice, "alice", in)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(out)
		for _, v := range []string{"alphaFixture", "betaFixture", "secret-alice"} {
			if strings.Contains(string(data), v) {
				t.Fatal("secret returned")
			}
		}
		if !strings.Contains(string(data), "9007199254740993") || !strings.Contains(string(data), `"isError":true`) {
			t.Fatal("result semantics damaged")
		}
	}
	_ = m.store.view(func(d database) error {
		for _, v := range d.Executions {
			if strings.Contains(string(v.Result), "Fixture") || strings.Contains(string(v.Result), "secret-alice") {
				t.Fatal("raw tool result persisted")
			}
		}
		return nil
	})
	if mock.calls.Load() != 1 {
		t.Fatal("result retry repeated side effect")
	}
}

func TestA08ConsentBindsExactArgumentsWithoutStoringPreviewText(t *testing.T) {
	mock := newMock(t)
	m := testModule(t, mock)
	_, i, _ := prepared(t, m, mock, "none", "free")
	in := invocation(t, m, alice, i.ID, "exact-intent")
	in.Arguments = json.RawMessage(`{"q":"approved-target-fixture"}`)
	preview := grantPreviewFor(t, m, in, 1, 0)
	if string(preview.Arguments) != string(in.Arguments) || preview.Grant.ArgumentsHash == "" {
		t.Fatal("preview did not bind and display the exact arguments")
	}
	in.Grant = confirmFor(t, m, preview).ID
	if err := m.store.view(func(d database) error {
		data, err := json.Marshal(d)
		if err != nil || strings.Contains(string(data), "approved-target-fixture") {
			t.Fatal("preview input persisted instead of its hash")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	changed := in
	changed.IdempotencyKey = "changed-target"
	changed.Arguments = json.RawMessage(`{"q":"unapproved-target-fixture"}`)
	if _, err := m.execute(context.Background(), alice, "alice", changed); !errors.Is(err, ErrConsent) {
		t.Fatal("consent allowed changing the confirmed action", err)
	}
	if mock.calls.Load() != 0 {
		t.Fatal("unapproved target reached the provider")
	}
	status, err := m.grantStatus(alice, in.Grant)
	if err != nil || status.UsedCalls != 0 {
		t.Fatal("rejected parameters consumed consent")
	}
	in.Arguments = json.RawMessage("{\n  \"q\" : \"approved-target-fixture\"\n}")
	if _, err := m.execute(context.Background(), alice, "alice", in); err != nil {
		t.Fatal("equivalent JSON whitespace changed consent", err)
	}
	if mock.calls.Load() != 1 {
		t.Fatal("approved action did not run exactly once")
	}
}
