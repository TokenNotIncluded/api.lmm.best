/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { isRedirect } from '@tanstack/react-router'

import { AuthBootstrapPendingError } from '@/lib/auth-bootstrap-navigation'
import { getBuildRevision } from '@/lib/build-metadata'

export type DiagnosticKind =
  | 'http_error'
  | 'business_error'
  | 'http_response'
  | 'transport_error'
  | 'unexpected_error'

export type ErrorDiagnostics = Readonly<{
  occurred_at_utc: string
  build_id: string
  route_template: string
  local_event_id: string
  http_status: number | null
  kind: DiagnosticKind
}>

// Read only the compile-time revision, never DOM metadata or browser storage.
const revision = getBuildRevision()
const BUILD_ID = /^[a-zA-Z0-9][a-zA-Z0-9._+-]{0,95}$/.test(revision)
  ? revision
  : 'unknown'

/** Accessors and hostile proxies cannot make projection read an error payload. */
function ownData(value: unknown, key: string): unknown {
  if (typeof value !== 'object' || value === null) return undefined
  try {
    const descriptor = Object.getOwnPropertyDescriptor(value, key)
    return descriptor && 'value' in descriptor ? descriptor.value : undefined
  } catch {
    return undefined
  }
}

export function isNonFailureError(error: unknown): boolean {
  try {
    if (isRedirect(error) || error instanceof AuthBootstrapPendingError) {
      return true
    }
    if (typeof DOMException !== 'undefined' && error instanceof DOMException) {
      if (error.name === 'AbortError') return true
    }
  } catch {
    // A hostile value is unknown, never a reason to inspect its message.
  }
  return (
    ownData(error, '__CANCEL__') === true ||
    ownData(error, 'code') === 'ERR_CANCELED'
  )
}

export function classifyError(error: unknown): {
  http_status: number | null
  kind: DiagnosticKind
} {
  const response = ownData(error, 'response')
  const status = ownData(response, 'status')
  if (
    typeof status === 'number' &&
    Number.isInteger(status) &&
    status >= 100 &&
    status <= 599
  ) {
    return {
      http_status: status,
      kind:
        status >= 400
          ? 'http_error'
          : ownData(ownData(response, 'data'), 'success') === false
            ? 'business_error'
            : 'http_response',
    }
  }
  const code = ownData(error, 'code')
  return {
    http_status: null,
    kind:
      code === 'ERR_NETWORK' || code === 'ETIMEDOUT' || code === 'ECONNABORTED'
        ? 'transport_error'
        : 'unexpected_error',
  }
}

/** Call only with TanStack's matches and static routesById, never a URL. */
export function diagnosticRouteTemplate(
  matches: ReadonlyArray<{ routeId?: unknown }>,
  routesById: object
): string {
  const routeId = ownData(matches.at(-1), 'routeId')
  try {
    return typeof routeId === 'string' &&
      routeId.length <= 256 &&
      /^[A-Za-z0-9_/$().-]+$/.test(routeId) &&
      Object.hasOwn(routesById, routeId)
      ? routeId
      : 'unknown'
  } catch {
    return 'unknown'
  }
}

/** Local correlation only; this ID is not a server request ID. No weak fallback. */
export function createErrorDiagnostics(
  error: unknown,
  matches: ReadonlyArray<{ routeId?: unknown }>,
  routesById: object,
  entropy: Pick<Crypto, 'getRandomValues'> | undefined = globalThis.crypto
): ErrorDiagnostics | null {
  if (isNonFailureError(error)) return null
  try {
    if (!entropy?.getRandomValues) return null
    const bytes = new Uint8Array(16)
    entropy.getRandomValues(bytes)
    const localEventId = Array.from(bytes, (byte) =>
      byte.toString(16).padStart(2, '0')
    ).join('')
    return Object.freeze({
      occurred_at_utc: new Date().toISOString(),
      build_id: BUILD_ID,
      route_template: diagnosticRouteTemplate(matches, routesById),
      local_event_id: localEventId,
      ...classifyError(error),
    })
  } catch {
    return null
  }
}

/** Explicit clipboard write only. No logs, storage, fallback or network. */
export async function copyErrorDiagnostics(
  diagnostics: ErrorDiagnostics,
  clipboard: Pick<Clipboard, 'writeText'> | undefined
): Promise<boolean> {
  try {
    if (!clipboard?.writeText) return false
    const result: unknown = await clipboard.writeText(
      JSON.stringify(
        {
          occurred_at_utc: diagnostics.occurred_at_utc,
          build_id: diagnostics.build_id,
          route_template: diagnostics.route_template,
          local_event_id: diagnostics.local_event_id,
          http_status: diagnostics.http_status,
          kind: diagnostics.kind,
        },
        null,
        2
      )
    )
    return result !== false
  } catch {
    return false
  }
}
