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
import axios, { type AxiosRequestConfig } from 'axios'
import { t } from 'i18next'
import { toast } from 'sonner'

import { signInHref } from '@/features/auth/lib/auth-redirect'
import { AUTH_COOKIE_TIMEOUT_MS } from '@/lib/auth-cookie'
import {
  applyAuthRotation,
  clearAuthentication,
  refreshAuthentication,
  refreshAndRunAuthenticatedRead,
} from '@/lib/auth-session'
import { rateLimitMessage } from '@/lib/request-rate-limit'
import {
  getServerErrorMessageKey,
  getServerErrorToastId,
} from '@/lib/server-error-message'
import { useAuthStore, type AuthBundle } from '@/stores/auth-store'

declare module 'axios' {
  export interface AxiosRequestConfig {
    skipBusinessError?: boolean
    skipErrorHandler?: boolean
    disableDuplicate?: boolean
    skipAuthRefresh?: boolean
    authRetry?: boolean
    authReadRetryInLock?: boolean
    acceptAuthRotation?: boolean
    authScope?: { userId: number | undefined; sessionId: string | undefined }
    authDispatchEpoch?: number
  }
}

export type ApiRequestConfig = AxiosRequestConfig

export const api = axios.create({
  baseURL: '',
  withCredentials: true,
  headers: {
    'Cache-Control': 'no-store',
  },
})

const inFlightGet = new Map<string, Promise<unknown>>()
const originalGet = api.get.bind(api)
const refreshBeforeSeconds = 60

api.get = ((url: string, config: ApiRequestConfig = {}) => {
  // AbortSignal belongs to one observer's lifecycle. Sharing its promise can
  // cancel a newly mounted page when the previous observer is disposed.
  if (config.disableDuplicate || config.signal || config.authScope) {
    return originalGet(url, config)
  }

  const params = config.params ? JSON.stringify(config.params) : '{}'
  const sessionSID = useAuthStore.getState().auth.session?.sid || 'anonymous'
  const key = `${sessionSID}:${url}?${params}`
  const existingRequest = inFlightGet.get(key)
  if (existingRequest) return existingRequest

  const request = originalGet(url, config).finally(() => {
    inFlightGet.delete(key)
  })
  inFlightGet.set(key, request)
  return request
}) as typeof api.get

// Read identity and token from one snapshot. A refresh or late 401 must never
// turn an operation initiated in one session into an operation in another.
function requestAuthentication(config: ApiRequestConfig) {
  const auth = useAuthStore.getState().auth
  const scope = config.authScope
  if (
    scope &&
    (auth.user?.id !== scope.userId || auth.session?.sid !== scope.sessionId)
  ) {
    throw Object.assign(
      new axios.CanceledError(
        'Authentication changed before request could be sent'
      ),
      { config }
    )
  }
  return auth
}

function dispatchedAuthentication(config: ApiRequestConfig) {
  const auth = requestAuthentication(config)
  // A same-SID credential rotation supersedes an already-dispatched request.
  // Profile-only writes can advance the store epoch without changing its bearer.
  const sentAuthorization =
    config.headers instanceof axios.AxiosHeaders
      ? config.headers.get('Authorization')
      : (config.headers?.Authorization ?? config.headers?.authorization)
  const currentAuthorization = auth.accessToken
    ? `Bearer ${auth.accessToken}`
    : undefined
  if (
    config.authDispatchEpoch !== undefined &&
    config.authDispatchEpoch !== auth.accountWriteEpoch &&
    sentAuthorization !== currentAuthorization
  ) {
    throw Object.assign(
      new axios.CanceledError('Authentication changed after request was sent'),
      {
        config,
      }
    )
  }
  return auth
}

function isProtectedAPIRead(config: ApiRequestConfig): boolean {
  if (
    !['get', 'head'].includes(config.method?.toLowerCase() ?? '') ||
    config.acceptAuthRotation ||
    (config.responseType && config.responseType !== 'json') ||
    typeof window === 'undefined'
  ) {
    return false
  }
  const accept =
    config.headers instanceof axios.AxiosHeaders
      ? config.headers.get('Accept')
      : (config.headers?.Accept ?? config.headers?.accept)
  if (
    String(accept ?? '')
      .toLowerCase()
      .includes('text/event-stream')
  ) {
    return false
  }
  try {
    const url = new URL(api.getUri(config), window.location.origin)
    return (
      url.origin === window.location.origin &&
      url.pathname.startsWith('/api/') &&
      !/^\/api\/(?:oauth|reset_password|verification)(?:\/|$)/.test(
        url.pathname
      )
    )
  } catch {
    return false
  }
}

function redirectToSignIn(): void {
  if (
    typeof window !== 'undefined' &&
    window.location.pathname !== '/sign-in'
  ) {
    window.location.replace(signInHref(window.location))
  }
}

function createConfiguredRequestError(
  message: string,
  config: ApiRequestConfig,
  cause?: unknown
): Error & { config: ApiRequestConfig } {
  const error =
    cause === undefined ? new Error(message) : new Error(message, { cause })
  return Object.assign(error, { config })
}

