//! A01 pure decisions and the transaction-bound platform permission loader.
//! Pure values are not bearer tokens or database proofs.
//!
//! Baseline: main@53bf14fca577c61e75de2b3e1f1edf93e4e2fd18,
//! apps/api-go/service/authz/{resources_channel,resources_acquisition,resolver}.go.
//! Native team rules use accounts::TeamRole; no second membership model is added.
//! Callers must load all facts from Core in the operation's transaction.
use crate::accounts::TeamRole;
use crate::identity::CredentialKind;
use sha2::{Digest, Sha256};

pub(crate) const CONTRACT_VERSION: &str = "a01-permissions-v2";

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) enum PlatformRole {
    User,
    Admin,
    Superadmin,
}
impl PlatformRole {
    pub(crate) fn from_storage(value: &str) -> Result<Self, Denial> {
        match value {
            "user" => Ok(Self::User),
            "admin" => Ok(Self::Admin),
            "superadmin" => Ok(Self::Superadmin),
            _ => Err(Denial::InvalidFacts),
        }
    }
    pub(crate) fn is_admin(self) -> bool {
        matches!(self, Self::Admin | Self::Superadmin)
    }
    pub(crate) fn storage_name(self) -> &'static str {
        match self {
            Self::User => "user",
            Self::Admin => "admin",
            Self::Superadmin => "superadmin",
        }
    }
    fn hash_tag(self) -> u8 {
        match self {
            Self::User => 1,
            Self::Admin => 10,
            Self::Superadmin => 100,
        }
    }
}

/// Core-only storage facts. Deliberately not Deserialize.
/// `current_role` is users.platform_role, never Principal.platform_level.
/// `active` requires both user and personal account to be active.
/// The loader must first check credential expiry/revocation and session policy.
#[derive(Clone, Copy, Debug)]
pub(crate) struct ActorFacts {
    pub(crate) user_id: i64,
    pub(crate) credential_kind: CredentialKind,
    pub(crate) credential_auth_version: i64,
    pub(crate) current_auth_version: i64,
    pub(crate) current_role: PlatformRole,
    pub(crate) active: bool,
}
impl ActorFacts {
    fn check_session(self) -> Result<(), Denial> {
        if self.user_id <= 0 || self.current_auth_version <= 0 {
            return Err(Denial::InvalidFacts);
        }
        if !self.active {
            return Err(Denial::Inactive);
        }
        if self.credential_auth_version != self.current_auth_version {
            return Err(Denial::StaleIdentity);
        }
        if self.credential_kind != CredentialKind::Session {
            return Err(Denial::SessionRequired);
        }
        Ok(())
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) enum Denial {
    InvalidFacts,
    UnknownPermission,
    WrongResource,
    Inactive,
    StaleIdentity,
    StalePolicy,
    SessionRequired,
    AdminRequired,
    ExplicitDeny,
    MissingGrant,
    InvalidPolicy,
    WrongSubject,
    WrongTeam,
    StaleTeam,
    StaleMember,
    TeamRoleRequired,
    DeveloperUnmapped,
}
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) enum PermissionDecision {
    Allow,
    Deny(Denial),
}
impl From<Result<(), Denial>> for PermissionDecision {
    fn from(value: Result<(), Denial>) -> Self {
        match value {
            Ok(()) => Self::Allow,
            Err(reason) => Self::Deny(reason),
        }
    }
}

