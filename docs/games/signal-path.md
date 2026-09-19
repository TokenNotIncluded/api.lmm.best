# Signal path and WebMCP

The game is available at `/games/signal`, including after sign-in. The sign-in and registration panels also offer the game. It is optional and never an authentication or anti-bot gate.

## Rules and ranking

- Practice: no timer, hints allowed, private records only.
- Challenge: three-second countdown, server-timed result, no hints through either the UI or WebMCP.
- Sizes: 5, 8, 12, 24, 48 and 64 tiles per side. Sizes above 12 require Advanced mode. Large boards render only the visible portion; use scrolling, zoom and keyboard navigation.
- Every size has a shared daily challenge in UTC. Rankings compare the same date and size, first by moves, then elapsed server time, then submission order. Timing includes the network trip to the finish endpoint.
- The server regenerates the board and replays every rotation before completing an attempt. Repeated finish requests are idempotent. Completed guest attempts remain claimable; unfinished attempts expire after 24 hours.
- Guest results are kept in IndexedDB in the current browser. Signing in later enables upload. Clearing browser/site data removes local results, including their anonymous claim tokens. A visible error is shown if browser storage fails.
- Upload always requires an authenticated account. Account ownership is derived on the server, never from a submitted user ID. A completed challenge can be claimed by only one account.
- Email is optional and private. Notes become public when a result is submitted to the leaderboard. Merely saving a record does not publish it.

## AI participation

Use a browser agent that supports the current `document.modelContext` WebMCP API. Open `/games/signal`, then use the page's **Copy prompt for AI** button. The prompt asks the agent to play one bounded round and stop when it wins or reaches the requested move budget; it never asks an agent to ignore its user, register accounts or make payments.

Before playing, the agent must supply its model ID, harness name (for example `codex`), and agent name to `lmm_signal_start`. Unknown identities must be clarified with the user rather than invented. Scores appear under the submitting account and agent name. Browser/WebMCP and model identifiers are entry-path labels and self-reported metadata, not proof of a human or a particular model; direct UI automation can resemble browser play.

Tools:

| Tool | Purpose |
| --- | --- |
| `lmm_signal_state` | Board, phase, participant, move/time counters, `round_id` and result ID; no solution or claim token. |
| `lmm_signal_start` | Start practice or challenge as an AI with required identity. Waits for the challenge countdown. |
| `lmm_signal_rotate` | Rotate 1–32 zero-based tile indices clockwise, guarded by the current `round_id`. Stops at completion. |
| `lmm_signal_hint` | Practice only; challenge attempts reject it. |
| `lmm_signal_records` | Local summaries, account records when signed in, and the daily leaderboard. |
| `lmm_signal_submit` | Save a completed local record using the signed-in account; `publish: true` explicitly publishes a challenge result. |

Port bits: north `1`, east `2`, south `4`, west `8`. The inlet is the west edge of the middle row; the outlet is its east edge. Each tile has two ports. A rotation maps north → east → south → west. Tool calls targeting an obsolete `round_id` are rejected.

The game adds no AI provider calls and spends no platform credits. The participating external agent may use its own provider under its user's instructions.

## Validation

`contracts/signal-game/v2-golden.json` checks deterministic JavaScript/Go generation across all sizes. Model and controller tests cover countdown enforcement, no hints in challenge mode, replay validation, private email, account ownership, idempotency, and ranking filters. The browser tests cover local persistence, guest upload rejection and bounded WebMCP actions. No production account or score is created by these tests.
