// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package toolmarket

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type authorityWithoutConfirmation struct{ Authority }

func TestA08ConfirmationRequiresIndependentHumanVerifier(t *testing.T) {
	mock := newMock(t)
	m := testModule(t, mock)
	_, i, _ := prepared(t, m, mock, "none", "free")
	in := invocation(t, m, alice, i.ID, "no-human-verifier")
	preview := grantPreviewFor(t, m, in, 1, 0)
	m.authority = authorityWithoutConfirmation{m.authority}
	_, err := m.confirmGrant(context.Background(), alice, "alice", GrantConfirmInput{preview.Grant.ID, preview.Confirmation})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ordinary authorization activated a grant without independent human verification: %v", err)
	}
	g, err := m.grantStatus(alice, preview.Grant.ID)
	if err != nil || g.State != "preview" || g.UsedCalls != 0 || mock.calls.Load() != 0 {
		t.Fatal("failed human verification changed state")
	}
}

func (a *testAuthority) VerifyToolConfirmation(ctx context.Context, credential string, intent ConfirmationIntent) (ConfirmationEvidence, error) {
	p, err := a.Authorize(ctx, credential, intent.Owner.Account, "toolmarket.authorize")
	if err != nil {
		return ConfirmationEvidence{}, err
	}
	return ConfirmationEvidence{ID: "synthetic-" + intent.Grant, Audience: ConfirmationAudience,
		IntentHash: digest(intent), Owner: p, ActorKind: "human_session", ValidUntil: min(time.Now().Unix()+120, intent.Expires)}, nil
}

type confirmationAuthority struct {
	Authority
	verify func(context.Context, string, ConfirmationIntent) (ConfirmationEvidence, error)
}

func (a confirmationAuthority) VerifyToolConfirmation(ctx context.Context, token string, intent ConfirmationIntent) (ConfirmationEvidence, error) {
	return a.verify(ctx, token, intent)
}

func TestA08ConfirmationRejectsUnboundOrNonhumanEvidence(t *testing.T) {
	cases := []struct {
		name   string
		change func(*ConfirmationEvidence)
	}{
		{"agent", func(e *ConfirmationEvidence) { e.ActorKind = "delegated_agent" }},
		{"api-key", func(e *ConfirmationEvidence) { e.ActorKind = "api_key" }},
		{"unknown-kind", func(e *ConfirmationEvidence) { e.ActorKind = "" }},
		{"other-user", func(e *ConfirmationEvidence) { e.Owner = bob }},
		{"other-account", func(e *ConfirmationEvidence) { e.Owner.Account = "other-team" }},
		{"other-intent", func(e *ConfirmationEvidence) { e.IntentHash = digest("other operation") }},
		{"other-audience", func(e *ConfirmationEvidence) { e.Audience = "other-module" }},
		{"missing-reference", func(e *ConfirmationEvidence) { e.ID = "" }},
		{"expired", func(e *ConfirmationEvidence) { e.ValidUntil = time.Now().Unix() - 1 }},
		{"excessive-lifetime", func(e *ConfirmationEvidence) { e.ValidUntil = time.Now().Unix() + 3600 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := newMock(t)
			m := testModule(t, mock)
			_, i, _ := prepared(t, m, mock, "none", "free")
			in := invocation(t, m, alice, i.ID, "bad-confirmation")
			preview := grantPreviewFor(t, m, in, 1, 0)
			base := &testAuthority{}
			m.authority = confirmationAuthority{base, func(ctx context.Context, c string, intent ConfirmationIntent) (ConfirmationEvidence, error) {
				evidence, err := base.VerifyToolConfirmation(ctx, c, intent)
				tc.change(&evidence)
				return evidence, err
			}}
			if _, err := m.confirmGrant(context.Background(), alice, "alice", GrantConfirmInput{preview.Grant.ID, preview.Confirmation}); !errors.Is(err, ErrDenied) {
				t.Fatal("accepted invalid confirmation", err)
			}
			g, err := m.grantStatus(alice, preview.Grant.ID)
			if err != nil || g.State != "preview" || g.ApprovalID != "" || mock.calls.Load() != 0 {
				t.Fatal("invalid confirmation changed execution state")
			}
		})
	}
}

