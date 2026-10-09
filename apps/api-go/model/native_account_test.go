package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/pkg/account"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func nativeAccountFixture(t *testing.T, pg bool) (*NativeAccountStore, []NativeAccountActor) {
	t.Helper()
	var db *gorm.DB
	models := append([]interface{}{&User{}, &UserSession{}}, nativeAccountModels()...)
	if pg {
		db = openIsolatedPostgresCacheTestDB(t, models...)
		usePostgresDatabaseType(t)
	} else {
		db = setupSubscriptionPreConsumeErrorTestDB(t)
		require.NoError(t, db.AutoMigrate(models...))
		sqlDB, err := db.DB()
		require.NoError(t, err)
		sqlDB.SetMaxOpenConns(1)
		require.NoError(t, db.Exec("PRAGMA foreign_keys = ON").Error)
	}
	s, err := NewNativeAccountStore(db)
	require.NoError(t, err)
	actors := make([]NativeAccountActor, 6)
	for i := range actors {
		id := i + 1
		role := common.RoleCommonUser
		if id == 6 {
			role = common.RoleRootUser
		}
		u := User{Id: id, Username: fmt.Sprintf("native-user-%d", id), DisplayName: fmt.Sprintf("Person %d", id), AffCode: fmt.Sprintf("native-aff-%d", id), Status: common.UserStatusEnabled, Role: role, AuthVersion: 1, Quota: 12345}
		require.NoError(t, db.Create(&u).Error)
		sid := fmt.Sprintf("native-session-%d", id)
		require.NoError(t, db.Create(&UserSession{SID: sid, UserID: id, Version: 1, UserAuthVersion: 1, Status: UserSessionStatusActive, RefreshHash: strings.Repeat("f", 64), LoginMethod: "password", ExpiresAt: time.Now().Add(time.Hour).Unix()}).Error)
		actors[i] = NativeAccountActor{UserID: id, AuthVersion: 1, SessionID: sid, SessionVersion: 1}
	}
	return s, actors
}

func nativeTeam(t *testing.T, s *NativeAccountStore, a NativeAccountActor) int64 {
	t.Helper()
	team, err := s.CreateTeam(context.Background(), a, "Example team", "create-native-team-001")
	require.NoError(t, err)
	return team.Account.ID
}

func nativeJoin(t *testing.T, s *NativeAccountStore, owner, member NativeAccountActor, id int64, role account.TeamRole) {
	t.Helper()
	invite, err := s.Invite(context.Background(), owner, id, member.UserID, role, fmt.Sprintf("invite-%d-%s-00000001", member.UserID, role))
	require.NoError(t, err)
	_, err = s.AcceptInvitation(context.Background(), member, invite.ID)
	require.NoError(t, err)
}

func TestNativeAccountCreateAndReplay(t *testing.T) {
	s, a := nativeAccountFixture(t, false)
	ctx := context.Background()
	list, err := s.ListAccounts(ctx, a[0], 0)
	require.NoError(t, err)
	require.Empty(t, list.Teams)
	var n int64
	require.NoError(t, s.db.Model(&NativeAccountRecord{}).Count(&n).Error)
	require.Zero(t, n, "GET must not create registry rows")
	id := nativeTeam(t, s, a[0])
	team, err := s.CreateTeam(ctx, a[0], "Example team", "create-native-team-001")
	require.NoError(t, err)
	require.Equal(t, id, team.Account.ID)
	require.Equal(t, account.Owner, team.TeamRole)
	require.True(t, team.CanManageTeam)
	require.False(t, team.TeamBillingAvailable)
	_, err = s.CreateTeam(ctx, a[0], "Another name", "create-native-team-001")
	require.ErrorIs(t, err, ErrNativeAccountInput)
	_, err = s.CreateTeam(ctx, a[0], "Example team", "create-native-team-002")
	require.ErrorIs(t, err, ErrNativeAccountExists)
	require.NoError(t, s.db.Model(&NativeAccountRecord{}).Count(&n).Error)
	require.EqualValues(t, 2, n)
	require.NoError(t, s.db.Model(&NativeAccountEvent{}).Count(&n).Error)
	require.EqualValues(t, 1, n)
	require.NoError(t, s.db.Model(&NativeAccountMember{}).Count(&n).Error)
	require.Zero(t, n, "owner must not be stored twice")
	var user User
	require.NoError(t, s.db.First(&user, 1).Error)
	require.Zero(t, user.ConsoleActivatedAt)
	require.Equal(t, 12345, user.Quota)
	require.Equal(t, common.RoleCommonUser, user.Role)
	_, err = s.GetTeam(ctx, a[5], id)
	require.Error(t, err, "root is not an implicit team member")
	list, err = s.ListAccounts(ctx, a[5], 0)
	require.NoError(t, err)
	require.Empty(t, list.Teams)
}

