# Pi Remote Control API v1

`/api/remote-control/v1/pi` is an authenticated, ephemeral relay for a Pi
plugin and its browser controller. It stores data only in the current API
process memory. Restarting the API or reaching the two-minute heartbeat TTL
removes a session and its messages.

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


## Interactive client contract

Install/update the LMM Pi extension, run `/login lmm` and explicitly approve remote control. Run `/lmm-remote on` locally once. Future interactive sessions reconnect until `/lmm-remote off`; `/lmm-remote status` shows the current PIN only in the local UI. Model calls can use any other provider. This is not an anonymous or zero-consent remote shell.

The plugin generates a fresh 128-bit PIN and session ID. PINs and decrypted messages remain in client memory. No PIN is included in a URL or session/model history. The persisted local setting contains only `enabled`.

Heartbeat replies include an opaque `generation`. When it changes after relay restart/expiry, the plugin resets its read cursor. `DELETE /sessions/:session_id` removes only the caller's session and is idempotent. Disconnect falls back to the two-minute TTL. Multi-instance deployments still need session affinity because the relay is process-local.

Controller plaintext is `{version:1,type:"command",id,issued_at,action,...}`. IDs are unique 8–64-character ASCII identifiers. `issued_at` is Unix milliseconds and expires after two minutes. Authenticated sender AAD is `controller`.

- `prompt`: `content` (at most 32,000 UTF-8 bytes), optional `delivery` (`followUp` or `steer`). Does not expand slash commands, skills or prompt templates.
- `abort`: stops Pi and cancels active UI prompts. The receive loop never waits for a model turn or an unanswered prompt.
- `ui_response`: active `request_id`, `value` (selection index, boolean or text), or `cancelled:true`. Indices preserve original option values, including duplicate or shortened display labels.
- `ui_input`: active `request_id` and exactly one allow-listed `key` or plain `text`. Text uses bracketed paste; arbitrary escape/control sequences are rejected.

The plugin returns encrypted `ack` events with `command_id`, `ok` and an optional error. This confirms command acceptance, not task completion. The browser waits up to 20 seconds and never silently retries an uncertain command. Encrypted `state` events carry `busy`, `provider`, `model` and up to four pending `requests`; these replace previous state and are not conversation messages. Older messages with the same ID are replaced by the latest snapshot. The page disables controls when its last state is over 45 seconds old.

Standard Pi `select`, `confirm` and `input` prompts are answered directly. Public `custom` UI and the editor run their original component, with a bounded plain-text terminal preview and keyboard controls. This supports `pi-ask-user` without depending on the tool name or fabricating its result. Terminal previews are bounded, not a pixel-perfect terminal, mouse/clipboard/file-upload interface, or a guarantee that every third-party component is compatible. Local and remote answers race safely; stale question IDs are rejected. Turning remote control off must not break completion of a local dialog already open.

Locking/unmounting the production session view cancels requests, removes decrypted query data and discards the key. It does not stop Pi or disable sharing on the local machine.

## Reproducible checks

Backend: `cd apps/api-go && go test ./controller ./router -run 'TestPiRemote|TestOAuthHTTPDiscovery' -count=1`.

Browser protocol: `cd apps/web && bun test src/features/remote-control/*.test.ts`.

The browser review fixture imports the production controls, encryption and command hook. Its test-only HTTP adapter connects to the plugin repository's loopback relay and official Pi SDK acceptance test. It does not sign in to production, spend model credits, or replace production authentication checks. Real OAuth consent/revocation/account isolation are tested separately against the Go routes.
