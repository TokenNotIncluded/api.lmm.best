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

All endpoints require normal user authentication. Sessions are keyed by both
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