func TestNativeAccountInviteLifecycleAndHierarchy(t *testing.T) {
	s, a := nativeAccountFixture(t, false)
	ctx := context.Background()
	id := nativeTeam(t, s, a[0])
	nativeJoin(t, s, a[0], a[1], id, account.Admin)
	nativeJoin(t, s, a[0], a[2], id, account.Admin)
	nativeJoin(t, s, a[0], a[3], id, account.Member)
	_, err := s.Invite(ctx, a[1], id, a[4].UserID, account.Admin, "cannot-grant-admin-01")
	require.Error(t, err)
	_, err = s.Invite(ctx, a[3], id, a[4].UserID, account.Member, "member-cannot-invite-01")
	require.Error(t, err)
	require.Error(t, s.SetMemberRole(ctx, a[1], id, a[2].UserID, account.Member))
	require.Error(t, s.SetMemberRole(ctx, a[1], id, a[3].UserID, account.Admin))
	require.Error(t, s.SetMemberRole(ctx, a[0], id, a[3].UserID, account.Owner))
	require.Error(t, s.RemoveMember(ctx, a[0], id, a[0].UserID))
	invite, err := s.Invite(ctx, a[1], id, a[4].UserID, account.Member, "member-invite-00001")
	require.NoError(t, err)
	replay, err := s.Invite(ctx, a[1], id, a[4].UserID, account.Member, "member-invite-00001")
	require.NoError(t, err)
	require.Equal(t, invite.ID, replay.ID)
	_, err = s.AcceptInvitation(ctx, a[3], invite.ID)
	require.Error(t, err, "another member cannot claim the invitation")
	list, err := s.ListInvitations(ctx, a[4], "")
	require.NoError(t, err)
	require.Len(t, list, 1)
	joined, err := s.AcceptInvitation(ctx, a[4], invite.ID)
	require.NoError(t, err)
	require.Equal(t, account.Member, joined.TeamRole)
	_, err = s.AcceptInvitation(ctx, a[4], invite.ID)
	require.NoError(t, err)
	var count int64
	require.NoError(t, s.db.Model(&NativeAccountEvent{}).Where("action = ? AND target_user_id = ?", "member.accept", a[4].UserID).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, s.RemoveMember(ctx, a[1], id, a[4].UserID))
	_, err = s.AcceptInvitation(ctx, a[4], invite.ID)
	require.Error(t, err, "accepted invitation must not restore removed membership")
	var removed NativeAccountMember
	require.NoError(t, s.db.Where("account_kind = ? AND account_id = ? AND user_id = ?", account.Team, id, a[4].UserID).Take(&removed).Error)
	require.False(t, removed.Active)
	require.EqualValues(t, 2, removed.Version)
	require.NoError(t, s.RemoveMember(ctx, a[3], id, a[3].UserID))
	members, err := s.ListMembers(ctx, a[0], id, 0)
	require.NoError(t, err)
	require.Equal(t, a[0].UserID, members.OwnerUserID)
	require.Len(t, members.Members, 2)
}

