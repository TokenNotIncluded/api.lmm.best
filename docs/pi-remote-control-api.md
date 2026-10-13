# Pi Remote Control API v1

This is the v1 contract implemented by the historical site and reused by the
WIP browser and Pi plugin. **The current WIP hosts do not yet implement these
relay endpoints or the `lmm-pi` OAuth authorization-server contract.** Client
and loopback-fixture checks do not establish that a WIP deployment can serve
remote control. Do not promote the WIP site as a replacement for this feature
until those authenticated server contracts and their acceptance checks exist.

On the historical site, `/api/remote-control/v1/pi` is an authenticated,
ephemeral relay for a Pi plugin and its browser controller. It stores data only
in the current API process memory. Restarting the API or reaching the two-minute
heartbeat TTL removes a session and its messages.

The server never receives a PIN or plaintext. The v1 client encryption contract
is fixed so browser and Pi implementations are interoperable:

- Encode the PIN exactly as entered with UTF-8. Do not trim or normalize it.
- Derive a non-exportable AES-256 key with WebCrypto PBKDF2, SHA-256, 210,000
  iterations, and UTF-8 salt `lmm-pi-remote:v1:<session_id>`.
- Encrypt with AES-GCM, a new cryptographically random 12-byte nonce for every
  payload, and a 128-bit authentication tag. WebCrypto appends the tag to the
  ciphertext returned by `subtle.encrypt`.
- Encode the nonce and ciphertext-plus-tag as unpadded base64url.
- Metadata additional authenticated data is UTF-8
  `lmm-pi-remote:v1:metadata:<session_id>:<device_id>`.
- Message additional authenticated data is UTF-8
  `lmm-pi-remote:v1:message:<session_id>:<sender>`.

Metadata plaintext is a UTF-8 JSON object. It may contain `version` (`1`),
`started_at`, `runtime`, `directory`, and `summary`. Each message plaintext is a
UTF-8 JSON object containing `type` and the type-specific fields such as
`content`, `question`, `options`, `tool_name`, or `arguments`. The relay's
`sequence`, `sender`, and `created_at` remain outside the ciphertext, but
`sender` is authenticated by the message AAD.

The browser keeps the entered PIN and derived key only in the mounted session
view. It never sends them to the API or writes them to browser storage. Locking
or switching sessions unmounts that view and drops its key and decrypted query
cache.

Browser requests require normal user authentication. The Pi plugin may instead use a live `lmm-pi` OAuth grant with the explicitly approved `remote:control` scope. Legacy grants are not widened; sign in again to approve this scope. No model API key, model selection or model quota is required by these endpoints. Sessions are keyed by both
the authenticated user ID and `session_id`; one user cannot list or read
another user's sessions even when the session ID is known.

## Endpoints

- `PUT /api/remote-control/v1/pi/sessions/:session_id` upserts a plugin
  heartbeat. Body:

  ```json
  {
    "device_id": "device_abcdefgh",
    "metadata": {"nonce": "...", "ciphertext": "..."}
  }
  ```

- `GET /api/remote-control/v1/pi/sessions` lists the caller's active encrypted
  session snapshots.
- `POST /api/remote-control/v1/pi/sessions/:session_id/messages` appends an
  opaque message. Body:

  ```json
  {"sender": "plugin", "nonce": "...", "ciphertext": "..."}
  ```

  `sender` is `plugin` or `controller` and is routing metadata, not a trusted
  identity assertion.
- `GET /api/remote-control/v1/pi/sessions/:session_id/messages?after=12`
  returns messages whose server-assigned `sequence` is greater than `after`.

Metadata plaintext is limited to a 16 KiB ciphertext envelope and messages to
64 KiB each. A session retains its newest 128 messages. IDs are 8-64 ASCII
letters, digits, `_`, or `-`. The service validates only envelope syntax and
size; it does not decrypt or interpret ciphertext.


## WIP internal relay foundation

`services/extensions/internal/modules/remotecontrol` now provides an internal
Go module with the same opaque envelope format. It is **not registered in the
running host**, has no public Core adapter, and does not make the browser or Pi
plugin usable against a WIP deployment yet. Its module-relative endpoints are
`/pi/sessions`, `/pi/sessions/{id}[/messages]` and
`/pi/sessions/{id}/events`; the authenticated extension
host would mount them under `/extensions/v1/remote-control`.

