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
import type { AuthUser, OnboardingStage } from '@/stores/auth-store'

import { getCachedSidebarModulesAdmin } from './nav-modules'
import { ROLE } from './roles'
import {
  isSidebarRouteHidden,
  isSidebarRouteEnabledByModules,
  parseSidebarUserSettings,
  SIDEBAR_DEFAULT_PREFERENCES,
  SIDEBAR_DEFAULT_ROUTE_ALLOWLIST,
} from './sidebar-preferences'

const ONBOARDING_STAGES = new Set<OnboardingStage>([
  'activate',
  'credential',
  'first_request',
  'complete',
])

export type NormalizedOnboardingState = {
  activationComplete: boolean
  credentialComplete: boolean
  firstRequestComplete: boolean
  stage: OnboardingStage
  isExplicit: boolean
}

function isOnboardingStage(value: unknown): value is OnboardingStage {
  return (
    typeof value === 'string' && ONBOARDING_STAGES.has(value as OnboardingStage)
  )
}

function deriveStage(state: {
  activationComplete: boolean
  credentialComplete: boolean
  firstRequestComplete: boolean
}): OnboardingStage {
  if (!state.activationComplete) return 'activate'
  if (!state.credentialComplete) return 'credential'
  if (!state.firstRequestComplete) return 'first_request'
  return 'complete'
}

/**
 * Normalizes onboarding details without inferring access from legacy fields.
 * The server-provided developer access decision is the only access boundary.
 */
export function getOnboardingState(
  user: AuthUser | null | undefined
): NormalizedOnboardingState {
  if (!user) {
    return {
      activationComplete: false,
      credentialComplete: false,
      firstRequestComplete: false,
      stage: 'activate',
      isExplicit: false,
    }
  }

  const activationComplete = user.developer_access_granted === true

  const hasNestedState = user.onboarding !== undefined
  const nested =
    user.onboarding && typeof user.onboarding === 'object'
      ? (user.onboarding as unknown as Record<string, unknown>)
      : undefined
  const hasFlatState =
    user.activation_complete !== undefined ||
    user.credential_complete !== undefined ||
    user.first_request_complete !== undefined ||
    user.onboarding_stage !== undefined

  const source = nested ?? {
    activation_complete: user.activation_complete,
    credential_complete: user.credential_complete,
    first_request_complete: user.first_request_complete,
    stage: user.onboarding_stage,
  }
  const statedStage = isOnboardingStage(source.stage) ? source.stage : undefined
  const mayInferFromStage = !hasNestedState
  const rawCredentialComplete =
    typeof source.credential_complete === 'boolean'
      ? source.credential_complete
      : mayInferFromStage &&
        (statedStage === 'first_request' || statedStage === 'complete')
  const rawFirstRequestComplete =
    typeof source.first_request_complete === 'boolean'
      ? source.first_request_complete
      : mayInferFromStage && statedStage === 'complete'
  const credentialComplete =
    activationComplete && rawCredentialComplete === true
  const firstRequestComplete =
    credentialComplete && rawFirstRequestComplete === true

  return {
    activationComplete,
    credentialComplete,
    firstRequestComplete,
    stage: deriveStage({
      activationComplete,
      credentialComplete,
      firstRequestComplete,
    }),
    isExplicit:
      user.developer_access_granted !== undefined ||
      hasNestedState ||
      hasFlatState,
  }
}

export function isConsoleActivated(user: AuthUser | null | undefined): boolean {
  return user?.developer_access_granted === true
}

export function getAuthenticatedLandingRoute(
  user: AuthUser | null | undefined,
  sidebarModulesAdmin: unknown = getCachedSidebarModulesAdmin()
): string {
  // Administrator approval is the access boundary.  The remaining setup
  // checklist is guidance for an already-enabled account and must not trap a
  // newly approved L1 user on the L0 welcome page.
  if (!getOnboardingState(user).activationComplete) return '/getting-started'

  // Keep the login redirect aligned with the rendered sidebar. Accounts that
  // cannot edit sidebar settings do not have an effective user overlay.
  const settings =
    user?.permissions?.sidebar_settings === false
      ? null
      : parseSidebarUserSettings(user?.sidebar_modules)
  const requested = settings?.preferences.default_route
  const role = user?.role ?? ROLE.GUEST
  const adminOnlyRoutes = new Set([
    '/channels',
    '/models/metadata',
    '/users',
    '/redemption-codes',
    '/discount-codes',
    '/red-packets',
    '/subscriptions',
  ])
  const superAdminOnlyRoutes = new Set([
    '/system-info',
    '/system-settings/site',
  ])
  const allowed =
    typeof requested === 'string' &&
    SIDEBAR_DEFAULT_ROUTE_ALLOWLIST.has(requested) &&
    !isSidebarRouteHidden(
      requested,
      settings?.preferences ?? SIDEBAR_DEFAULT_PREFERENCES
    ) &&
    isSidebarRouteEnabledByModules(requested, sidebarModulesAdmin) &&
    isSidebarRouteEnabledByModules(requested, settings?.modules) &&
    (!adminOnlyRoutes.has(requested) || role >= ROLE.ADMIN) &&
    (!superAdminOnlyRoutes.has(requested) || role === ROLE.SUPER_ADMIN)

  return allowed ? requested : '/dashboard'
}

export function isContributorRoute(pathname: string): boolean {
  // L0 can explore and reach checkout before paid activation. Payment methods
  // remain subject to the server's payment access gate; developer and bounty
  // management routes still require L1.
  return (
    pathname === '/getting-started' ||
    pathname.startsWith('/getting-started/') ||
    pathname === '/wallet' ||
    pathname === '/wallet/' ||
    pathname === '/tool-market' ||
    pathname.startsWith('/tool-market/')
  )
}

export function isRestrictedPublicRoute(pathname: string): boolean {
  return ['/about', '/rankings'].some(
    (path) => pathname === path || pathname.startsWith(`${path}/`)
  )
}
