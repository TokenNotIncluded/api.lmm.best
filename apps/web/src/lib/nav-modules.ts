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
import { getStatus } from '@/lib/api'
import {
  cloneHeaderNavDefaults,
  isHeaderNavAccessModule,
  parseHeaderNavAccess,
  parseHeaderNavBoolean,
  type HeaderNavModule,
  type HeaderNavModules,
  type ModuleAccess,
} from '@/lib/header-nav-config'
import { isSidebarModuleEnabledByModules } from '@/lib/sidebar-preferences'

export { parseHeaderNavBoolean } from '@/lib/header-nav-config'
export type {
  HeaderNavModule,
  HeaderNavModules,
  ModuleAccess,
} from '@/lib/header-nav-config'

const DEFAULT_HEADER_NAV_MODULES = cloneHeaderNavDefaults()

const DEFAULTS: Record<HeaderNavModule, ModuleAccess> = {
  pricing: DEFAULT_HEADER_NAV_MODULES.pricing,
  rankings: DEFAULT_HEADER_NAV_MODULES.rankings,
  security: DEFAULT_HEADER_NAV_MODULES.security,
}

function isUnknownRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function parseHeaderNavRecord(raw: unknown): Record<string, unknown> | null {
  if (isUnknownRecord(raw)) return raw
  if (raw == null || String(raw).trim() === '') return null

  try {
    const parsed: unknown = JSON.parse(String(raw))
    return isUnknownRecord(parsed) ? parsed : null
  } catch {
    return null
  }
}

export function parseHeaderNavModules(raw: unknown): HeaderNavModules {
  const result = cloneHeaderNavDefaults()
  const parsed = parseHeaderNavRecord(raw)
  if (!parsed) return result

  Object.entries(parsed).forEach(([key, value]) => {
    if (isHeaderNavAccessModule(key)) {
      // Runtime status treats arrays as invalid access objects.
      result[key] = parseHeaderNavAccess(
        Array.isArray(value) ? undefined : value,
        result[key]
      )
      return
    }

    const fallback = result[key]
    if (
      typeof fallback === 'boolean' ||
      typeof value === 'boolean' ||
      typeof value === 'number' ||
      typeof value === 'string'
    ) {
      result[key] = parseHeaderNavBoolean(
        value,
        typeof fallback === 'boolean' ? fallback : true
      )
    }
  })

  return result
}

export function parseHeaderNavModulesFromStatus(
  status: Record<string, unknown> | null
): HeaderNavModules {
  return parseHeaderNavModules(status?.HeaderNavModules)
}

function getCachedStatus(): Record<string, unknown> | null {
  try {
    if (typeof window === 'undefined') return null
    const raw = window.localStorage.getItem('status')
    if (!raw) return null
    const parsed: unknown = JSON.parse(raw)
    return isUnknownRecord(parsed) ? parsed : null
  } catch {
    return null
  }
}

/** The cached, administrator-controlled sidebar configuration, if present. */
export function getCachedSidebarModulesAdmin(): unknown {
  return getCachedStatus()?.SidebarModulesAdmin
}

function cacheStatus(status: Record<string, unknown> | null): void {
  try {
    if (typeof window !== 'undefined' && status) {
      window.localStorage.setItem('status', JSON.stringify(status))
    }
  } catch {
    /* empty */
  }
}

export function getModuleAccessFromStatus(
  status: Record<string, unknown> | null,
  module: HeaderNavModule
): ModuleAccess {
  return parseHeaderNavModulesFromStatus(status)[module] ?? DEFAULTS[module]
}

export function getModuleAccess(module: HeaderNavModule): ModuleAccess {
  return getModuleAccessFromStatus(getCachedStatus(), module)
}

export async function getFreshModuleAccess(
  module: HeaderNavModule
): Promise<ModuleAccess> {
  try {
    const status = await getStatus()
    cacheStatus(status)
    return getModuleAccessFromStatus(status, module)
  } catch {
    return { enabled: false, requireAuth: true }
  }
}

export function isSidebarModuleEnabled(
  section: string,
  module: string
): boolean {
  return isSidebarModuleEnabledByModules(
    section,
    module,
    getCachedSidebarModulesAdmin()
  )
}
