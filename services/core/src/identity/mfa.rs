//! Core-owned second-factor state. A password proof never becomes a session
//! until this module's persisted policy and generation checks have succeeded.
mod crypto;
use super::*;
pub(super) use crypto::MfaKeyring;
use serde::Deserialize;
use zeroize::Zeroizing;

pub type MfaResult<T> = std::result::Result<T, MfaError>;
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum MfaError {
    Identity(IdentityError),
    CodeInvalid,
    FlowExpired,
    Locked(i64),
    Unavailable,
}
impl From<IdentityError> for MfaError {
    fn from(value: IdentityError) -> Self {
        Self::Identity(value)
    }
}
impl From<sqlx::Error> for MfaError {
    fn from(value: sqlx::Error) -> Self {
        Self::Identity(value.into())
    }
}
impl From<BrowserAuthError> for MfaError {
    fn from(value: BrowserAuthError) -> Self {
        match value {
            BrowserAuthError::Identity(e) => e.into(),
            _ => IdentityError::Unauthorized.into(),
        }
    }
}
#[derive(Serialize)]
pub struct MfaChallenge {
    pub require_2fa: bool,
    pub flow_token: String,
    pub expires_at: i64,
}
pub enum LoginOutcome {
    Session(IssuedCredential),
    Challenge(MfaChallenge),
}
pub enum BrowserLoginOutcome {
    Session(BrowserBundle),
    Challenge(MfaChallenge),
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct MfaCompletion {
    pub flow_token: String,
    pub code: String,
}
#[derive(Serialize)]
pub struct MfaStatus {
    pub enabled: bool,
    pub locked: bool,
    pub backup_codes_remaining: i64,
}
#[derive(Serialize)]
pub struct MfaSetup {
    pub secret: String,
    pub qr_code_data: String,
    pub backup_codes: Vec<String>,
}

/// The fields cannot be supplied by HTTP, Go or a caller's boolean. Instances
/// originate only in a verified database state or a successful factor mutation.
pub(super) struct Assurance {
    user: i64,
    version: i64,
    revision: i64,
    level: i16,
    method: String,
    authenticated_at: i64,
    mfa_at: Option<i64>,
}
impl Assurance {
    pub(super) fn user(&self) -> i64 {
        self.user
    }
    pub(super) fn version(&self) -> i64 {
        self.version
    }
    pub(super) fn rebind_version(mut self, version: i64) -> Self {
        self.version = version;
        self
    }
    pub(super) async fn persist(&self, c: &mut PgConnection, id: i64) -> Result<()> {
        sqlx::query("INSERT INTO core_identity.session_authentication(credential_id,user_id,user_version,factor_revision,factor_level,method,authenticated_at,mfa_verified_at) VALUES ($1,$2,$3,$4,$5,$6,to_timestamp($7),to_timestamp($8))")
            .bind(id).bind(self.user).bind(self.version).bind(self.revision).bind(self.level)
            .bind(&self.method).bind(self.authenticated_at as f64).bind(self.mfa_at.map(|v|v as f64)).execute(c).await?;
        Ok(())
    }
    pub(super) async fn validate(&self, c: &mut PgConnection, user: i64) -> Result<()> {
        let (version, required, revision) = policy(c, user, AssuranceRead::Locked).await?;
        let known = (self.level == 1 && self.method == "primary" && self.mfa_at.is_none())
            || (self.level == 2
                && matches!(self.method.as_str(), "totp" | "recovery")
                && self.mfa_at.is_some());
        if !known
            || self.user != user
            || self.version != version
            || (required
                && (self.level != 2
                    || self.revision != revision
                    || self.mfa_at.is_none()
                    || !matches!(self.method.as_str(), "totp" | "recovery")))
        {
            return Err(IdentityError::Unauthorized);
        }
        Ok(())
    }
}
async fn epoch(c: &mut PgConnection) -> Result<i64> {
    Ok(
        sqlx::query_scalar("SELECT floor(extract(epoch FROM clock_timestamp()))::bigint")
            .fetch_one(c)
            .await?,
    )
}
#[derive(Clone, Copy)]
pub(super) enum AssuranceRead {
    Snapshot,
    Locked,
}
async fn policy(c: &mut PgConnection, user: i64, mode: AssuranceRead) -> Result<(i64, bool, i64)> {
    // Snapshot mode is used only by the existing REPEATABLE READ READ ONLY
    // identity/RPC entry points. Locked mode stays on the writer connection.
    let suffix = match mode {
        AssuranceRead::Snapshot => "",
        AssuranceRead::Locked => " FOR SHARE OF u,a NOWAIT",
    };
    let version:i64=sqlx::query_scalar(&format!("SELECT u.auth_version FROM core_identity.users u JOIN core_identity.accounts a ON a.id=u.personal_account_id WHERE u.id=$1 AND u.active AND a.active{suffix}"))
        .bind(user).fetch_optional(&mut *c).await.map_err(auth::password_change_error)?.ok_or(IdentityError::Unauthorized)?;
    let suffix = match mode {
        AssuranceRead::Snapshot => "",
        AssuranceRead::Locked => " FOR SHARE NOWAIT",
    };
    let required: bool = sqlx::query_scalar(&format!(
        "SELECT second_factor_required FROM core_identity.user_profiles WHERE user_id=$1{suffix}"
    ))
    .bind(user)
    .fetch_optional(&mut *c)
    .await
    .map_err(auth::password_change_error)?
    .ok_or(IdentityError::Unauthorized)?;
    let factor = sqlx::query(&format!(
        "SELECT enabled,revision FROM core_identity.mfa_factors WHERE user_id=$1{suffix}"
    ))
    .bind(user)
    .fetch_optional(c)
    .await
    .map_err(auth::password_change_error)?;
    let enabled = factor
        .as_ref()
        .map(|r| r.try_get::<bool, _>("enabled"))
        .transpose()?
        .unwrap_or(false);
    if required != enabled {
        return Err(IdentityError::Unauthorized);
    }
    Ok((
        version,
        required,
        factor
            .as_ref()
            .map(|r| r.try_get("revision"))
            .transpose()?
            .unwrap_or(0),
    ))
}
pub(super) async fn primary(c: &mut PgConnection, user: i64) -> Result<Assurance> {
    let (version, required, revision) = policy(c, user, AssuranceRead::Locked).await?;
    if required {
        return Err(IdentityError::Unauthorized);
    }
    Ok(Assurance {
        user,
        version,
        revision,
        level: 1,
        method: "primary".into(),
        authenticated_at: epoch(c).await?,
        mfa_at: None,
    })
}
pub(super) async fn from_session(
    c: &mut PgConnection,
    id: i64,
    user: i64,
    version: i64,
) -> Result<Assurance> {
    from_session_in_mode(c, id, user, version, AssuranceRead::Locked).await
}
async fn from_session_in_mode(
    c: &mut PgConnection,
    id: i64,
    user: i64,
    version: i64,
    mode: AssuranceRead,
) -> Result<Assurance> {
    let (current, required, revision) = policy(c, user, mode).await?;
    if current != version {
        return Err(IdentityError::Unauthorized);
    }
    let row=sqlx::query("SELECT *,floor(extract(epoch FROM authenticated_at))::bigint AS at,floor(extract(epoch FROM mfa_verified_at))::bigint AS mfa_at FROM core_identity.session_authentication WHERE credential_id=$1")
        .bind(id).fetch_optional(c).await?.ok_or(IdentityError::Unauthorized)?;
    let proof = Assurance {
        user: row.try_get("user_id")?,
        version: row.try_get("user_version")?,
        revision: row.try_get("factor_revision")?,
        level: row.try_get("factor_level")?,
        method: row.try_get("method")?,
        authenticated_at: row.try_get("at")?,
        mfa_at: row.try_get("mfa_at")?,
    };
    let known = (proof.level == 1 && proof.method == "primary" && proof.mfa_at.is_none())
        || (proof.level == 2
            && matches!(proof.method.as_str(), "totp" | "recovery")
            && proof.mfa_at.is_some());
    if !known
        || proof.user != user
        || proof.version != version
        || (required && (proof.revision != revision || proof.level != 2 || proof.mfa_at.is_none()))
    {
        return Err(IdentityError::Unauthorized);
    }
    Ok(proof)
}
/// Locked financial/identity authority: caller keeps its normal authority locks.
pub(crate) async fn require_session_assurance(
    c: &mut PgConnection,
    id: i64,
    user: i64,
    version: i64,
) -> Result<()> {
    require_session_assurance_in_mode(c, id, user, version, AssuranceRead::Locked).await
}
pub(super) async fn require_session_assurance_in_mode(
    c: &mut PgConnection,
    id: i64,
    user: i64,
    version: i64,
    mode: AssuranceRead,
) -> Result<()> {
    from_session_in_mode(c, id, user, version, mode)
        .await
        .map(|_| ())
}

pub(super) struct RecoverySet {
    pub(super) codes: Vec<String>,
    pub(super) hashes: Vec<String>,
}
pub(super) struct PreparedCode {
    user: i64,
    version: i64,
    credential: i64,
    revision: i64,
    code: Zeroizing<String>,
    backup: Option<(i64, String)>,
}
fn normalized_backup(code: &str) -> Option<String> {
    let code = code.to_ascii_uppercase();
    let raw = if code.len() == 9 && code.as_bytes()[4] == b'-' {
        format!("{}{}", &code[..4], &code[5..])
    } else {
        code
    };
    if raw.len() != 8
        || !raw
            .bytes()
            .all(|b| b.is_ascii_uppercase() || b.is_ascii_digit())
    {
        return None;
    }
    Some(format!("{}-{}", &raw[..4], &raw[4..]))
}
fn keyring(store: &IdentityStore) -> MfaResult<&crypto::MfaKeyring> {
    store.mfa_keys.as_deref().ok_or(MfaError::Unavailable)
}
async fn locked_factor(c: &mut PgConnection, user: i64) -> MfaResult<PgRow> {
    sqlx::query("SELECT *,GREATEST(0,ceil(extract(epoch FROM locked_until-clock_timestamp())))::bigint AS retry FROM core_identity.mfa_factors WHERE user_id=$1 FOR UPDATE NOWAIT")
        .bind(user).fetch_optional(c).await.map_err(auth::password_change_error)?.ok_or(MfaError::Unavailable)
}
fn check_lock(row: &PgRow) -> MfaResult<()> {
    if let Some(retry) = row.try_get::<Option<i64>, _>("retry")?
        && retry > 0
    {
        return Err(MfaError::Locked(retry));
    }
    Ok(())
}
async fn failed(c: &mut PgConnection, user: i64) -> MfaResult<Assurance> {
    let attempts:i32=sqlx::query_scalar("UPDATE core_identity.mfa_factors SET failed_attempts=LEAST(failed_attempts+1,5),locked_until=CASE WHEN failed_attempts+1>=5 THEN clock_timestamp()+interval '300 seconds' ELSE NULL END WHERE user_id=$1 RETURNING failed_attempts")
        .bind(user).fetch_one(c).await?;
    if attempts >= 5 {
        Err(MfaError::Locked(300))
    } else {
        Err(MfaError::CodeInvalid)
    }
}
async fn successful(c: &mut PgConnection, user: i64) -> MfaResult<()> {
    sqlx::query(
        "UPDATE core_identity.mfa_factors SET failed_attempts=0,locked_until=NULL WHERE user_id=$1",
    )
    .bind(user)
    .execute(c)
    .await?;
    Ok(())
}
async fn make_factor_proof(c: &mut PgConnection, user: i64, method: &str) -> MfaResult<Assurance> {
    let (version, required, revision) = policy(c, user, AssuranceRead::Locked).await?;
    if !required {
        return Ok(primary(c, user).await?);
    }
    let at = epoch(c).await?;
    Ok(Assurance {
        user,
        version,
        revision,
        level: 2,
        method: method.into(),
        authenticated_at: at,
        mfa_at: Some(at),
    })
}
impl IdentityStore {
    pub fn with_mfa_keys(mut self, active: String, keys: Vec<(String, Vec<u8>)>) -> Result<Self> {
        self.mfa_keys = Some(std::sync::Arc::new(crypto::MfaKeyring::new(active, keys)?));
        Ok(self)
    }
    pub fn mfa_enabled(&self) -> bool {
        self.mfa_keys.is_some()
    }
    pub(super) async fn new_backup_codes(&self) -> MfaResult<RecoverySet> {
        use rand::distributions::{Distribution, Uniform};
        let alphabet = b"ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789";
        let draw = Uniform::from(0..alphabet.len());
        let mut codes = Vec::new();
        let mut hashes = Vec::new();
        while codes.len() < 4 {
            let raw: String = (0..8)
                .map(|_| alphabet[draw.sample(&mut OsRng)] as char)
                .collect();
            let code = format!("{}-{}", &raw[..4], &raw[4..]);
            if codes.contains(&code) {
                continue;
            }
            hashes.push(self.hash_password(code.clone()).await?);
            codes.push(code);
        }
        Ok(RecoverySet { codes, hashes })
    }
    pub(super) async fn prepare_mfa_code(
        &self,
        access: &str,
        code: &str,
        allow_backup: bool,
    ) -> MfaResult<PreparedCode> {
        let actor = {
            let mut c = self.pool.acquire().await?;
            session(&mut c, access).await?
        };
        keyring(self)?;
        let revision: i64 = sqlx::query_scalar(
            "SELECT revision FROM core_identity.mfa_factors WHERE user_id=$1 AND enabled",
        )
        .bind(actor.user_id)
        .fetch_optional(&self.pool)
        .await?
        .ok_or(MfaError::Unavailable)?;
        let mut backup = None;
        if allow_backup && let Some(normal) = normalized_backup(code) {
            let rows=sqlx::query("SELECT id,verifier FROM core_identity.mfa_backup_codes WHERE user_id=$1 AND factor_revision=$2 AND consumed_at IS NULL ORDER BY id LIMIT 4")
                .bind(actor.user_id).bind(revision).fetch_all(&self.pool).await?;
            for row in rows {
                let hash: String = row.try_get("verifier")?;
                if self.verify_password(normal.clone(), hash.clone()).await? {
                    backup = Some((row.try_get("id")?, hash));
                    break;
                }
            }
        }
        Ok(PreparedCode {
            user: actor.user_id,
            version: actor.user_version,
            credential: actor.credential_id,
            revision,
            code: Zeroizing::new(code.chars().take(33).collect()),
            backup,
        })
    }
    pub async fn mfa_status(&self, access: &str) -> MfaResult<MfaStatus> {
        let mut tx = self.pool.begin().await?;
        let actor = session(&mut tx, access).await?;
        let required: bool = sqlx::query_scalar(
            "SELECT second_factor_required FROM core_identity.user_profiles WHERE user_id=$1",
        )
        .bind(actor.user_id)
        .fetch_optional(&mut *tx)
        .await?
        .unwrap_or(false);
        let row=sqlx::query("SELECT *,locked_until>clock_timestamp() AS locked FROM core_identity.mfa_factors WHERE user_id=$1").bind(actor.user_id).fetch_optional(&mut *tx).await?;
        let enabled = row
            .as_ref()
            .is_some_and(|r| r.try_get::<bool, _>("enabled").unwrap_or(false));
        if enabled != required {
            return Err(MfaError::Unavailable);
        }
        if enabled {
            let r = row.as_ref().unwrap();
            let secret = Zeroizing::new(
                keyring(self)?
                    .decrypt(
                        actor.user_id,
                        r.try_get("revision")?,
                        "totp-factor",
                        &r.try_get::<String, _>("key_id")?,
                        &r.try_get::<Vec<u8>, _>("encrypted_secret")?,
                    )
                    .map_err(|_| MfaError::Unavailable)?,
            );
            if secret.len() != 20 {
                return Err(MfaError::Unavailable);
            }
        } else {
            keyring(self)?;
        }
        let count:i64=sqlx::query_scalar("SELECT count(*) FROM core_identity.mfa_backup_codes WHERE user_id=$1 AND factor_revision=$2 AND consumed_at IS NULL").bind(actor.user_id).bind(row.as_ref().map(|r|r.try_get::<i64,_>("revision")).transpose()?.unwrap_or(0)).fetch_one(&mut *tx).await?;
        Ok(MfaStatus {
            enabled,
            locked: row
                .as_ref()
                .map(|r| r.try_get::<Option<bool>, _>("locked"))
                .transpose()?
                .flatten()
                .unwrap_or(false),
            backup_codes_remaining: if enabled { count } else { 0 },
        })
    }
    pub(super) async fn mfa_setup(&self, access: &str, sid: Option<&str>) -> MfaResult<MfaSetup> {
        let keys = keyring(self)?;
        let recovery = self.new_backup_codes().await?;
        let secret = Zeroizing::new(crypto::create_secret()?);
        let mut tx = self.pool.begin().await?;
        let row = sqlx::query(&format!(
            "{AUTH} AND c.digest=$1 FOR UPDATE OF c,u NOWAIT FOR SHARE OF pa,own NOWAIT"
        ))
        .bind(digest(access)?)
        .fetch_optional(&mut *tx)
        .await
        .map_err(auth::password_change_error)?
        .ok_or(IdentityError::Unauthorized)?;
        let actor = principal(&mut tx, row).await?;
        if actor.credential_kind != CredentialKind::Session {
            return Err(IdentityError::Forbidden.into());
        }
        let bound:Option<String>=sqlx::query_scalar("SELECT sid FROM core_identity.web_sessions WHERE current_credential_id=$1 AND revoked_at IS NULL").bind(actor.credential_id).fetch_optional(&mut *tx).await?;
        if bound.as_deref() != sid {
            return Err(IdentityError::Forbidden.into());
        }
        let required:bool=sqlx::query_scalar("SELECT second_factor_required FROM core_identity.user_profiles WHERE user_id=$1 FOR UPDATE NOWAIT").bind(actor.user_id).fetch_one(&mut *tx).await.map_err(auth::password_change_error)?;
        if required {
            return Err(IdentityError::Conflict.into());
        }
        sqlx::query("INSERT INTO core_identity.mfa_factors(user_id) VALUES ($1) ON CONFLICT(user_id) DO NOTHING").bind(actor.user_id).execute(&mut *tx).await?;
        let factor = locked_factor(&mut tx, actor.user_id).await?;
        check_lock(&factor)?;
        if factor.try_get::<bool, _>("enabled")? {
            return Err(MfaError::Unavailable);
        }
        sqlx::query("UPDATE core_identity.mfa_enrollments SET revoked_at=clock_timestamp() WHERE user_id=$1 AND consumed_at IS NULL AND revoked_at IS NULL").bind(actor.user_id).execute(&mut *tx).await?;
        let id: i64 = sqlx::query_scalar("SELECT nextval('core_identity.mfa_enrollments_id_seq')")
            .fetch_one(&mut *tx)
            .await?;
        let encrypted = keys
            .encrypt(actor.user_id, id, "totp-enrollment", &secret)
            .map_err(|_| MfaError::Unavailable)?;
        sqlx::query("INSERT INTO core_identity.mfa_enrollments(id,user_id,user_version,source_credential_id,sid,key_id,encrypted_secret,backup_verifiers) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)")
            .bind(id).bind(actor.user_id).bind(actor.user_version).bind(actor.credential_id).bind(sid).bind(encrypted.key_id).bind(encrypted.ciphertext).bind(&recovery.hashes).execute(&mut *tx).await?;
        audit(&mut tx, &actor, "mfa.setup", json!({"enrollment_id":id})).await?;
        let response = MfaSetup {
            secret: crypto::secret_text(&secret)?,
            qr_code_data: crypto::uri(&secret, actor.user_id)?,
            backup_codes: recovery.codes,
        };
        tx.commit().await?;
        Ok(response)
    }
    pub async fn login_with_mfa(&self, request: &LoginRequest) -> MfaResult<LoginOutcome> {
        let name =
            auth::normalize_login(&request.login_name).map_err(|_| IdentityError::Unauthorized)?;
        let verified = self.verified_password(&name, &request.password).await?;
        let mut tx = self.pool.begin().await?;
        let user = self.lock_password_user(&mut tx, &name, &verified).await?;
        let required: bool = sqlx::query_scalar(
            "SELECT second_factor_required FROM core_identity.user_profiles WHERE user_id=$1",
        )
        .bind(user)
        .fetch_one(&mut *tx)
        .await?;
        let outcome = if required {
            LoginOutcome::Challenge(
                begin_challenge(self, &mut tx, user, "native_password_login", None, None).await?,
            )
        } else {
            LoginOutcome::Session(auth::issue_session(&mut tx, user, "session.login").await?)
        };
        tx.commit().await?;
        Ok(outcome)
    }
    pub async fn complete_mfa_login(&self, input: &MfaCompletion) -> MfaResult<IssuedCredential> {
        let prepared = prepare_login_code(self, input, "native_password_login", None).await?;
        let mut tx = self.pool.begin().await?;
        let result = complete_challenge(
            self,
            &mut tx,
            input,
            "native_password_login",
            None,
            &prepared,
        )
        .await;
        let proof = match result {
            Ok(p) => p,
            Err(e @ (MfaError::CodeInvalid | MfaError::Locked(_))) => {
                tx.commit().await?;
                return Err(e);
            }
            Err(e) => return Err(e),
        };
        let issued = auth::issue_session_with_assurance(
            &mut tx,
            proof.user,
            "session.mfa_login",
            new_secret("lmms_")?,
            &proof,
        )
        .await?;
        tx.commit().await?;
        Ok(issued)
    }
}

pub(super) async fn begin_challenge(
    store: &IdentityStore,
    c: &mut PgConnection,
    user: i64,
    purpose: &str,
    binding: Option<&str>,
    metadata: Option<&BrowserSessionMetadata>,
) -> MfaResult<MfaChallenge> {
    keyring(store)?;
    let version: i64 =
        sqlx::query_scalar("SELECT auth_version FROM core_identity.users WHERE id=$1")
            .bind(user)
            .fetch_one(&mut *c)
            .await?;
    let factor = locked_factor(c, user).await?;
    check_lock(&factor)?;
    if !factor.try_get::<bool, _>("enabled")? {
        return Err(MfaError::Unavailable);
    }
    let secret = Zeroizing::new(
        keyring(store)?
            .decrypt(
                user,
                factor.try_get("revision")?,
                "totp-factor",
                &factor.try_get::<String, _>("key_id")?,
                &factor.try_get::<Vec<u8>, _>("encrypted_secret")?,
            )
            .map_err(|_| MfaError::Unavailable)?,
    );
    if secret.len() != 20 {
        return Err(MfaError::Unavailable);
    }
    let token = new_secret("lmmf_")?;
    let expires:i64=sqlx::query_scalar("INSERT INTO core_identity.mfa_challenges(digest,user_id,user_version,factor_revision,purpose,binding_digest,ip,user_agent) VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING floor(extract(epoch FROM expires_at))::bigint")
        .bind(digest(&token)?).bind(user).bind(version).bind(factor.try_get::<i64,_>("revision")?).bind(purpose).bind(binding.map(digest).transpose()?).bind(metadata.map(|m|m.ip.as_str()).unwrap_or("")).bind(metadata.map(|m|m.user_agent.as_str()).unwrap_or("")).fetch_one(c).await?;
    Ok(MfaChallenge {
        require_2fa: true,
        flow_token: token,
        expires_at: expires,
    })
}
pub(super) struct LoginCode {
    user: i64,
    version: i64,
    revision: i64,
    backup: Option<(i64, String)>,
}
pub(super) async fn prepare_login_code(
    store: &IdentityStore,
    input: &MfaCompletion,
    purpose: &str,
    binding: Option<&str>,
) -> MfaResult<LoginCode> {
    if !input.flow_token.starts_with("lmmf_")
        || input.flow_token.len() != 69
        || !input.flow_token.as_bytes().get(5..).is_some_and(|v| {
            v.iter()
                .all(|b| b.is_ascii_hexdigit() && !b.is_ascii_uppercase())
        })
        || input.code.len() > 32
    {
        return Err(MfaError::FlowExpired);
    }
    keyring(store)?;
    let row=sqlx::query("SELECT user_id,user_version,factor_revision FROM core_identity.mfa_challenges WHERE digest=$1 AND purpose=$2 AND binding_digest IS NOT DISTINCT FROM $3 AND consumed_at IS NULL AND expires_at>clock_timestamp()")
        .bind(digest(&input.flow_token)?).bind(purpose).bind(binding.map(digest).transpose()?).fetch_optional(&store.pool).await?.ok_or(MfaError::FlowExpired)?;
    let user = row.try_get("user_id")?;
    let revision = row.try_get("factor_revision")?;
    let mut backup = None;
    if let Some(normal) = normalized_backup(&input.code) {
        let codes=sqlx::query("SELECT id,verifier FROM core_identity.mfa_backup_codes WHERE user_id=$1 AND factor_revision=$2 AND consumed_at IS NULL ORDER BY id LIMIT 4").bind(user).bind(revision).fetch_all(&store.pool).await?;
        for r in codes {
            let hash: String = r.try_get("verifier")?;
            if store.verify_password(normal.clone(), hash.clone()).await? {
                backup = Some((r.try_get("id")?, hash));
                break;
            }
        }
    }
    Ok(LoginCode {
        user,
        version: row.try_get("user_version")?,
        revision,
        backup,
    })
}
async fn verify_code(
    store: &IdentityStore,
    c: &mut PgConnection,
    user: i64,
    factor: &PgRow,
    code: &str,
    backup: Option<&(i64, String)>,
) -> MfaResult<Option<&'static str>> {
    check_lock(factor)?;
    let revision: i64 = factor.try_get("revision")?;
    let secret = Zeroizing::new(
        keyring(store)?
            .decrypt(
                user,
                revision,
                "totp-factor",
                &factor.try_get::<String, _>("key_id")?,
                &factor.try_get::<Vec<u8>, _>("encrypted_secret")?,
            )
            .map_err(|_| MfaError::Unavailable)?,
    );
    if secret.len() != 20 {
        return Err(MfaError::Unavailable);
    }
    if let Some((id, hash)) = backup {
        let changed=sqlx::query("UPDATE core_identity.mfa_backup_codes SET consumed_at=clock_timestamp() WHERE id=$1 AND user_id=$2 AND factor_revision=$3 AND verifier=$4 AND consumed_at IS NULL")
            .bind(id).bind(user).bind(revision).bind(hash).execute(&mut *c).await?.rows_affected();
        if changed == 1 {
            successful(c, user).await?;
            return Ok(Some("recovery"));
        }
        return Ok(None);
    }
    let matched =
        crypto::verify(&secret, code, epoch(c).await?).map_err(|_| MfaError::Unavailable)?;
    if let Some(step) = matched
        && factor
            .try_get::<Option<i64>, _>("last_used_step")?
            .is_none_or(|old| step > old)
    {
        sqlx::query("UPDATE core_identity.mfa_factors SET last_used_step=$2 WHERE user_id=$1")
            .bind(user)
            .bind(step)
            .execute(&mut *c)
            .await?;
        successful(c, user).await?;
        return Ok(Some("totp"));
    }
    Ok(None)
}
pub(super) async fn complete_challenge(
    store: &IdentityStore,
    c: &mut PgConnection,
    input: &MfaCompletion,
    purpose: &str,
    binding: Option<&str>,
    prepared: &LoginCode,
) -> MfaResult<Assurance> {
    // NOWAIT prevents inverse profile-trigger/user and browser-family cycles.
    sqlx::query("SELECT u.id FROM core_identity.users u JOIN core_identity.user_profiles p ON p.user_id=u.id WHERE u.id=$1 FOR UPDATE OF u,p NOWAIT")
        .bind(prepared.user).fetch_one(&mut *c).await.map_err(auth::password_change_error)?;
    let factor = locked_factor(c, prepared.user).await?;
    let (version, required, revision) = policy(c, prepared.user, AssuranceRead::Locked).await?;
    if !required || version != prepared.version || revision != prepared.revision {
        return Err(MfaError::FlowExpired);
    }
    let row=sqlx::query("SELECT *,expires_at>clock_timestamp() AS live,consumed_at IS NOT NULL AS consumed FROM core_identity.mfa_challenges WHERE digest=$1 FOR UPDATE")
        .bind(digest(&input.flow_token)?).fetch_optional(&mut *c).await?.ok_or(MfaError::FlowExpired)?;
    if row.try_get::<String, _>("purpose")? != purpose
        || row.try_get::<i64, _>("user_id")? != prepared.user
        || row.try_get::<i64, _>("user_version")? != version
        || row.try_get::<i64, _>("factor_revision")? != revision
        || row.try_get::<Option<Vec<u8>>, _>("binding_digest")?
            != binding.map(digest).transpose()?
        || row.try_get::<bool, _>("consumed")?
        || !row.try_get::<bool, _>("live")?
        || row.try_get::<i32, _>("attempts")? >= 5
    {
        return Err(MfaError::FlowExpired);
    }
    if let Some(method) = verify_code(
        store,
        c,
        prepared.user,
        &factor,
        &input.code,
        prepared.backup.as_ref(),
    )
    .await?
    {
        sqlx::query(
            "UPDATE core_identity.mfa_challenges SET consumed_at=clock_timestamp() WHERE digest=$1",
        )
        .bind(digest(&input.flow_token)?)
        .execute(&mut *c)
        .await?;
        make_factor_proof(c, prepared.user, method).await
    } else {
        sqlx::query("UPDATE core_identity.mfa_challenges SET attempts=attempts+1 WHERE digest=$1")
            .bind(digest(&input.flow_token)?)
            .execute(&mut *c)
            .await?;
        failed(c, prepared.user).await
    }
}

