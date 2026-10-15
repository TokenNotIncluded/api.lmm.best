/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */

const MAX_POSITIVE_I64 = 9223372036854775807n

/** Only already-exact legacy numbers may cross into the string-only v2 wire. */
export function legacyUserIDsToStrings(
  ids: readonly unknown[]
): string[] | undefined {
  for (const id of ids) {
    if (
      typeof id !== 'number' ||
      !Number.isSafeInteger(id) ||
      id <= 0 ||
      BigInt(id) > MAX_POSITIVE_I64
    ) {
      return undefined
    }
  }
  return ids.map((id) => String(id))
}
