/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */

/** Preserve issued credentials; only legacy bare API secrets need the sk- prefix. */
export function formatApiKey(key: string): string {
  return key.startsWith('sk-') || key.startsWith('lmmk_') ? key : `sk-${key}`
}

// Core issues 32 random bytes as 64 lowercase hex digits, including derived sessions.
// Reject partial matches inside longer identifiers and leave ordinary labels intact.
export const NATIVE_CREDENTIAL_PATTERN =
  /(?<![A-Za-z0-9_-])lmm[ksi]_[a-f0-9]{64}(?![A-Za-z0-9_-])/g
