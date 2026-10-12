//! Native role/scope contract tests, not live team/database acceptance.
use lmm_core::{accounts, identity};
#[allow(dead_code)]
#[path = "../src/identity/permissions.rs"]
mod permissions;
use accounts::TeamRole;
use identity::CredentialKind;
use permissions::*;
fn actor(user_id: i64, role: PlatformRole) -> ActorFacts {
    ActorFacts {
        user_id,
        current_role: role,
        active: true,
        credential_kind: CredentialKind::Session,
        credential_auth_version: 1,
        current_auth_version: 1,
    }
}
fn team() -> TeamFacts {
    TeamFacts {
        id: 7,
        owner_user_id: 1,
        version: 3,
        active: true,
    }
}
fn member(user_id: i64, role: TeamRole) -> MemberFacts {
    MemberFacts {
        team_id: 7,
        user_id,
        role,
        version: 2,
        active: true,
    }
}
fn decide(a: ActorFacts, m: Option<MemberFacts>, action: TeamAction) -> PermissionDecision {
    decide_team(
        a,
        team(),
        m,
        3,
        if a.user_id == 1 { 0 } else { 2 },
        ResourceScope::Team(7),
        action,
    )
}

#[test]
fn all_platform_and_native_team_role_invitation_combinations() {
    for platform in [
        PlatformRole::User,
        PlatformRole::Admin,
        PlatformRole::Superadmin,
    ] {
        for role in [TeamRole::Owner, TeamRole::Admin, TeamRole::Member] {
            let user = if role == TeamRole::Owner { 1 } else { 2 };
            let membership = if role == TeamRole::Owner {
                None
            } else {
                Some(member(user, role))
            };
            for target in [TeamRole::Owner, TeamRole::Admin, TeamRole::Member] {
                let got = decide(
                    actor(user, platform),
                    membership,
                    TeamAction::Invite {
                        recipient_user_id: 8,
                        role: target,
                    },
                );
                assert_eq!(
                    got == PermissionDecision::Allow,
                    role.can_manage_member(target),
                    "{platform:?} {role:?} -> {target:?}"
                );
            }
        }
    }
}

#[test]
fn platform_root_without_membership_has_no_team_authority() {
    assert_eq!(
        decide(actor(9, PlatformRole::Superadmin), None, TeamAction::Read),
        PermissionDecision::Deny(Denial::TeamRoleRequired)
    );
}

#[test]
fn cross_team_actor_and_target_rows_reject() {
    let a = actor(2, PlatformRole::Admin);
    let cross = MemberFacts {
        team_id: 8,
        ..member(2, TeamRole::Admin)
    };
    assert_eq!(
        decide(a, Some(cross), TeamAction::Read),
        PermissionDecision::Deny(Denial::WrongTeam)
    );
    let target = MemberFacts {
        team_id: 8,
        ..member(3, TeamRole::Member)
    };
    assert_eq!(
        decide(
            a,
            Some(member(2, TeamRole::Admin)),
            TeamAction::Remove {
                target,
                expected_version: 2
            }
        ),
        PermissionDecision::Deny(Denial::WrongTeam)
    );
    assert_eq!(
        decide_team(
            a,
            team(),
            Some(member(2, TeamRole::Admin)),
            3,
            2,
            ResourceScope::Team(8),
            TeamAction::Read
        ),
        PermissionDecision::Deny(Denial::WrongTeam)
    );
}

#[test]
fn team_and_membership_version_changes_refuse_old_snapshot() {
    let a = actor(2, PlatformRole::User);
    let m = Some(member(2, TeamRole::Admin));
    assert_eq!(
        decide_team(a, team(), m, 2, 2, ResourceScope::Team(7), TeamAction::Read),
        PermissionDecision::Deny(Denial::StaleTeam)
    );
    assert_eq!(
        decide_team(a, team(), m, 3, 1, ResourceScope::Team(7), TeamAction::Read),
        PermissionDecision::Deny(Denial::StaleMember)
    );
}

#[test]
fn self_leave_allowed_but_owner_removal_rejected() {
    for role in [TeamRole::Admin, TeamRole::Member] {
        let m = member(2, role);
        assert_eq!(
            decide(
                actor(2, PlatformRole::User),
                Some(m),
                TeamAction::Remove {
                    target: m,
                    expected_version: 2
                }
            ),
            PermissionDecision::Allow
        );
    }
    let owner = member(1, TeamRole::Owner);
    assert_ne!(
        decide(
            actor(1, PlatformRole::Superadmin),
            None,
            TeamAction::Remove {
                target: owner,
                expected_version: 2
            }
        ),
        PermissionDecision::Allow
    );
}

#[test]
fn an_admin_cannot_remove_a_peer_and_a_member_cannot_remove_another_member() {
    for (actor_role, target_role) in [
        (TeamRole::Admin, TeamRole::Admin),
        (TeamRole::Member, TeamRole::Member),
    ] {
        assert_eq!(
            decide(
                actor(2, PlatformRole::Superadmin),
                Some(member(2, actor_role)),
                TeamAction::Remove {
                    target: member(3, target_role),
                    expected_version: 2
                }
            ),
            PermissionDecision::Deny(Denial::TeamRoleRequired)
        );
    }
}

#[test]
fn stale_target_conflicting_self_snapshot_and_forged_owner_refuse() {
    let a = actor(2, PlatformRole::User);
    let m = Some(member(2, TeamRole::Admin));
    assert_eq!(
        decide(
            a,
            m,
            TeamAction::Remove {
                target: member(3, TeamRole::Member),
                expected_version: 1
            }
        ),
        PermissionDecision::Deny(Denial::StaleMember)
    );
    assert_eq!(
        decide(
            a,
            m,
            TeamAction::Remove {
                target: member(2, TeamRole::Member),
                expected_version: 2
            }
        ),
        PermissionDecision::Deny(Denial::StaleMember)
    );
    assert_eq!(
        decide(a, Some(member(2, TeamRole::Owner)), TeamAction::Read),
        PermissionDecision::Deny(Denial::InvalidFacts)
    );
}

#[test]
fn owner_and_self_invites_refuse() {
    for recipient in [1, 2] {
        assert_eq!(
            decide(
                actor(2, PlatformRole::User),
                Some(member(2, TeamRole::Admin)),
                TeamAction::Invite {
                    recipient_user_id: recipient,
                    role: TeamRole::Member
                }
            ),
            PermissionDecision::Deny(Denial::TeamRoleRequired)
        );
    }
}