/// The two reviewed registered main resources, not a full operation inventory.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
#[repr(u8)]
pub(crate) enum Permission {
    ChannelRead,
    ChannelOperate,
    ChannelWrite,
    ChannelSensitiveWrite,
    ChannelSecretView,
    AcquisitionRead,
    AcquisitionWrite,
    AcquisitionDetails,
    AcquisitionExport,
}
impl Permission {
    pub(crate) const ALL: [Self; 9] = [
        Self::ChannelRead,
        Self::ChannelOperate,
        Self::ChannelWrite,
        Self::ChannelSensitiveWrite,
        Self::ChannelSecretView,
        Self::AcquisitionRead,
        Self::AcquisitionWrite,
        Self::AcquisitionDetails,
        Self::AcquisitionExport,
    ];
    pub(crate) fn parse(resource: &str, action: &str) -> Result<Self, Denial> {
        match (resource, action) {
            ("channel", "read") => Ok(Self::ChannelRead),
            ("channel", "operate") => Ok(Self::ChannelOperate),
            ("channel", "write") => Ok(Self::ChannelWrite),
            ("channel", "sensitive_write") => Ok(Self::ChannelSensitiveWrite),
            ("channel", "secret_view") => Ok(Self::ChannelSecretView),
            ("acquisition", "read") => Ok(Self::AcquisitionRead),
            ("acquisition", "write") => Ok(Self::AcquisitionWrite),
            ("acquisition", "details") => Ok(Self::AcquisitionDetails),
            ("acquisition", "export") => Ok(Self::AcquisitionExport),
            _ => Err(Denial::UnknownPermission),
        }
    }
    pub(crate) fn key(self) -> &'static str {
        match self {
            Self::ChannelRead => "channel.read",
            Self::ChannelOperate => "channel.operate",
            Self::ChannelWrite => "channel.write",
            Self::ChannelSensitiveWrite => "channel.sensitive_write",
            Self::ChannelSecretView => "channel.secret_view",
            Self::AcquisitionRead => "acquisition.read",
            Self::AcquisitionWrite => "acquisition.write",
            Self::AcquisitionDetails => "acquisition.details",
            Self::AcquisitionExport => "acquisition.export",
        }
    }
    pub(crate) fn admin_default(self) -> bool {
        matches!(
            self,
            Self::ChannelRead
                | Self::ChannelOperate
                | Self::ChannelWrite
                | Self::AcquisitionRead
                | Self::AcquisitionWrite
        )
    }
    fn accepts(self, scope: ResourceScope) -> bool {
        let channel = matches!(
            self,
            Self::ChannelRead
                | Self::ChannelOperate
                | Self::ChannelWrite
                | Self::ChannelSensitiveWrite
                | Self::ChannelSecretView
        );
        match scope {
            ResourceScope::ChannelCollection => channel,
            ResourceScope::Channel(id) => channel && id > 0,
            ResourceScope::AcquisitionCollection => !channel,
            ResourceScope::Acquisition(id) => !channel && id > 0,
            ResourceScope::CompanyProfile(_) | ResourceScope::Team(_) => false,
        }
    }
}

/// A named collection is explicit. An empty resource string is never a scope.
/// Instance ownership must be resolved from storage, not copied from JSON.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) enum ResourceScope {
    ChannelCollection,
    Channel(i64),
    AcquisitionCollection,
    Acquisition(i64),
    CompanyProfile(i64),
    Team(i64),
}
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) enum Effect {
    Allow,
    Deny,
}

/// Immutable, validated policy snapshot; no runtime or HTTP deserializer.
#[derive(Clone, Debug)]
pub(crate) struct PolicySnapshot {
    subject_user_id: i64,
    role: PlatformRole,
    version: i64,
    overrides: [Option<Effect>; 9],
}
impl PolicySnapshot {
    pub(crate) fn new(
        subject_user_id: i64,
        role: PlatformRole,
        version: i64,
        overrides: &[(Permission, Effect)],
    ) -> Result<Self, Denial> {
        if subject_user_id <= 0 || version <= 0 || overrides.len() > Permission::ALL.len() {
            return Err(Denial::InvalidPolicy);
        }
        let mut result = Self {
            subject_user_id,
            role,
            version,
            overrides: [None; 9],
        };
        for &(permission, effect) in overrides {
            let slot = &mut result.overrides[permission as usize];
            // Duplicate and contradictory source rows need review, not last-write-wins.
            if slot.replace(effect).is_some() {
                return Err(Denial::InvalidPolicy);
            }
        }
        Ok(result)
    }
    pub(crate) fn subject_user_id(&self) -> i64 {
        self.subject_user_id
    }
    pub(crate) fn role(&self) -> PlatformRole {
        self.role
    }
    pub(crate) fn version(&self) -> i64 {
        self.version
    }