Every supported endpoint calls the Rust Core `Authorize` interface again using
the separate `X-LMM-User-Credential`. Only native personal browser sessions are
accepted by this foundation. A host credential, model API key, team account,
platform level or funding-account candidate is not a remote-control grant.
The current Core RPC does not validate `lmm-pi` OAuth grants, so this module must
not accept or translate them. Core failure or revocation fails closed, including
reads and deletion. The required public adapter and OAuth contract remain
production blockers.

Storage is process-local and starts no workers, database setup or network calls
at construction. Every operation sweeps expired sessions. Idle expired records
remain encrypted in memory until another operation or process exit; mounting
this module later must decide whether to add host-owned periodic expiry.
Restart and recreation after expiry produce a fresh random generation. An
unexpired session cannot be overwritten with a different device ID; remove it
or use a fresh session ID instead.

Limits are 32 sessions per user, 512 globally, 128 messages per session, and
encoded-payload budgets of 1 MiB per session, 4 MiB per user and 32 MiB globally.
History rolls by both count and byte budget, so cursors may have gaps. Only
plugin heartbeats extend the two-minute TTL; message traffic does not. Capacity
rejection must not mutate records, consume a sequence or return a successful
command receipt. These internal budgets are additional bounds, not changes to
the client ciphertext limits.

Run the internal module tests with:

```sh
cd services/extensions
go test -race ./internal/modules/remotecontrol -count=1
```

These tests use the real extension host and a controlled Core interface double
to check reauthorization, refusals, envelope validation, owner isolation,
expiry, generation, capacity and concurrency. They are **not** acceptance against
an actual Rust identity store, public browser authentication or plugin OAuth.

## Internal SSE foundation and new plugin transport

The foundation adds `GET /pi/sessions/{id}/events?after=N&generation=G`.
This is **internal-only source**, not a registered or deployed route. The new
plugin expects the future public
`GET /api/remote-control/v1/pi/sessions/{id}/events` adapter. That adapter is
absent. The historical site's documented HTTP relay does **not** establish that
it has an SSE endpoint. Plugin transport checks must not claim public readiness
or silently fall back to message polling.

The handshake requires extension-host service authentication plus the separate
native personal-session credential checked live by Core. Authentication precedes
session existence checks; malformed, revoked, cross-owner and non-session
credentials receive ordinary JSON status errors before SSE headers. This is not
an OAuth-to-native-session or OAuth-to-model/host-key translation. Core-owned
plugin OAuth consent, scope, revocation and a reviewed public adapter are still
required before the plugin can use this foundation.

A successful response is `text/event-stream`, `Cache-Control: no-store`, and
`X-Accel-Buffering: no`. It first sends `event: ready`; subsequent message pushes
use `event: messages`. Both have JSON data with exactly the shape
`{"generation":"opaque_identifier","messages":[...]}`. Each batch contains at
most 128 existing v1 opaque envelopes, with only `nonce`, `ciphertext`,
`sequence`, `sender` and `created_at`; there is no PIN, credential or decrypted
body. Empty batches are arrays. The browser's existing HTTP message endpoints
remain unchanged for compatibility.

`after` is optional (default zero) and must be decimal digits in the JavaScript
safe range 0–9007199254740991. `generation` is optional and, if supplied, must be
an 8–64-character opaque ASCII identifier. Duplicate or unknown query keys and
invalid encoding are rejected. A missing/different generation resets catch-up
to retained history after zero and returns the fresh generation in `ready`.
An impossible future cursor also resets to retained history. On the same
generation, catch-up returns retained messages strictly after the cursor; rolled
history may start beyond the next expected sequence. A client must detect such
gaps and resynchronize its current encrypted state, not infer that evicted
commands were delivered. Reconnect with the last generation and highest received
sequence; reset the cursor when `ready` changes generation. Only retained
history is recoverable.

The stream is driven by relay change-channel broadcasts, not periodic message
reads. Append, heartbeat, deletion and expiry wake readers; snapshot and wake
channel capture share the relay mutex to prevent lost wake-ups. Idle streams
use comment keepalives and an expiry timer. No worker/timer exists for an
inactive session, and construction starts no background activity.

