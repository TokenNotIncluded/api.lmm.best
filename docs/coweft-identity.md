# CoWeft subproject identity contract

CoWeft is a resource server. LMM owns authentication, stable subjects, consent, delegation, signing keys and revocation. It does not create a second login system. The existing native OAuth issuer is unchanged.

## Endpoints

The issuer is `https://api.lmm.best/oidc`. OIDC discovery is `/oidc/.well-known/openid-configuration`; RFC 8414 discovery is `/.well-known/oauth-authorization-server/oidc`. Browser authorization and grant management live beneath `/api/user/auth/oidc/` so the existing refresh cookie path does not need widening. Machine token, JWKS, userinfo, introspection and revocation endpoints live beneath `/api/oidc/`.

Only authorization-code + S256 PKCE is accepted. ID tokens are RS256, with client audience, nonce and access-token hash. Access and rotating refresh tokens are opaque; only hashes are stored. Refresh-token consumption and the replay marker are atomic. Every introspection checks both the family and the current LMM user session. A disabled account, logout, session version change or grant revocation invalidates access without waiting for JWT expiry. Public clients have no embedded shared secret. Each resource server has a separate introspection credential and cannot inspect another resource's audience.

Subjects are `lmm:<immutable LMM user id>`. Never recycle user IDs. Username, user group, VIP status and spending are not identity keys or forum voting weights. Controller provenance (`human` or `agent`) comes from trusted client registration, not a request header. Native-agent clients must be registered explicitly; arbitrary dynamic registration is not offered.

## Enable on the parent

Generate an RSA key outside the repository (`openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:3072 -out /run/secrets/lmm-oidc.pem`), restrict its filesystem permissions, and mount it read-only into the existing Go API service. All replicas must use the same key; planned overlapping-key rotation is still required before high-availability production rollout.

Set `LMM_OIDC_ENABLED=true`, `LMM_OIDC_SIGNING_KEY_FILE=/run/secrets/lmm-oidc.pem`, `LMM_OIDC_ISSUER=https://api.lmm.best/oidc` and the registrations from `deploy/coweft/oidc.env.example`. Set the introspection secret in both services, never in frontend code. Configure only your actual trusted TLS proxy CIDRs; the provider does not trust arbitrary forwarding headers. Without explicit enablement these endpoints return 404.

Use the actual CoWeft origin in both its registered callback and resource. Open `/api/user/auth/oidc/grants` to revoke access. The existing `/login?redirect=...` return flow must be verified against the deployed frontend before enabling real users.

## Scope policy

`openid profile` identifies the user and shares the display name. `coweft:read`, `coweft:write`, `coweft:propose`, and `coweft:vote` are separate permissions. All forum scopes require `coweft:read`. Model execution and spending are NOT included in this issuer's scopes. An AI may act only within the consented client grant. Human and agent use the same subject, never an extra vote.

## Rollout and limits

This is a new authorization boundary. Deploy behind TLS, run the complete provider and resource-server suites, verify browser login/consent/deny/revoke, and perform independent security review before public production exposure. Configuration is fail-closed. Persistent records are pruned on startup; operators should schedule expiry cleanup for long-running deployments. Browser grant management shows at most 100 recent families.

This implementation is not a certified OpenID Provider. No password grant, implicit flow, arbitrary dynamic registration, DPoP, SAML, email disclosure, back-channel logout or automatic trust of external identities is advertised. OAuth authenticates accounts, not unique natural people; governance still needs an explicit Sybil-resistance policy.
