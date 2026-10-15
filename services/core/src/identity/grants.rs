//! Read-only lmm public-client issuer plus the unissued Pi grant foundation.
//! PKCE uses oxide-auth (MIT OR Apache-2.0), pinned in Cargo.toml.
mod delegated;
mod issuer;
mod protocol;
mod readonly;
use super::*;
pub use delegated::OAuthSource;
pub use issuer::{AuthorizationRedirect, ConsentPage, LmmIssuer};
use oxide_auth::code_grant::extensions::Pkce;
pub use protocol::{
    AuthorizationRequest, ConsentForm, ContinueForm, LmmProfile, RevokeForm, TokenForm,
    TokenRequest, TokenWire,
};
pub use readonly::{BalanceV2, ReadError};
use sqlx::{Postgres, Transaction};

/// Canonical selector codec only; callers must still verify current consent.
pub fn decode_group_selector(value: &str) -> Result<String> {
    protocol::decode_group(&format!("group:{value}"))
}

pub const SCHEMA: &str = include_str!("../../schema/oauth_grants.sql");
const SCOPES: &[&str] = &[
    "catalog:read",
    "balance:read",
    "usage:read",
    "models:invoke",
    "mcp:bounties",
    "mcp:drawing",
    "remote:control",
];

#[derive(Clone, Copy)]
pub enum IssuerMode {
    Production,
    LoopbackDevelopment,
}
#[derive(Clone)]
pub struct GrantProfile {
    issuer: String,
    resource: String,
}
impl GrantProfile {
    pub fn new(issuer: &str, mode: IssuerMode) -> Result<Self> {
        let url = reqwest::Url::parse(issuer).map_err(|_| IdentityError::Invalid)?;
        if issuer.len() > 256
            || url.origin().ascii_serialization() != issuer
            || !url.username().is_empty()
            || url.password().is_some()
            || url.query().is_some()
            || url.fragment().is_some()
            || url.path() != "/"
            || !(url.scheme() == "https"
                || (matches!(mode, IssuerMode::LoopbackDevelopment)
                    && url.scheme() == "http"
                    && url.host_str() == Some("127.0.0.1")))
        {
            return Err(IdentityError::Invalid);
        }
        Ok(Self {
            issuer: issuer.into(),
            resource: format!("{issuer}/api/oauth2"),
        })
    }
    fn binding(&self, issuer: &str, resource: &str, client: &str) -> Result<()> {
        if issuer != self.issuer || resource != self.resource || client != "lmm-pi" {
            return Err(IdentityError::Unauthorized);
        }
        Ok(())
    }
}
fn redirect(value: &str) -> Result<()> {
    let url = reqwest::Url::parse(value).map_err(|_| IdentityError::Invalid)?;
    if value.len() > 256
        || url.scheme() != "http"
        || url.host_str() != Some("127.0.0.1")
        || url.port().is_none_or(|p| p == 0)
        || !url.username().is_empty()
        || url.password().is_some()
        || url.path() != "/oauth/lmm/callback"
        || url.query().is_some()
        || url.fragment().is_some()
        || url.as_str() != value
    {
        return Err(IdentityError::Invalid);
    }
    Ok(())
}
fn scopes(values: &[String]) -> Result<()> {
    if values.is_empty() || values.len() > SCOPES.len() {
        return Err(IdentityError::Invalid);
    }
    for (i, value) in values.iter().enumerate() {
        if !SCOPES.contains(&value.as_str()) || values[..i].contains(value) {
            return Err(IdentityError::Invalid);
        }
    }
    Ok(())
}
fn token_digest(value: &str, prefix: &str) -> Result<Vec<u8>> {
    if value.len() != prefix.len() + 64
        || !value.starts_with(prefix)
        || !value[prefix.len()..]
            .bytes()
            .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
    {
        return Err(IdentityError::Unauthorized);
    }
    digest(value)
}
fn pkce(challenge: &str, method: Option<&str>, verifier: Option<&str>) -> Result<()> {
    use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
    if challenge.len() != 43
        || URL_SAFE_NO_PAD.decode(challenge).is_err()
        || verifier.is_some_and(|v| {
            !(43..=128).contains(&v.len())
                || !v
                    .bytes()
                    .all(|b| b.is_ascii_alphanumeric() || b"-._~".contains(&b))
        })
    {
        return Err(IdentityError::Invalid);
    }
    let value = Pkce::required()
        .challenge(method.map(Into::into), Some(challenge.into()))
        .map_err(|_| IdentityError::Invalid)?;
    Pkce::required()
        .verify(value, verifier.map(Into::into))
        .map_err(|_| IdentityError::Unauthorized)
}
/// Secret-bearing request/response types deliberately do not implement Debug.
pub struct CodeExchange<'a> {
    pub code: &'a str,
    pub issuer: &'a str,
    pub resource: &'a str,
    pub client_id: &'a str,
    pub redirect_uri: &'a str,
    pub verifier: Option<&'a str>,
}
pub struct RefreshExchange<'a> {
    pub refresh: &'a str,
    pub issuer: &'a str,
    pub resource: &'a str,
    pub client_id: &'a str,
    pub scopes: Option<&'a [String]>,
}
pub struct TokenBundle {
    pub access_token: String,
    pub refresh_token: String,
    pub expires_in: i64,
    pub scopes: Vec<String>,
}
#[derive(Debug)]
pub enum RemoteSource {
    NativePersonal { credential_id: i64 },
    OAuthGrant { grant_id: String, access_id: String },
}
#[derive(Debug)]
pub struct RemoteAuthority {
    pub source: RemoteSource,
    pub user_id: i64,
    pub owner_account_id: i64,
    pub auth_version: i64,
    pub expires_at: i64,
}
#[derive(Clone)]
pub struct GrantStore {
    identity: IdentityStore,
    profile: GrantProfile,
}

