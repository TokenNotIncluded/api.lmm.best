/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import type { QueryClient } from '@tanstack/react-query'
import axios, { type AxiosAdapter } from 'axios'
import { t } from 'i18next'

import {
  AUTH_COOKIE_TIMEOUT_MS,
  withAuthCookieActivity,
  withAuthCookieLock,
  type AuthCookieTransaction,
} from '@/lib/auth-cookie'
import { publishAuthSessionEvent } from '@/lib/auth-session-sync'
import {
  isRateLimitedError,
  rateLimitWaitSeconds,
} from '@/lib/request-rate-limit'
import {
  useAuthStore,
  type AuthBootstrapState,
  type AuthBundle,
  type AuthUser,
  type LoginSession,
} from '@/stores/auth-store'

export type RefreshOutcome =
  | { kind: 'authenticated'; bundle: AuthBundle }
  | { kind: 'anonymous' }
  | { kind: 'transient_error'; error: unknown }
  | { kind: 'out_of_sync'; code?: string }

export interface AuthRefreshHTTPResponse {
  status: number
  data?: unknown
  error?: unknown
}

export interface AuthRefreshRuntime {
  request: (expectedSID?: string) => Promise<AuthRefreshHTTPResponse>
  getExpectedSID: () => string | undefined
  parseBundle: (value: unknown) => AuthBundle | null
  acceptBundle: (bundle: AuthBundle) => void
  clear: (synchronizeTabs: boolean, bootstrapState?: AuthBootstrapState) => void
  markTransient: () => void
  wait: (delay: number) => Promise<void>
  isCurrent?: () => boolean
}

export interface AuthTokenRotation {
  access_token: string
  token_type: string
  access_expires_at: number
  session: LoginSession
}

export class AuthRotationError extends Error {
  constructor(message: string) {
    super(message)
    this.name = 'AuthRotationError'
  }
}

const authClient = axios.create({
  baseURL: '',
  withCredentials: true,
  headers: {
    'Cache-Control': 'no-store',
  },
})

/** Installs the isolated refresh transport used by the development persona lab. */
export function setDevelopmentAuthRefreshAdapter(adapter: AxiosAdapter): void {
  authClient.defaults.adapter = adapter
}

const refreshRaceDelays = [80, 200, 500] as const
let refreshPromise: Promise<RefreshOutcome> | null = null
type AuthenticationGeneration = {
  authEpoch: number
  accountWriteEpoch: number
}
type RefreshAccountWrite = {
  before?: AuthenticationGeneration
  after?: AuthenticationGeneration
}
let refreshAccountWrite: RefreshAccountWrite | null = null
let refreshCooldown: {
  epoch: number
  sid: string | undefined
  until: number
  outcome: RefreshOutcome
} | null = null
let authEpoch = 0
let authCache: QueryClient | null = null

function authenticationGeneration(): AuthenticationGeneration {
  return {
    authEpoch,
    accountWriteEpoch: useAuthStore.getState().auth.accountWriteEpoch,
  }
}

function sameGeneration(
  left: AuthenticationGeneration,
  right?: AuthenticationGeneration
): boolean {
  return (
    left.authEpoch === right?.authEpoch &&
    left.accountWriteEpoch === right?.accountWriteEpoch
  )
}

/** Only an already-running passive refresh may precede this explicit login. */
export function capturePasswordLoginGuard(): () => void {
  const initial = authenticationGeneration()
  const passive = refreshPromise ? refreshAccountWrite : null
  return () => {
    const current = authenticationGeneration()
    if (
      sameGeneration(current, initial) ||
      (sameGeneration(initial, passive?.before) &&
        sameGeneration(current, passive?.after))
    ) {
      return
    }
    throw new axios.CanceledError('Login superseded')
  }
}