func TestNativeAccountInvitationRevocationAndScope(t *testing.T) {
	s, a := nativeAccountFixture(t, false)
	ctx := context.Background()
	id := nativeTeam(t, s, a[0])
	other := nativeTeam(t, s, a[1])
	nativeJoin(t, s, a[0], a[2], id, account.Admin)
	invite, err := s.Invite(ctx, a[2], id, a[3].UserID, account.Member, "revoked-invite-00001")
	require.NoError(t, err)
	require.Error(t, s.RevokeInvitation(ctx, a[1], other, invite.ID))
	require.NoError(t, s.SetMemberRole(ctx, a[0], id, a[2].UserID, account.Member))
	_, err = s.AcceptInvitation(ctx, a[3], invite.ID)
	require.Error(t, err)
	invite, err = s.Invite(ctx, a[0], id, a[3].UserID, account.Member, "expired-invite-00001")
	require.NoError(t, err)
	require.NoError(t, s.db.Model(invite).Update("expires_at", time.Now().Unix()-1).Error)
	_, err = s.AcceptInvitation(ctx, a[3], invite.ID)
	require.ErrorIs(t, err, ErrNativeInvitationUnavailable)
	invite, err = s.Invite(ctx, a[0], id, a[3].UserID, account.Member, "security-invite-0001")
	require.NoError(t, err)
	require.NoError(t, s.db.Model(&User{}).Where("id = ?", a[0].UserID).Update("auth_version", 2).Error)
	_, err = s.AcceptInvitation(ctx, a[3], invite.ID)
	require.ErrorIs(t, err, ErrNativeInvitationUnavailable)
}

func TestNativeAccountSecurityStateAndInput(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*gorm.DB, NativeAccountActor) error
	}{
		{"disabled", func(db *gorm.DB, a NativeAccountActor) error {
			return db.Model(&User{}).Where("id = ?", a.UserID).Update("status", common.UserStatusDisabled).Error
		}},
		{"explicit L0", func(db *gorm.DB, a NativeAccountActor) error {
			return db.Model(&User{}).Where("id = ?", a.UserID).Update("trust_level_override", 0).Error
		}},
		{"session revoked", func(db *gorm.DB, a NativeAccountActor) error {
			return db.Model(&UserSession{}).Where("sid = ?", a.SessionID).Update("status", UserSessionStatusRevoked).Error
		}},
		{"session expired", func(db *gorm.DB, a NativeAccountActor) error {
			return db.Model(&UserSession{}).Where("sid = ?", a.SessionID).Update("expires_at", time.Now().Unix()-1).Error
		}},
		{"session version", func(db *gorm.DB, a NativeAccountActor) error {
			return db.Model(&UserSession{}).Where("sid = ?", a.SessionID).Update("version", 2).Error
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, a := nativeAccountFixture(t, false)
			require.NoError(t, tc.mutate(s.db, a[0]))
			_, err := s.CreateTeam(context.Background(), a[0], "Team", "native-invalid-00001")
			require.Error(t, err)
			var n int64
			require.NoError(t, s.db.Model(&NativeAccountRecord{}).Count(&n).Error)
			require.Zero(t, n)
		})
	}
	for _, name := range []string{"", " padded", "padded ", "line\nbreak", "hidden\u202ename", strings.Repeat("x", 81)} {
		require.False(t, nativeNameValid(name))
	}
	require.True(t, nativeNameValid("研发团队"))
	for _, key := range []string{"", "short", strings.Repeat("x", 65), "invalid request key"} {
		require.False(t, nativeRequestKeyValid(key))
	}
}

func TestNativeAccountAuditFailureRollsBack(t *testing.T) {
	s, a := nativeAccountFixture(t, false)
	failure := errors.New("audit storage failure")
	require.NoError(t, s.db.Callback().Create().Before("gorm:create").Register("native_audit_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "account_events" {
			tx.AddError(failure)
		}
	}))
	_, err := s.CreateTeam(context.Background(), a[0], "Team", "native-rollback-00001")
	require.ErrorIs(t, err, failure)
	var n int64
	require.NoError(t, s.db.Model(&NativeAccountRecord{}).Count(&n).Error)
	require.Zero(t, n)
}