pub(super) async fn apply_enable(
    store: &IdentityStore,
    c: &mut PgConnection,
    actor: &Principal,
    sid: Option<&str>,
    code: &str,
) -> MfaResult<Assurance> {
    let factor = locked_factor(c, actor.user_id).await?;
    check_lock(&factor)?;
    if factor.try_get::<bool, _>("enabled")? {
        return Err(IdentityError::Conflict.into());
    }
    let candidate=sqlx::query("SELECT * FROM core_identity.mfa_enrollments WHERE user_id=$1 AND user_version=$2 AND sid IS NOT DISTINCT FROM $3 AND ($3::text IS NOT NULL OR source_credential_id=$4) AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at>clock_timestamp() FOR UPDATE")
        .bind(actor.user_id).bind(actor.user_version).bind(sid).bind(actor.credential_id).fetch_optional(&mut *c).await?.ok_or(MfaError::FlowExpired)?;
    let id: i64 = candidate.try_get("id")?;
    let secret = Zeroizing::new(
        keyring(store)?
            .decrypt(
                actor.user_id,
                id,
                "totp-enrollment",
                &candidate.try_get::<String, _>("key_id")?,
                &candidate.try_get::<Vec<u8>, _>("encrypted_secret")?,
            )
            .map_err(|_| MfaError::Unavailable)?,
    );
    let Some(step) =
        crypto::verify(&secret, code, epoch(c).await?).map_err(|_| MfaError::Unavailable)?
    else {
        return failed(c, actor.user_id).await;
    };
    let revision = factor.try_get::<i64, _>("revision")? + 1;
    let encrypted = keyring(store)?
        .encrypt(actor.user_id, revision, "totp-factor", &secret)
        .map_err(|_| MfaError::Unavailable)?;
    sqlx::query("UPDATE core_identity.mfa_factors SET enabled=TRUE,revision=$2,key_id=$3,encrypted_secret=$4,last_used_step=$5,failed_attempts=0,locked_until=NULL WHERE user_id=$1")
        .bind(actor.user_id).bind(revision).bind(encrypted.key_id).bind(encrypted.ciphertext).bind(step).execute(&mut *c).await?;
    for hash in candidate.try_get::<Vec<String>, _>("backup_verifiers")? {
        sqlx::query("INSERT INTO core_identity.mfa_backup_codes(user_id,factor_revision,verifier) VALUES ($1,$2,$3)").bind(actor.user_id).bind(revision).bind(hash).execute(&mut *c).await?;
    }
    sqlx::query(
        "UPDATE core_identity.mfa_enrollments SET consumed_at=clock_timestamp() WHERE id=$1",
    )
    .bind(id)
    .execute(&mut *c)
    .await?;
    sqlx::query(
        "UPDATE core_identity.user_profiles SET second_factor_required=TRUE WHERE user_id=$1",
    )
    .bind(actor.user_id)
    .execute(&mut *c)
    .await?;
    make_factor_proof(c, actor.user_id, "totp").await
}
async fn checked_management(
    store: &IdentityStore,
    c: &mut PgConnection,
    actor: &Principal,
    prepared: &PreparedCode,
) -> MfaResult<(PgRow, &'static str)> {
    if actor.user_id != prepared.user
        || actor.user_version != prepared.version
        || actor.credential_id != prepared.credential
    {
        return Err(IdentityError::Unauthorized.into());
    }
    let factor = locked_factor(c, actor.user_id).await?;
    if !factor.try_get::<bool, _>("enabled")?
        || factor.try_get::<i64, _>("revision")? != prepared.revision
    {
        return Err(MfaError::FlowExpired);
    }
    match verify_code(
        store,
        c,
        actor.user_id,
        &factor,
        &prepared.code,
        prepared.backup.as_ref(),
    )
    .await?
    {
        Some(method) => Ok((factor, method)),
        None => match failed(c, actor.user_id).await {
            Err(e) => Err(e),
            Ok(_) => unreachable!(),
        },
    }
}
pub(super) async fn apply_disable(
    store: &IdentityStore,
    c: &mut PgConnection,
    actor: &Principal,
    prepared: &PreparedCode,
) -> MfaResult<Assurance> {
    let (factor, _) = checked_management(store, c, actor, prepared).await?;
    sqlx::query("UPDATE core_identity.mfa_backup_codes SET consumed_at=COALESCE(consumed_at,clock_timestamp()) WHERE user_id=$1").bind(actor.user_id).execute(&mut *c).await?;
    sqlx::query("UPDATE core_identity.mfa_factors SET enabled=FALSE,revision=$2,key_id=NULL,encrypted_secret=NULL,last_used_step=NULL WHERE user_id=$1").bind(actor.user_id).bind(factor.try_get::<i64,_>("revision")?+1).execute(&mut *c).await?;
    sqlx::query(
        "UPDATE core_identity.user_profiles SET second_factor_required=FALSE WHERE user_id=$1",
    )
    .bind(actor.user_id)
    .execute(&mut *c)
    .await?;
    Ok(primary(c, actor.user_id).await?)
}
pub(super) async fn apply_regenerate(
    store: &IdentityStore,
    c: &mut PgConnection,
    actor: &Principal,
    prepared: &PreparedCode,
    recovery: &RecoverySet,
) -> MfaResult<Assurance> {
    let (factor, method) = checked_management(store, c, actor, prepared).await?;
    if method != "totp" {
        return Err(MfaError::CodeInvalid);
    }
    let revision = factor.try_get::<i64, _>("revision")?;
    let secret = Zeroizing::new(
        keyring(store)?
            .decrypt(
                actor.user_id,
                revision,
                "totp-factor",
                &factor.try_get::<String, _>("key_id")?,
                &factor.try_get::<Vec<u8>, _>("encrypted_secret")?,
            )
            .map_err(|_| MfaError::Unavailable)?,
    );
    let next = revision + 1;
    let encrypted = keyring(store)?
        .encrypt(actor.user_id, next, "totp-factor", &secret)
        .map_err(|_| MfaError::Unavailable)?;
    sqlx::query("UPDATE core_identity.mfa_backup_codes SET consumed_at=COALESCE(consumed_at,clock_timestamp()) WHERE user_id=$1").bind(actor.user_id).execute(&mut *c).await?;
    for hash in &recovery.hashes {
        sqlx::query("INSERT INTO core_identity.mfa_backup_codes(user_id,factor_revision,verifier) VALUES ($1,$2,$3)").bind(actor.user_id).bind(next).bind(hash).execute(&mut *c).await?;
    }
    sqlx::query("UPDATE core_identity.mfa_factors SET revision=$2,key_id=$3,encrypted_secret=$4 WHERE user_id=$1").bind(actor.user_id).bind(next).bind(encrypted.key_id).bind(encrypted.ciphertext).execute(&mut *c).await?;
    sqlx::query("UPDATE core_identity.users SET auth_version=auth_version+1 WHERE id=$1")
        .bind(actor.user_id)
        .execute(&mut *c)
        .await?;
    make_factor_proof(c, actor.user_id, "totp").await
}
