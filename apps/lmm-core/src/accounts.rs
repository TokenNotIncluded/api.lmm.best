use serde::{Deserialize, Serialize};

#[derive(Clone, Copy, Debug, Deserialize, Eq, Hash, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum AccountKind {
    Personal,
    Team,
}

/// Stable resource owner; a team is never represented by its founder's user ID.
#[derive(Clone, Copy, Debug, Deserialize, Eq, Hash, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Account {
    pub kind: AccountKind,
    pub id: i64,
}

impl Account {
    pub fn is_valid(self) -> bool {
        self.id > 0
    }
}

/// Deliberately distinct from platform L0-L6. Platform administration is not
/// a membership or a spending grant. Owner transfer needs a separate operation.
#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum TeamRole {
    Owner,
    Admin,
    Member,
}

impl TeamRole {
    pub fn can_manage_member(self, target: Self) -> bool {
        matches!(
            (self, target),
            (Self::Owner, Self::Admin | Self::Member) | (Self::Admin, Self::Member)
        )
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn personal_and_team_ids_do_not_alias() {
        assert_ne!(
            Account {
                kind: AccountKind::Personal,
                id: 7
            },
            Account {
                kind: AccountKind::Team,
                id: 7
            }
        );
    }

    #[test]
    fn membership_management_does_not_grant_platform_authority() {
        let roles = [TeamRole::Owner, TeamRole::Admin, TeamRole::Member];
        let expected = [
            [false, true, true],
            [false, false, true],
            [false, false, false],
        ];
        for (i, actor) in roles.into_iter().enumerate() {
            for (j, target) in roles.into_iter().enumerate() {
                assert_eq!(actor.can_manage_member(target), expected[i][j]);
            }
        }
    }

    #[test]
    fn unknown_account_kinds_and_extra_authority_fields_are_rejected() {
        for json in [
            r#"{"kind":"root","id":7}"#,
            r#"{"kind":"team","id":7,"can_spend":true}"#,
        ] {
            assert!(serde_json::from_str::<Account>(json).is_err());
        }
    }
}
