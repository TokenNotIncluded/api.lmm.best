package model

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/pkg/account"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestNativeAccountInvitationManagement(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(fmt.Sprintf("postgres=%t", pg), func(t *testing.T) {
			s, a := nativeAccountFixture(t, pg)
			ctx := context.Background()
			id := nativeTeam(t, s, a[0])
			nativeJoin(t, s, a[0], a[1], id, account.Admin)
			nativeJoin(t, s, a[0], a[2], id, account.Admin)
			nativeJoin(t, s, a[0], a[3], id, account.Member)
			first, err := s.Invite(ctx, a[1], id, a[4].UserID, account.Member, "first-member-offer-001")
			require.NoError(t, err)
			second, err := s.Invite(ctx, a[0], id, a[4].UserID, account.Member, "second-member-offer-001")
			require.NoError(t, err)
			_, err = s.Invite(ctx, a[2], id, a[5].UserID, account.Member, "other-admin-offer-001")
			require.NoError(t, err)

			own, err := s.ListSentInvitations(ctx, a[1], id, "")
			require.NoError(t, err)
			require.Len(t, own, 1, "admin must not see owner or peer invitations")
			require.Equal(t, first.ID, own[0].ID)
			require.Equal(t, "Example team", own[0].TeamDisplayName)
			all, err := s.ListSentInvitations(ctx, a[0], id, "")
			require.NoError(t, err)
			require.Len(t, all, 6)
			for _, denied := range []NativeAccountActor{a[3], a[4], a[5]} {
				_, err := s.ListSentInvitations(ctx, denied, id, "")
				require.Error(t, err, "members, outsiders and platform root need management membership")
			}
			_, err = s.ListSentInvitations(ctx, a[0], id, "invalid")
			require.ErrorIs(t, err, ErrNativeAccountInput)

			received, err := s.ListInvitations(ctx, a[4], "")
			require.NoError(t, err)
			require.Len(t, received, 2)
			require.Equal(t, "Example team", received[0].TeamDisplayName)
			require.Error(t, s.DeclineInvitation(ctx, a[5], first.ID), "root cannot decline for the recipient")
			require.NoError(t, s.DeclineInvitation(ctx, a[4], first.ID))
			require.NoError(t, s.DeclineInvitation(ctx, a[4], first.ID), "retry is harmless")
			_, err = s.AcceptInvitation(ctx, a[4], first.ID)
			require.ErrorIs(t, err, ErrNativeInvitationUnavailable)
			var count int64
			require.NoError(t, s.db.Model(&NativeAccountEvent{}).Where("action = ? AND target_user_id = ?", "invite.decline", a[4].UserID).Count(&count).Error)
			require.EqualValues(t, 1, count)
			received, err = s.ListInvitations(ctx, a[4], "")
			require.NoError(t, err)
			require.Len(t, received, 1)
			require.Equal(t, second.ID, received[0].ID)
			_, err = s.AcceptInvitation(ctx, a[4], second.ID)
			require.NoError(t, err)
			require.ErrorIs(t, s.DeclineInvitation(ctx, a[4], second.ID), ErrNativeInvitationUnavailable, "declining cannot remove accepted membership")
			_, err = s.GetTeam(ctx, a[4], id)
			require.NoError(t, err)
			require.NoError(t, s.db.Model(&NativeAccountRecord{}).Where("kind = ? AND id = ?", account.Team, id).Update("enabled", false).Error)
			received, err = s.ListInvitations(ctx, a[5], "")
			require.NoError(t, err)
			require.Empty(t, received, "frozen teams cannot advertise an actionable invitation")
		})
	}
}

func TestNativeAccountInvitationFeedPagination(t *testing.T) {
	s, a := nativeAccountFixture(t, false)
	ctx := context.Background()
	id := nativeTeam(t, s, a[0])
	for i := 0; i < 51; i++ {
		_, err := s.Invite(ctx, a[0], id, a[1].UserID, account.Member, fmt.Sprintf("page-invitation-%04d", i))
		require.NoError(t, err)
	}
	first, err := s.ListInvitations(ctx, a[1], "")
	require.NoError(t, err)
	require.Len(t, first, 50)
	last, err := s.ListInvitations(ctx, a[1], first[49].ID)
	require.NoError(t, err)
	require.Len(t, last, 1)
	require.Greater(t, last[0].ID, first[49].ID)
	sent, err := s.ListSentInvitations(ctx, a[0], id, first[49].ID)
	require.NoError(t, err)
	require.Len(t, sent, 1)
	require.Equal(t, last[0].ID, sent[0].ID)
	_, err = s.ListInvitations(ctx, a[1], "invalid")
	require.ErrorIs(t, err, ErrNativeAccountInput)
}

