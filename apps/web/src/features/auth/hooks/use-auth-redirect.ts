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
import { useNavigate } from '@tanstack/react-router'
import i18n from 'i18next'
import { useCallback } from 'react'

import {
  getSavedLanguage,
  sanitizeAuthRedirect,
} from '@/features/auth/lib/auth-redirect'
import { normalizeInterfaceLanguage } from '@/i18n/languages'
import { applyAuthBundle } from '@/lib/api'
import { getAuthenticatedLandingRoute } from '@/lib/console-activation'
import { useAuthStore, type AuthBundle } from '@/stores/auth-store'

/**
 * Hook for handling authentication redirects and user data management
 */
export function useAuthRedirect() {
  const navigate = useNavigate()

  /**
   * Handle successful login
   * @param userData - Optional user data from login response
   * @param redirectTo - Redirect path after login
   */
  const handleLoginSuccess = async (
    bundle: AuthBundle,
    redirectTo?: string,
    alreadyCommitted = false,
    signal?: AbortSignal
  ) => {
    if (!alreadyCommitted) applyAuthBundle(bundle)
    const epoch = useAuthStore.getState().auth.accountWriteEpoch
    const isCurrent = () => {
      const auth = useAuthStore.getState().auth
      return (
        !signal?.aborted &&
        auth.accountWriteEpoch === epoch &&
        auth.user?.id === bundle.user.id &&
        auth.session?.sid === bundle.session.sid &&
        auth.accessToken === bundle.access_token
      )
    }
    if (!isCurrent()) return
    const preference = getSavedLanguage(bundle.user)
    const savedLang = preference
      ? normalizeInterfaceLanguage(preference)
      : undefined
    if (savedLang && savedLang !== i18n.language) {
      // Loading resources must not change the global language for a login that
      // is cancelled or superseded while its backend request is outstanding.
      await i18n.loadLanguages(savedLang)
      // loadLanguages can resolve early for an already-preloaded language.
      // loadResources joins pending loads and includes the current complete
      // namespace list, resolution hierarchy and configured fallback languages.
      await new Promise<void>((resolve, reject) => {
        i18n.loadResources((error) => (error ? reject(error) : resolve()))
      })
      if (!isCurrent()) return
      await i18n.changeLanguage(savedLang)
    }
    if (!isCurrent()) return

    const requestedPath = sanitizeAuthRedirect(
      redirectTo,
      window.location.origin
    )
    const targetPath =
      requestedPath ??
      sanitizeAuthRedirect(
        getAuthenticatedLandingRoute(bundle.user),
        window.location.origin
      ) ??
      '/'
    // Preserve the complete checked href and wait for navigation to finish.
    await navigate({ href: targetPath, replace: true })
  }

  /**
   * Redirect to 2FA page
   */
  const redirectTo2FA = useCallback(
    (redirectTo?: string) => {
      const requestedPath = sanitizeAuthRedirect(
        redirectTo,
        window.location.origin
      )
      navigate({
        to: '/otp',
        search: requestedPath ? { redirect: requestedPath } : {},
        replace: true,
      })
    },
    [navigate]
  )

  /**
   * Redirect to login page
   */
  const redirectToLogin = (redirectTo?: string) => {
    const requestedPath = sanitizeAuthRedirect(
      redirectTo,
      window.location.origin
    )
    navigate({
      to: '/sign-in',
      search: requestedPath ? { redirect: requestedPath } : {},
      replace: true,
    })
  }

  /**
   * Redirect to register page
   */
  const redirectToRegister = () => {
    navigate({ to: '/sign-up', replace: true })
  }

  return {
    handleLoginSuccess,
    redirectTo2FA,
    redirectToLogin,
    redirectToRegister,
  }
}
