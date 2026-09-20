# Saving OAuth settings

Saving an unrelated login provider, or saving an unchanged form, does not fetch
an unchanged OIDC discovery document. Disabling OIDC also skips discovery, even
when its saved URL is invalid or its provider is unavailable.

Enabling OIDC with a non-empty discovery URL, or changing that URL without
simultaneously disabling OIDC, validates an absolute HTTP(S) URL and fetches the
document before any settings updates. A successful discovery persists the
trimmed URL. Empty URLs retain the existing manual-endpoint configuration mode.
Discovery failures select the OIDC tab and display the existing localized error
at the URL field; they do not submit a partial configuration. Other failures in
the existing sequential option-update API are not made transactional by this
change.

The save and reset actions remain disabled for the complete form submission,
including discovery and all option updates. Provider credentials, Telegram
fields, callback URLs and registration restrictions are unchanged.

`apps/web/src/features/system-settings/auth/oidc-discovery.test.ts` covers the
save decision, request timing, input immutability, normalization and failures
with local fixtures, not live identity providers.
