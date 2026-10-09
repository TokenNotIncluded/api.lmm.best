use std::collections::HashSet;

use serde::{Deserialize, Serialize};

use crate::accounts::{Account, AccountKind};

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum BillingPreference {
    SubscriptionFirst,
    WalletFirst,
    SubscriptionOnly,
    WalletOnly,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub struct FundingPolicy {
    pub version: u8,
    pub account_order: Option<Vec<Account>>,
    pub personal_billing_preference: Option<BillingPreference>,
}

impl FundingPolicy {
    /// New personal keys use only their own wallet unless explicitly changed.
    pub fn new_personal_key() -> Self {
        Self {
            version: 1,
            account_order: None,
            personal_billing_preference: Some(BillingPreference::WalletOnly),
        }
    }

    /// Old personal keys keep their existing subscription preference. Team keys
    /// always inherit the team's preference; neither inherits a member's wallet.
    pub fn inherit_account_settings() -> Self {
        Self {
            version: 1,
            account_order: None,
            personal_billing_preference: None,
        }
    }

    pub fn resolve(
        &self,
        owner: Account,
        team_grants: &HashSet<i64>,
    ) -> Result<Vec<Account>, FundingError> {
        if self.version != 1
            || (owner.kind == AccountKind::Team && self.personal_billing_preference.is_some())
        {
            return Err(FundingError::InvalidOrder);
        }
        resolve_order(owner, self.account_order.as_deref(), team_grants)
    }

    /// Call only for a payer returned by resolve(). Team preferences are read
    /// from the team, never overridden by the personal key's preference.
    pub fn preference_for(&self, payer: Account, stored: BillingPreference) -> BillingPreference {
        if payer.kind == AccountKind::Personal {
            self.personal_billing_preference.unwrap_or(stored)
        } else {
            stored
        }
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum FundingError {
    InvalidOwner,
    InvalidOrder,
    NotAuthorized,
}

/// Validate the entire order before any reservation. Grants MUST come from
/// trusted membership/spending checks, not from request JSON or platform role.
/// Recheck them inside the eventual balance/budget reservation transaction.
/// This pure rule neither grants authority nor reserves money.
pub fn resolve_order(
    owner: Account,
    configured: Option<&[Account]>,
    team_grants: &HashSet<i64>,
) -> Result<Vec<Account>, FundingError> {
    if !owner.is_valid() {
        return Err(FundingError::InvalidOwner);
    }
    let default = [owner];
    let order = configured.unwrap_or(&default);
    if order.is_empty() || (owner.kind == AccountKind::Team && order != default) {
        return Err(FundingError::InvalidOrder);
    }
    let mut seen = HashSet::with_capacity(order.len());
    for account in order {
        if !account.is_valid() || !seen.insert(*account) {
            return Err(FundingError::InvalidOrder);
        }
        if (account.kind == AccountKind::Personal && *account != owner)
            || (account.kind == AccountKind::Team && !team_grants.contains(&account.id))
        {
            return Err(FundingError::NotAuthorized);
        }
    }
    Ok(order.to_vec())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[derive(Deserialize)]
    struct Case {
        name: String,
        owner: Account,
        configured: Option<Vec<Account>>,
        grants: Vec<i64>,
        expected: Option<Vec<Account>>,
        error: Option<String>,
    }

    #[test]
    fn shared_go_rust_funding_contract() {
        let cases: Vec<Case> = serde_json::from_str(include_str!(concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/../../contracts/core/v1/funding-cases.json"
        )))
        .unwrap();
        assert!(cases.len() >= 20);
        for case in cases {
            let result = resolve_order(
                case.owner,
                case.configured.as_deref(),
                &case.grants.into_iter().collect(),
            );
            match case.error.as_deref() {
                None => assert_eq!(result, Ok(case.expected.unwrap()), "{}", case.name),
                Some(name) => {
                    let expected = match name {
                        "invalid_owner" => FundingError::InvalidOwner,
                        "invalid_order" => FundingError::InvalidOrder,
                        "not_authorized" => FundingError::NotAuthorized,
                        _ => panic!("unknown fixture error {name}"),
                    };
                    assert_eq!(result, Err(expected), "{}", case.name);
                }
            }
        }
    }

    #[test]
    fn new_and_imported_personal_keys_keep_different_defaults() {
        let personal = Account {
            kind: AccountKind::Personal,
            id: 1,
        };
        let old = FundingPolicy::inherit_account_settings();
        let new = FundingPolicy::new_personal_key();
        assert_eq!(
            new.preference_for(personal, BillingPreference::SubscriptionFirst),
            BillingPreference::WalletOnly
        );
        assert_eq!(
            old.preference_for(personal, BillingPreference::SubscriptionFirst),
            BillingPreference::SubscriptionFirst
        );
        assert_eq!(
            new.resolve(personal, &HashSet::new()),
            old.resolve(personal, &HashSet::new())
        );
    }

    #[test]
    fn personal_policy_cannot_override_team_subscription_settings() {
        let team = Account {
            kind: AccountKind::Team,
            id: 9,
        };
        let policy = FundingPolicy::new_personal_key();
        assert_eq!(
            policy.preference_for(team, BillingPreference::SubscriptionOnly),
            BillingPreference::SubscriptionOnly
        );
        assert_eq!(
            policy.resolve(team, &HashSet::from([9])),
            Err(FundingError::InvalidOrder)
        );
    }

    #[test]
    fn unsupported_policy_versions_and_preferences_are_rejected() {
        let mut policy = FundingPolicy::new_personal_key();
        policy.version = 2;
        assert_eq!(
            policy.resolve(
                Account {
                    kind: AccountKind::Personal,
                    id: 1
                },
                &HashSet::new()
            ),
            Err(FundingError::InvalidOrder)
        );
        assert!(serde_json::from_str::<BillingPreference>(r#""unknown""#).is_err());
    }
}
