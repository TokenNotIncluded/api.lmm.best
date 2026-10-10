/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */

export const CATALOGUE_ID_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/

export function parseMarketServiceID(value: unknown): string | undefined {
  return typeof value === 'string' && CATALOGUE_ID_PATTERN.test(value)
    ? value
    : undefined
}