// Application-schema-only catalog: stable across pg_dump/restore; no data/ACLs.
const CATALOG: &str = r#"
WITH catalog_path AS MATERIALIZED (
 SELECT pg_catalog.set_config('search_path','pg_catalog',true)
), oauth_namespace AS MATERIALIZED (
 SELECT n.oid FROM pg_namespace n CROSS JOIN catalog_path WHERE n.nspname='core_oauth'
), definitions AS (
 SELECT json_build_array('relation',c.relname,c.relkind)::text AS d
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.oid IN (SELECT oid FROM oauth_namespace)
 UNION ALL
 SELECT json_build_array('column',c.relname,a.attnum,a.attname,format_type(a.atttypid,a.atttypmod),a.attnotnull,a.attidentity,a.attgenerated,pg_get_expr(ad.adbin,ad.adrelid))::text
 FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace LEFT JOIN pg_attrdef ad ON ad.adrelid=a.attrelid AND ad.adnum=a.attnum WHERE n.oid IN (SELECT oid FROM oauth_namespace) AND a.attnum>0 AND NOT a.attisdropped
 UNION ALL
 SELECT json_build_array('constraint',k.conname,pg_get_constraintdef(k.oid,true))::text FROM pg_constraint k JOIN pg_namespace n ON n.oid=k.connamespace WHERE n.oid IN (SELECT oid FROM oauth_namespace)
 UNION ALL
 SELECT json_build_array('index',c.relname,pg_get_indexdef(i.indexrelid))::text FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.oid IN (SELECT oid FROM oauth_namespace)
 UNION ALL
 SELECT json_build_array('function',p.proname,pg_get_functiondef(p.oid))::text FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.oid IN (SELECT oid FROM oauth_namespace) AND p.prokind IN ('f','p')
 UNION ALL
 SELECT json_build_array('trigger',c.relname,t.tgname,t.tgenabled,pg_get_triggerdef(t.oid,true))::text FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.oid IN (SELECT oid FROM oauth_namespace) AND NOT t.tgisinternal
 UNION ALL
 SELECT json_build_array('internal-trigger',c.relname,k.conname,pn.nspname,p.proname,t.tgtype,t.tgenabled,t.tgdeferrable,t.tginitdeferred,t.tgattr::text,encode(t.tgargs,'hex'),pg_get_expr(t.tgqual,t.tgrelid))::text FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_proc p ON p.oid=t.tgfoid JOIN pg_namespace pn ON pn.oid=p.pronamespace LEFT JOIN pg_constraint k ON k.oid=t.tgconstraint WHERE n.oid IN (SELECT oid FROM oauth_namespace) AND t.tgisinternal
) SELECT d FROM definitions ORDER BY d COLLATE "C"
"#;
async fn catalog(c: &mut PgConnection) -> Result<Vec<u8>> {
    let definitions: Vec<String> = sqlx::query_scalar(CATALOG).fetch_all(c).await?;
    let mut hash = Sha256::new();
    for definition in definitions {
        hash.update((definition.len() as u64).to_be_bytes());
        hash.update(definition.as_bytes());
    }
    Ok(hash.finalize().to_vec())
}
pub(crate) async fn record_schema_contract(c: &mut PgConnection) -> Result<()> {
    let fingerprint = catalog(c).await?;
    sqlx::query("INSERT INTO core_oauth.contract(version,ddl_fingerprint,catalog_fingerprint) VALUES (1,$1,$2)")
        .bind(Sha256::digest(SCHEMA.as_bytes()).to_vec()).bind(fingerprint).execute(c).await?;
    Ok(())
}
async fn transaction(pool: &PgPool) -> Result<Transaction<'_, Postgres>> {
    let mut tx = pool.begin().await?;
    sqlx::query("SET LOCAL lock_timeout='1s';")
        .execute(&mut *tx)
        .await?;
    sqlx::query("SET LOCAL statement_timeout='1500ms'")
        .execute(&mut *tx)
        .await?;
    sqlx::query("SET LOCAL synchronous_commit=on")
        .execute(&mut *tx)
        .await?;
    Ok(tx)
}
async fn family(c: &mut PgConnection, id: &str, write: bool) -> Result<PgRow> {
    let lock = if write { "FOR UPDATE" } else { "FOR SHARE" };
    sqlx::query(&format!(
        "SELECT * FROM core_oauth.families WHERE id=$1 {lock}"
    ))
    .bind(id)
    .fetch_optional(c)
    .await?
    .ok_or(IdentityError::Unauthorized)
}
async fn live(
    c: &mut PgConnection,
    row: &PgRow,
    profile: &GrantProfile,
    version: i64,
) -> Result<()> {
    profile.binding(
        &row.try_get::<String, _>("issuer")?,
        &row.try_get::<String, _>("resource")?,
        &row.try_get::<String, _>("client_id")?,
    )?;
    if version != row.try_get::<i64, _>("consent_version")? {
        return Err(IdentityError::Unauthorized);
    }
    let current: bool = sqlx::query_scalar("SELECT u.active AND a.active AND a.kind='personal' AND u.personal_account_id=$2 AND u.auth_version=$3 FROM core_identity.users u JOIN core_identity.accounts a ON a.id=u.personal_account_id WHERE u.id=$1 FOR SHARE OF u,a")
        .bind(row.try_get::<i64,_>("user_id")?).bind(row.try_get::<i64,_>("owner_account_id")?)
        .bind(row.try_get::<i64,_>("user_version")?).fetch_optional(&mut *c).await?.unwrap_or(false);
    // Recheck exact TIMESTAMPTZ after all authority locks, not before an await.
    let family_live: bool = sqlx::query_scalar("SELECT revoked_at IS NULL AND expires_at>clock_timestamp() FROM core_oauth.families WHERE id=$1")
        .bind(row.try_get::<String, _>("id")?).fetch_one(c).await?;
    if !current || !family_live {
        return Err(IdentityError::Unauthorized);
    }
    scopes(&row.try_get::<Vec<String>, _>("scopes")?).map_err(|_| IdentityError::Storage)
}
async fn issue(
    c: &mut PgConnection,
    row: &PgRow,
    selected: &[String],
    ttl: i64,
) -> Result<TokenBundle> {
    let access = new_secret("lmm_at_")?;
    let refresh = new_secret("lmm_rt_")?;
    let id = row.try_get::<String, _>("id")?;
    let version = row.try_get::<i64, _>("consent_version")?;
    // Round only the response lifetime; authorization still checks the exact DB expiry.
    let expires: i64 = sqlx::query_scalar("INSERT INTO core_oauth.access_tokens(id,digest,family_id,consent_version,scopes,created_at,expires_at) SELECT $1,$2,id,$3,$4,statement_timestamp(),LEAST(expires_at,statement_timestamp()+make_interval(secs=>$5::double precision)) FROM core_oauth.families WHERE id=$6 RETURNING ceil(extract(epoch FROM expires_at-clock_timestamp()))::bigint")
        .bind(new_secret("lmm_ac_")?).bind(digest(&access)?).bind(version).bind(selected).bind(ttl).bind(&id).fetch_one(&mut *c).await?;
    if expires <= 0 {
        return Err(IdentityError::Unauthorized);
    }
    sqlx::query("INSERT INTO core_oauth.refresh_tokens(digest,family_id,consent_version,scopes,expires_at) SELECT $1,id,$2,$3,expires_at FROM core_oauth.families WHERE id=$4")
        .bind(digest(&refresh)?).bind(version).bind(selected).bind(id).execute(c).await?;
    Ok(TokenBundle {
        access_token: access,
        refresh_token: refresh,
        expires_in: expires,
        scopes: selected.to_vec(),
    })
}
impl GrantStore {
    /// Read-only verification. The caller must use separately reviewed runtime
    /// credentials; this method neither installs DDL nor grants privileges.
    pub async fn open(identity: IdentityStore, profile: GrantProfile) -> Result<Self> {
        let mut tx = transaction(&identity.pool).await?;
        IdentityStore::check_schema_connection(&mut tx).await?;
        let receipt = sqlx::query("SELECT version,ddl_fingerprint,catalog_fingerprint FROM core_oauth.contract WHERE singleton").fetch_one(&mut *tx).await?;
        if receipt.try_get::<i32, _>("version")? != 1
            || receipt.try_get::<Vec<u8>, _>("ddl_fingerprint")?
                != Sha256::digest(SCHEMA.as_bytes()).to_vec()
            || receipt.try_get::<Vec<u8>, _>("catalog_fingerprint")? != catalog(&mut tx).await?
        {
            return Err(IdentityError::Storage);
        }
        tx.commit().await?;
        Ok(Self { identity, profile })
    }
    pub async fn exchange(&self, input: CodeExchange<'_>, access_ttl: i64) -> Result<TokenBundle> {
        valid_ttl(access_ttl, 86400)?;
        self.profile
            .binding(input.issuer, input.resource, input.client_id)?;
        redirect(input.redirect_uri)?;
        let hash = token_digest(input.code, "lmm_oc_")?;
        let mut tx = transaction(&self.identity.pool).await?;
        let id: String =
            sqlx::query_scalar("SELECT family_id FROM core_oauth.codes WHERE digest=$1")
                .bind(&hash)
                .fetch_optional(&mut *tx)
                .await?
                .ok_or(IdentityError::Unauthorized)?;
        let f = family(&mut tx, &id, true).await?;
        let code = sqlx::query("SELECT consent_version,redirect_uri,pkce_challenge,consumed_at IS NOT NULL AS consumed,expires_at>clock_timestamp() AS live FROM core_oauth.codes WHERE digest=$1 FOR UPDATE").bind(&hash).fetch_one(&mut *tx).await?;
        if code.try_get::<bool, _>("consumed")?
            || !code.try_get::<bool, _>("live")?
            || code.try_get::<String, _>("redirect_uri")? != input.redirect_uri
        {
            return Err(IdentityError::Unauthorized);
        }
        pkce(
            &code.try_get::<String, _>("pkce_challenge")?,
            Some("S256"),
            input.verifier,
        )?;
        live(&mut tx, &f, &self.profile, code.try_get("consent_version")?).await?;
        sqlx::query("UPDATE core_oauth.codes SET consumed_at=clock_timestamp() WHERE digest=$1 AND consumed_at IS NULL AND expires_at>clock_timestamp() RETURNING digest")
            .bind(hash)
            .fetch_optional(&mut *tx)
            .await?
            .ok_or(IdentityError::Unauthorized)?;
        let bundle = issue(
            &mut tx,
            &f,
            &f.try_get::<Vec<String>, _>("scopes")?,
            access_ttl,
        )
        .await?;
        tx.commit().await?;
        Ok(bundle)
    }
    pub async fn refresh(
        &self,
        input: RefreshExchange<'_>,
        access_ttl: i64,
    ) -> Result<TokenBundle> {
        valid_ttl(access_ttl, 86400)?;
        self.profile
            .binding(input.issuer, input.resource, input.client_id)?;
        let hash = token_digest(input.refresh, "lmm_rt_")?;
        let mut tx = transaction(&self.identity.pool).await?;
        let id: String =
            sqlx::query_scalar("SELECT family_id FROM core_oauth.refresh_tokens WHERE digest=$1")
                .bind(&hash)
                .fetch_optional(&mut *tx)
                .await?
                .ok_or(IdentityError::Unauthorized)?;
        let f = family(&mut tx, &id, true).await?;
        // Binding must precede replay mutation, but expiry must not: correctly
        // bound consumed lineage still revokes even after its own expiry.
        self.profile.binding(
            &f.try_get::<String, _>("issuer")?,
            &f.try_get::<String, _>("resource")?,
            &f.try_get::<String, _>("client_id")?,
        )?;
        let token = sqlx::query("SELECT consent_version,scopes,consumed_at IS NOT NULL AS consumed FROM core_oauth.refresh_tokens WHERE digest=$1 FOR UPDATE").bind(&hash).fetch_one(&mut *tx).await?;
        if token.try_get::<bool, _>("consumed")? {
            sqlx::query("UPDATE core_oauth.families SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE id=$1").bind(id).execute(&mut *tx).await?;
            tx.commit().await?;
            return Err(IdentityError::Unauthorized);
        }
        live(
            &mut tx,
            &f,
            &self.profile,
            token.try_get("consent_version")?,
        )
        .await?;
        let previous: Vec<String> = token.try_get("scopes")?;
        let selected = input.scopes.unwrap_or(&previous);
        scopes(selected)?;
        let allowed: Vec<String> = f.try_get("scopes")?;
        if selected
            .iter()
            .any(|s| !previous.contains(s) || !allowed.contains(s))
        {
            return Err(IdentityError::Forbidden);
        }
        // Shrink the family's effective authority too: old access tokens cannot
        // preserve removed scopes after a successful refresh.
        sqlx::query("UPDATE core_oauth.families SET scopes=$2 WHERE id=$1")
            .bind(&id)
            .bind(selected)
            .execute(&mut *tx)
            .await?;
        sqlx::query(
            "UPDATE core_oauth.refresh_tokens SET consumed_at=clock_timestamp() WHERE digest=$1 AND consumed_at IS NULL AND expires_at>clock_timestamp() RETURNING digest",
        )
        .bind(hash)
        .fetch_optional(&mut *tx)
        .await?
        .ok_or(IdentityError::Unauthorized)?;
        let bundle = issue(&mut tx, &f, selected, access_ttl).await?;
        tx.commit().await?;
        Ok(bundle)
    }
    pub async fn revoke_access(&self, token: &str, client: &str) -> Result<()> {
        if client != "lmm-pi" {
            return Err(IdentityError::Unauthorized);
        }
        let hash = token_digest(token, "lmm_at_")?;
        let mut tx = transaction(&self.identity.pool).await?;
        let id: Option<String> =
            sqlx::query_scalar("SELECT family_id FROM core_oauth.access_tokens WHERE digest=$1")
                .bind(hash)
                .fetch_optional(&mut *tx)
                .await?;
        if let Some(id) = id {
            let f = family(&mut tx, &id, true).await?;
            self.profile.binding(
                &f.try_get::<String, _>("issuer")?,
                &f.try_get::<String, _>("resource")?,
                client,
            )?;
            sqlx::query("UPDATE core_oauth.families SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE id=$1").bind(id).execute(&mut *tx).await?;
        }
        tx.commit().await?;
        Ok(())
    }
    /// Purpose-specific read authority, never a native Principal or spend grant.
    pub async fn authorize_remote(&self, token: &str) -> Result<RemoteAuthority> {
        let hash = token_digest(token, "lmm_at_")?;
        let mut tx = transaction(&self.identity.pool).await?;
        let token = sqlx::query("SELECT id,family_id,consent_version,scopes FROM core_oauth.access_tokens WHERE digest=$1").bind(&hash).fetch_optional(&mut *tx).await?.ok_or(IdentityError::Unauthorized)?;
        let f = family(&mut tx, &token.try_get::<String, _>("family_id")?, false).await?;
        live(
            &mut tx,
            &f,
            &self.profile,
            token.try_get("consent_version")?,
        )
        .await?;
        let selected: Vec<String> = token.try_get("scopes")?;
        let allowed: Vec<String> = f.try_get("scopes")?;
        let expiry: i64 = sqlx::query_scalar("SELECT ceil(extract(epoch FROM expires_at))::bigint FROM core_oauth.access_tokens WHERE digest=$1 AND expires_at>clock_timestamp()")
            .bind(hash).fetch_optional(&mut *tx).await?.ok_or(IdentityError::Unauthorized)?;
        if !selected.iter().any(|s| s == "remote:control")
            || !allowed.iter().any(|s| s == "remote:control")
        {
            return Err(IdentityError::Forbidden);
        }
        let authority = RemoteAuthority {
            source: RemoteSource::OAuthGrant {
                grant_id: f.try_get("id")?,
                access_id: token.try_get("id")?,
            },
            user_id: f.try_get("user_id")?,
            owner_account_id: f.try_get("owner_account_id")?,
            auth_version: f.try_get("user_version")?,
            expires_at: expiry,
        };
        tx.commit().await?;
        Ok(authority)
    }
}
impl IdentityStore {
    pub async fn authorize_remote_native(&self, secret: &str) -> Result<RemoteAuthority> {
        let mut tx = transaction(&self.pool).await?;
        sqlx::query("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY")
            .execute(&mut *tx)
            .await?;
        // Native authority and expiry come from one read-only snapshot. The RPC
        // startup pool prohibits FOR SHARE; no spending authority is acquired.
        let row = sqlx::query(&format!("SELECT native.*,ceil(extract(epoch FROM c.expires_at))::bigint AS expiry FROM ({AUTH} AND c.digest=$1) native JOIN core_identity.credentials c ON c.id=native.id"))
            .bind(digest(secret)?).fetch_optional(&mut *tx).await?.ok_or(IdentityError::Unauthorized)?;
        if row.try_get::<String, _>("kind")? != "session" {
            return Err(IdentityError::Forbidden);
        }
        let expiry: i64 = row.try_get("expiry")?;
        let actor =
            principal_with_assurance_mode(&mut tx, row, mfa::AssuranceRead::Snapshot).await?;
        if actor.owner.kind != AccountKind::Personal || actor.owner.id != actor.user_id {
            return Err(IdentityError::Forbidden);
        }
        let authority = RemoteAuthority {
            source: RemoteSource::NativePersonal {
                credential_id: actor.credential_id,
            },
            user_id: actor.user_id,
            owner_account_id: actor.owner_account_id,
            auth_version: actor.user_version,
            expires_at: expiry,
        };
        tx.commit().await?;
        Ok(authority)
    }
}
#[cfg(test)]
pub(crate) mod tests;