    /// Fixed slots prevent duplicate/unknown persisted grants and JSON authority injection.
    pub(crate) fn stored_effects(&self) -> Vec<i16> {
        self.overrides
            .iter()
            .map(|effect| match effect {
                None => 0,
                Some(Effect::Allow) => 1,
                Some(Effect::Deny) => 2,
            })
            .collect()
    }
    pub(crate) fn from_storage(
        user_id: i64,
        role: &str,
        version: i64,
        contract: &str,
        effects: &[i16],
        fingerprint: &[u8],
    ) -> Result<Self, Denial> {
        if contract != CONTRACT_VERSION || effects.len() != Permission::ALL.len() {
            return Err(Denial::InvalidPolicy);
        }
        let mut overrides = Vec::new();
        for (&permission, &effect) in Permission::ALL.iter().zip(effects) {
            match effect {
                0 => (),
                1 => overrides.push((permission, Effect::Allow)),
                2 => overrides.push((permission, Effect::Deny)),
                _ => return Err(Denial::InvalidPolicy),
            }
        }
        let policy = Self::new(
            user_id,
            PlatformRole::from_storage(role)?,
            version,
            &overrides,
        )?;
        if policy.fingerprint().as_slice() != fingerprint {
            return Err(Denial::InvalidPolicy);
        }
        Ok(policy)
    }

    /// Exact encoding: contract + NUL, i64 BE subject/version, role byte,
    /// then the fixed catalog's UTF-8 key + NUL + {0=inherit,1=allow,2=deny}.
    /// Never hash a caller-supplied JSON map or unordered policy rows.
    pub(crate) fn fingerprint(&self) -> [u8; 32] {
        let mut hash = Sha256::new();
        hash.update(CONTRACT_VERSION.as_bytes());
        hash.update([0]);
        hash.update(self.subject_user_id.to_be_bytes());
        hash.update(self.version.to_be_bytes());
        hash.update([self.role.hash_tag()]);
        for permission in Permission::ALL {
            hash.update(permission.key().as_bytes());
            hash.update([0]);
            hash.update([match self.overrides[permission as usize] {
                None => 0,
                Some(Effect::Allow) => 1,
                Some(Effect::Deny) => 2,
            }]);
        }
        hash.finalize().into()
    }
}

/// Only the permission layer. Secret access still needs A02 step-up, and channel
/// writes still need their resource-specific validation. This is not an HTTP gate.
pub(crate) fn decide_platform(
    actor: ActorFacts,
    policy: &PolicySnapshot,
    expected_policy_version: i64,
    permission: Permission,
    scope: ResourceScope,
) -> PermissionDecision {
    (|| {
        actor.check_session()?;
        if !permission.accepts(scope) {
            return Err(Denial::WrongResource);
        }
        if policy.subject_user_id != actor.user_id {
            return Err(Denial::WrongSubject);
        }
        if policy.role != actor.current_role || policy.version != expected_policy_version {
            return Err(Denial::StalePolicy);
        }
        // An override cannot cross the route's minimum platform-role boundary.
        if !actor.current_role.is_admin() {
            return Err(Denial::AdminRequired);
        }
        match policy.overrides[permission as usize] {
            Some(Effect::Deny) => Err(Denial::ExplicitDeny),
            Some(Effect::Allow) => Ok(()),
            None if actor.current_role == PlatformRole::Superadmin
                || permission.admin_default() =>
            {
                Ok(())
            }
            None => Err(Denial::MissingGrant),
        }
    })()
    .into()
}

pub(crate) fn decide_company(actor: ActorFacts, scope: ResourceScope) -> PermissionDecision {
    (|| {
        actor.check_session()?;
        match scope {
            ResourceScope::CompanyProfile(id) if id > 0 && id == actor.user_id => Ok(()),
            _ => Err(Denial::WrongResource),
        }
    })()
    .into()
}

/// Reuse core_identity.developer_access.granted. No balance, discount, paid flag,
/// team role, L-level or client-supplied claim establishes this fact.
#[derive(Clone, Copy, Debug)]
pub(crate) struct DeveloperFacts {
    pub(crate) user_id: i64,
    pub(crate) granted: Option<bool>,
}
pub(crate) fn decide_developer(actor: ActorFacts, facts: DeveloperFacts) -> PermissionDecision {
    (|| {
        actor.check_session()?;
        if facts.user_id != actor.user_id {
            return Err(Denial::WrongSubject);
        }
        if actor.current_role.is_admin() {
            return Ok(());
        }
        match facts.granted {
            Some(true) => Ok(()),
            Some(false) => Err(Denial::MissingGrant),
            None => Err(Denial::DeveloperUnmapped),
        }
    })()
    .into()
}

