use crate::auth::{
    AuthBundle, AuthError, AuthErrorKind, CriticalRateLimitOutcome, DashboardAuth, DashboardUser,
    LoginOutcome, LoginRequest, LogoutRequest, LogoutResult, RequestMetadata,
    TwoFactorLoginRequest,
};
use async_trait::async_trait;
use secrecy::SecretString;

pub(crate) struct RejectingDashboardAuth;

#[async_trait]
impl DashboardAuth for RejectingDashboardAuth {
    async fn check_critical_rate_limit(
        &self,
        _: &str,
    ) -> Result<CriticalRateLimitOutcome, AuthError> {
        Ok(CriticalRateLimitOutcome::Allowed)
    }

    async fn login(&self, _: LoginRequest, _: RequestMetadata) -> Result<LoginOutcome, AuthError> {
        Err(AuthError::new(AuthErrorKind::Unauthorized))
    }

    async fn login_2fa(
        &self,
        _: TwoFactorLoginRequest,
        _: RequestMetadata,
    ) -> Result<AuthBundle, AuthError> {
        Err(AuthError::new(AuthErrorKind::Unauthorized))
    }

    async fn refresh(
        &self,
        _: SecretString,
        _: Option<String>,
        _: RequestMetadata,
    ) -> Result<AuthBundle, AuthError> {
        Err(AuthError::new(AuthErrorKind::Unauthorized))
    }

    async fn self_user(&self, _: SecretString) -> Result<DashboardUser, AuthError> {
        Err(AuthError::new(AuthErrorKind::Unauthorized))
    }

    async fn logout(&self, _: LogoutRequest) -> Result<LogoutResult, AuthError> {
        Err(AuthError::new(AuthErrorKind::Unauthorized))
    }

    async fn generate_personal_access_token(&self, _: SecretString) -> Result<String, AuthError> {
        Err(AuthError::new(AuthErrorKind::Unauthorized))
    }
}
