/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { isAxiosError } from 'axios'
import i18next from 'i18next'

/** Refresh failures retain the HTTP error as their cause. */
export function isRateLimitedError(error: unknown): boolean {
  return rateLimitedError(error) !== null
}

function rateLimitedError(error: unknown) {
  for (let depth = 0; depth < 5; depth++) {
    if (isAxiosError(error) && error.response?.status === 429) return error
    if (!(error instanceof Error)) return null
    error = error.cause
  }
  return null
}

/** Retry-After allows integer delay seconds or an HTTP date. */
export function rateLimitWaitSeconds(
  error: unknown,
  now = Date.now()
): number | null {
  const limited = rateLimitedError(error)
  if (!limited) return null
  const headers = limited.response?.headers
  const raw = headers?.['retry-after'] ?? headers?.['Retry-After']
  if (typeof raw !== 'string' && typeof raw !== 'number') return null
  const text = String(raw).trim()
  if (/^\d+$/.test(text)) {
    const seconds = Number(text)
    return Number.isSafeInteger(seconds) ? seconds : null
  }
  if (!/GMT$/i.test(text)) return null
  const date = Date.parse(text)
  return Number.isFinite(date)
    ? Math.max(0, Math.ceil((date - now) / 1000))
    : null
}

export function rateLimitMessage(error: unknown): string | null {
  if (!isRateLimitedError(error)) return null
  const seconds = rateLimitWaitSeconds(error)
  return seconds !== null && seconds > 0
    ? i18next.t('Too many requests. Please try again in {{seconds}} seconds.', {
        seconds,
      })
    : i18next.t('Too many requests. Please wait before trying again.')
}
