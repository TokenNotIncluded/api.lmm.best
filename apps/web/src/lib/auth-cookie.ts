/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import axios from 'axios'
import { t } from 'i18next'

export const AUTH_COOKIE_LOCK = 'new-api:auth-refresh'
// A dispatched writer keeps its lock until transport completion. A finite
// Axios timeout turns a stalled transport into explicit uncertain recovery.
export const AUTH_COOKIE_TIMEOUT_MS = 10_000
const BLOCKED_KEY = 'new-api:auth-cookie:uncertain'
const PROBE_KEY = 'new-api:auth-cookie:storage-probe'
export const authCookieRecoveryMessage =
  'Sign-in could not be completed safely. Close all tabs for this site, clear this site’s cookies and site data in your browser settings, then open this site and sign in again.'
export const authCookieStorageMessage =
  'Sign-in needs browser storage. Allow this site to save site data in your browser settings, then reload and try again.'

type BlockedState = 'uncertain' | 'storage_unavailable' | null
let blocked: BlockedState = null
const listeners = new Set<() => void>()
let localOperations = 0
let reloadingDocument = false
const idleWaiters = new Set<() => void>()

/** Includes queued acquisition and management's out-of-lock refresh phases. */
export async function withAuthCookieActivity<T>(
  operation: () => Promise<T>
): Promise<T> {
  if (reloadingDocument) {
    throw new axios.CanceledError('Authentication document is reloading')
  }
  localOperations++
  try {
    return await operation()
  } finally {
    localOperations--
    if (localOperations === 0) {
      for (const resolve of idleWaiters) resolve()
      idleWaiters.clear()
    }
  }
}

/** Never reload an old document while its queued writer can still acquire. */
export async function waitForAuthCookieIdle(): Promise<void> {
  while (localOperations > 0) {
    await new Promise<void>((resolve) => idleWaiters.add(resolve))
  }
}

export function isAuthCookieIdle(): boolean {
  return localOperations === 0
}

/** Fence observers/timers between requesting reload and realm destruction. */
export function beginAuthCookieReload(): boolean {
  if (!isAuthCookieIdle() || reloadingDocument) return false
  reloadingDocument = true
  return true
}

function coordinated(): boolean {
  return (
    globalThis.isSecureContext === true &&
    typeof navigator !== 'undefined' &&
    Boolean(navigator.locks)
  )
}

function notify(): void {
  for (const listener of listeners) listener()
}

export function getAuthCookieBlockedState(): BlockedState {
  if (typeof window === 'undefined') return blocked
  try {
    if (window.localStorage.getItem(BLOCKED_KEY)?.startsWith('uncertain:')) {
      blocked = 'uncertain'
    }
  } catch {
    if (coordinated() && blocked !== 'uncertain') {
      blocked = 'storage_unavailable'
    }
  }
  return blocked
}

export function subscribeAuthCookieState(listener: () => void): () => void {
  listeners.add(listener)
  const onStorage = (event: StorageEvent) => {
    if (event.key === BLOCKED_KEY || event.key === null) {
      getAuthCookieBlockedState()
      notify()
    }
  }
  window.addEventListener('storage', onStorage)
  return () => {
    listeners.delete(listener)
    window.removeEventListener('storage', onStorage)
  }
}

export class AuthCookieBlockedError extends Error {
  readonly code = 'AUTH_COOKIE_BLOCKED'
  constructor(state: Exclude<BlockedState, null>) {
    super(
      t(
        state === 'uncertain'
          ? authCookieRecoveryMessage
          : authCookieStorageMessage
      )
    )
    this.name = 'AuthCookieBlockedError'
  }
}

function assertAvailable(checkStorage: boolean): void {
  const state = getAuthCookieBlockedState()
  if (state) throw new AuthCookieBlockedError(state)
  try {
    if (
      typeof window !== 'undefined' &&
      window.localStorage.getItem(BLOCKED_KEY)
    ) {
      blocked = 'uncertain'
      notify()
      throw new AuthCookieBlockedError(blocked)
    }
    if (!checkStorage) return
    window.localStorage.setItem(PROBE_KEY, '1')
    window.localStorage.removeItem(PROBE_KEY)
  } catch {
    if (!checkStorage && blocked !== 'uncertain') return
    if (blocked === 'uncertain') throw new AuthCookieBlockedError(blocked)
    blocked = 'storage_unavailable'
    notify()
    throw new AuthCookieBlockedError(blocked)
  }
}

function markUncertain(intent: string): void {
  blocked = 'uncertain'
  try {
    // No identity, credentials or timeout-based expiry. Other tabs and reloads
    // must retain the recovery boundary until site data is explicitly cleared.
    window.localStorage.setItem(BLOCKED_KEY, `uncertain:${intent}`)
  } catch {
    // The current tab remains blocked even if storage becomes unavailable
    // after the preflight probe. Other-tab persistence then is not guaranteed.
  }
  notify()
}

export interface AuthCookieTransaction {
  request: <T>(dispatch: () => Promise<T>) => Promise<T>
}

/** The callback includes response validation and the client state commit. */
export function withAuthCookieLock<T>(
  operation: (transaction: AuthCookieTransaction) => Promise<T>,
  signal?: AbortSignal
): Promise<T> {
  return withAuthCookieActivity(() => acquireAuthCookieLock(operation, signal))
}

async function acquireAuthCookieLock<T>(
  operation: (transaction: AuthCookieTransaction) => Promise<T>,
  signal?: AbortSignal
): Promise<T> {
  const ordered = coordinated()
  const run = async () => {
    assertAvailable(ordered)
    let intent: string | undefined
    let uncertain = false
    try {
      return await operation({
        request: async (dispatch) => {
          if (ordered && !intent) {
            intent = globalThis.crypto.randomUUID()
            try {
              window.localStorage.setItem(BLOCKED_KEY, `pending:${intent}`)
              if (
                window.localStorage.getItem(BLOCKED_KEY) !== `pending:${intent}`
              ) {
                throw new Error('Storage write failed')
              }
            } catch {
              blocked = 'storage_unavailable'
              notify()
              throw new AuthCookieBlockedError(blocked)
            }
          }
          try {
            return await dispatch()
          } catch (error) {
            if (
              ordered &&
              axios.isAxiosError(error) &&
              !error.response &&
              ['ERR_NETWORK', 'ECONNABORTED', 'ETIMEDOUT'].includes(
                error.code ?? ''
              )
            ) {
              uncertain = true
              markUncertain(intent ?? 'transport')
              throw new AuthCookieBlockedError('uncertain')
            }
            throw error
          }
        },
      })
    } finally {
      if (ordered && intent && !uncertain) {
        try {
          if (
            window.localStorage.getItem(BLOCKED_KEY) === `pending:${intent}`
          ) {
            window.localStorage.removeItem(BLOCKED_KEY)
          }
        } catch {
          blocked = 'storage_unavailable'
          notify()
        }
      }
    }
  }
  // Compatibility fallback remains unordered. An existing recovery marker
  // still blocks participating clients; old clients/provider callbacks do not.
  if (!ordered) {
    if (signal?.aborted) {
      throw new axios.CanceledError('Authentication cancelled')
    }
    return run()
  }
  return navigator.locks.request(
    AUTH_COOKIE_LOCK,
    { mode: 'exclusive', signal },
    run
  )
}
