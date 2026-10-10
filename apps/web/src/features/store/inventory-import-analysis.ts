/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */

export function analyzeInventoryItems(items: readonly string[]) {
  // Card values can contain meaningful whitespace or differ only in case.
  // Compare their exact text and keep the first occurrence unchanged.
  const unique = [...new Set(items)]
  return { count: items.length, repeated: items.length - unique.length, unique }
}