#[derive(Clone, Copy, Debug)]
pub(crate) struct TeamFacts {
    pub(crate) id: i64,
    pub(crate) owner_user_id: i64,
    pub(crate) version: i64,
    pub(crate) active: bool,
}
#[derive(Clone, Copy, Debug)]
pub(crate) struct MemberFacts {
    pub(crate) team_id: i64,
    pub(crate) user_id: i64,
    pub(crate) role: TeamRole,
    pub(crate) version: i64,
    pub(crate) active: bool,
}
impl MemberFacts {
    fn check(self, team: TeamFacts) -> Result<(), Denial> {
        if self.team_id != team.id {
            return Err(Denial::WrongTeam);
        }
        if self.user_id <= 0
            || self.version <= 0
            || self.role == TeamRole::Owner
            || self.user_id == team.owner_user_id
        {
            return Err(Denial::InvalidFacts);
        }
        if !self.active {
            return Err(Denial::Inactive);
        }
        Ok(())
    }
}
#[derive(Clone, Copy, Debug)]
pub(crate) enum TeamAction {
    Read,
    Invite {
        recipient_user_id: i64,
        role: TeamRole,
    },
    Remove {
        target: MemberFacts,
        expected_version: i64,
    },
}
pub(crate) fn decide_team(
    actor: ActorFacts,
    team: TeamFacts,
    membership: Option<MemberFacts>,
    expected_team_version: i64,
    expected_membership_version: i64,
    scope: ResourceScope,
    action: TeamAction,
) -> PermissionDecision {
    (|| {
        actor.check_session()?;
        if team.id <= 0 || team.owner_user_id <= 0 || team.version <= 0 {
            return Err(Denial::InvalidFacts);
        }
        if scope != ResourceScope::Team(team.id) {
            return Err(Denial::WrongTeam);
        }
        if !team.active {
            return Err(Denial::Inactive);
        }
        if team.version != expected_team_version {
            return Err(Denial::StaleTeam);
        }
        let role = if actor.user_id == team.owner_user_id {
            if expected_membership_version != 0 {
                return Err(Denial::StaleMember);
            }
            if membership.is_some() {
                return Err(Denial::InvalidFacts);
            }
            TeamRole::Owner
        } else {
            let member = membership.ok_or(Denial::TeamRoleRequired)?;
            member.check(team)?;
            if member.user_id != actor.user_id {
                return Err(Denial::WrongSubject);
            }
            if member.version != expected_membership_version {
                return Err(Denial::StaleMember);
            }
            member.role
        };
        match action {
            TeamAction::Read => Ok(()),
            TeamAction::Invite {
                recipient_user_id,
                role: target_role,
            } => {
                if recipient_user_id <= 0 {
                    return Err(Denial::InvalidFacts);
                }
                if recipient_user_id == actor.user_id
                    || recipient_user_id == team.owner_user_id
                    || !role.can_manage_member(target_role)
                {
                    return Err(Denial::TeamRoleRequired);
                }
                // Recipient enabled status and current membership are still checked by invite().
                Ok(())
            }
            TeamAction::Remove {
                target,
                expected_version,
            } => {
                target.check(team)?;
                if target.version != expected_version {
                    return Err(Denial::StaleMember);
                }
                if target.user_id == actor.user_id {
                    // Both snapshots must describe the same row, not two conflicting roles.
                    if target.role != role || target.version != expected_membership_version {
                        return Err(Denial::StaleMember);
                    }
                    return Ok(());
                }
                if !role.can_manage_member(target.role) {
                    return Err(Denial::TeamRoleRequired);
                }
                Ok(())
            }
        }
    })()
    .into()
}

