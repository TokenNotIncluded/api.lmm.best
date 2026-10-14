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
import axios from 'axios'

import { api } from '@/lib/api'
import { AUTH_COOKIE_TIMEOUT_MS, withAuthCookieLock } from '@/lib/auth-cookie'
import {
  applyAuthBundle,
  applyPending2FAFlow,
  capturePasswordLoginGuard,
  clearAuthentication,
  isAuthBundle,
} from '@/lib/auth-session'
import { useAuthStore } from '@/stores/auth-store'

import { getAffiliateCode } from './lib/storage'
import type { TelegramAuthorization } from './lib/telegram-login'
import type {
  LoginPayload,
  LoginResponse,
  Login2FAResponse,
  TwoFAPayload,
  RegisterPayload,
  ApiResponse,
} from './types'

// ============================================================================
// Authentication APIs
// ============================================================================

// ----------------------------------------------------------------------------
// Login & Logout
// ----------------------------------------------------------------------------

// User login with username and password
export async function login(payload: LoginPayload, signal?: AbortSignal) {
  const turnstile = payload.turnstile ?? ''
  const assertCurrent = capturePasswordLoginGuard()
  return withAuthCookieLock(async (transaction) => {
    assertCurrent()
    const res = await transaction.request(() =>
      api.post<LoginResponse>(
        `/api/user/login?turnstile=${turnstile}`,
        {
          username: payload.username,
          password: payload.password,
        },
        { skipAuthRefresh: true, timeout: AUTH_COOKIE_TIMEOUT_MS }
      )
    )
    assertCurrent()
    if (res.data.success) {
      const data = res.data.data
      if (data && 'require_2fa' in data && data.require_2fa) {
        if (!data.flow_token) {
          throw new Error('Login flow expired. Please sign in again.')
        }
        applyPending2FAFlow(data.flow_token, data.expires_at)
      } else {
        if (!isAuthBundle(data)) throw new Error('Login failed')
        applyAuthBundle(data)
      }
    }
    if (signal?.aborted) {
      throw new axios.CanceledError('Login view cancelled after response')
    }
    return res.data
  }, signal)
}

export type AccountAppealResponse = {
  id: number
  status: 'pending' | 'approved' | 'rejected'
  reason: string
  created_at: number
  reviewed_at: number
  admin_note: string
}

export async function submitAccountAppeal(input: {
  username: string
  password: string
  reason: string
  turnstile?: string
}): Promise<ApiResponse<AccountAppealResponse>> {
  const res = await api.post<ApiResponse<AccountAppealResponse>>(
    `/api/user/account-action-requests/appeal?turnstile=${encodeURIComponent(input.turnstile ?? '')}`,
    {
      username: input.username,
      password: input.password,
      reason: input.reason,
    },
    { skipAuthRefresh: true, skipErrorHandler: true }
  )
  return res.data
}

// Two-factor authentication login
export async function login2fa(payload: TwoFAPayload, signal?: AbortSignal) {
  const epoch = useAuthStore.getState().auth.accountWriteEpoch
  const assertCurrent = (completed = false) => {
    const auth = useAuthStore.getState().auth
    const cancelledFlow = completed && auth.pending2FAFlowToken === null
    if (
      auth.accountWriteEpoch !== epoch ||
      (auth.pending2FAFlowToken !== payload.flow_token && !cancelledFlow)
    ) {
      throw new axios.CanceledError('Login superseded')
    }
    if (
      !completed &&
      auth.pending2FAFlowExpiresAt !== null &&
      auth.pending2FAFlowExpiresAt <= Date.now() / 1000
    ) {
      throw Object.assign(
        new Error('Login flow expired. Please sign in again.'),
        { code: 'MFA_FLOW_EXPIRED' }
      )
    }
  }
  return withAuthCookieLock(async (transaction) => {
    assertCurrent()
    // The signal cancels waiting for the lock, not an active cookie response.
    const res = await transaction.request(() =>
      api.post<Login2FAResponse>('/api/user/login/2fa', payload, {
        skipAuthRefresh: true,
        timeout: AUTH_COOKIE_TIMEOUT_MS,
      })
    )
    assertCurrent(true)
    const cancelled =
      signal?.aborted ||
      useAuthStore.getState().auth.pending2FAFlowToken === null
    if (res.data.success) {
      if (!isAuthBundle(res.data.data)) throw new Error('Login failed')
      applyAuthBundle(res.data.data)
    }
    if (cancelled) {
      throw new axios.CanceledError('Login view cancelled after response')
    }
    return res.data
  }, signal)
}

interface LogoutRuntime {
  getExpectedSID: () => string | undefined
  request: (expectedSID?: string) => Promise<ApiResponse>
}

export async function executeLogout(
  runtime: LogoutRuntime
): Promise<ApiResponse> {
  try {
    return await runtime.request(runtime.getExpectedSID())
  } catch (error: unknown) {
    const status = axios.isAxiosError(error)
      ? error.response?.status
      : undefined
    // Logout is intentionally idempotent at the client boundary. During a
    // rolling deployment an older gateway can briefly answer 404, and an
    // already-expired session can answer 401. In both cases there is no
    // active session the browser can continue using, so the caller should
    // still clear its local auth state. Keep origin failures and server
    // errors visible; they indicate a real request/configuration problem.
    if (axios.isAxiosError(error) && (status === 401 || status === 404)) {
      return { success: true, message: '' }
    }
    throw error
  }
}

