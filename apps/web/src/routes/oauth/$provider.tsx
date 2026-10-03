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
import {
  createFileRoute,
  useNavigate,
  useParams,
  useSearch,
} from '@tanstack/react-router'
import type { AxiosRequestConfig } from 'axios'
import i18next from 'i18next'
import { useCallback, useEffect, useRef, useState } from 'react'
import { toast } from 'sonner'

import { OAuthCallbackScreen } from '@/features/auth/components/oauth-callback-screen'
import {
  OAUTH_BIND_CALLBACK_MESSAGE,
  OAUTH_BIND_RESULT_MESSAGE,
} from '@/features/auth/constants'
import { useAuthRedirect } from '@/features/auth/hooks/use-auth-redirect'
import { sanitizeAuthRedirect } from '@/features/auth/lib/auth-redirect'
import {
  parseTelegramBindCallback,
  postTelegramBindResult,
  startOAuthBindResponseDeadline,
} from '@/features/auth/lib/oauth-bind-window'
import {
  getOAuthSessionStorage,
  resolveOAuthCallbackMode,
} from '@/features/auth/lib/oauth-callback-mode'
import { finishPasskeyLogin } from '@/features/auth/passkey'
import type { ApiResponse } from '@/features/auth/types'
import { api, applyAuthBundle, isAuthBundle } from '@/lib/api'
import { getAuthenticatedLandingRoute } from '@/lib/console-activation'
import {
  prepareCredentialRequestOptions,
  requestPasskeyAuthentication,
} from '@/lib/passkey'
import { getServerErrorMessageKey } from '@/lib/server-error-message'
import { useAuthStore, type AuthBundle } from '@/stores/auth-store'

type OAuthRequestConfig = AxiosRequestConfig & {
  skipBusinessError?: boolean
}

interface OAuthBindingResult {
  type: typeof OAUTH_BIND_RESULT_MESSAGE
  provider: string
  state: string
  success: boolean
  message?: string
}

interface OAuthPasskeyChallenge {
  options: unknown
  flowToken: string
  expiresAt?: number
}

