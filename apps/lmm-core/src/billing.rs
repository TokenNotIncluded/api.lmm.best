//! Exact credit values and immutable request ownership. Durable wallet operations
//! live in `ledger`; forwarding still requires integrated authorization and budgets.
//! No extension may perform these transitions directly.
pub mod ledger;

use serde::{Deserialize, Serialize};

use crate::accounts::{Account, AccountKind};

pub const CREDITS_PER_USD: i64 = 500_000;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum CreditError {
    Negative,
    Overflow,
    Insufficient,
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(try_from = "i64", into = "i64")]
pub struct Credits(i64);

impl TryFrom<i64> for Credits {
    type Error = &'static str;
    fn try_from(value: i64) -> Result<Self, Self::Error> {
        if value < 0 {
            Err("credits cannot be negative")
        } else {
            Ok(Self(value))
        }
    }
}

impl From<Credits> for i64 {
    fn from(value: Credits) -> Self {
        value.0
    }
}

impl Credits {
    pub fn checked_add(self, other: Self) -> Result<Self, CreditError> {
        self.0
            .checked_add(other.0)
            .map(Self)
            .ok_or(CreditError::Overflow)
    }

    pub fn checked_sub(self, other: Self) -> Result<Self, CreditError> {
        if self.0 < other.0 {
            Err(CreditError::Insufficient)
        } else {
            Ok(Self(self.0 - other.0))
        }
    }
}

/// The actor, API-key owner and payer must be stored separately for every
/// request. Changing browser account, key policy or membership cannot rewrite
/// an already accepted charge. Settlement and refund use its original identity.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ChargeIdentity {
    actor_user_id: i64,
    key_id: i64,
    key_owner: Account,
    payer: Account,
    price_version: u64,
}

impl ChargeIdentity {
    /// The caller must additionally validate current membership, permissions,
    /// model/group restrictions and budgets in the same durable transaction.
    pub fn new(
        actor_user_id: i64,
        key_id: i64,
        key_owner: Account,
        payer: Account,
        price_version: u64,
    ) -> Result<Self, &'static str> {
        if actor_user_id <= 0
            || key_id <= 0
            || !key_owner.is_valid()
            || !payer.is_valid()
            || price_version == 0
        {
            return Err("invalid charge identity");
        }
        if key_owner.kind == AccountKind::Personal && key_owner.id != actor_user_id {
            return Err("personal key actor does not own the key");
        }
        if (key_owner.kind == AccountKind::Team && payer != key_owner)
            || (payer.kind == AccountKind::Personal && payer != key_owner)
        {
            return Err("payer is outside the key ownership boundary");
        }
        Ok(Self {
            actor_user_id,
            key_id,
            key_owner,
            payer,
            price_version,
        })
    }

    pub fn payer(&self) -> Account {
        self.payer
    }
    pub fn key_owner(&self) -> Account {
        self.key_owner
    }
    pub fn actor_user_id(&self) -> i64 {
        self.actor_user_id
    }
    pub fn key_id(&self) -> i64 {
        self.key_id
    }
    pub fn price_version(&self) -> u64 {
        self.price_version
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn money_is_exact_nonnegative_and_checked() {
        assert_eq!(CREDITS_PER_USD, 500_000);
        assert!(Credits::try_from(-1).is_err());
        assert!(serde_json::from_str::<Credits>("-1").is_err());
        assert!(serde_json::from_str::<Credits>("0.1").is_err());
        let zero = Credits::try_from(0).unwrap();
        let one = Credits::try_from(1).unwrap();
        assert_eq!(zero.checked_sub(one), Err(CreditError::Insufficient));
        assert_eq!(
            Credits::try_from(i64::MAX).unwrap().checked_add(one),
            Err(CreditError::Overflow)
        );
        assert_eq!(one.checked_sub(one), Ok(zero));
        assert_eq!(serde_json::to_string(&one).unwrap(), "1");
    }

    #[test]
    fn team_key_never_falls_back_to_its_founders_wallet() {
        let team = Account {
            kind: AccountKind::Team,
            id: 42,
        };
        let person = Account {
            kind: AccountKind::Personal,
            id: 7,
        };
        assert!(ChargeIdentity::new(7, 9, team, person, 1).is_err());
        assert!(
            ChargeIdentity::new(
                7,
                9,
                team,
                Account {
                    kind: AccountKind::Team,
                    id: 43
                },
                1
            )
            .is_err()
        );
        let charge = ChargeIdentity::new(7, 9, team, team, 1).unwrap();
        assert_eq!(charge.actor_user_id(), 7);
        assert_eq!(charge.key_owner(), team);
        assert_eq!(charge.payer(), team);
        assert_eq!(charge.key_id(), 9);
        assert_eq!(charge.price_version(), 1);
    }

    #[test]
    fn personal_key_records_authorized_team_payment_without_becoming_a_team_key() {
        let person = Account {
            kind: AccountKind::Personal,
            id: 7,
        };
        let team = Account {
            kind: AccountKind::Team,
            id: 42,
        };
        let charge = ChargeIdentity::new(7, 9, person, team, 1).unwrap();
        assert_eq!(charge.key_owner(), person);
        assert_eq!(charge.payer(), team);
        assert!(ChargeIdentity::new(8, 9, person, team, 1).is_err());
        assert!(ChargeIdentity::new(7, 9, person, team, 0).is_err());
    }
}
