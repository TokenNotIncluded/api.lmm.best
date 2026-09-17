# Moderation evidence validation

The moderation editor and API use the same existing limit: **1–1000 UTF-8 bytes after Go-compatible Unicode whitespace trimming**, not 1000 characters. The editor displays a byte counter, marks invalid input accessibly, and leaves it editable. It does not truncate evidence. The HTML character limit remains only a coarse editor bound.

334 Chinese characters or 251 four-byte emoji exceed this limit. Combining sequences are counted as stored bytes, without NFC/NFKC conversion. Frontend trimming follows Unicode White_Space to agree with Go strings.TrimSpace; JavaScript trim differs at U+0085 and U+FEFF. Invalid surrogate input is rejected in the editor, and NUL/invalid UTF-8 are rejected at the API validation boundary before database work.

The retained-request helper validates and normalizes evidence before freezing it. Invalid local input is not retained. Once a valid request has been submitted, network errors and application errors continue to reuse the same immutable request ID and evidence. This fix does not authorize a new request after an ambiguous server response, change moderation permissions, change rewards, or alter balances.

`contracts/referral-evidence.json` is the shared frontend/Go boundary fixture. The Go controller regression also verifies that rejected input does not consume the request ID, disable the account, or change purchased quota; corrected evidence at exactly 1000 bytes can be submitted once and retried unchanged.

No migration, version bump, workflow, production request, or deployment is included. Full repository checks and pre-release payment/refund/upgrade exercises remain separate requirements.
