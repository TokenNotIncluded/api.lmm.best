//! Pure contract tests only. These do not exercise HTTP, PostgreSQL or a host token.
use lmm_core::accounts;
#[allow(dead_code)]
#[path = "../src/identity/permissions.rs"]
pub(crate) mod permissions;
mod identity {
    pub use lmm_core::identity::{CredentialKind, IdentityError, Principal};
    pub(crate) use crate::permissions;
}
#[allow(dead_code)]
#[path = "../src/admin_compat.rs"]
mod admin_compat;
use identity::CredentialKind;
use permissions::*;

fn actor(role: PlatformRole) -> ActorFacts {
    ActorFacts { user_id: 41, current_role: role, active: true,
        credential_kind: CredentialKind::Session,
        credential_auth_version: 7, current_auth_version: 7 }
}
fn scope(permission: Permission) -> ResourceScope {
    if permission.key().starts_with("channel.") {
        ResourceScope::ChannelCollection
    } else {
        ResourceScope::AcquisitionCollection
    }
}
fn policy(role: PlatformRole, rows: &[(Permission, Effect)]) -> PolicySnapshot {
    PolicySnapshot::new(41, role, 3, rows).unwrap()
}

#[test]
fn complete_registered_catalog_role_matrix() {
    for role in [PlatformRole::User, PlatformRole::Admin, PlatformRole::Superadmin] {
        let policy = policy(role, &[]);
        for permission in Permission::ALL {
            let result = decide_platform(actor(role), &policy, 3, permission, scope(permission));
            let expected = match role {
                PlatformRole::User => PermissionDecision::Deny(Denial::AdminRequired),
                PlatformRole::Admin if !permission.admin_default() => PermissionDecision::Deny(Denial::MissingGrant),
                _ => PermissionDecision::Allow,
            };
            assert_eq!(result, expected, "{role:?} {}", permission.key());
        }
    }
}

#[test]
fn explicit_deny_precedes_admin_and_superadmin_defaults() {
    for role in [PlatformRole::Admin, PlatformRole::Superadmin] {
        for permission in Permission::ALL {
            let p = policy(role, &[(permission, Effect::Deny)]);
            assert_eq!(decide_platform(actor(role), &p, 3, permission, scope(permission)),
                PermissionDecision::Deny(Denial::ExplicitDeny));
        }
    }
}

#[test]
fn explicit_allow_cannot_promote_an_ordinary_user() {
    for permission in Permission::ALL {
        let p = policy(PlatformRole::User, &[(permission, Effect::Allow)]);
        assert_eq!(decide_platform(actor(PlatformRole::User), &p, 3, permission, scope(permission)),
            PermissionDecision::Deny(Denial::AdminRequired));
    }
}

#[test]
fn sensitive_permission_layer_requires_explicit_admin_grants() {
    for permission in [Permission::ChannelSensitiveWrite, Permission::ChannelSecretView,
        Permission::AcquisitionDetails, Permission::AcquisitionExport] {
        let p = policy(PlatformRole::Admin, &[(permission, Effect::Allow)]);
        assert_eq!(decide_platform(actor(PlatformRole::Admin), &p, 3, permission, scope(permission)),
            PermissionDecision::Allow);
    }
    // This is only the permission layer; it does not exercise A02 secure verification.
}

#[test]
fn unknown_actions_empty_resources_and_role_aliases_reject() {
    for (resource, action) in [("", "read"), ("channel", ""), ("*", "read"),
        ("channel", "*"), ("channel", "READ"), ("channel ", "read"),
        ("user", "write"), ("financial_profile", "read"), ("team", "read")] {
        assert_eq!(Permission::parse(resource, action), Err(Denial::UnknownPermission));
    }
    for role in ["root", "L5", "L6", "5", "6", "ADMIN", "developer", ""] {
        assert_eq!(PlatformRole::from_storage(role), Err(Denial::InvalidFacts));
    }
}

#[test]
fn platform_permissions_do_not_authorize_other_domains() {
    let a = actor(PlatformRole::Superadmin);
    let p = policy(PlatformRole::Superadmin, &[]);
    for resource in [ResourceScope::CompanyProfile(41), ResourceScope::CompanyProfile(99),
        ResourceScope::Team(41), ResourceScope::Channel(0), ResourceScope::Channel(-1),
        ResourceScope::AcquisitionCollection] {
        assert_eq!(decide_platform(a, &p, 3, Permission::ChannelRead, resource),
            PermissionDecision::Deny(Denial::WrongResource));
    }
}

#[test]
fn stale_policy_wrong_subject_and_changed_role_reject() {
    let a = actor(PlatformRole::Admin);
    let p = policy(PlatformRole::Admin, &[]);
    assert_eq!(decide_platform(a, &p, 2, Permission::ChannelRead, ResourceScope::Channel(5)),
        PermissionDecision::Deny(Denial::StalePolicy));
    let other = PolicySnapshot::new(42, PlatformRole::Admin, 3, &[]).unwrap();
    assert_eq!(decide_platform(a, &other, 3, Permission::ChannelRead, ResourceScope::Channel(5)),
        PermissionDecision::Deny(Denial::WrongSubject));
    let old_role = policy(PlatformRole::Superadmin, &[]);
    assert_eq!(decide_platform(a, &old_role, 3, Permission::ChannelRead, ResourceScope::Channel(5)),
        PermissionDecision::Deny(Denial::StalePolicy));
}