class AuthRefreshSupersededError extends Error {
  constructor() {
    super('Authentication refresh was superseded')
    this.name = 'AuthRefreshSupersededError'
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object'
}

function isAuthUser(value: unknown): value is AuthUser {
  if (!isRecord(value)) return false
  return (
    Number.isInteger(value.id) &&
    Number(value.id) > 0 &&
    typeof value.username === 'string' &&
    typeof value.role === 'number'
  )
}

function isLoginSession(value: unknown): value is LoginSession {
  if (!isRecord(value)) return false
  return (
    typeof value.sid === 'string' &&
    value.sid.length > 0 &&
    typeof value.current === 'boolean' &&
    typeof value.login_method === 'string' &&
    typeof value.ip === 'string' &&
    typeof value.user_agent === 'string' &&
    typeof value.created_at === 'number' &&
    typeof value.last_active_at === 'number' &&
    typeof value.expires_at === 'number'
  )
}

function hasValidTokenFields(value: Record<string, unknown>): boolean {
  return (
    typeof value.access_token === 'string' &&
    value.access_token.length > 0 &&
    typeof value.token_type === 'string' &&
    value.token_type.length > 0 &&
    typeof value.access_expires_at === 'number' &&
    Number.isFinite(value.access_expires_at) &&
    value.access_expires_at > 0
  )
}

export function isAuthBundle(value: unknown): value is AuthBundle {
  if (!isRecord(value)) return false
  return (
    hasValidTokenFields(value) &&
    isAuthUser(value.user) &&
    isLoginSession(value.session)
  )
}

function isAuthTokenRotation(value: unknown): value is AuthTokenRotation {
  return (
    isRecord(value) &&
    hasValidTokenFields(value) &&
    value.token_type === 'Bearer' &&
    isLoginSession(value.session) &&
    value.session.current
  )
}

export function applyAuthBundle(
  bundle: AuthBundle,
  synchronizeTabs = true
): void {
  const previousSID = useAuthStore.getState().auth.session?.sid
  if (previousSID && previousSID !== bundle.session.sid) {
    authCache?.clear()
  }
  refreshCooldown = null
  authEpoch += 1
  useAuthStore.getState().auth.setBundle(bundle)
  if (synchronizeTabs && previousSID !== bundle.session.sid) {
    publishAuthSessionEvent('authenticated', bundle.session.sid)
  }
}

export function applyPending2FAFlow(flow: string, expiresAt?: number): void {
  // A refresh queued before the password challenge cannot consume the ceremony.
  authEpoch += 1
  useAuthStore.getState().auth.setPending2FAFlowToken(flow, expiresAt)
}

export function applyAuthRotation(value: unknown): void {
  if (!isAuthTokenRotation(value)) {
    throw new AuthRotationError('Invalid authentication rotation response')
  }

  const auth = useAuthStore.getState().auth
  if (!auth.user || !auth.session) {
    throw new AuthRotationError('Authentication rotation has no active session')
  }
  if (value.session.sid !== auth.session.sid) {
    throw new AuthRotationError('Authentication rotation session mismatch')
  }

  applyAuthBundle(
    {
      access_token: value.access_token,
      token_type: value.token_type,
      access_expires_at: value.access_expires_at,
      session: value.session,
      user: auth.user,
    },
    false
  )
}

export function clearAuthentication(
  synchronizeTabs = true,
  bootstrapState: AuthBootstrapState = 'complete',
  preserveValidPending2FA = false
): void {
  const sid = useAuthStore.getState().auth.session?.sid
  authCache?.clear()
  refreshCooldown = null
  authEpoch += 1
  useAuthStore.getState().auth.reset(bootstrapState, preserveValidPending2FA)
  if (synchronizeTabs && sid) {
    publishAuthSessionEvent('signed_out', sid)
  }
}

/** Binds the single application query cache to the authentication lifecycle. */
export function bindAuthCache(queryClient: QueryClient): () => void {
  authCache = queryClient
  return () => {
    if (authCache === queryClient) authCache = null
  }
}

export function clearAuthenticatedClientState(
  queryClient: QueryClient,
  synchronizeTabs = true
): void {
  if (queryClient !== authCache) queryClient.clear()
  clearAuthentication(synchronizeTabs)
}

/** A deleted account cannot invalidate a later login, even if logout failed. */
export function clearAuthenticationIfCurrent(scope: {
  userId: number | undefined
  sessionId: string | undefined
}): void {
  const auth = useAuthStore.getState().auth
  if (auth.user?.id === scope.userId && auth.session?.sid === scope.sessionId) {
    clearAuthentication()
  }
}

function waitForRefreshRace(delay: number): Promise<void> {
  return new Promise((resolve) => globalThis.setTimeout(resolve, delay))
}

export function createRefreshRunner(
  runtime: AuthRefreshRuntime
): () => Promise<RefreshOutcome> {
  const superseded = (): RefreshOutcome => ({
    kind: 'transient_error',
    error: new AuthRefreshSupersededError(),
  })
  const run = async (raceAttempt: number): Promise<RefreshOutcome> => {
    if (runtime.isCurrent && !runtime.isCurrent()) return superseded()
    const expectedSID = runtime.getExpectedSID()
    const response = await runtime.request(expectedSID)
    if (runtime.isCurrent && !runtime.isCurrent()) return superseded()
    const responseData = isRecord(response.data) ? response.data : undefined
    const code =
      typeof responseData?.code === 'string' ? responseData.code : undefined
    const bundle = runtime.parseBundle(responseData?.data)
    if (responseData?.success === true && bundle) {
      if (expectedSID && bundle.session.sid !== expectedSID) {
        runtime.clear(false)
        return { kind: 'out_of_sync', code: 'AUTH_SESSION_MISMATCH' }
      }
      runtime.acceptBundle(bundle)
      return { kind: 'authenticated', bundle }
    }

    if (response.status === 409 && code === 'AUTH_REFRESH_RACE') {
      const delay = refreshRaceDelays[raceAttempt]
      if (delay !== undefined) {
        await runtime.wait(delay)
        return run(raceAttempt + 1)
      }
      runtime.clear(false)
      return { kind: 'out_of_sync', code }
    }

    if (response.status === 409 && code === 'AUTH_SESSION_MISMATCH') {
      // Removing expectedSID here would let a late foreign cookie family
      // silently become this tab's signed-in account.
      runtime.clear(false)
      return { kind: 'out_of_sync', code }
    }

    if (response.status === 401) {
      runtime.clear(true)
      return { kind: 'anonymous' }
    }

    if (
      !response.status ||
      response.status === 408 ||
      response.status === 425 ||
      response.status === 429 ||
      response.status >= 500
    ) {
      runtime.markTransient()
      return {
        kind: 'transient_error',
        error: response.error ?? response.data,
      }
    }

    runtime.clear(false)
    return {
      kind: 'out_of_sync',
      code: code ?? 'AUTH_INVALID_REFRESH_RESPONSE',
    }
  }

  return () => run(0)
}

async function requestRefresh(
  expectedSID: string | undefined,
  transaction: AuthCookieTransaction
): Promise<AuthRefreshHTTPResponse> {
  try {
    const response = await transaction.request(() =>
      authClient.post('/api/user/auth/refresh', undefined, {
        headers: expectedSID ? { 'X-Auth-Session': expectedSID } : undefined,
        timeout: AUTH_COOKIE_TIMEOUT_MS,
      })
    )
    return { status: response.status, data: response.data }
  } catch (error: unknown) {
    if (!axios.isAxiosError(error)) return { status: 0, error }
    return {
      status: error.response?.status ?? 0,
      data: error.response?.data,
      error,
    }
  }
}

function runRefresh(
  refreshEpoch: number,
  transaction: AuthCookieTransaction,
  accountWrite?: RefreshAccountWrite
): Promise<RefreshOutcome> {
  const commitBundle = (bundle: AuthBundle) => {
    const before = authenticationGeneration()
    applyAuthBundle(bundle, false)
    const after = authenticationGeneration()
    if (
      accountWrite &&
      after.authEpoch === before.authEpoch + 1 &&
      after.accountWriteEpoch === before.accountWriteEpoch + 1
    ) {
      accountWrite.before = before
      // A synchronous store listener's additional write must not be
      // attributed to this refresh. Both generations advance exactly once.
      accountWrite.after = after
    }
  }
  return createRefreshRunner({
    request: (sid) => requestRefresh(sid, transaction),
    getExpectedSID: () => useAuthStore.getState().auth.session?.sid,
    parseBundle: (value) => (isAuthBundle(value) ? value : null),
    acceptBundle: commitBundle,
    clear: (synchronizeTabs, bootstrapState) => {
      if (!synchronizeTabs && bootstrapState === 'idle') {
        useAuthStore.getState().auth.reset('idle')
        return
      }
      clearAuthentication(synchronizeTabs, bootstrapState)
    },
    markTransient: () => useAuthStore.getState().auth.setBootstrapState('idle'),
    wait: waitForRefreshRace,
    isCurrent: () => authEpoch === refreshEpoch,
  })()
}

async function performRefreshWithBrowserLock(
  refreshEpoch: number,
  accountWrite: RefreshAccountWrite
): Promise<RefreshOutcome> {
  try {
    return await withAuthCookieLock((transaction) =>
      runRefresh(refreshEpoch, transaction, accountWrite)
    )
  } catch (error: unknown) {
    useAuthStore.getState().auth.setBootstrapState('idle')
    return { kind: 'transient_error', error }
  }
}

function currentRefreshCooldown(epoch: number, sid: string | undefined) {
  if (
    refreshCooldown?.epoch === epoch &&
    refreshCooldown.sid === sid &&
    refreshCooldown.until > Date.now()
  ) {
    return refreshCooldown.outcome
  }
  return null
}

function recordRefreshCooldown(
  epoch: number,
  sid: string | undefined,
  outcome: RefreshOutcome
): void {
  if (
    authEpoch === epoch &&
    outcome.kind === 'transient_error' &&
    isRateLimitedError(outcome.error)
  ) {
    const seconds = Math.max(1, rateLimitWaitSeconds(outcome.error) ?? 30)
    refreshCooldown = {
      epoch,
      sid,
      until: Date.now() + seconds * 1000,
      outcome,
    }
  }
}

function hasPendingTwoFactorFlow(): boolean {
  const auth = useAuthStore.getState().auth
  return Boolean(
    auth.pending2FAFlowToken &&
    (auth.pending2FAFlowExpiresAt === null ||
      auth.pending2FAFlowExpiresAt > Date.now() / 1000)
  )
}

/** Keep refresh and one protected read together against other Cookie writers. */
export function refreshAndRunAuthenticatedRead<T>(
  scope: { userId: number; sessionId: string },
  operation: (bundle: AuthBundle) => Promise<T>,
  signal?: AbortSignal
): Promise<
  | { kind: 'response'; response: T }
  | { kind: 'session'; outcome: RefreshOutcome }
> {
  return withAuthCookieLock(async (transaction) => {
    const assertScope = () => {
      const auth = useAuthStore.getState().auth
      if (
        auth.user?.id !== scope.userId ||
        auth.session?.sid !== scope.sessionId
      ) {
        throw new axios.CanceledError(
          'Authentication changed while waiting for read recovery'
        )
      }
    }
    assertScope()
    if (hasPendingTwoFactorFlow()) {
      return {
        kind: 'session',
        outcome: {
          kind: 'transient_error',
          error: new AuthRefreshSupersededError(),
        },
      }
    }
    const epoch = authEpoch
    const outcome =
      currentRefreshCooldown(epoch, scope.sessionId) ??
      (await runRefresh(epoch, transaction))
    recordRefreshCooldown(epoch, scope.sessionId, outcome)
    if (outcome.kind !== 'authenticated') return { kind: 'session', outcome }
    assertScope()
    // The Cookie response is known and committed. The read itself cannot
    // create Cookie uncertainty, even if it fails or its observer cancels.
    const response = await operation(outcome.bundle)
    assertScope()
    return { kind: 'response', response }
  }, signal)
}

export function refreshAuthentication(): Promise<RefreshOutcome> {
  if (hasPendingTwoFactorFlow()) {
    return Promise.resolve({
      kind: 'transient_error',
      error: new AuthRefreshSupersededError(),
    })
  }
  if (!refreshPromise) {
    const refreshEpoch = authEpoch
    const sid = useAuthStore.getState().auth.session?.sid
    const cooldown = currentRefreshCooldown(refreshEpoch, sid)
    if (cooldown) return Promise.resolve(cooldown)
    const accountWrite: RefreshAccountWrite = {}
    refreshAccountWrite = accountWrite
    refreshPromise = performRefreshWithBrowserLock(refreshEpoch, accountWrite)
      .then((outcome) => {
        recordRefreshCooldown(refreshEpoch, sid, outcome)
        return outcome
      })
      .finally(() => {
        refreshPromise = null
        refreshAccountWrite = null
      })
  }
  return refreshPromise
}

export type AuthWriteConfig = {
  skipAuthRefresh: true
  timeout: number
  authScope: { userId: number | undefined; sessionId: string | undefined }
}

/** Prepare outside the cookie lock; never replay a factor/password mutation. */
export function withAuthenticatedCookieWrite<T>(
  operation: (config: AuthWriteConfig) => Promise<T>,
  writesCookie = true
): Promise<T> {
  return withAuthCookieActivity(() =>
    performAuthenticatedCookieWrite(operation, writesCookie)
  )
}

async function performAuthenticatedCookieWrite<T>(
  operation: (config: AuthWriteConfig) => Promise<T>,
  writesCookie = true
): Promise<T> {
  const initial = useAuthStore.getState().auth
  const scope = { userId: initial.user?.id, sessionId: initial.session?.sid }
  const assertScope = () => {
    const auth = useAuthStore.getState().auth
    if (
      auth.user?.id !== scope.userId ||
      auth.session?.sid !== scope.sessionId
    ) {
      throw new axios.CanceledError('Authentication changed before operation')
    }
    return auth
  }
  if (
    initial.user &&
    initial.session &&
    (!initial.accessToken ||
      !initial.accessExpiresAt ||
      initial.accessExpiresAt <= Date.now() / 1000 + 60)
  ) {
    const outcome = await refreshAuthentication()
    assertScope()
    if (outcome.kind !== 'authenticated') {
      throw new Error(t('Session expired!'))
    }
  }
  const bearer = assertScope().accessToken
  try {
    return await withAuthCookieLock((transaction) => {
      if (assertScope().accessToken !== bearer) {
        throw new axios.CanceledError('Authentication changed while waiting')
      }
      const dispatch = () =>
        operation({
          skipAuthRefresh: true,
          timeout: AUTH_COOKIE_TIMEOUT_MS,
          authScope: scope,
        })
      return writesCookie ? transaction.request(dispatch) : dispatch()
    })
  } catch (error) {
    // A clearly rejected request may refresh the session only after the
    // callback has released the lock. The user must explicitly retry input.
    if (axios.isAxiosError(error) && error.response?.status === 401) {
      if (assertScope().accessToken === bearer) await refreshAuthentication()
    }
    throw error
  }
}

function currentValidAuthBundle(): AuthBundle | null {
  const auth = useAuthStore.getState().auth
  if (
    !auth.user ||
    !auth.accessToken ||
    !auth.accessExpiresAt ||
    !auth.session ||
    auth.accessExpiresAt <= Math.floor(Date.now() / 1000)
  ) {
    return null
  }
  return {
    access_token: auth.accessToken,
    token_type: 'Bearer',
    access_expires_at: auth.accessExpiresAt,
    user: auth.user,
    session: auth.session,
  }
}

export async function bootstrapAuthentication(): Promise<RefreshOutcome> {
  const bundle = currentValidAuthBundle()
  if (bundle) {
    useAuthStore.getState().auth.setBootstrapState('complete')
    return { kind: 'authenticated', bundle }
  }

  const auth = useAuthStore.getState().auth
  const hasStaleSession = Boolean(auth.user && auth.session)
  if (auth.bootstrapState === 'complete' && !hasStaleSession) {
    return { kind: 'anonymous' }
  }

  auth.setBootstrapState('checking')
  return refreshAuthentication()
}

export function getCommonHeaders(): Record<string, string> {
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
  }
  const accessToken = useAuthStore.getState().auth.accessToken
  if (accessToken) {
    headers.Authorization = `Bearer ${accessToken}`
  }
  return headers
}

export async function getFreshAuthHeaders(): Promise<Record<string, string>> {
  const auth = useAuthStore.getState().auth
  const refreshBefore = Math.floor(Date.now() / 1000) + 60
  if (
    auth.accessToken &&
    auth.accessExpiresAt &&
    auth.accessExpiresAt > refreshBefore
  ) {
    return getCommonHeaders()
  }

  const outcome = await refreshAuthentication()
  if (outcome.kind === 'authenticated') {
    return getCommonHeaders()
  }

  const current = useAuthStore.getState().auth
  if (
    current.accessToken &&
    current.accessExpiresAt &&
    current.accessExpiresAt > Math.floor(Date.now() / 1000)
  ) {
    return getCommonHeaders()
  }

  if (outcome.kind === 'transient_error') {
    throw new Error(t('Request failed'), { cause: outcome.error })
  }
  throw new Error(t('Session expired!'))
}
