//! Explicit native-client browser ceremony inside the existing refresh path.
use super::*;
use crate::identity::grants::{AuthorizationRequest, ConsentForm, ConsentPage, ContinueForm};
use axum::extract::{Form, rejection::FormRejection};
use sha2::{Digest, Sha256};
pub(super) fn routes() -> Router<AuthState> {
    Router::new()
        .route("/api/oauth2/authorize", get(authorize))
        .route(
            "/api/user/auth/oauth2/continue",
            post(continue_authorization),
        )
        .route("/api/user/auth/oauth2/consent", post(consent))
}
// Every immutable browser stage has a different CSRF-selected cookie name.
// Delayed responses may clear only their own stage, never another tab's flow.
// This selector grants no authority: consume still binds both independent
// random cookie and one-use CSRF to the persisted request and native Session.
fn cookie_name(config: &LegacyAuthConfig, csrf: &str) -> String {
    let prefix = if config.secure { "__Secure-" } else { "" };
    let mut hash = Sha256::new();
    hash.update(b"lmm-oauth2-browser-stage-v1\0");
    hash.update(csrf.as_bytes());
    format!("{prefix}lmm_oauth2_{:x}", hash.finalize())
}
fn cookie<'a>(
    headers: &'a HeaderMap,
    config: &LegacyAuthConfig,
    csrf: &str,
) -> Result<&'a str, IdentityError> {
    let expected = cookie_name(config, csrf);
    let mut found = None;
    for value in headers.get_all(header::COOKIE) {
        for part in value
            .to_str()
            .map_err(|_| IdentityError::Unauthorized)?
            .split(';')
        {
            if let Some((name, value)) = part.trim().split_once('=')
                && name == expected
            {
                if found.is_some()
                    || value.len() != 43
                    || !value
                        .bytes()
                        .all(|v| v.is_ascii_alphanumeric() || b"-_".contains(&v))
                {
                    return Err(IdentityError::Unauthorized);
                }
                found = Some(value);
            }
        }
    }
    found.ok_or(IdentityError::Unauthorized)
}
fn flow_cookie(
    mut response: Response,
    binding: Option<&str>,
    csrf: &str,
    config: &LegacyAuthConfig,
) -> Response {
    let secure = if config.secure { "Secure; " } else { "" };
    let value = format!(
        "{}={}; Path=/api/user/auth/oauth2; {secure}HttpOnly; SameSite=Strict; Max-Age={}",
        cookie_name(config, csrf),
        binding.unwrap_or_default(),
        if binding.is_some() { 300 } else { 0 }
    );
    if let Ok(value) = HeaderValue::from_str(&value) {
        response.headers_mut().append(header::SET_COOKIE, value);
    }
    response
}
fn escape(value: &str) -> String {
    value
        .replace('&', "&amp;")
        .replace('<', "&lt;")
        .replace('>', "&gt;")
        .replace('"', "&quot;")
        .replace('\'', "&#39;")
}
fn page(page: ConsentPage, config: &LegacyAuthConfig) -> Response {
    let mut body = String::from(
        "<!doctype html><meta charset=utf-8><title>LMM CLI authorization</title><h1>LMM CLI</h1>",
    );
    let action = if let Some(account) = &page.account {
        body.push_str(&format!("<p>Account: {}</p><p>Read the model catalog and your personal wallet balance.</p><p>Approved groups:</p><ul>",escape(account)));
        for group in &page.groups {
            body.push_str(&format!("<li>{}</li>", escape(group)));
        }
        body.push_str("</ul>");
        "/api/user/auth/oauth2/consent"
    } else {
        body.push_str("<p>Sign in, then explicitly continue here. This does not authorize LMM CLI.</p><p><a href=\"/sign-in?reauth=1\" target=\"_blank\" rel=\"noopener noreferrer\">Open sign in</a></p>");
        "/api/user/auth/oauth2/continue"
    };
    body.push_str(&format!(
        "<form method=post action=\"{action}\"><input type=hidden name=csrf value=\"{}\">",
        escape(&page.csrf)
    ));
    if page.account.is_some() {
        body.push_str("<button name=decision value=allow>Allow read-only access</button> <button name=decision value=deny>Deny</button>");
    } else {
        body.push_str("<button>Continue after sign in</button>");
    }
    body.push_str("</form>");
    let mut response = axum::response::Html(body).into_response();
    response.headers_mut().insert(
        header::CONTENT_SECURITY_POLICY,
        HeaderValue::from_static(
            "default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'",
        ),
    );
    response.headers_mut().insert(
        header::REFERRER_POLICY,
        HeaderValue::from_static("strict-origin"),
    );
    flow_cookie(response, Some(&page.binding), &page.csrf, config)
}
async fn authorize(
    State(state): State<AuthState>,
    input: Result<Query<AuthorizationRequest>, QueryRejection>,
) -> Response {
    let Some(issuer) = &state.config.oauth2 else {
        return StatusCode::NOT_FOUND.into_response();
    };
    let Ok(Query(input)) = input else {
        return auth_failure(IdentityError::Invalid.into());
    };
    match issuer.begin(input).await {
        Ok(v) => page(v, &state.config),
        Err(e) => auth_failure(e.into()),
    }
}
async fn continue_authorization(
    State(state): State<AuthState>,
    headers: HeaderMap,
    input: Result<Form<ContinueForm>, FormRejection>,
) -> Response {
    let Some(issuer) = &state.config.oauth2 else {
        return StatusCode::NOT_FOUND.into_response();
    };
    let Ok(Form(form)) = input else {
        return auth_failure(IdentityError::Invalid.into());
    };
    let result = async {
        let binding = cookie(&headers, &state.config, &form.csrf)?;
        issuer
            .continue_authorization(binding, &form.csrf, refresh_cookie(&headers).ok().flatten())
            .await
    }
    .await;
    let response = match result {
        Ok(v) => page(v, &state.config),
        Err(e) => auth_failure(e.into()),
    };
    flow_cookie(response, None, &form.csrf, &state.config)
}
async fn consent(
    State(state): State<AuthState>,
    headers: HeaderMap,
    input: Result<Form<ConsentForm>, FormRejection>,
) -> Response {
    let Some(issuer) = &state.config.oauth2 else {
        return StatusCode::NOT_FOUND.into_response();
    };
    let Ok(Form(form)) = input else {
        return auth_failure(IdentityError::Invalid.into());
    };
    let result = async {
        let allow = match form.decision.as_str() {
            "allow" => true,
            "deny" => false,
            _ => return Err(IdentityError::Invalid),
        };
        let binding = cookie(&headers, &state.config, &form.csrf)?;
        let refresh = refresh_cookie(&headers).ok().flatten();
        issuer.consent(binding, &form.csrf, refresh, allow).await
    }
    .await;
    let response = match result {
        Ok(v) => Redirect::to(&v.uri).into_response(),
        Err(e) => auth_failure(e.into()),
    };
    flow_cookie(response, None, &form.csrf, &state.config)
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn stage_cookie_selection_requires_its_own_random_binding() {
        let config = LegacyAuthConfig::new("https://issuer.example.test", &[3; 32]).unwrap();
        let a = "A".repeat(43);
        let b = "B".repeat(43);
        let a_name = cookie_name(&config, &a);
        let b_name = cookie_name(&config, &b);
        assert_ne!(a_name, b_name);
        let mut headers = HeaderMap::new();
        headers.insert(
            header::COOKIE,
            HeaderValue::from_str(&format!("{a_name}={a}; {b_name}={b}")).unwrap(),
        );
        assert_eq!(cookie(&headers, &config, &a).unwrap(), a);
        assert_eq!(cookie(&headers, &config, &b).unwrap(), b);
        assert!(cookie(&headers, &config, &"C".repeat(43)).is_err());
        headers.append(
            header::COOKIE,
            HeaderValue::from_str(&format!("{a_name}={a}")).unwrap(),
        );
        assert!(cookie(&headers, &config, &a).is_err());
        assert_eq!(cookie(&headers, &config, &b).unwrap(), b);
    }
    #[test]
    fn delayed_clear_is_scoped_to_the_original_stage_cookie() {
        let config = LegacyAuthConfig::new("https://issuer.example.test", &[3; 32]).unwrap();
        let a = "A".repeat(43);
        let b = "B".repeat(43);
        let response = flow_cookie(StatusCode::OK.into_response(), None, &a, &config);
        let cleared = response.headers()[header::SET_COOKIE].to_str().unwrap();
        assert!(cleared.starts_with(&format!("{}=;", cookie_name(&config, &a))));
        assert!(!cleared.contains(&cookie_name(&config, &b)));
        assert!(cleared.contains("HttpOnly; SameSite=Strict; Max-Age=0"));
        assert!(cleared.contains("Path=/api/user/auth/oauth2; Secure;"));
    }
}