function OAuthCallback() {
  const navigate = useNavigate()
  const { redirectTo2FA } = useAuthRedirect()
  const setPending2FAFlowToken = useAuthStore(
    (state) => state.auth.setPending2FAFlowToken
  )
  const [passkeyChallenge, setPasskeyChallenge] =
    useState<OAuthPasskeyChallenge | null>(null)
  const [isPasskeyPending, setIsPasskeyPending] = useState(false)
  const passkeyPending = useRef(false)
  const passkeyAttempt = useRef(0)
  const callbackRequest = useRef<{
    key: string
    promise: Promise<ApiResponse>
  } | null>(null)
  const { provider } = useParams({ from: '/oauth/$provider' }) as {
    provider: string
  }
  const search = useSearch({ from: '/oauth/$provider' }) as {
    code?: string
    state?: string
    error?: string
    error_description?: string
    redirect?: string
    telegram_bind?: string
    flow_token?: string
    error_code?: string
  }
  const callbackState = search.state ?? ''

  const safeNavigate = useCallback(
    (target: unknown, fallback: string = '/open-source-bounties') => {
      const href =
        sanitizeAuthRedirect(target, window.location.origin) ?? fallback
      void navigate({ href, replace: true })
    },
    [navigate]
  )
  const completeLogin = useCallback(
    (bundle: AuthBundle) => {
      applyAuthBundle(bundle)
      safeNavigate(search.redirect, getAuthenticatedLandingRoute(bundle.user))
      toast.success(i18next.t('Signed in successfully!'))
    },
    [safeNavigate, search.redirect]
  )

  useEffect(() => {
    return () => {
      passkeyAttempt.current += 1
    }
  }, [])

  const handlePasskeyCancel = () => {
    passkeyAttempt.current += 1
    passkeyPending.current = false
    setIsPasskeyPending(false)
    setPasskeyChallenge(null)
    safeNavigate('/sign-in', '/sign-in')
  }

  const handlePasskeyVerification = async () => {
    if (!passkeyChallenge || passkeyPending.current) return
    if (!navigator.credentials?.get) {
      toast.error(i18next.t('Passkey is not available in this browser'))
      return
    }
    if (
      passkeyChallenge.expiresAt !== undefined &&
      passkeyChallenge.expiresAt * 1000 <= Date.now()
    ) {
      toast.error(i18next.t('Login flow expired. Please sign in again.'))
      safeNavigate('/sign-in', '/sign-in')
      return
    }

    const attempt = ++passkeyAttempt.current
    passkeyPending.current = true
    setIsPasskeyPending(true)
    try {
      const assertion = await requestPasskeyAuthentication(
        passkeyChallenge.options
      )
      if (attempt !== passkeyAttempt.current) return
      if (!assertion) {
        toast.info(i18next.t('Passkey login was cancelled'))
        return
      }
      const response = await finishPasskeyLogin(
        passkeyChallenge.flowToken,
        assertion
      )
      if (attempt !== passkeyAttempt.current) return
      if (!response.success || !isAuthBundle(response.data)) {
        const messageKey = getServerErrorMessageKey(response)
        toast.error(
          messageKey
            ? i18next.t(messageKey)
            : response.message || i18next.t('Failed to complete Passkey login')
        )
        return
      }
      completeLogin(response.data)
    } catch (error: unknown) {
      if (attempt !== passkeyAttempt.current) return
      if (getServerErrorMessageKey(error)) return
      if (error instanceof DOMException && error.name === 'NotAllowedError') {
        toast.info(i18next.t('Passkey login was cancelled or timed out'))
      } else {
        toast.error(i18next.t('Passkey login failed'))
      }
    } finally {
      if (attempt === passkeyAttempt.current) {
        passkeyPending.current = false
        setIsPasskeyPending(false)
      }
    }
  }
  const isTelegramBindCallback =
    provider === 'telegram' &&
    (search.telegram_bind === 'success' || search.telegram_bind === 'error')
  let mode: 'login' | 'bind' = 'login'
  if (isTelegramBindCallback) {
    mode = 'bind'
  } else if (typeof window !== 'undefined') {
    mode = resolveOAuthCallbackMode(provider, callbackState, {
      opener: window.opener,
      storage: getOAuthSessionStorage(window),
    })
  }

  useEffect(() => {
    if (typeof window === 'undefined') return

    const code = search.code ?? ''
    const state = callbackState
    const telegramCallback =
      provider === 'telegram'
        ? parseTelegramBindCallback({
            telegram_bind: search.telegram_bind,
            flow_token: search.flow_token,
            error_code: search.error_code,
          })
        : null
    if (telegramCallback) {
      const opener = window.opener
      if (
        !postTelegramBindResult(
          telegramCallback,
          opener,
          window.location.origin
        )
      ) {
        toast.error(i18next.t('Telegram binding failed. Please try again.'))
        const closeTimeout = window.setTimeout(() => window.close(), 1500)
        return () => window.clearTimeout(closeTimeout)
      }
      window.close()
      return
    }

    if (mode === 'bind') {
      const opener = window.opener
      if (!opener || opener.closed) {
        toast.error(i18next.t('OAuth binding window is no longer available'))
        return
      }

      let cancelResultTimeout: () => void = () => undefined
      let delayedClose: number | undefined
      const handleBindingResult = (event: MessageEvent<unknown>) => {
        if (
          event.origin !== window.location.origin ||
          event.source !== opener
        ) {
          return
        }
        const result = event.data as Partial<OAuthBindingResult> | null
        if (
          !result ||
          result.type !== OAUTH_BIND_RESULT_MESSAGE ||
          result.provider !== provider ||
          result.state !== state
        ) {
          return
        }
        cancelResultTimeout()
        if (result.success) {
          toast.success(i18next.t('Binding successful!'))
          window.close()
          return
        }
        toast.error(result.message || i18next.t('OAuth failed'))
        delayedClose = window.setTimeout(() => window.close(), 1500)
      }

      window.addEventListener('message', handleBindingResult)
      cancelResultTimeout = startOAuthBindResponseDeadline(() => {
        toast.error(i18next.t('OAuth binding timed out. Please try again.'))
        delayedClose = window.setTimeout(() => window.close(), 1500)
      })
      opener.postMessage(
        {
          type: OAUTH_BIND_CALLBACK_MESSAGE,
          provider,
          code,
          state,
          error: search.error,
          errorDescription: search.error_description,
        },
        window.location.origin
      )
      return () => {
        window.removeEventListener('message', handleBindingResult)
        cancelResultTimeout()
        if (delayedClose !== undefined) window.clearTimeout(delayedClose)
      }
    }

    if (!code && !search.error) {
      toast.error(i18next.t('Missing code'))
      safeNavigate('/sign-in', '/sign-in')
      return
    }

    let active = true
    void (async () => {
      try {
        const config: OAuthRequestConfig = {
          params: {
            code: code || undefined,
            state,
            error: search.error,
            error_description: search.error_description,
          },
          skipBusinessError: true,
        }
        const key = JSON.stringify([
          provider,
          code,
          state,
          search.error,
          search.error_description,
        ])
        if (callbackRequest.current?.key !== key) {
          passkeyAttempt.current += 1
          passkeyPending.current = false
          setIsPasskeyPending(false)
          setPasskeyChallenge(null)
          callbackRequest.current = {
            key,
            promise: api
              .get<ApiResponse>(`/api/oauth/${provider}`, config)
              .then((response) => response.data),
          }
        }
        const response = await callbackRequest.current.promise
        if (!active) return
        if (response?.success) {
          const data = response.data as Record<string, unknown> | undefined
          if (data?.require_2fa === true || data?.require_passkey === true) {
            if (typeof data.flow_token !== 'string' || !data.flow_token) {
              throw new Error(
                i18next.t('Login flow expired. Please sign in again.')
              )
            }
            if (data.require_2fa === true) {
              setPending2FAFlowToken(data.flow_token)
              redirectTo2FA()
              return
            }
            prepareCredentialRequestOptions(data.options)
            setPasskeyChallenge({
              options: data.options,
              flowToken: data.flow_token,
              expiresAt:
                typeof data.expires_at === 'number'
                  ? data.expires_at
                  : undefined,
            })
            return
          }
          if (isAuthBundle(response.data)) {
            completeLogin(response.data)
            return
          }
        }
        const messageKey = getServerErrorMessageKey(response)
        toast.error(
          messageKey
            ? i18next.t(messageKey)
            : response?.message || i18next.t('OAuth failed')
        )
      } catch (error: unknown) {
        if (!active) return
        const messageKey = getServerErrorMessageKey(error)
        const responseMessage = (
          error as { response?: { data?: { message?: string } } }
        ).response?.data?.message
        if (!messageKey) {
          toast.error(
            responseMessage ||
              (error instanceof Error
                ? error.message
                : i18next.t('OAuth failed'))
          )
        }
      }
      safeNavigate('/sign-in', '/sign-in')
    })()
    return () => {
      active = false
    }
  }, [
    callbackState,
    completeLogin,
    mode,
    navigate,
    provider,
    redirectTo2FA,
    safeNavigate,
    setPending2FAFlowToken,
    search.code,
    search.error,
    search.error_code,
    search.error_description,
    search.flow_token,
    search.redirect,
    search.telegram_bind,
  ])

  return (
    <OAuthCallbackScreen
      provider={provider}
      mode={mode}
      passkeyChallenge={
        mode === 'login' && passkeyChallenge
          ? {
              pending: isPasskeyPending,
              onVerify: handlePasskeyVerification,
              onCancel: handlePasskeyCancel,
            }
          : undefined
      }
    />
  )
}

export const Route = createFileRoute('/oauth/$provider')({
  component: OAuthCallback,
})
