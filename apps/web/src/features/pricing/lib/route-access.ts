/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { bootstrapAuthentication } from '@/lib/auth-session'
import { isConsoleActivated } from '@/lib/console-activation'
import { getFreshModuleAccess, type ModuleAccess } from '@/lib/nav-modules'
import { useAuthStore, type AuthUser } from '@/stores/auth-store'

type PricingRouteKind = 'marketplace' | 'details'

export type PricingRouteAccessDecision =
  | { kind: 'allow' }
  | { kind: 'redirect-home' }
  | { kind: 'redirect-marketplace' }
  | { kind: 'redirect-sign-in'; redirect: string }

export interface PricingRouteAccessDependencies {
  loadModuleAccess: () => Promise<ModuleAccess>
  bootstrapAuth: () => Promise<void>
  getUser: () => AuthUser | null
}

const defaultDependencies: PricingRouteAccessDependencies = {
  loadModuleAccess: () => getFreshModuleAccess('pricing'),
  bootstrapAuth: async () => {
    await bootstrapAuthentication()
  },
  getUser: () => useAuthStore.getState().auth.user,
}

export async function resolvePricingRouteAccess(
  locationHref: string,
  routeKind: PricingRouteKind,
  dependencies: PricingRouteAccessDependencies = defaultDependencies
): Promise<PricingRouteAccessDecision> {
  const access = await dependencies.loadModuleAccess()
  if (!access.enabled) {
    return { kind: 'redirect-home' }
  }

  const requiresResolvedAuth = access.requireAuth || routeKind === 'details'
  if (requiresResolvedAuth) {
    await dependencies.bootstrapAuth()
  }

  const user = dependencies.getUser()
  if (routeKind === 'details' && !isConsoleActivated(user)) {
    return { kind: 'redirect-marketplace' }
  }

  if (access.requireAuth && !user) {
    return { kind: 'redirect-sign-in', redirect: locationHref }
  }

  return { kind: 'allow' }
}
