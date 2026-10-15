//! Adapter of the existing exact Core Web wallet reader, never a new formula.
use super::*;
#[derive(Serialize)]
#[serde(tag = "status", rename_all = "snake_case")]
pub enum WalletProjection {
    Available {
        unit: &'static str,
        available_units: String,
        wallet_units: String,
        reserved_units: String,
        debt_units: String,
        money_basis: &'static str,
        money_available: bool,
        currency: Option<&'static str>,
        provenance: WalletProvenance,
    },
    Unavailable {
        code: &'static str,
    },
}
#[derive(Serialize)]
pub struct WalletProvenance {
    reader: &'static str,
    user_id: String,
    personal_account_id: String,
}
pub(super) async fn read(
    c: &mut PgConnection,
    user: i64,
    account: i64,
) -> ReadResult<WalletProjection> {
    // Subject/account come only from this module's authorized selected row.
    // Existing self reader intentionally keeps inactive targets unavailable.
    let bound:Option<bool>=sqlx::query_scalar("SELECT u.active AND a.active FROM core_identity.users u JOIN core_identity.accounts a ON a.id=u.personal_account_id WHERE u.id=$1 AND u.personal_account_id=$2 AND a.kind='personal'").bind(user).bind(account).fetch_optional(&mut *c).await?;
    if bound != Some(true) {
        return Ok(WalletProjection::Unavailable {
            code: "WALLET_SUBJECT_UNAVAILABLE",
        });
    }
    match crate::financial_profile::read_wallet(c, user).await {
        Ok(value) => Ok(WalletProjection::Available {
            unit: value.wallet_unit,
            available_units: value.quota.to_string(),
            wallet_units: value.wallet_spendable_quota.to_string(),
            reserved_units: value.wallet_reserved_quota.to_string(),
            // Nonnegative debt magnitude returned by the existing Web reader.
            // The canonical ledger's stored signed debt must not be re-negated.
            debt_units: value.wallet_debt.to_string(),
            money_basis: value.wallet_money_basis,
            money_available: value.wallet_money_available,
            currency: None,
            provenance: WalletProvenance {
                reader: "financial_profile.read_wallet.v1",
                user_id: user.to_string(),
                personal_account_id: account.to_string(),
            },
        }),
        Err(crate::financial_profile::ProfileError::Unavailable) => {
            Ok(WalletProjection::Unavailable {
                code: "WALLET_PROJECTION_UNAVAILABLE",
            })
        }
        Err(
            crate::financial_profile::ProfileError::Invalid
            | crate::financial_profile::ProfileError::Storage,
        ) => Err(AdminUsersError::Unavailable),
    }
}
