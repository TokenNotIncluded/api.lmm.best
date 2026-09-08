/*
Copyright (C) 2026 LIghtJUNction
*/

export type DrawingRequestErrorKind =
  | 'unauthenticated'
  | 'forbidden'
  | 'unavailable'
  | 'network'
  | 'http'

export function getDrawingRequestStatus(error: unknown): number | null {
  if (typeof error !== 'object' || error === null) return null
  const response = (error as { response?: unknown }).response
  if (typeof response !== 'object' || response === null) return null
  const status = (response as { status?: unknown }).status
  return typeof status === 'number' && Number.isInteger(status) ? status : null
}

export function getDrawingRequestErrorKind(
  error: unknown
): DrawingRequestErrorKind {
  const status = getDrawingRequestStatus(error)
  if (status === 401) return 'unauthenticated'
  if (status === 403) return 'forbidden'
  if (status !== null && status >= 500) return 'unavailable'
  if (status === null) return 'network'
  return 'http'
}

function record(value: unknown): Record<string, unknown> | null {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null
}

function message(value: unknown): string | null {
  return typeof value === 'string' ? value.trim() || null : null
}

export function getDrawingRequestErrorMessage(
  error: unknown,
  fallback: string
): string {
  const response = record(record(error)?.response)
  const data = record(response?.data)
  // Only structured error fields are displayable; gateways may return HTML.
  // The caller renders this as text, never as HTML or Markdown.
  return (
    message(record(data?.error)?.message) ?? message(data?.message) ?? fallback
  )
}
