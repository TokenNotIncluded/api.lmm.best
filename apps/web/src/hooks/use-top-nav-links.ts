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
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { useStatus } from '@/hooks/use-status'
import { getAuthenticatedLandingRoute } from '@/lib/console-activation'
import { parseHeaderNavModulesFromStatus } from '@/lib/nav-modules'
import { useAuthStore } from '@/stores/auth-store'

export type TopNavLink = {
  title: string
  href: string
  disabled?: boolean
  requiresAuth?: boolean
  external?: boolean
}

/**
 * Generate top navigation links based on HeaderNavModules configuration from backend /api/status
 * Backend format example (stringified JSON):
 * {
 *   home: true,
 *   console: true,
 *   pricing: { enabled: true, requireAuth: false },
 *   rankings: { enabled: true, requireAuth: false },
 *   security: { enabled: true, requireAuth: false },
 *   docs: true,
 *   about: true
 * }
 */
export function useTopNavLinks(): TopNavLink[] {
  const { t } = useTranslation()
  const { status } = useStatus()
  const { auth } = useAuthStore()

  // Parse HeaderNavModules
  const modules = useMemo(() => {
    return parseHeaderNavModulesFromStatus(
      status as Record<string, unknown> | null
    )
  }, [status])

  // Documentation link (may be external)
  const docsLink: string | undefined = status?.docs_link as string | undefined

  const isAuthed = !!auth?.user
  const hasDocsAccess = auth.user?.permissions?.docs_access === true

  const links: TopNavLink[] = []

  // Home
  if (modules?.home !== false) {
    links.push({ title: t('Home'), href: '/' })
  }

  // Keep the product workspace ahead of the optional developer console. The
  // landing route already resolves to the surface the account may actually
  // open, so an L0 account is sent to onboarding instead of a guarded page.
  if (modules?.console !== false && isAuthed) {
    links.push({
      title: t('Open workspace'),
      href: getAuthenticatedLandingRoute(auth.user),
    })
  }

  // Pricing
  const pricing = modules?.pricing
  if (pricing && typeof pricing === 'object' && pricing.enabled) {
    const requiresAuth = pricing.requireAuth && !isAuthed
    links.push({
      title: t('Models and pricing'),
      href: '/pricing',
      requiresAuth,
    })
  }

  links.push({ title: t('Scripts'), href: '/scripts' })
  links.push({ title: t('Developers'), href: '/developers' })
  links.push({ title: 'WebMCP', href: '/webmcp' })
  links.push({ title: t('Signal path'), href: '/games/signal' })

  // Rankings
  const rankings = modules?.rankings
  if (rankings && typeof rankings === 'object' && rankings.enabled) {
    const requiresAuth = rankings.requireAuth && !isAuthed
    links.push({ title: t('Rankings'), href: '/rankings', requiresAuth })
  }

  // Security is an access-controlled public module, just like pricing and
  // rankings. The default keeps the page public; administrators can hide it
  // or require authentication through HeaderNavModules.
  const security = modules?.security
  if (security && typeof security === 'object' && security.enabled) {
    const requiresAuth = security.requireAuth && !isAuthed
    links.push({ title: t('Security'), href: '/security', requiresAuth })
  }

  // Paid documentation is never synthesized as a public local route. The
  // backend only returns the configured link to eligible accounts.
  if (modules?.docs !== false && hasDocsAccess && docsLink) {
    links.push({ title: t('Docs'), href: docsLink, external: true })
  }

  // About
  if (modules?.about !== false) {
    links.push({ title: t('About'), href: '/about' })
  }

  return links
}