/// Gate for a *live* operation, not an authorization token that can be cached.
/// The transaction argument is mandatory. Hold it through the protected write.
/// `permission` and `scope` come from a literal server operation and a Core-loaded
/// resource, never a caller-selected action, host credential or model output.
pub(crate) async fn require_platform_in_transaction(
    tx: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    actor: &crate::identity::Principal,
    permission: Permission,
    scope: ResourceScope,
) -> Result<(), crate::identity::IdentityError> {
    use crate::identity::IdentityError;
    use sqlx::Row;

    // This helper cannot stand in for A02's action-bound secure confirmation.
    // Keep these sensitive live operations unavailable until that provider is
    // integrated; a policy grant alone is not a secret-view/write authorization.
    if matches!(
        permission,
        Permission::ChannelSecretView | Permission::ChannelSensitiveWrite
    ) {
        return Err(IdentityError::Forbidden);
    }
    // A02 supplies a Core-authenticated Principal. It is not Deserialize and
    // contains a private generation. Revalidate its immutable credential row in
    // THIS transaction: a previous authentication result is not a live grant.
    // Read authority from storage; do not use the public platform_level/owner.
    if actor.credential_kind != CredentialKind::Session {
        return Err(IdentityError::Forbidden);
    }
    let row = sqlx::query(
        "SELECT u.platform_role,u.auth_version,c.user_version \
         FROM core_identity.users u JOIN core_identity.credentials c ON c.user_id=u.id \
         JOIN core_identity.accounts pa ON pa.id=u.personal_account_id \
         JOIN core_identity.accounts own ON own.id=c.owner_account_id \
         WHERE u.id=$1 AND c.id=$2 AND c.kind='session' \
         AND c.owner_account_id=u.personal_account_id \
         AND u.active AND pa.active AND own.active \
         AND c.revoked_at IS NULL AND c.expires_at>clock_timestamp() \
         AND c.user_version=u.auth_version \
         AND NOT EXISTS (SELECT 1 FROM core_identity.api_key_settings ks \
             WHERE ks.credential_id=c.id AND NOT ks.enabled) \
         AND NOT EXISTS (SELECT 1 FROM core_identity.web_sessions ws \
             WHERE ws.current_credential_id=c.id AND (ws.revoked_at IS NOT NULL \
             OR ws.expires_at<=clock_timestamp() OR ws.user_version<>u.auth_version \
             OR (ws.weekly_logout AND ws.created_at<=clock_timestamp()-interval '7 days'))) \
         FOR SHARE OF u,c,pa,own",
    )
    .bind(actor.user_id)
    .bind(actor.credential_id)
    .fetch_optional(&mut **tx)
    .await?
    .ok_or(IdentityError::Unauthorized)?;
    let current_role = PlatformRole::from_storage(&row.try_get::<String, _>("platform_role")?)
        .map_err(|_| IdentityError::Storage)?;
    if !current_role.is_admin() {
        return Err(IdentityError::Forbidden);
    }
    let facts = ActorFacts {
        user_id: actor.user_id,
        credential_kind: actor.credential_kind,
        credential_auth_version: row.try_get("user_version")?,
        current_auth_version: row.try_get("auth_version")?,
        current_role,
        active: true,
    };
    // The native user SHARE lock is the authorization fence. Policy mutations
    // advance/lock that same user via guard_admin_policy before becoming visible.
    // Do NOT lock this SELECT: FOR SHARE needs UPDATE privilege on policy rows,
    // while the network role must have SELECT only. No stale in-memory grants.
    let row = sqlx::query(
        "SELECT role,version,contract_version,effects,fingerprint \
         FROM core_identity.admin_policies WHERE user_id=$1",
    )
    .bind(actor.user_id)
    .fetch_optional(&mut **tx)
    .await?
    .ok_or(IdentityError::Forbidden)?;
    let policy = PolicySnapshot::from_storage(
        actor.user_id,
        &row.try_get::<String, _>("role")?,
        row.try_get("version")?,
        &row.try_get::<String, _>("contract_version")?,
        &row.try_get::<Vec<i16>, _>("effects")?,
        &row.try_get::<Vec<u8>, _>("fingerprint")?,
    )
    .map_err(|_| IdentityError::Storage)?;
    match decide_platform(facts, &policy, policy.version(), permission, scope) {
        PermissionDecision::Allow => Ok(()),
        PermissionDecision::Deny(_) => Err(IdentityError::Forbidden),
    }
}