#[test]
fn stale_identity_disabled_account_and_api_key_reject() {
    let p = policy(PlatformRole::Admin, &[]);
    for (a, reason) in [
        (ActorFacts { current_auth_version: 8, ..actor(PlatformRole::Admin) }, Denial::StaleIdentity),
        (ActorFacts { active: false, ..actor(PlatformRole::Admin) }, Denial::Inactive),
        (ActorFacts { credential_kind: CredentialKind::ApiKey, ..actor(PlatformRole::Admin) }, Denial::SessionRequired),
        (ActorFacts { user_id: 0, ..actor(PlatformRole::Admin) }, Denial::InvalidFacts),
    ] {
        assert_eq!(decide_platform(a, &p, 3, Permission::ChannelRead, ResourceScope::Channel(5)),
            PermissionDecision::Deny(reason));
    }
}

#[test]
fn policy_hash_is_order_independent_but_binds_subject_role_version_and_effect() {
    let rows = [(Permission::ChannelRead, Effect::Deny), (Permission::AcquisitionExport, Effect::Allow)];
    let a = policy(PlatformRole::Admin, &rows);
    let b = policy(PlatformRole::Admin, &[rows[1], rows[0]]);
    assert_eq!(a.fingerprint(), b.fingerprint());
    for other in [
        PolicySnapshot::new(42, PlatformRole::Admin, 3, &rows).unwrap(),
        PolicySnapshot::new(41, PlatformRole::Superadmin, 3, &rows).unwrap(),
        PolicySnapshot::new(41, PlatformRole::Admin, 4, &rows).unwrap(),
        policy(PlatformRole::Admin, &[]),
    ] {
        assert_ne!(a.fingerprint(), other.fingerprint());
    }
}

#[test]
fn duplicate_policy_rows_reject_instead_of_last_write_wins() {
    for effect in [Effect::Allow, Effect::Deny] {
        assert!(PolicySnapshot::new(41, PlatformRole::Admin, 1,
            &[(Permission::ChannelRead, Effect::Allow), (Permission::ChannelRead, effect)]).is_err());
    }
}

#[test]
fn legacy_conversion_never_discards_unknown_policy() {
    use admin_compat::{LegacyOverride, legacy_overrides};
    for row in [
        LegacyOverride { resource: "new_resource", action: "read", effect: "allow" },
        LegacyOverride { resource: "channel", action: "new_action", effect: "allow" },
        LegacyOverride { resource: "channel", action: "read", effect: "maybe" },
    ] {
        assert!(legacy_overrides(41, "admin", 1, &[row]).is_err());
    }
    let p = legacy_overrides(41, "admin", 1,
        &[LegacyOverride { resource: "channel", action: "sensitive_write", effect: "" }]).unwrap();
    assert_eq!(decide_platform(actor(PlatformRole::Admin), &p, 1,
        Permission::ChannelSensitiveWrite, ResourceScope::ChannelCollection), PermissionDecision::Allow);
}

#[test]
fn company_identity_stays_self_only_even_for_root() {
    for role in [PlatformRole::User, PlatformRole::Admin, PlatformRole::Superadmin] {
        assert_eq!(decide_company(actor(role), ResourceScope::CompanyProfile(41)), PermissionDecision::Allow);
        assert_eq!(decide_company(actor(role), ResourceScope::CompanyProfile(42)),
            PermissionDecision::Deny(Denial::WrongResource));
        assert_eq!(decide_company(actor(role), ResourceScope::Team(41)),
            PermissionDecision::Deny(Denial::WrongResource));
    }
}

#[test]
fn developer_gate_consumes_persisted_grant_and_does_not_infer_payment() {
    for (granted, expected) in [
        (Some(true), PermissionDecision::Allow),
        (Some(false), PermissionDecision::Deny(Denial::MissingGrant)),
        (None, PermissionDecision::Deny(Denial::DeveloperUnmapped)),
    ] {
        assert_eq!(decide_developer(actor(PlatformRole::User), DeveloperFacts { user_id: 41, granted }), expected);
    }
    for role in [PlatformRole::Admin, PlatformRole::Superadmin] {
        assert_eq!(decide_developer(actor(role), DeveloperFacts { user_id: 41, granted: None }), PermissionDecision::Allow);
        assert_eq!(decide_developer(actor(role), DeveloperFacts { user_id: 42, granted: Some(true) }),
            PermissionDecision::Deny(Denial::WrongSubject));
    }
}

#[test]
fn stored_policy_round_trip_binds_every_slot_and_rejects_corruption() {
    let p = policy(PlatformRole::Admin, &[(Permission::ChannelRead, Effect::Deny)]);
    let effects = p.stored_effects();
    let restored = PolicySnapshot::from_storage(41, "admin", 3, CONTRACT_VERSION, &effects, &p.fingerprint()).unwrap();
    assert_eq!(restored.fingerprint(), p.fingerprint());
    for changed in [vec![0; 8], vec![0; 10], vec![3; 9], vec![-1; 9]] {
        assert!(PolicySnapshot::from_storage(41, "admin", 3, CONTRACT_VERSION, &changed, &p.fingerprint()).is_err());
    }
    assert!(PolicySnapshot::from_storage(41, "admin", 3, "a01-permissions-v1", &effects, &p.fingerprint()).is_err());
    assert!(PolicySnapshot::from_storage(42, "admin", 3, CONTRACT_VERSION, &effects, &p.fingerprint()).is_err());
    assert!(PolicySnapshot::from_storage(41, "admin", 3, CONTRACT_VERSION, &effects, &[0; 32]).is_err());
}
