//! Strict offline legacy-policy conversion, not an HTTP permission endpoint.
//! Unknown source rows must remain unmapped instead of being dropped.
use crate::identity::permissions::{Denial, Effect, Permission, PlatformRole, PolicySnapshot};

/// Normalized source row after the importer has verified its original subject.
/// This type is deliberately not Deserialize and does not accept a host token.
pub(crate) struct LegacyOverride<'a> {
    pub(crate) resource: &'a str,
    pub(crate) action: &'a str,
    pub(crate) effect: &'a str,
}

pub(crate) fn legacy_overrides(
    user_id: i64,
    stored_role: &str,
    policy_version: i64,
    rows: &[LegacyOverride<'_>],
) -> Result<PolicySnapshot, Denial> {
    if rows.len() > Permission::ALL.len() { return Err(Denial::InvalidPolicy); }
    let role = PlatformRole::from_storage(stored_role)?;
    let mut overrides = Vec::with_capacity(rows.len());
    for row in rows {
        let permission = Permission::parse(row.resource, row.action)?;
        // main resolver.policyEffect interprets a missing/empty effect as allow.
        // The source loader must distinguish a real legacy blank from a missing field.
        let effect = match row.effect {
            "" | "allow" => Effect::Allow,
            "deny" => Effect::Deny,
            _ => return Err(Denial::InvalidPolicy),
        };
        overrides.push((permission, effect));
    }
    PolicySnapshot::new(user_id, role, policy_version, &overrides)
}
