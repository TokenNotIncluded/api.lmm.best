//! Built-in Google login creates the same browser family as password login.
use super::*;

impl BrowserAuth {
    // Domain separation prevents a native OAuth flow from being used as a
    // consent-bearing browser flow. The raw binding is only in an HttpOnly cookie.
    fn google_binding(&self, binding: &str) -> Result<String> {
        if binding.len() != 64 || !binding.bytes().all(|b| b.is_ascii_hexdigit()) {
            return Err(IdentityError::Unauthorized);
        }
        self.derived_secret(b"browser-google-flow-v1", "", binding, "")
    }
    pub async fn begin_google_login(&self, binding: &str) -> BrowserResult<String> {
        Ok(self
            .store
            .begin_google_login(&self.google_binding(binding)?)
            .await?)
    }
    pub async fn finish_google_login(
        &self,
        state: &str,
        binding: &str,
        code: &str,
        metadata: &BrowserSessionMetadata,
    ) -> BrowserResult<BrowserBundle> {
        let subject = self
            .store
            .exchange_google_code(state, &self.google_binding(binding)?, code)
            .await?;
        let mut tx = self.store.pool.begin().await?;
        let user = self.store.google_user(&mut tx, &subject).await?;
        // Native provider-only identities may not yet have a Web profile. Do
        // not overwrite existing imported profiles or link by provider email.
        let username = format!("g_{}", &new_secret("")?[..18]);
        sqlx::query("INSERT INTO core_identity.user_profiles(user_id,username,display_name,password_login_allowed) VALUES ($1,$2,$2,FALSE) ON CONFLICT (user_id) DO NOTHING")
            .bind(user).bind(username).execute(&mut *tx).await?;
        // Shared issuance locks active user/account and checks local MFA using
        // the fresh snapshot / generation guard introduced by #877.
        let bundle = self
            .issue_browser_family(&mut tx, user, metadata, "session.google_browser_login")
            .await?;
        tx.commit().await?;
        Ok(bundle)
    }
}