api.interceptors.response.use(
  (response) => {
    if (response.config.authScope) requestAuthentication(response.config)
    delete response.config.authReadRetryInLock
    if (response.config.acceptAuthRotation && response.data?.success === true) {
      dispatchedAuthentication(response.config)
      try {
        applyAuthRotation(response.data.data)
      } catch (cause) {
        clearAuthentication(true)
        if (!response.config.skipErrorHandler) {
          toast.error(t('Session expired!'))
        }
        throw Object.assign(
          createConfiguredRequestError(
            'Invalid authentication rotation response',
            response.config,
            cause
          ),
          { code: 'AUTH_ROTATION_INVALID' }
        )
      }
    }

    if (
      !response.config.skipBusinessError &&
      typeof response.data?.success === 'boolean' &&
      !response.data.success
    ) {
      const messageKey = getServerErrorMessageKey(response.data)
      toast.error(
        messageKey
          ? t(messageKey)
          : response.data.message || t('Request failed')
      )
    }
    return response
  },
  async (error) => {
    // Route/query cancellation is control flow, not an application failure.
    // Preserve the rejection for the caller without a toast or auth retry.
    if (axios.isCancel(error)) throw error
    const config = error?.config as ApiRequestConfig | undefined
    const skipErrorHandler = config?.skipErrorHandler
    const status = error?.response?.status

    if (status === 401) {
      if (config) dispatchedAuthentication(config)
      // The outer recovery must release its Cookie lock before a hard redirect.
      if (config?.authReadRetryInLock) throw error
      if (config && !config.skipAuthRefresh && !config.authRetry) {
        config.authRetry = true
        const retry = (bundle: AuthBundle) => {
          const auth = requestAuthentication(config)
          if (auth.accessToken !== bundle.access_token) {
            throw Object.assign(
              new axios.CanceledError('Authentication changed after refresh'),
              {
                config,
              }
            )
          }
          config.headers = {
            ...config.headers,
            Authorization: `Bearer ${bundle.access_token}`,
          }
          return api.request(config)
        }
        const auth = requestAuthentication(config)
        // Provider ceremonies can write Cookies even through legacy GET.
        const protectedRead =
          isProtectedAPIRead(config) && auth.user && auth.session
        let outcome
        if (protectedRead && auth.user && auth.session) {
          const scope = { userId: auth.user.id, sessionId: auth.session.sid }
          config.skipAuthRefresh = true
          config.timeout =
            config.timeout && config.timeout > 0
              ? Math.min(config.timeout, AUTH_COOKIE_TIMEOUT_MS)
              : AUTH_COOKIE_TIMEOUT_MS
          try {
            const recovery = await refreshAndRunAuthenticatedRead(
              scope,
              (bundle) => {
                config.authScope ??= scope
                config.authReadRetryInLock = true
                return retry(bundle)
              },
              config.signal as AbortSignal | undefined
            )
            if (recovery.kind === 'response') return recovery.response
            requestAuthentication(config)
            outcome = recovery.outcome
          } catch (failure) {
            if (axios.isAxiosError(failure)) {
              const failedConfig = failure.config as
                | ApiRequestConfig
                | undefined
              const lockedRetry = failedConfig?.authReadRetryInLock
              if (failedConfig) delete failedConfig.authReadRetryInLock
              if (
                lockedRetry &&
                failure.response?.status === 401 &&
                failedConfig
              ) {
                // The lock and its known-result marker are now released. A
                // newer account/token may have committed while we resumed.
                dispatchedAuthentication(failedConfig)
                clearAuthentication(false)
                if (!skipErrorHandler) toast.error(t('Session expired!'))
                redirectToSignIn()
              }
            }
            throw failure
          }
        } else {
          outcome = await refreshAuthentication()
          requestAuthentication(config)
          if (outcome.kind === 'authenticated') return retry(outcome.bundle)
        }

        if (outcome.kind === 'anonymous' || outcome.kind === 'out_of_sync') {
          if (!skipErrorHandler) toast.error(t('Session expired!'))
          redirectToSignIn()
        }
      } else if (config?.authRetry) {
        clearAuthentication(false)
        if (!skipErrorHandler) toast.error(t('Session expired!'))
        redirectToSignIn()
      } else if (!skipErrorHandler) {
        toast.error(t('Session expired!'))
      }
    } else if (!skipErrorHandler) {
      const messageKey = getServerErrorMessageKey(error)
      const message =
        rateLimitMessage(error) ??
        (messageKey
          ? t(messageKey)
          : error?.response?.data?.message ||
            error?.message ||
            t('Request failed'))
      toast.error(message, { id: getServerErrorToastId(error) })
    }
    throw error
  }
)

api.interceptors.request.use(async (config) => {
  let auth = requestAuthentication(config)
  const now = Math.floor(Date.now() / 1000)
  const needsRefresh =
    !config.skipAuthRefresh &&
    Boolean(auth.user && auth.session) &&
    (!auth.accessToken ||
      !auth.accessExpiresAt ||
      auth.accessExpiresAt <= now + refreshBeforeSeconds)

  if (needsRefresh) {
    const outcome = await refreshAuthentication()
    auth = requestAuthentication(config)
    if (outcome.kind === 'anonymous' || outcome.kind === 'out_of_sync') {
      redirectToSignIn()
      throw createConfiguredRequestError(t('Session expired!'), config)
    }
    const refreshedNow = Math.floor(Date.now() / 1000)
    if (
      outcome.kind === 'transient_error' &&
      (!auth.accessToken ||
        !auth.accessExpiresAt ||
        auth.accessExpiresAt <= refreshedNow)
    ) {
      throw createConfiguredRequestError(
        t('Request failed'),
        config,
        outcome.error
      )
    }
  }

  const accessToken = auth.accessToken
  config.authDispatchEpoch = auth.accountWriteEpoch
  if (accessToken) {
    config.headers.Authorization = `Bearer ${accessToken}`
  }
  // This client rejects cached denominations other than 500,000/USD. Old
  // tabs omit this acknowledgement and must refresh before dashboard writes.
  config.headers.set('X-LMM-Credit-Unit', '500000', true)
  return config
})