// User logout
export async function logout(): Promise<ApiResponse> {
  const initial = useAuthStore.getState().auth
  return withAuthCookieLock(async (transaction) => {
    const auth = useAuthStore.getState().auth
    if (
      auth.user?.id !== initial.user?.id ||
      auth.session?.sid !== initial.session?.sid ||
      auth.accessToken !== initial.accessToken
    ) {
      throw new axios.CanceledError('Logout superseded')
    }
    const result = await executeLogout({
      getExpectedSID: () => initial.session?.sid,
      request: async (sid) => {
        const res = await transaction.request(() =>
          api.post('/api/user/auth/logout', undefined, {
            headers: sid ? { 'X-Auth-Session': sid } : undefined,
            skipAuthRefresh: true,
            timeout: AUTH_COOKIE_TIMEOUT_MS,
            skipErrorHandler: true,
            authScope: {
              userId: initial.user?.id,
              sessionId: initial.session?.sid,
            },
          })
        )
        return res.data
      },
    })
    const current = useAuthStore.getState().auth
    if (
      current.user?.id !== initial.user?.id ||
      current.session?.sid !== initial.session?.sid ||
      current.accessToken !== initial.accessToken
    ) {
      throw new axios.CanceledError('Logout superseded')
    }
    if (result.success) clearAuthentication()
    return result
  })
}

// ----------------------------------------------------------------------------
// Password Management
// ----------------------------------------------------------------------------

// Send password reset email
export async function sendPasswordResetEmail(
  email: string,
  turnstile?: string
): Promise<ApiResponse> {
  const res = await api.get('/api/reset_password', {
    params: { email, turnstile },
  })
  return res.data
}

// ----------------------------------------------------------------------------
// OAuth
// ----------------------------------------------------------------------------

// Start GitHub OAuth flow
export async function githubOAuthStart(clientId: string, state: string) {
  const url = `https://github.com/login/oauth/authorize?client_id=${clientId}&state=${state}&scope=user:email`
  window.open(url)
}

// Get OAuth state for CSRF protection
export async function createOAuthFlow(
  provider: string,
  intent: 'login' | 'bind',
  acceptedLegal = false
): Promise<string> {
  const aff = intent === 'login' ? getAffiliateCode() : ''
  const res = await api.post(
    '/api/oauth/state',
    {
      provider,
      intent,
      aff: aff || undefined,
      accepted_legal: intent === 'login' ? acceptedLegal : undefined,
    },
    { skipAuthRefresh: intent === 'login' }
  )
  if (res.data?.success) {
    if (typeof res.data.data === 'string') return res.data.data
    if (typeof res.data.data?.flow_token === 'string') {
      return res.data.data.flow_token
    }
  }
  throw new Error(res.data?.message || 'Failed to initialize OAuth')
}

// Start a browser-bound WeChat login flow before submitting the provider code.
export async function startWechatLogin(acceptedLegal = false): Promise<string> {
  const aff = getAffiliateCode()
  const res = await api.post(
    '/api/oauth/wechat/start',
    { accepted_legal: acceptedLegal, aff: aff || undefined },
    { skipAuthRefresh: true }
  )
  if (res.data?.success && typeof res.data.data?.flow_token === 'string') {
    return res.data.data.flow_token
  }
  throw new Error(res.data?.message || 'Failed to initialize WeChat login')
}

// WeChat login by authorization code and browser-bound state.
export async function wechatLoginByCode(
  code: string,
  state: string
): Promise<ApiResponse> {
  const res = await api.get('/api/oauth/wechat', { params: { code, state } })
  return res.data
}

export async function startTelegramLogin(): Promise<string> {
  const res = await api.post('/api/oauth/telegram/login/start', undefined, {
    skipAuthRefresh: true,
  })
  if (res.data?.success && typeof res.data.data?.flow_token === 'string') {
    return res.data.data.flow_token
  }
  throw new Error(res.data?.message || 'Failed to initialize Telegram login')
}

export async function telegramLogin(
  authorization: TelegramAuthorization,
  state: string
): Promise<ApiResponse> {
  const res = await api.get('/api/oauth/telegram/login', {
    params: { ...authorization, state },
    disableDuplicate: true,
    skipAuthRefresh: true,
    skipBusinessError: true,
    skipErrorHandler: true,
  })
  return res.data
}

// ----------------------------------------------------------------------------
// Registration
// ----------------------------------------------------------------------------

// User registration
export async function register(payload: RegisterPayload): Promise<ApiResponse> {
  const res = await api.post(`/api/user/register`, payload, {
    params: { turnstile: payload.turnstile ?? '' },
  })
  return res.data
}

// Send email verification code
export async function sendEmailVerification(
  email: string,
  turnstile?: string
): Promise<ApiResponse> {
  const res = await api.get('/api/verification', {
    params: { email, turnstile },
  })
  return res.data
}

// Bind email to OAuth account
export async function bindEmail(
  email: string,
  code: string
): Promise<ApiResponse> {
  const res = await api.post('/api/oauth/email/bind', {
    email,
    code,
  })
  return res.data
}
