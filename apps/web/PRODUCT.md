# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

- Authenticated LMM Forge users who need a short-lived phone number or email address to complete an external account-verification flow and want to pay from their existing platform quota.
- Administrators who operate a dedicated HeroSMS account and control provider credentials, feature availability, and the customer price multiplier.

## Product Purpose

LMM Forge is a web-first collaboration and API platform with accountable, quota-backed workflows. The temporary-activation surface adds a focused paid utility: users default to phone/SMS activation, search for a service, choose a country, review inventory and exact customer price, purchase one or more independent numbers, and manage verification codes without leaving the authenticated product. Temporary email activation remains available as a secondary mode.

Success means that a user can complete either purchase-to-code workflow without seeing provider credentials or ambiguous charges, while administrators can configure and rotate the integration safely.

## Positioning

The service turns a dedicated upstream HeroSMS account into a tenant-safe, audited LMM Forge workflow: provider inventory is priced through the platform's own quota system, every order remains bound to its local owner, and failed or incompatible purchases are compensated rather than silently charged.

## Operating Context

- Users work inside the existing authenticated React application on desktop and mobile.
- The default path is: choose phone service → choose country → review inventory, quantity, and multiplied USD price → confirm purchase → monitor every active number → copy the phone number or received code/message → cancel when allowed.
- The secondary email path remains: choose target site and domain → review multiplied USD price → confirm purchase → monitor active activation → copy the email or received code/message → cancel or reorder when allowed.
- Administrators configure the integration in the existing system settings area.
- The Go backend is the only caller of HeroSMS; the browser never communicates with HeroSMS directly.

## Capabilities and Constraints

- The initial provider is HeroSMS only, using a dedicated account.
- The account currency is USD (ISO 4217 numeric code `840`). Domain-list costs are interpreted as USD, and completed purchases must validate the returned currency before local settlement.
- Supported upstream functionality includes HeroSMS phone/SMS country and service catalogs, live offers, single-number purchase, status refresh, cancellation, and local quantity orchestration, plus the complete HeroSMS Emails API group: list, single purchase, batch purchase, detail, cancel, reorder, and domain availability.
- The default customer price multiplier is `10`; the administrator can change it in settings.
- The HeroSMS API key is server-side secret material. Reads expose only configured/masked state; blank updates preserve the stored value, and explicit removal requires confirmation.
- Clients cannot choose provider cost, charged quota, refund amount, local owner, or provider order ownership.
- Every local activation and audit record is bound to an authenticated local user. Provider identifiers alone are never an authorization boundary.
- Purchase, cancel, refresh, and reorder operations must be duplicate-safe and leave an auditable local result.
- Multi-number phone purchases are independent provider orders. Partial success must remain visible and must never be presented as an atomic transaction.

## Evidence on Hand

- Product and architecture overview: `../../README.md`
- HeroSMS email reference contract and integration boundaries: `.scratch/herosms-email-activation/reference/contract.md`
- Phone/SMS implementation contracts and tests under `src/features/email-activations/` and `../api-go/model/hero_sms_sms*.go`.
- Existing application routes, settings, quota, authentication, and component implementations under `src/` and `../api-go/`.
- No HeroSMS brand assets, endorsements, availability promises, or performance claims are available and none should be fabricated.

## Product Principles

1. **Price before commitment:** show a deterministic final customer price before every paid action.
2. **Secrets stay server-side:** credentials and raw provider failures never cross into browser payloads or logs.
3. **Ownership before convenience:** every provider operation is authorized through a local user-owned record.
4. **Recover visibly:** provider delay and failure produce actionable states, safe retries, and compensation rather than uncertain charges.
5. **Current activation first:** active phone numbers, email addresses, and received verification content outrank dashboards or decorative metrics.

## Accessibility & Inclusion

The workflow must remain usable by keyboard, expose status through text rather than color alone, preserve focus through asynchronous updates, and adapt order details into a readable single-column mobile flow.