Every outgoing batch and keepalive gets a fresh bounded Core RPC and must retain
both the user and personal-account owner. Idle rechecks/comments have a maximum
15-second window (the normal wait is seven seconds, reserving three seconds for
RPC and five for slow writes). A stream has a hard 60-second request-context
lease. Request cancellation or a canceled host-supplied `BaseContext` stops it;
ordinary `http.Server.Shutdown` alone does not cancel an active stream. Host
assembly must supply and cancel the drain context. Every write/flush has a
ResponseController deadline of at most five seconds, bounded further by the
lease; it is cleared on exit for subsequent keepalive HTTP requests.

After SSE opens, revocation, a refused principal or owner change sends
`event: error` with `{"code":"unauthorized"}` and closes. Temporary Core failure,
missing/deleted/expired sessions use `{"code":"remote_unavailable"}` and close,
without a later ciphertext batch. Cancellation, lease expiry or failed writes
close the transport; a peer that cannot read may not receive an error frame.
Keepalives, reads, successful pushes and capacity rejection **never** renew the
two-minute session TTL; only the plugin's separate `PUT` heartbeat does.

Admission is bounded to **two streams per user and four globally**. A rejected
stream gets JSON HTTP 429 and `Retry-After: 1` before headers; every exit releases
its quota exactly once. The existing extension host additionally admits only
eight total in-flight requests per module and can return 503 `module_busy`.
Keeping SSE below four leaves four host slots for heartbeat, commands and
other short requests; it does not remove that host admission bound or guarantee
capacity under unrelated load. This foundation is intentionally small and
process-local. Public OAuth/Core authorization, adapter implementation,
proxy buffering/timeouts and affinity, drain wiring, server deployment and real
end-to-end acceptance remain promotion blockers.

The module race tests use actual HTTP servers and the real extension host, with
an explicitly controlled Core interface double. They cover push/catch-up,
resume/rolled history, wake capture, owner isolation, live refusals and timeout,
quotas while PUT/POST/DELETE remain available, expiry/deletion, cancellation,
lease, actual TCP slow-reader deadlines and deadline restoration. They are not
acceptance against a real Rust identity store or the absent public OAuth route.

## Interactive client contract

Install/update the LMM Pi extension, run `/login lmm` and explicitly approve remote control. Run `/lmm-remote on` locally once. Future interactive sessions reconnect until `/lmm-remote off`; `/lmm-remote status` shows the current PIN only in the local UI. Model calls can use any other provider. This is not an anonymous or zero-consent remote shell.

The plugin generates a fresh 128-bit PIN and session ID. PINs and decrypted messages remain in client memory. No PIN is included in a URL or session/model history. The persisted local setting contains only `enabled`.

Heartbeat replies include an opaque `generation`. When it changes after relay restart/expiry, the plugin resets its read cursor. `DELETE /sessions/:session_id` removes only the caller's session and is idempotent. Disconnect falls back to the two-minute TTL. Multi-instance deployments still need session affinity because the relay is process-local.

Controller plaintext is `{version:1,type:"command",id,issued_at,action,...}`. IDs are unique 8–64-character ASCII identifiers. `issued_at` is Unix milliseconds and expires after two minutes. Authenticated sender AAD is `controller`.

- `prompt`: `content` (at most 32,000 UTF-8 bytes), optional `delivery` (`followUp` or `steer`). Does not expand slash commands, skills or prompt templates. Clients advertising `prompt_images` also accept one `images` entry: `{type:"image",mimeType:"image/jpeg",data:"<canonical standard base64>"}`. Text may be empty only with a valid image. The JPEG is bounded to 24 KiB and the entire command to 48 KiB of UTF-8 JSON. The browser accepts PNG/JPEG/WebP files up to 10 MiB, strips metadata and resizes locally to fit; it does not send the original file or silently discard attachments. This is compressed single-image input, not original-resolution or multi-file transfer. Unsupported Pi models reject the image before a model turn.
- `abort`: stops Pi and cancels active UI prompts. The receive loop never waits for a model turn or an unanswered prompt.
- `ui_response`: active `request_id`, `value` (selection index, boolean or text), or `cancelled:true`. Indices preserve original option values, including duplicate or shortened display labels.
- `ui_input`: active `request_id` and exactly one allow-listed `key` or plain `text`. Text uses bracketed paste; arbitrary escape/control sequences are rejected.

