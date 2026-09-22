# CoWeft subproject identity contract

CoWeft is a resource server and OIDC relying party. LMM owns authentication, stable subjects, consent, delegated authorization, signing keys and revocation. The child is pinned at `apps/coweft` as a Git submodule; it does not create a second login system. The existing native OAuth issuer and Pi/DSH/Codewhale endpoints are unchanged.

## Endpoints

The issuer is `https://api.lmm.best/oidc`. OIDC discovery is `/oidc/.well-known/openid-configuration`; RFC 8414 discovery is `/.well-known/oauth-authorization-server/oidc`. Browser authorization and grant management live beneath `/api/user/auth/oidc/` so the existing refresh cookie path does not need widening. Machine token, JWKS, userinfo, introspection, revocation and public-content attestation endpoints live beneath `/api/oidc/`.

Only authorization-code + S256 PKCE is accepted. ID tokens are RS256, with client audience, nonce and access-token hash. Access and rotating refresh tokens are opaque; only their hashes are stored as lookup keys. Refresh-token consumption and its replay marker are atomic. Every introspection checks both the grant family and the current LMM user session. Disabled accounts, revoked sessions, session-version changes and explicit grant revocation are rejected at resource access. Public clients have no embedded shared secret. Each resource server has a separate introspection credential and cannot inspect another resource's audience.

Subjects are `lmm:<immutable LMM user id>`. Never recycle user IDs. Username, group, VIP status and spending are not identity keys or forum voting weights. Controller provenance (`human` or `agent`) comes from trusted client registration, not a request header. Native-agent clients must be registered explicitly; arbitrary dynamic registration is not offered. Controller provenance identifies the authorized submission channel, not whether text was actually written by a human.

## Browser login and consent

The browser entry bridge validates the complete client/callback/resource request before rendering anything. A cross-site arrival may lack LMM's existing SameSite=Strict refresh cookie. The user can continue through a same-site link; if not logged in, the existing LMM login opens in a separate tab while the authorization request stays in the original tab. After login, continuing presents the consent screen.

This does not widen the refresh-cookie path, invent another password login, or depend on a SPA `redirect` parameter. Consent is explicit, bound to the current LMM session, a short-lived transaction, a host-scoped cookie and a CSRF token. Denial returns an OAuth error to the registered callback. `/api/user/auth/oidc/grants` lists and revokes a user's active subproject grants. Verify the entire flow against the actual TLS proxy and frontend before enabling public users.

## Scope policy

`openid profile` identifies the user and shares the display name. `coweft:read`, `coweft:write`, `coweft:propose` and `coweft:vote` are separate permissions. All forum scopes require `coweft:read`. Model execution and spending are NOT included in this issuer's scopes. An agent acts only within its consented client grant. Human and agent use the same subject, not separate governance identities.

## Public federation receipts

`POST /api/oidc/attest` requires TWO independent approvals: the registered resource's HTTP Basic credential and an active user access token in a bounded JSON body. The JSON contains `token`, `digest` and `purpose`; the only supported purpose is `public-thread-v1`. The token must have `coweft:write` and an audience equal to the authenticating resource's registered URI. A user's bearer token alone cannot impersonate an origin node's publication approval.

The result is an RS256-signed public receipt with dedicated type `coweft-event+jwt`, audience `urn:coweft:public-thread-v1`, source resource, subject, controller and SHA-256 content digest. It is not an access token or ID token, contains no bearer credential and cannot authorize an API call. Peers receive the public envelope, never either credential used to obtain it. A receipt attests authorized publication at that time, not the truth of the content or unique natural-person authorship.

Public receipts intentionally outlive login grants. Revoking a login does not erase content already distributed. Historical verification keys must remain available before planned key rotation. This first profile replicates public thread snapshots; it is not ActivityPub, globally replicated voting, migration or guaranteed remote deletion.

## Enable on the parent

Default: disabled. Generate an RSA key outside the repository, restrict its filesystem permissions and mount it read-only into the existing Go API service. For example:

```sh
openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:3072 -out /run/secrets/lmm-oidc.pem
chmod 600 /run/secrets/lmm-oidc.pem
```

Set `LMM_OIDC_ENABLED=true`, `LMM_OIDC_SIGNING_KEY_FILE=/run/secrets/lmm-oidc.pem`, `LMM_OIDC_ISSUER=https://api.lmm.best/oidc` and the registrations from `packaging/common/lmm-api/lmm-oidc.env.example`. Use the actual CoWeft HTTPS origin in both callback and resource. Generate a separate random resource credential and configure it in both backends; never expose it in frontend code or native clients. Configure only the actual trusted TLS-proxy CIDRs. Arbitrary forwarding headers are not trusted. Without explicit enablement all new endpoints return 404.

All LMM replicas need the same signing key and persistent database. Overlapping-key rotation is not implemented and must be addressed before a high-availability public rollout. No domain, production secret, live client registration or paid model credential is provisioned by this change.

## Verification and rollout limits

The provider suite covers strict authorization requests, registered redirects, PKCE, browser consent, code reuse, refresh replay, current-session checks, wrong-resource rejection, revocation and resource-plus-user publication approval. CoWeft CI consumes a real Go-signed receipt and verifies it in Rust; this is cross-language protocol verification, not a claim that production browser login or a multi-host deployment has been exercised.

Persistent records are pruned on startup; long-running deployments should also schedule expiry cleanup. Browser grant management currently shows at most 100 recent families. No password grant, implicit flow, arbitrary dynamic registration, DPoP, SAML, email disclosure, back-channel logout or automatic trust of external identities is advertised. This implementation is not a certified OpenID Provider. Run the complete suites and obtain an independent review of this new authorization boundary before public enablement.

OAuth authenticates accounts, not unique natural people. CoWeft prevents a human and authorized agents within one account from multiplying votes; it does not claim Sybil-proof personhood or complete decentralized identity.
