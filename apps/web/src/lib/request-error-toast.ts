/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object'
}

/** Repeated HTTP errors update one toast without hiding different failures. */
export function getRequestErrorToastId(error: unknown): string | undefined {
  if (!isRecord(error) || !isRecord(error.response)) return
  const { status, data } = error.response
  if (
    typeof status !== 'number' ||
    !Number.isInteger(status) ||
    status < 400 ||
    status > 599
  ) {
    return
  }

  const message = (isRecord(data) && data.message) || error.message
  if (typeof message !== 'string' || !message.trim()) return
  const code = isRecord(data)
    ? typeof data.code === 'string'
      ? data.code
      : isRecord(data.error) && typeof data.error.code === 'string'
        ? data.error.code
        : null
    : null

  // No timer or request cache: Sonner owns the notification's lifetime.
  return JSON.stringify(['request-error', status, code, message])
}
