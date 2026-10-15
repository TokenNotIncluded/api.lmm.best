//! Durable `lmm` issuer, isolated from the unissued Pi fixture profile.
use super::protocol::{self, AuthorizationRequest, LmmProfile, TokenRequest, TokenWire};
use super::*;
use std::collections::BTreeSet;

#[derive(Clone)]
pub struct LmmIssuer {
    pub(super) identity: IdentityStore,
    pub(super) profile: LmmProfile,
    access_ttl: i64,
    family_ttl: i64,
    pub(super) catalog_surface: Option<std::sync::Arc<crate::model_http::CatalogSurface>>,
}
/// Server-rendered ceremony material only. Never Debug or a login credential.
pub struct ConsentPage {
    pub binding: String,
    pub csrf: String,
    pub expires_at: i64,
    pub account: Option<String>,
    pub groups: Vec<String>,
}
pub struct AuthorizationRedirect {
    pub uri: String,
}

impl LmmIssuer {
    pub async fn open(
        identity: IdentityStore,
        profile: LmmProfile,
        access_ttl: i64,
        family_ttl: i64,
    ) -> Result<Self> {
        valid_ttl(access_ttl, 86400)?;
        valid_ttl(family_ttl, 31536000)?;
        let mut tx = transaction(&identity.pool).await?;
        IdentityStore::check_schema_connection(&mut tx).await?;
        let r=sqlx::query("SELECT version,ddl_fingerprint,catalog_fingerprint FROM core_oauth.contract WHERE singleton")
            .fetch_one(&mut *tx).await?;
        if r.try_get::<i32, _>("version")? != 1
            || r.try_get::<Vec<u8>, _>("ddl_fingerprint")?
                != Sha256::digest(SCHEMA.as_bytes()).to_vec()
            || r.try_get::<Vec<u8>, _>("catalog_fingerprint")? != catalog(&mut tx).await?
        {
            return Err(IdentityError::Storage);
        }
        // Require explicit installation of the new issuer relations. Startup
        // only checks; it never creates tables or repairs stored fingerprints.
        sqlx::query("SELECT binding_digest FROM core_oauth.authorization_requests LIMIT 0")
            .execute(&mut *tx)
            .await?;
        sqlx::query("SELECT digest FROM core_oauth.authorization_codes LIMIT 0")
            .execute(&mut *tx)
            .await?;
        tx.commit().await?;
        Ok(Self {
            identity,
            profile,
            access_ttl,
            family_ttl,
            catalog_surface: None,
        })
    }
    /// Only server startup can obtain the opaque validated gateway surface.
    pub fn with_catalog_surface(
        mut self,
        surface: std::sync::Arc<crate::model_http::CatalogSurface>,
    ) -> Self {
        self.catalog_surface = Some(surface);
        self
    }
    pub(crate) async fn rate_limit(&self, peer: &str) -> Result<bool> {
        self.identity
            .auth_rate_limit("oauth2_native_peer", peer, 60)
            .await
    }
    pub fn profile(&self) -> &LmmProfile {
        &self.profile
    }
    pub async fn begin(&self, request: AuthorizationRequest) -> Result<ConsentPage> {
        let request = self.profile.validate_authorization(request)?;
        let mut tx = transaction(&self.identity.pool).await?;
        let page = self.pending(&mut tx, &request, None, &[], None).await?;
        tx.commit().await?;
        Ok(page)
    }
    async fn pending(
        &self,
        c: &mut PgConnection,
        request: &AuthorizationRequest,
        identity: Option<&browser::grants::OAuthBrowserIdentity>,
        groups: &[String],
        predecessor: Option<&str>,
    ) -> Result<ConsentPage> {
        let binding = protocol::secret("")?;
        let csrf = protocol::secret("")?;
        let mut granted = Vec::new();
        if identity.is_some() {
            granted = vec!["catalog:read".into(), "balance:read".into()];
            for group in groups {
                granted.push(protocol::group_scope(group)?);
            }
        }
        let expires=sqlx::query_scalar("INSERT INTO core_oauth.authorization_requests(binding_digest,csrf_digest,client_id,issuer,resource,redirect_uri,state,pkce_challenge,requested_scopes,user_id,owner_account_id,user_version,sid,granted_scopes) SELECT $1,$2,'lmm',$3,$4,$5,$6,$7,ARRAY['catalog:read','balance:read']::text[],$8,$9,$10,$11,$12 WHERE $13::bytea IS NULL OR EXISTS(SELECT 1 FROM core_oauth.authorization_requests WHERE binding_digest=$13 AND consumed_at IS NOT NULL AND expires_at>clock_timestamp()) RETURNING floor(extract(epoch FROM expires_at))::bigint")
            .bind(digest(&binding)?).bind(digest(&csrf)?).bind(self.profile.issuer()).bind(self.profile.resource())
            .bind(&request.redirect_uri).bind(&request.state).bind(&request.code_challenge)
            .bind(identity.map(|v|v.user)).bind(identity.map(|v|v.account)).bind(identity.map(|v|v.version))
            .bind(identity.map(|v|v.sid.as_str())).bind(granted)
            .bind(predecessor.map(digest).transpose()?).fetch_optional(&mut *c).await?
            .ok_or(IdentityError::Unauthorized)?;
        let account = if let Some(identity) = identity {
            Some(
                sqlx::query_scalar(
                    "SELECT username FROM core_identity.user_profiles WHERE user_id=$1",
                )
                .bind(identity.user)
                .fetch_one(c)
                .await?,
            )
        } else {
            None
        };
        Ok(ConsentPage {
            binding,
            csrf,
            expires_at: expires,
            account,
            groups: groups.to_vec(),
        })
    }
    async fn consume(&self, c: &mut PgConnection, binding: &str, csrf: &str) -> Result<PgRow> {
        protocol::canonical(binding, "")?;
        protocol::canonical(csrf, "")?;
        let row=sqlx::query("UPDATE core_oauth.authorization_requests SET consumed_at=clock_timestamp() WHERE binding_digest=$1 AND csrf_digest=$2 AND consumed_at IS NULL AND expires_at>clock_timestamp() RETURNING *")
            .bind(digest(binding)?).bind(digest(csrf)?).fetch_optional(c).await?.ok_or(IdentityError::Unauthorized)?;
        self.profile.binding(
            &row.try_get::<String, _>("client_id")?,
            &row.try_get::<String, _>("resource")?,
        )?;
        if row.try_get::<String, _>("issuer")? != self.profile.issuer() {
            return Err(IdentityError::Unauthorized);
        }
        Ok(row)
    }
    fn request(row: &PgRow) -> Result<AuthorizationRequest> {
        Ok(AuthorizationRequest {
            response_type: "code".into(),
            client_id: row.try_get("client_id")?,
            redirect_uri: row.try_get("redirect_uri")?,
            scope: protocol::REQUESTED_SCOPE.into(),
            resource: row.try_get("resource")?,
            code_challenge: row.try_get("pkce_challenge")?,
            code_challenge_method: "S256".into(),
            state: row.try_get("state")?,
        })
    }
    pub async fn continue_authorization(
        &self,
        binding: &str,
        csrf: &str,
        cookie: Option<&str>,
    ) -> Result<ConsentPage> {
        let mut tx = transaction(&self.identity.pool).await?;
        let row = self.consume(&mut tx, binding, csrf).await?;
        if row.try_get::<Option<i64>, _>("user_id")?.is_some() {
            tx.commit().await?;
            return Err(IdentityError::Unauthorized);
        }
        let request = self.profile.validate_authorization(Self::request(&row)?)?;
        let identity = match cookie {
            Some(cookie) => match BrowserAuth::oauth_identity_in_transaction(&mut tx, cookie).await
            {
                Ok(identity) => Some(identity),
                Err(IdentityError::Unauthorized | IdentityError::Forbidden) => None,
                Err(error) => return Err(error),
            },
            None => None,
        };
        let groups = if let Some(identity) = &identity {
            match self
                .current_groups(&mut tx, identity.user, identity.account, identity.version)
                .await
            {
                Ok(groups) => groups,
                Err(error @ (IdentityError::Unauthorized | IdentityError::Forbidden)) => {
                    tx.commit().await?;
                    return Err(error);
                }
                Err(error) => return Err(error),
            }
        } else {
            Vec::new()
        };
        // The original flow may expire while the browser family is locked.
        // The final INSERT, rather than a prior bool, decides whether renewal
        // is still allowed. Consume remains durable on this functional refusal.
        let page = match self
            .pending(&mut tx, &request, identity.as_ref(), &groups, Some(binding))
            .await
        {
            Ok(page) => page,
            Err(error @ IdentityError::Unauthorized) => {
                tx.commit().await?;
                return Err(error);
            }
            Err(error) => return Err(error),
        };
        tx.commit().await?;
        Ok(page)
    }
    pub async fn consent(
        &self,
        binding: &str,
        csrf: &str,
        cookie: Option<&str>,
        allow: bool,
    ) -> Result<AuthorizationRedirect> {
        let mut tx = transaction(&self.identity.pool).await?;
        let row = self.consume(&mut tx, binding, csrf).await?;
        let Some(cookie) = cookie else {
            tx.commit().await?;
            return Err(IdentityError::Unauthorized);
        };
        let identity = match BrowserAuth::oauth_identity_in_transaction(&mut tx, cookie).await {
            Ok(identity) => identity,
            Err(error @ (IdentityError::Unauthorized | IdentityError::Forbidden)) => {
                tx.commit().await?;
                return Err(error);
            }
            Err(error) => return Err(error),
        };
        if row.try_get::<Option<i64>, _>("user_id")? != Some(identity.user)
            || row.try_get::<Option<i64>, _>("owner_account_id")? != Some(identity.account)
            || row.try_get::<Option<i64>, _>("user_version")? != Some(identity.version)
            || row.try_get::<Option<String>, _>("sid")?.as_deref() != Some(identity.sid.as_str())
        {
            tx.commit().await?;
            return Err(IdentityError::Unauthorized);
        }
        let current = match self
            .current_groups(&mut tx, identity.user, identity.account, identity.version)
            .await
        {
            Ok(groups) => groups,
            Err(error @ (IdentityError::Unauthorized | IdentityError::Forbidden)) => {
                tx.commit().await?;
                return Err(error);
            }
            Err(error) => return Err(error),
        };
        let scopes: Vec<String> = row.try_get("granted_scopes")?;
        let groups = scope_groups(&scopes)?;
        if groups.iter().any(|group| !current.contains(group)) {
            tx.commit().await?;
            return Err(IdentityError::Forbidden);
        }
        // Recheck after identity/policy locks; a flow that expires while waiting
        // cannot mint a family, code or even a successful denial callback.
        let flow_live: bool = sqlx::query_scalar("SELECT expires_at>clock_timestamp() FROM core_oauth.authorization_requests WHERE binding_digest=$1")
            .bind(digest(binding)?).fetch_one(&mut *tx).await?;
        if !flow_live {
            tx.commit().await?;
            return Err(IdentityError::Unauthorized);
        }
        let mut redirect = reqwest::Url::parse(&row.try_get::<String, _>("redirect_uri")?)
            .map_err(|_| IdentityError::Storage)?;
        if allow {
            let family = new_secret("lmm_gr_")?;
            let inserted: Option<String> = sqlx::query_scalar("INSERT INTO core_oauth.families(id,user_id,owner_account_id,user_version,issuer,resource,client_id,scopes,created_at,expires_at) SELECT $1,$2,$3,$4,$5,$6,'lmm',$7,statement_timestamp(),statement_timestamp()+make_interval(secs=>$8::double precision) FROM core_oauth.authorization_requests WHERE binding_digest=$9 AND consumed_at IS NOT NULL AND expires_at>clock_timestamp() RETURNING id")
                .bind(&family).bind(identity.user).bind(identity.account).bind(identity.version).bind(self.profile.issuer()).bind(self.profile.resource()).bind(&scopes).bind(self.family_ttl).bind(digest(binding)?).fetch_optional(&mut *tx).await?;
            if inserted.is_none() {
                tx.commit().await?;
                return Err(IdentityError::Unauthorized);
            }
            let code = protocol::secret("lmm_oc_")?;
            sqlx::query("INSERT INTO core_oauth.authorization_codes(digest,family_id,user_id,owner_account_id,user_version,consent_version,issuer,resource,client_id,redirect_uri,state,pkce_challenge,scopes,expires_at) SELECT $1,id,user_id,owner_account_id,user_version,consent_version,issuer,resource,client_id,$2,$3,$4,$5,LEAST(expires_at,statement_timestamp()+interval '120 seconds') FROM core_oauth.families WHERE id=$6")
                .bind(digest(&code)?).bind(row.try_get::<String,_>("redirect_uri")?).bind(row.try_get::<String,_>("state")?).bind(row.try_get::<String,_>("pkce_challenge")?).bind(&scopes).bind(family).execute(&mut *tx).await?;
            redirect.query_pairs_mut().append_pair("code", &code);
        } else {
            redirect
                .query_pairs_mut()
                .append_pair("error", "access_denied");
        }
        redirect
            .query_pairs_mut()
            .append_pair("state", &row.try_get::<String, _>("state")?)
            .append_pair("iss", self.profile.issuer());
        tx.commit().await?;
        Ok(AuthorizationRedirect {
            uri: redirect.into(),
        })
    }
    async fn current_groups(
        &self,
        c: &mut PgConnection,
        user: i64,
        account: i64,
        version: i64,
    ) -> Result<Vec<String>> {
        let r=sqlx::query("SELECT u.active AND a.active AND a.kind='personal' AND u.auth_version=$3 AND u.personal_account_id=$2 AS live,u.platform_role FROM core_identity.users u JOIN core_identity.accounts a ON a.id=u.personal_account_id WHERE u.id=$1 FOR SHARE OF u,a NOWAIT")
            .bind(user).bind(account).bind(version).fetch_optional(&mut *c).await.map_err(auth::password_change_error)?.ok_or(IdentityError::Unauthorized)?;
        if !r.try_get::<bool, _>("live")? {
            return Err(IdentityError::Unauthorized);
        }
        // Runtime has SELECT only on policy tables. Revocation triggers update
        // this locked user's auth_version in the same transaction, so they
        // cannot commit past this authorization until it finishes. Do not ask
        // for row-lock UPDATE privileges on developer_access/routing policy.
        let developer: Option<bool> = sqlx::query_scalar(
            "SELECT granted FROM core_identity.developer_access WHERE user_id=$1",
        )
        .bind(user)
        .fetch_optional(&mut *c)
        .await
        .map_err(auth::password_change_error)?;
        let role: String = r.try_get("platform_role")?;
        if !matches!(role.as_str(), "admin" | "superadmin") && developer != Some(true) {
            return Err(IdentityError::Forbidden);
        }
        let candidates:Vec<String>=sqlx::query_scalar("SELECT group_name FROM core_identity.user_routing_groups WHERE user_id=$1 AND enabled ORDER BY group_name")
            .bind(user).fetch_all(&mut *c).await.map_err(auth::password_change_error)?;
        let enabled: Vec<String> = if let Some(router) = &self.identity.dashboard_routing {
            candidates
                .iter()
                .filter(|group| router.catalog(group).is_ok())
                .cloned()
                .collect()
        } else {
            let version: Option<i64> = sqlx::query_scalar(
                "SELECT config_version FROM public.routing_active_release WHERE singleton",
            )
            .fetch_optional(&mut *c)
            .await?;
            let version = version.ok_or(IdentityError::Forbidden)?;
            sqlx::query_scalar(
                "SELECT name FROM public.routing_groups WHERE version=$1 AND enabled ORDER BY name",
            )
            .bind(version)
            .fetch_all(c)
            .await?
        };
        let allowed: Vec<String> = candidates
            .into_iter()
            .filter(|g| {
                self.profile.allows_group(g)
                    && enabled.contains(g)
                    && protocol::group_scope(g).is_ok()
            })
            .collect();
        if allowed.is_empty() {
            return Err(IdentityError::Forbidden);
        }
        Ok(allowed)
    }
    pub(super) async fn live(
        &self,
        c: &mut PgConnection,
        f: &PgRow,
        version: i64,
    ) -> Result<Vec<String>> {
        self.profile.binding(
            &f.try_get::<String, _>("client_id")?,
            &f.try_get::<String, _>("resource")?,
        )?;
        if f.try_get::<String, _>("issuer")? != self.profile.issuer()
            || f.try_get::<i64, _>("consent_version")? != version
        {
            return Err(IdentityError::Unauthorized);
        }
        let current = self
            .current_groups(
                c,
                f.try_get("user_id")?,
                f.try_get("owner_account_id")?,
                f.try_get("user_version")?,
            )
            .await?;
        let live:bool=sqlx::query_scalar("SELECT revoked_at IS NULL AND expires_at>clock_timestamp() FROM core_oauth.families WHERE id=$1")
            .bind(f.try_get::<String,_>("id")?).fetch_one(c).await?;
        if !live {
            return Err(IdentityError::Unauthorized);
        }
        let snapshot = scope_groups(&f.try_get::<Vec<String>, _>("scopes")?)?;
        let groups: Vec<String> = snapshot
            .into_iter()
            .filter(|g| current.contains(g))
            .collect();
        if groups.is_empty() {
            return Err(IdentityError::Forbidden);
        }
        Ok(groups)
    }
    async fn issue(&self, c: &mut PgConnection, f: &PgRow, scopes: &[String]) -> Result<TokenWire> {
        scope_groups(scopes)?;
        let access = protocol::secret("lmm_at_")?;
        let refresh = protocol::secret("lmm_rt_")?;
        let id: String = f.try_get("id")?;
        let version: i64 = f.try_get("consent_version")?;
        let expires=sqlx::query_scalar("INSERT INTO core_oauth.access_tokens(id,digest,family_id,consent_version,scopes,created_at,expires_at) SELECT $1,$2,id,$3,$4,statement_timestamp(),LEAST(expires_at,statement_timestamp()+make_interval(secs=>$5::double precision)) FROM core_oauth.families WHERE id=$6 RETURNING ceil(extract(epoch FROM expires_at-clock_timestamp()))::bigint")
            .bind(new_secret("lmm_ac_")?).bind(digest(&access)?).bind(version).bind(scopes).bind(self.access_ttl).bind(&id).fetch_one(&mut *c).await?;
        sqlx::query("INSERT INTO core_oauth.refresh_tokens(digest,family_id,consent_version,scopes,expires_at) SELECT $1,id,$2,$3,expires_at FROM core_oauth.families WHERE id=$4")
            .bind(digest(&refresh)?).bind(version).bind(scopes).bind(id).execute(c).await?;
        TokenWire::from_bundle(TokenBundle {
            access_token: access,
            refresh_token: refresh,
            expires_in: expires,
            scopes: scopes.to_vec(),
        })
    }
    pub async fn token(&self, input: TokenRequest) -> Result<TokenWire> {
        match input {
            TokenRequest::Code {
                code,
                redirect,
                verifier,
            } => self.exchange(&code, &redirect, &verifier).await,
            TokenRequest::Refresh { refresh, scope } => {
                self.refresh(&refresh, scope.as_deref()).await
            }
        }
    }
    async fn exchange(&self, code: &str, redirect: &str, verifier: &str) -> Result<TokenWire> {
        protocol::canonical(code, "lmm_oc_")?;
        super::redirect(redirect)?;
        let hash = digest(code)?;
        let mut tx = transaction(&self.identity.pool).await?;
        let id: String = sqlx::query_scalar(
            "SELECT family_id FROM core_oauth.authorization_codes WHERE digest=$1",
        )
        .bind(&hash)
        .fetch_optional(&mut *tx)
        .await?
        .ok_or(IdentityError::Unauthorized)?;
        let f = family(&mut tx, &id, true).await?;
        let code=sqlx::query("SELECT *,consumed_at IS NULL AND expires_at>clock_timestamp() AS live FROM core_oauth.authorization_codes WHERE digest=$1 FOR UPDATE")
            .bind(&hash).fetch_one(&mut *tx).await?;
        self.profile.binding(
            &code.try_get::<String, _>("client_id")?,
            &code.try_get::<String, _>("resource")?,
        )?;
        if !code.try_get::<bool, _>("live")?
            || code.try_get::<String, _>("redirect_uri")? != redirect
            || code.try_get::<String, _>("issuer")? != self.profile.issuer()
            || code.try_get::<i64, _>("user_id")? != f.try_get::<i64, _>("user_id")?
            || code.try_get::<i64, _>("owner_account_id")?
                != f.try_get::<i64, _>("owner_account_id")?
            || code.try_get::<i64, _>("user_version")? != f.try_get::<i64, _>("user_version")?
        {
            return Err(IdentityError::Unauthorized);
        }
        pkce(
            &code.try_get::<String, _>("pkce_challenge")?,
            Some("S256"),
            Some(verifier),
        )?;
        let current = self
            .live(&mut tx, &f, code.try_get("consent_version")?)
            .await?;
        let scopes: Vec<String> = code.try_get("scopes")?;
        let family_scopes: Vec<String> = f.try_get("scopes")?;
        if scopes.iter().any(|s| !family_scopes.contains(s))
            || scope_groups(&scopes)?.iter().any(|g| !current.contains(g))
        {
            return Err(IdentityError::Forbidden);
        }
        sqlx::query("UPDATE core_oauth.authorization_codes SET consumed_at=clock_timestamp() WHERE digest=$1 AND consumed_at IS NULL AND expires_at>clock_timestamp() RETURNING digest")
            .bind(hash).fetch_optional(&mut *tx).await?.ok_or(IdentityError::Unauthorized)?;
        let issued = self.issue(&mut tx, &f, &scopes).await?;
        tx.commit().await?;
        Ok(issued)
    }
    async fn refresh(&self, refresh: &str, scope: Option<&str>) -> Result<TokenWire> {
        protocol::canonical(refresh, "lmm_rt_")?;
        let hash = digest(refresh)?;
        let mut tx = transaction(&self.identity.pool).await?;
        let id: String =
            sqlx::query_scalar("SELECT family_id FROM core_oauth.refresh_tokens WHERE digest=$1")
                .bind(&hash)
                .fetch_optional(&mut *tx)
                .await?
                .ok_or(IdentityError::Unauthorized)?;
        let f = family(&mut tx, &id, true).await?;
        // Validate binding before any replay mutation, including expired lineage.
        self.profile.binding(
            &f.try_get::<String, _>("client_id")?,
            &f.try_get::<String, _>("resource")?,
        )?;
        if f.try_get::<String, _>("issuer")? != self.profile.issuer() {
            return Err(IdentityError::Unauthorized);
        }
        let token=sqlx::query("SELECT *,consumed_at IS NOT NULL AS consumed FROM core_oauth.refresh_tokens WHERE digest=$1 FOR UPDATE").bind(&hash).fetch_one(&mut *tx).await?;
        if token.try_get::<bool, _>("consumed")? {
            sqlx::query("UPDATE core_oauth.families SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE id=$1").bind(id).execute(&mut *tx).await?;
            tx.commit().await?;
            return Err(IdentityError::Unauthorized);
        }
        let current = self
            .live(&mut tx, &f, token.try_get("consent_version")?)
            .await?;
        let previous: Vec<String> = token.try_get("scopes")?;
        let selected: Vec<String> = scope
            .map(|s| s.split(' ').map(str::to_owned).collect())
            .unwrap_or(previous.clone());
        if !scope_groups(&selected)?
            .iter()
            .any(|group| current.contains(group))
        {
            return Err(IdentityError::Forbidden);
        }
        let allowed: Vec<String> = f.try_get("scopes")?;
        if selected
            .iter()
            .any(|s| !previous.contains(s) || !allowed.contains(s))
        {
            return Err(IdentityError::Forbidden);
        }
        sqlx::query("UPDATE core_oauth.families SET scopes=$2 WHERE id=$1")
            .bind(&id)
            .bind(&selected)
            .execute(&mut *tx)
            .await?;
        sqlx::query("UPDATE core_oauth.refresh_tokens SET consumed_at=clock_timestamp() WHERE digest=$1 AND consumed_at IS NULL AND expires_at>clock_timestamp() RETURNING digest")
            .bind(hash).fetch_optional(&mut *tx).await?.ok_or(IdentityError::Unauthorized)?;
        let issued = self.issue(&mut tx, &f, &selected).await?;
        tx.commit().await?;
        Ok(issued)
    }
    pub async fn revoke(&self, token: &str) -> Result<()> {
        let table = if token.starts_with("lmm_at_") {
            protocol::canonical(token, "lmm_at_")?;
            "access_tokens"
        } else {
            protocol::canonical(token, "lmm_rt_")?;
            "refresh_tokens"
        };
        let mut tx = transaction(&self.identity.pool).await?;
        let id: Option<String> = sqlx::query_scalar(&format!(
            "SELECT family_id FROM core_oauth.{table} WHERE digest=$1"
        ))
        .bind(digest(token)?)
        .fetch_optional(&mut *tx)
        .await?;
        if let Some(id) = id {
            let f = family(&mut tx, &id, true).await?;
            self.profile.binding(
                &f.try_get::<String, _>("client_id")?,
                &f.try_get::<String, _>("resource")?,
            )?;
            if f.try_get::<String, _>("issuer")? != self.profile.issuer() {
                return Err(IdentityError::Unauthorized);
            }
            sqlx::query("UPDATE core_oauth.families SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE id=$1").bind(id).execute(&mut *tx).await?;
        }
        tx.commit().await?;
        Ok(())
    }
}
pub(super) fn scope_groups(scopes: &[String]) -> Result<Vec<String>> {
    if scopes.is_empty() || scopes.len() > 66 {
        return Err(IdentityError::Invalid);
    }
    let mut unique = BTreeSet::new();
    let mut groups = Vec::new();
    let mut read = false;
    for scope in scopes {
        if !unique.insert(scope) {
            return Err(IdentityError::Invalid);
        }
        if matches!(scope.as_str(), "catalog:read" | "balance:read") {
            read = true;
        } else {
            groups.push(protocol::decode_group(scope)?);
        }
    }
    if !read || groups.is_empty() {
        return Err(IdentityError::Forbidden);
    }
    Ok(groups)
}
