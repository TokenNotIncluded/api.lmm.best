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
export type ModuleAccess = { enabled: boolean; requireAuth: boolean }

const HEADER_NAV_ACCESS_MODULES = ['rankings', 'pricing', 'security'] as const

export type HeaderNavModule = (typeof HEADER_NAV_ACCESS_MODULES)[number]

export function isHeaderNavAccessModule(key: string): key is HeaderNavModule {
  return HEADER_NAV_ACCESS_MODULES.some((module) => module === key)
}

export type HeaderNavModules = {
  home: boolean
  console: boolean
  pricing: ModuleAccess
  rankings: ModuleAccess
  security: ModuleAccess
  docs: boolean
  about: boolean
  [key: string]: boolean | ModuleAccess
}

const DEFAULT_HEADER_NAV_MODULES: HeaderNavModules = {
  home: true,
  console: true,
  pricing: { enabled: true, requireAuth: false },
  rankings: { enabled: true, requireAuth: false },
  security: { enabled: true, requireAuth: false },
  docs: true,
  about: true,
}

export function cloneHeaderNavDefaults(
  defaults: HeaderNavModules = DEFAULT_HEADER_NAV_MODULES
): HeaderNavModules {
  return {
    ...defaults,
    pricing: { ...defaults.pricing },
    rankings: { ...defaults.rankings },
    security: { ...defaults.security },
  }
}

export function parseHeaderNavBoolean(
  raw: unknown,
  fallback: boolean
): boolean {
  if (typeof raw === 'boolean') {
    return raw
  }
  if (typeof raw === 'number') {
    if (raw === 1) {
      return true
    }
    if (raw === 0) {
      return false
    }
    return fallback
  }
  if (typeof raw === 'string') {
    const normalized = raw.trim().toLowerCase()
    if (normalized === 'true' || normalized === '1') {
      return true
    }
    if (normalized === 'false' || normalized === '0') {
      return false
    }
  }
  return fallback
}

export function parseHeaderNavAccess(
  raw: unknown,
  fallback: ModuleAccess,
  parseBoolean = parseHeaderNavBoolean
): ModuleAccess {
  if (
    typeof raw === 'boolean' ||
    typeof raw === 'number' ||
    typeof raw === 'string'
  ) {
    return {
      enabled: parseBoolean(raw, fallback.enabled),
      requireAuth: fallback.requireAuth,
    }
  }
  if (raw && typeof raw === 'object') {
    const record = raw as Record<string, unknown>
    return {
      enabled: parseBoolean(record.enabled, fallback.enabled),
      requireAuth: parseBoolean(record.requireAuth, fallback.requireAuth),
    }
  }
  return { ...fallback }
}