func TestNativeAccountStorageConstraints(t *testing.T) {
	s, a := nativeAccountFixture(t, false)
	id := nativeTeam(t, s, a[0])
	for _, row := range []NativeAccountRecord{
		{Kind: account.Team, ID: id + 1, OwnerUserID: a[0].UserID, DisplayName: "duplicate owner", Enabled: true},
		{Kind: account.Personal, ID: 999, OwnerUserID: a[1].UserID, DisplayName: "wrong personal ID", Enabled: true},
		{Kind: "root", ID: 999, OwnerUserID: a[1].UserID, DisplayName: "invalid kind", Enabled: true},
		{Kind: account.Team, ID: 999, OwnerUserID: 999, DisplayName: "missing owner", Enabled: true},
	} {
		require.Error(t, s.db.Create(&row).Error)
	}
	member := NativeAccountMember{AccountKind: account.Team, AccountID: id, UserID: a[1].UserID, Role: account.Owner, Active: true, Version: 1}
	require.Error(t, s.db.Create(&member).Error)
	member.Role = account.Member
	member.AccountID = id + 1
	require.Error(t, s.db.Create(&member).Error)
	// Responses never expose record associations, credentials or request keys.
	inv := NativeAccountInvitation{RequestKey: "must-not-leak", InviterAuthVersion: 2, InviterMemberVersion: 3}
	raw, err := json.Marshal(inv)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "must-not-leak")
	require.NotContains(t, string(raw), "password")
}

func TestNativeAccountConcurrentCreatePostgres(t *testing.T) {
	s, a := nativeAccountFixture(t, true)
	var wg sync.WaitGroup
	results := make(chan *NativeAccountSummary, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := s.CreateTeam(context.Background(), a[0], "Concurrent team", "concurrent-create-001")
			results <- result
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var id int64
	for result := range results {
		require.NotNil(t, result)
		if id == 0 {
			id = result.Account.ID
		}
		require.Equal(t, id, result.Account.ID)
	}
	var count int64
	require.NoError(t, s.db.Model(&NativeAccountEvent{}).Where("action = ?", "team.create").Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestNativeAccountOptionalSchema(t *testing.T) {
	t.Setenv("NATIVE_ACCOUNTS_ENABLED", "")
	enabled, err := NativeAccountsEnabledFromEnv()
	require.NoError(t, err)
	require.False(t, enabled)
	models, err := nativeAccountMigrationModels(nil)
	require.NoError(t, err)
	require.Empty(t, models)
	t.Setenv("NATIVE_ACCOUNTS_ENABLED", "TRUE")
	_, err = NativeAccountsEnabledFromEnv()
	require.Error(t, err)
	t.Setenv("NATIVE_ACCOUNTS_ENABLED", "true")
	_, err = nativeAccountMigrationModels(nil)
	require.ErrorIs(t, err, gorm.ErrInvalidDB)
}

func TestNativeAccountSchemaPostgres(t *testing.T) {
	s, _ := nativeAccountFixture(t, true)
	t.Setenv("NATIVE_ACCOUNTS_ENABLED", "true")
	t.Setenv("OAUTH_SERVER_ENABLED", "false")
	t.Setenv("LMM_OIDC_ENABLED", "false")
	models, err := identityMigrationModels(s.db)
	require.NoError(t, err)
	require.Len(t, models, 4)
	require.NoError(t, EnsureNativeAccountSchemaAtStartup(s.db))
	require.NoError(t, s.db.Migrator().DropTable(&NativeAccountEvent{}))
	require.Error(t, EnsureNativeAccountSchemaAtStartup(s.db))
	require.False(t, s.db.Migrator().HasTable(&NativeAccountEvent{}), "verification must not recreate schema")
}
