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
import { useState, useRef, useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { clearAuthentication, isAuthBundle } from '@/lib/api'

import {
  createOAuthFlow,
  logout,
  startTelegramLogin,
  telegramLogin,
} from '../api'
import {
  buildGitHubOAuthUrl,
  buildDiscordOAuthUrl,
  buildOIDCOAuthUrl,
  buildLinuxDOOAuthUrl,
} from '../lib/oauth'
import { pickTelegramAuthorization } from '../lib/telegram-login'
import type { SystemStatus, CustomOAuthProviderInfo } from '../types'
import { useAuthRedirect } from './use-auth-redirect'

const GITHUB_AUTHORIZATION_ENDPOINT = 'https://github.com/login/oauth/authorize'
const DISCORD_AUTHORIZATION_ENDPOINT = 'https://discord.com/oauth2/authorize'
const LINUX_DO_AUTHORIZATION_ENDPOINT =
  'https://connect.linux.do/oauth2/authorize'
const LOOPBACK_HOSTNAMES = new Set(['localhost', '127.0.0.1', '::1', '[::1]'])

function trustedOAuthEndpoint(value: string): URL | null {
  try {
    const url = new URL(value)
    const isSecure = url.protocol === 'https:'
    const isLocalDevelopment =
      import.meta.env.DEV &&
      typeof window !== 'undefined' &&
      url.protocol === 'http:' &&
      LOOPBACK_HOSTNAMES.has(window.location.hostname) &&
      LOOPBACK_HOSTNAMES.has(url.hostname)

    if (
      (!isSecure && !isLocalDevelopment) ||
      !url.hostname ||
      url.username ||
      url.password ||
      url.hash
    ) {
      return null
    }
    return url
  } catch {
    return null
  }
}

function navigateToTrustedOAuthEndpoint(
  value: string,
  configuredEndpoint: string
): void {
  const target = trustedOAuthEndpoint(value)
  const endpoint = trustedOAuthEndpoint(configuredEndpoint)
  if (
    !target ||
    !endpoint ||
    target.origin !== endpoint.origin ||
    target.pathname !== endpoint.pathname
  ) {
    throw new Error('Untrusted OAuth authorization endpoint')
  }

  window.location.assign(target.toString())
}

/**
 * Hook for managing OAuth login
 */
export function useOAuthLogin(
  status: SystemStatus | null,
  redirectTo?: string,
  acceptedLegal = false
) {
  const { t } = useTranslation()
  const { handleLoginSuccess } = useAuthRedirect()
  const [isLoading, setIsLoading] = useState(false)
  const [isTelegramDialogOpen, setIsTelegramDialogOpen] = useState(false)
  const [isTelegramPending, setIsTelegramPending] = useState(false)
  const [telegramFlowToken, setTelegramFlowToken] = useState('')
  const [githubButtonText, setGithubButtonText] = useState('')
  const [githubButtonDisabled, setGithubButtonDisabled] = useState(false)
  const githubTimeoutRef = useRef<NodeJS.Timeout | null>(null)

  useEffect(() => {
    setGithubButtonText(t('Continue with GitHub'))

    return () => {
      if (githubTimeoutRef.current) {
        clearTimeout(githubTimeoutRef.current)
      }
    }
  }, [t])

  const resetSession = async () => {
    const response = await logout()
    if (!response.success) {
      throw new Error(response.message || t('Failed to sign out session'))
    }
    clearAuthentication()
  }

  const handleGitHubLogin = async () => {
    if (!status?.github_client_id) return
    if (githubButtonDisabled) return

    setIsLoading(true)
    setGithubButtonDisabled(true)
    setGithubButtonText(t('Redirecting to GitHub...'))

    if (githubTimeoutRef.current) {
      clearTimeout(githubTimeoutRef.current)
    }

    githubTimeoutRef.current = setTimeout(() => {
      setIsLoading(false)
      setGithubButtonText(
        t('Request timed out, please refresh and restart GitHub login')
      )
      setGithubButtonDisabled(true)
    }, 20000)

    try {
      await resetSession()
      const state = await createOAuthFlow('github', 'login', acceptedLegal)

      const url = buildGitHubOAuthUrl(status.github_client_id, state)
      navigateToTrustedOAuthEndpoint(url, GITHUB_AUTHORIZATION_ENDPOINT)
    } catch {
      toast.error(t('Failed to start GitHub login'))
      if (githubTimeoutRef.current) {
        clearTimeout(githubTimeoutRef.current)
      }
      setIsLoading(false)
      setGithubButtonText(t('Continue with GitHub'))
      setGithubButtonDisabled(false)
    }
  }

  const handleDiscordLogin = async () => {
    if (!status?.discord_client_id) return

    setIsLoading(true)
    try {
      await resetSession()
      const state = await createOAuthFlow('discord', 'login', acceptedLegal)

      const url = buildDiscordOAuthUrl(status.discord_client_id, state)
      navigateToTrustedOAuthEndpoint(url, DISCORD_AUTHORIZATION_ENDPOINT)
    } catch {
      toast.error(t('Failed to start Discord login'))
    } finally {
      setIsLoading(false)
    }
  }

  const handleOIDCLogin = async () => {
    if (!status?.oidc_authorization_endpoint || !status?.oidc_client_id) return

    setIsLoading(true)
    try {
      await resetSession()
      const state = await createOAuthFlow('oidc', 'login', acceptedLegal)

      const url = buildOIDCOAuthUrl(
        status.oidc_authorization_endpoint,
        status.oidc_client_id,
        state
      )
      navigateToTrustedOAuthEndpoint(url, status.oidc_authorization_endpoint)
    } catch {
      toast.error(t('Failed to start OIDC login'))
    } finally {
      setIsLoading(false)
    }
  }

  const handleLinuxDOLogin = async () => {
    if (!status?.linuxdo_client_id) return

    setIsLoading(true)
    try {
      await resetSession()
      const state = await createOAuthFlow('linuxdo', 'login', acceptedLegal)

      const url = buildLinuxDOOAuthUrl(status.linuxdo_client_id, state)
      navigateToTrustedOAuthEndpoint(url, LINUX_DO_AUTHORIZATION_ENDPOINT)
    } catch {
      toast.error(t('Failed to start LinuxDO login'))
    } finally {
      setIsLoading(false)
    }
  }

  const handleTelegramLogin = async () => {
    if (!status?.telegram_bot_name?.trim()) {
      toast.error(t('Login failed'))
      return
    }

    setIsLoading(true)
    try {
      await resetSession()
      const flowToken = await startTelegramLogin()
      setTelegramFlowToken(flowToken)
      setIsTelegramDialogOpen(true)
    } catch {
      toast.error(
        t('Failed to start {{provider}} login', { provider: 'Telegram' })
      )
    } finally {
      setIsLoading(false)
    }
  }

  const handleTelegramAuthorization = async (value: unknown) => {
    const authorization = pickTelegramAuthorization(value)
    if (!authorization) {
      toast.error(t('Login failed'))
      return
    }

    if (!telegramFlowToken) {
      toast.error(t('Login failed'))
      return
    }

    setIsTelegramPending(true)
    try {
      const response = await telegramLogin(authorization, telegramFlowToken)
      if (!response.success || !isAuthBundle(response.data)) {
        toast.error(t('Login failed'))
        return
      }

      setIsTelegramDialogOpen(false)
      setTelegramFlowToken('')
      await handleLoginSuccess(response.data, redirectTo)
      toast.success(t('Welcome back!'))
    } catch {
      toast.error(t('Login failed'))
    } finally {
      setIsTelegramPending(false)
    }
  }

  const handleTelegramDialogChange = (open: boolean) => {
    setIsTelegramDialogOpen(open)
    if (!open) {
      setTelegramFlowToken('')
      setIsTelegramPending(false)
    }
  }

  const handleCustomOAuthLogin = async (provider: CustomOAuthProviderInfo) => {
    if (!provider.authorization_endpoint || !provider.client_id) return

    setIsLoading(true)
    try {
      await resetSession()
      const state = await createOAuthFlow(provider.slug, 'login', acceptedLegal)

      const redirectUri = `${window.location.origin}/oauth/${provider.slug}`
      const url = new URL(provider.authorization_endpoint)
      url.searchParams.set('client_id', provider.client_id)
      url.searchParams.set('redirect_uri', redirectUri)
      url.searchParams.set('response_type', 'code')
      url.searchParams.set('state', state)
      if (provider.scopes) {
        url.searchParams.set('scope', provider.scopes)
      }

      navigateToTrustedOAuthEndpoint(
        url.toString(),
        provider.authorization_endpoint
      )
    } catch {
      toast.error(
        t('Failed to start {{provider}} login', { provider: provider.name })
      )
    } finally {
      setIsLoading(false)
    }
  }

  return {
    isLoading,
    githubButtonText,
    githubButtonDisabled,
    isTelegramDialogOpen,
    isTelegramPending,
    handleGitHubLogin,
    handleDiscordLogin,
    handleOIDCLogin,
    handleLinuxDOLogin,
    handleTelegramLogin,
    handleTelegramAuthorization,
    handleTelegramDialogChange,
    handleCustomOAuthLogin,
  }
}