func TestNativeAccountDeclineAuditFailureRollsBack(t *testing.T) {
	s, a := nativeAccountFixture(t, false)
	ctx := context.Background()
	id := nativeTeam(t, s, a[0])
	invite, err := s.Invite(ctx, a[0], id, a[1].UserID, account.Member, "decline-rollback-001")
	require.NoError(t, err)
	failure := errors.New("isolated audit failure")
	require.NoError(t, s.db.Callback().Create().Before("gorm:create").Register("native_decline_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "account_events" {
			tx.AddError(failure)
		}
	}))
	t.Cleanup(func() { _ = s.db.Callback().Create().Remove("native_decline_failure") })
	require.ErrorIs(t, s.DeclineInvitation(ctx, a[1], invite.ID), failure)
	var row NativeAccountInvitation
	require.NoError(t, s.db.Take(&row, "id = ?", invite.ID).Error)
	require.Equal(t, "pending", row.Status)
}

func TestNativeAccountConcurrentAcceptAndDeclinePostgres(t *testing.T) {
	s, a := nativeAccountFixture(t, true)
	ctx := context.Background()
	id := nativeTeam(t, s, a[0])
	invite, err := s.Invite(ctx, a[0], id, a[1].UserID, account.Member, "concurrent-decline-001")
	require.NoError(t, err)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	start := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, err := s.AcceptInvitation(ctx, a[1], invite.ID)
		results <- err
	}()
	go func() {
		defer wg.Done()
		<-start
		results <- s.DeclineInvitation(ctx, a[1], invite.ID)
	}()
	close(start)
	wg.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		} else {
			require.ErrorIs(t, err, ErrNativeInvitationUnavailable)
		}
	}
	require.Equal(t, 1, succeeded)
	var row NativeAccountInvitation
	require.NoError(t, s.db.Take(&row, "id = ?", invite.ID).Error)
	var members, events int64
	require.NoError(t, s.db.Model(&NativeAccountMember{}).Where("account_kind = ? AND account_id = ? AND user_id = ? AND active = ?", account.Team, id, a[1].UserID, true).Count(&members).Error)
	require.NoError(t, s.db.Model(&NativeAccountEvent{}).Where("action IN ?", []string{"member.accept", "invite.decline"}).Count(&events).Error)
	require.EqualValues(t, 1, events)
	if row.Status == "accepted" {
		require.EqualValues(t, 1, members)
	} else {
		require.Equal(t, "revoked", row.Status)
		require.Zero(t, members)
	}
}

func TestNativeAccountSchemaRejectsWeakenedChecksPostgres(t *testing.T) {
	s, _ := nativeAccountFixture(t, true)
	t.Setenv("NATIVE_ACCOUNTS_ENABLED", "true")
	require.NoError(t, EnsureNativeAccountSchemaAtStartup(s.db))
	for _, tc := range []struct{ table, name, weakened string }{
		{"accounts", "ck_accounts_identity", "id > 0"},
		{"accounts", "ck_accounts_kind", "kind IN ('personal','team','root')"},
		{"account_members", "ck_account_members_role", "role IN ('admin','member','owner')"},
		{"account_invitations", "ck_account_invites_status", "status IN ('pending','accepted','revoked','other')"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rollback := errors.New("rollback deliberate schema corruption")
			err := s.db.Transaction(func(tx *gorm.DB) error {
				// These identifiers and expressions are compile-time test cases,
				// never request data. Roll back every deliberately weaker check.
				require.NoError(t, tx.Exec("ALTER TABLE "+tc.table+" DROP CONSTRAINT "+tc.name).Error)
				require.NoError(t, tx.Exec("ALTER TABLE "+tc.table+" ADD CONSTRAINT "+tc.name+" CHECK ("+tc.weakened+")").Error)
				require.ErrorContains(t, EnsureNativeAccountSchemaAtStartup(tx), "incompatible semantics")
				return rollback
			})
			require.ErrorIs(t, err, rollback)
			require.NoError(t, EnsureNativeAccountSchemaAtStartup(s.db))
		})
	}
}
