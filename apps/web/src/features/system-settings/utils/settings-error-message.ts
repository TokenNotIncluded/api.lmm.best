/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import i18next from 'i18next'

import { getServerErrorMessageKey } from '@/lib/server-error-message'

function record(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null
}

/** Keep a recognized public error or the server's reason before Axios's generic status. */
export function getSettingsErrorMessage(
  error: unknown,
  fallback: string
): string {
  const key = getServerErrorMessageKey(error)
  if (key) return i18next.t(key)
  const response = record(record(error)?.response)
  const payload = record(response?.data) ?? record(error)
  for (const message of [
    payload?.message,
    record(payload?.error)?.message,
    record(error)?.message,
  ]) {
    if (typeof message === 'string' && message.trim()) return message
  }
  return fallback
}