The plugin returns encrypted `ack` events with `command_id`, `ok` and an optional error. This confirms command acceptance, not task completion. The browser waits up to 20 seconds and never silently retries an uncertain command. Encrypted `state` events carry `busy`, `provider`, `model` and up to four pending `requests`; these replace previous state and are not conversation messages. Older messages with the same ID are replaced by the latest snapshot. The page disables controls when its last state is over 45 seconds old.

Standard Pi `select`, `confirm` and `input` prompts are answered directly. Public `custom` UI and the editor run their original component, with a bounded plain-text terminal preview and keyboard controls. This supports `pi-ask-user` without depending on the tool name or fabricating its result. Terminal previews are bounded, not a pixel-perfect terminal, mouse/clipboard/file-upload interface, or a guarantee that every third-party component is compatible. Local and remote answers race safely; stale question IDs are rejected. Turning remote control off must not break completion of a local dialog already open.

Locking/unmounting the browser session view cancels requests, removes decrypted
query data and discards the key. Account/login-session changes remount the
workspace and clear its account-scoped caches. Relay generation or device
changes require unlocking again; expired sessions are removed locally even
before the next list poll. It does not stop Pi or disable sharing on the local
machine.

A successful poll with unchanged data does not renew an old state heartbeat:
the browser has an independent expiry clock and rechecks the signed timestamp
at dispatch. Unsigned outer relay timestamps cannot renew a state. The browser
rejects noncanonical base64url and ciphertext exceeding the 16 KiB metadata /
64 KiB message limits before decryption. Session/message lists are bounded to
32/128 entries.

## Production promotion gates

The following are required; the current client checks do not close the missing
WIP server contracts:

- Core owns plugin OAuth identity, scopes, consent, expiry and revocation. Go
  must not substitute its service credential or model-spending authority for
  user authorization. Existing grants must not silently gain `remote:control`.
- The relay must validate current browser/plugin credentials and isolate owners
  on every endpoint, including guessed IDs, cursor reads, deletion and reconnect.
  Verify refusals against actual isolated Core/Go processes, not a fixed-success
  HTTP adapter.
- Verify real browser ↔ relay ↔ Pi image/control delivery, cancellation, duplicate
  commands, expired sessions and process restart under the intended proxy and
  affinity topology. Local encryption/SDK fixtures do not test production OAuth.
- Qualify the selected artifacts and installation procedure, protect the current
  site's database and credentials, and prepare verified local rollback material
  before an authorized production switch. WIP fresh-install examples are not an
  existing-site upgrade procedure.

## Reproducible checks

Historical backend check (old monolith layout; these packages are not present in
the current `services/extensions` host): `cd apps/lmm-extensions && go test ./controller ./router -run 'TestPiRemote|TestOAuthHTTPDiscovery' -count=1`.

Browser protocol and account-isolation checks: `cd apps/web && bun test src/features/remote-control/*.test.ts src/features/remote-control/*.test.tsx`.

Plugin image acceptance: in the Pi plugin repository, run `npm test` and
`npm run test:pi`. `test/remote-images.test.mjs` exercises the official Pi SDK,
encrypted loopback relay and a synthetic vision/text-only model. It makes no
production account, model call or billing assertion.

After building Web and initializing the pinned `packages/pi-lmm-provider`
submodule, run the full-page safety review with an already installed Playwright
module and Chromium. The script starts/stops its own loopback server and retains
screenshots/results in a unique temporary directory:

```sh
BUN_BIN="$(command -v bun)" \
PLAYWRIGHT_MODULE=/absolute/path/to/playwright/index.mjs \
CHROME_PATH=/absolute/path/to/chromium \
node apps/web/scripts/remote-control-review/safety-check.mjs
```

This checks the actual page's image controls, account-cache clearing, restart
relocking and heartbeat expiry. Its users and relay are synthetic: it does not
prove Core/Go authorization, production login or multi-instance deployment.

The older browser review fixture imports the production controls, encryption and command hook. Its test-only HTTP adapter connects to the plugin repository's loopback relay and official Pi SDK acceptance test. It does not sign in to production, spend model credits, or replace production authentication checks. Real OAuth consent/revocation/account isolation are tested separately against the Go routes.
