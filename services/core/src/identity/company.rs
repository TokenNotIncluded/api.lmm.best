//! Ported contract/validation from api.lmm.best main 6efcb6fb, model/
//! company_billing_profile.go (2026 LIghtJUNction, AGPL-3.0-or-later).
//! Invoice identities are never included in audits or error messages.
use super::*;
use serde::Deserialize;
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct CompanyBillingProfileInput {
    pub country: String,
    pub is_business: bool,
    pub postcode: String,
    pub state: String,
    pub business_name: String,
    pub tax_id: String,
    pub use_for_invoices: bool,
}
impl CompanyBillingProfileInput {
    pub fn normalize(&mut self) {
        self.country = self.country.trim().to_ascii_uppercase();
        self.postcode = self.postcode.trim().to_owned();
        self.state = self.state.trim().to_owned();
        self.business_name = self.business_name.trim().to_owned();
        self.tax_id = self.tax_id.trim().to_owned();
    }
    pub fn invalid_field(&self) -> Option<&'static str> {
        const COUNTRIES: &str = "AD AE AF AG AI AL AM AO AQ AR AS AT AU AW AX AZ BA BB BD BE BF BG BH BI BJ BL BM BN BO BQ BR BS BT BV BW BY BZ CA CC CD CF CG CH CI CK CL CM CN CO CR CU CV CW CX CY CZ DE DJ DK DM DO DZ EC EE EG EH ER ES ET FI FJ FK FM FO FR GA GB GD GE GF GG GH GI GL GM GN GP GQ GR GS GT GU GW GY HK HM HN HR HT HU ID IE IL IM IN IO IQ IR IS IT JE JM JO JP KE KG KH KI KM KN KP KR KW KY KZ LA LB LC LI LK LR LS LT LU LV LY MA MC MD ME MF MG MH MK ML MM MN MO MP MQ MR MS MT MU MV MW MX MY MZ NA NC NE NF NG NI NL NO NP NR NU NZ OM PA PE PF PG PH PK PL PM PN PR PS PT PW PY QA RE RO RS RU RW SA SB SC SD SE SG SH SI SJ SK SL SM SN SO SR SS ST SV SX SY SZ TC TD TF TG TH TJ TK TL TM TN TO TR TT TV TW TZ UA UG UM US UY UZ VA VC VE VG VI VN VU WF WS YE YT ZA ZM ZW";
        if !COUNTRIES.split_whitespace().any(|c| c == self.country) {
            return Some("country");
        }
        for (name, value, max) in [
            ("postcode", &self.postcode, 32),
            ("state", &self.state, 128),
            ("businessName", &self.business_name, 255),
            ("taxId", &self.tax_id, 64),
        ] {
            if value.chars().count() > max || value.chars().any(char::is_control) {
                return Some(name);
            }
        }
        None
    }
}
fn project(row: PgRow) -> Result<Value> {
    Ok(
        json!({"country":row.try_get::<String,_>("country")?,"isBusiness":row.try_get::<bool,_>("is_business")?,
        "postcode":row.try_get::<String,_>("postcode")?,"state":row.try_get::<String,_>("state")?,
        "businessName":row.try_get::<String,_>("business_name")?,"taxId":row.try_get::<String,_>("tax_id")?,
        "useForInvoices":row.try_get::<bool,_>("use_for_invoices")?,
        "createdAt":row.try_get::<i64,_>("created_at")?,"updatedAt":row.try_get::<i64,_>("updated_at")?}),
    )
}
impl IdentityStore {
    pub async fn company_billing_profile(&self, secret: &str) -> Result<Value> {
        let mut tx = self.pool.begin().await?;
        let actor = session(&mut tx, secret).await?;
        let row =
            sqlx::query("SELECT * FROM core_identity.company_billing_profiles WHERE user_id=$1")
                .bind(actor.user_id)
                .fetch_optional(&mut *tx)
                .await?;
        let result = row.map(project).transpose()?.unwrap_or(Value::Null);
        tx.commit().await?;
        Ok(result)
    }
    pub async fn save_company_billing_profile(
        &self,
        secret: &str,
        mut input: CompanyBillingProfileInput,
    ) -> Result<Value> {
        input.normalize();
        if input.invalid_field().is_some() {
            return Err(IdentityError::Invalid);
        }
        let mut tx = self.pool.begin().await?;
        let actor = session(&mut tx, secret).await?;
        let row=sqlx::query("INSERT INTO core_identity.company_billing_profiles AS p(user_id,country,is_business,postcode,state,business_name,tax_id,use_for_invoices,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,extract(epoch FROM clock_timestamp())::bigint,extract(epoch FROM clock_timestamp())::bigint) ON CONFLICT(user_id) DO UPDATE SET country=EXCLUDED.country,is_business=EXCLUDED.is_business,postcode=EXCLUDED.postcode,state=EXCLUDED.state,business_name=EXCLUDED.business_name,tax_id=EXCLUDED.tax_id,use_for_invoices=EXCLUDED.use_for_invoices,updated_at=GREATEST(p.updated_at,EXCLUDED.updated_at) RETURNING *")
            .bind(actor.user_id).bind(input.country).bind(input.is_business).bind(input.postcode).bind(input.state).bind(input.business_name).bind(input.tax_id).bind(input.use_for_invoices).fetch_one(&mut *tx).await?;
        audit(
            &mut tx,
            &actor,
            "dashboard.company_profile.update",
            json!({"user_id":actor.user_id}),
        )
        .await?;
        let result = project(row)?;
        tx.commit().await?;
        Ok(result)
    }
}