func TestA08ConfirmationRechecksChangesDuringCoreVerification(t *testing.T) {
	for _, mode := range []string{"budget", "arguments", "generation", "revoke", "cancel", "panic", "unavailable"} {
		t.Run(mode, func(t *testing.T) {
			mock := newMock(t)
			m := testModule(t, mock)
			_, i, _ := prepared(t, m, mock, "none", "free")
			in := invocation(t, m, alice, i.ID, "confirmation-race")
			preview := grantPreviewFor(t, m, in, 1, 0)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			base := &testAuthority{}
			m.authority = confirmationAuthority{base, func(ctx context.Context, c string, intent ConfirmationIntent) (ConfirmationEvidence, error) {
				if intent != confirmationIntent(preview.Grant) {
					t.Fatal("verifier did not receive the exact server-held preview")
				}
				evidence, err := base.VerifyToolConfirmation(ctx, c, intent)
				switch mode {
				case "cancel":
					cancel()
				case "panic":
					panic("private synthetic verifier failure")
				case "unavailable":
					return ConfirmationEvidence{}, errors.New("private synthetic verifier failure")
				default:
					if err := m.store.update(func(d *database) error {
						g := d.Grants[preview.Grant.ID]
						switch mode {
						case "budget":
							g.MaxCalls++
						case "arguments":
							g.ArgumentsHash = digest("changed recipient")
						case "generation":
							v := d.Installs[i.ID]
							v.Generation++
							d.Installs[i.ID] = v
						case "revoke":
							g.State = "revoked"
						}
						d.Grants[g.ID] = g
						return nil
					}); err != nil {
						t.Fatal(err)
					}
				}
				return evidence, err
			}}
			_, err := m.confirmGrant(ctx, alice, "alice", GrantConfirmInput{preview.Grant.ID, preview.Confirmation})
			if err == nil || strings.Contains(err.Error(), "private") {
				t.Fatal("stale confirmation succeeded or exposed a private error")
			}
			g, err := m.grantStatus(alice, preview.Grant.ID)
			if err != nil || g.State == "active" || g.ApprovalID != "" || mock.calls.Load() != 0 {
				t.Fatal("stale confirmation became active")
			}
		})
	}
}

func TestA08ConfirmationEvidenceIsDurableAndCannotApproveAnotherIntent(t *testing.T) {
	mock := newMock(t)
	m := testModule(t, mock)
	_, i, _ := prepared(t, m, mock, "none", "free")
	in := invocation(t, m, alice, i.ID, "bound-confirmation")
	first := grantPreviewFor(t, m, in, 1, 0)
	second := grantPreviewFor(t, m, in, 2, 0)
	base := &testAuthority{}
	saved, err := base.VerifyToolConfirmation(context.Background(), "alice", confirmationIntent(first.Grant))
	if err != nil {
		t.Fatal(err)
	}
	m.authority = confirmationAuthority{base, func(context.Context, string, ConfirmationIntent) (ConfirmationEvidence, error) { return saved, nil }}
	g := confirmFor(t, m, first)
	if g.ApprovalID != saved.ID || g.ApprovalHash != saved.IntentHash {
		t.Fatal("approval reference was not recorded")
	}
	if _, err := m.confirmGrant(context.Background(), alice, "alice", GrantConfirmInput{second.Grant.ID, second.Confirmation}); !errors.Is(err, ErrDenied) {
		t.Fatal("reused approval authorized a different grant")
	}
	// A pre-verifier active grant is not silently upgraded at runtime.
	if err := m.store.update(func(d *database) error {
		v := d.Grants[g.ID]
		v.ApprovalID, v.ApprovalHash = "", ""
		d.Grants[g.ID] = v
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	in.Grant = g.ID
	if _, err := m.execute(context.Background(), alice, "alice", in); !errors.Is(err, ErrConsent) {
		t.Fatal("legacy unverified consent remained executable", err)
	}
	if mock.calls.Load() != 0 {
		t.Fatal("invalid approval called the provider")
	}
}
