package authz

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
)

func TestVerifyRetainsFutureResourceWithoutGrantingItOrChangingUserOverrides(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))
	require.NoError(t, SetUserPermissions(42, PermissionsMap{
		ResourceChannel: {ActionRead: false, ActionSensitiveWrite: true},
	}))
	for _, action := range []string{ActionRead, ActionWrite} {
		require.NoError(t, db.Create(&model.CasbinRule{Ptype: "p", V0: RoleSubject(BuiltInRoleAdmin), V1: "future_feature", V2: action, V3: EffectAllow}).Error)
	}
	var before, after []model.CasbinRule
	require.NoError(t, db.Order("id").Find(&before).Error)
	for range 2 {
		require.NoError(t, InitForStartup(db, false))
		require.NoError(t, ReloadPolicy())
		require.False(t, Can(42, common.RoleAdminUser, ChannelRead))
		require.True(t, Can(42, common.RoleAdminUser, ChannelSensitiveWrite))
		require.True(t, Can(43, common.RoleAdminUser, ChannelRead))
		require.False(t, Can(43, common.RoleAdminUser, ChannelSensitiveWrite))
		require.False(t, Can(43, common.RoleAdminUser, Permission{Resource: "future_feature", Action: ActionRead}))
		require.False(t, Can(44, common.RoleCommonUser, Permission{Resource: "future_feature", Action: ActionRead}))
	}
	require.NoError(t, db.Order("id").Find(&after).Error)
	require.Equal(t, before, after, "verify and policy reload must not rewrite any rule")
}

func TestVerifyRejectsKnownOrUnsafeExtraBuiltInGrants(t *testing.T) {
	for _, name := range []string{"known sensitive action", "new action on known resource", "root", "deny", "wildcard resource", "wildcard action", "empty resource", "invalid shape"} {
		t.Run(name, func(t *testing.T) {
			db := newAuthzTestDB(t)
			require.NoError(t, Init(db))
			policy := model.CasbinRule{Ptype: "p", V0: RoleSubject(BuiltInRoleAdmin), V1: "future_feature", V2: ActionRead, V3: EffectAllow}
			switch name {
			case "known sensitive action":
				policy.V1, policy.V2 = ResourceChannel, ActionSensitiveWrite
			case "new action on known resource":
				policy.V1, policy.V2 = ResourceChannel, "new_action"
			case "root":
				policy.V0 = RoleSubject(BuiltInRoleRoot)
			case "deny":
				policy.V3 = EffectDeny
			case "wildcard resource":
				policy.V1 = "*"
			case "wildcard action":
				policy.V2 = "*"
			case "empty resource":
				policy.V1 = ""
			case "invalid shape":
				policy.V4 = "extra"
			}
			require.NoError(t, db.Create(&policy).Error)
			require.Error(t, InitForStartup(db, false))
			var retained model.CasbinRule
			require.NoError(t, db.First(&retained, policy.Id).Error)
			require.Equal(t, policy, retained)
		})
	}
}

func TestFuturePoliciesDoNotHideMissingKnownBaseline(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))
	require.NoError(t, db.Where("ptype = ? AND v0 = ? AND v1 = ? AND v2 = ?", "p", RoleSubject(BuiltInRoleAdmin), ResourceChannel, ActionRead).Delete(&model.CasbinRule{}).Error)
	require.NoError(t, db.Create(&model.CasbinRule{Ptype: "p", V0: RoleSubject(BuiltInRoleAdmin), V1: "future_feature", V2: ActionRead, V3: EffectAllow}).Error)
	require.ErrorContains(t, InitForStartup(db, false), "built-in authorization policies are incomplete")
}
