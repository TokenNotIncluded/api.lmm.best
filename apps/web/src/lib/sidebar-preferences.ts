/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/

export const SIDEBAR_DENSITY_VALUES = ['comfortable', 'compact'] as const

export type SidebarDensity = (typeof SIDEBAR_DENSITY_VALUES)[number]

export type SidebarPreferences = {
  section_order: string[]
  module_order: Record<string, string[]>
  hidden_sections: string[]
  hidden: string[]
  density: SidebarDensity
  default_route: string
}

export type SidebarUserSettings = {
  modules: Record<string, Record<string, boolean>>
  preferences: SidebarPreferences
}

export const SIDEBAR_DEFAULT_PREFERENCES: SidebarPreferences = {
  section_order: [],
  module_order: {},
  hidden_sections: [],
  hidden: [],
  density: 'comfortable',
  default_route: '',
}

const MAX_PREFERENCE_ITEMS = 100

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function uniqueStrings(value: unknown): string[] {
  if (!Array.isArray(value)) return []
  return [
    ...new Set(
      value.filter(
        (item): item is string =>
          typeof item === 'string' && item.trim().length > 0
      )
    ),
  ].slice(0, MAX_PREFERENCE_ITEMS)
}

function normalizeModules(
  value: unknown
): Record<string, Record<string, boolean>> {
  if (!isRecord(value)) return {}

  const modules: Record<string, Record<string, boolean>> = {}
  for (const [sectionKey, sectionValue] of Object.entries(value)) {
    if (!isRecord(sectionValue)) continue
    const section: Record<string, boolean> = {}
    for (const [moduleKey, moduleValue] of Object.entries(sectionValue)) {
      if (typeof moduleValue === 'boolean') section[moduleKey] = moduleValue
    }
    if (Object.keys(section).length > 0) modules[sectionKey] = section
  }
  return modules
}

export function normalizeSidebarPreferences(
  value: unknown
): SidebarPreferences {
  const raw = isRecord(value) ? value : {}
  const rawModuleOrder = isRecord(raw.module_order) ? raw.module_order : {}
  const moduleOrder: Record<string, string[]> = {}

  for (const [sectionKey, itemOrder] of Object.entries(rawModuleOrder)) {
    const normalized = uniqueStrings(itemOrder)
    if (normalized.length > 0) moduleOrder[sectionKey] = normalized
  }

  return {
    section_order: uniqueStrings(raw.section_order),
    module_order: moduleOrder,
    hidden_sections: uniqueStrings(raw.hidden_sections),
    hidden: uniqueStrings(raw.hidden),
    density:
      raw.density === 'compact' || raw.density === 'comfortable'
        ? raw.density
        : SIDEBAR_DEFAULT_PREFERENCES.density,
    default_route:
      typeof raw.default_route === 'string' ? raw.default_route : '',
  }
}

/**
 * Reads both the current envelope and the legacy section-only JSON format.
 * Legacy values remain valid and receive default presentation preferences.
 */
export function parseSidebarUserSettings(
  value: unknown
): SidebarUserSettings | null {
  if (typeof value !== 'string' || value.trim() === '') return null

  try {
    const parsed: unknown = JSON.parse(value)
    if (!isRecord(parsed)) return null

    const hasEnvelope = isRecord(parsed.modules)
    return {
      modules: normalizeModules(hasEnvelope ? parsed.modules : parsed),
      preferences: normalizeSidebarPreferences(
        hasEnvelope ? parsed.preferences : undefined
      ),
    }
  } catch {
    return null
  }
}

export function serializeSidebarUserSettings(
  modules: Record<string, Record<string, boolean>>,
  preferences: SidebarPreferences
): string {
  return JSON.stringify({
    modules,
    preferences: normalizeSidebarPreferences(preferences),
  })
}

/** Routes that may be selected as a post-login landing page. */
export const SIDEBAR_DEFAULT_ROUTE_ALLOWLIST = new Set([
  '/getting-started',
  '/open-source-bounties',
  '/public-relay',
  '/challenges',
  '/rankings',
  '/chat-management',
  '/dashboard/overview',
  '/dashboard/models',
  '/status',
  '/keys',
  '/drawing',
  '/usage-logs/common',
  '/usage-logs/task',
  '/wallet',
  '/company',
  '/profile',
  '/support',
  '/todos',
  '/channels',
  '/models/metadata',
  '/users',
  '/redemption-codes',
  '/discount-codes',
  '/subscriptions',
  '/system-info',
  '/system-settings/site',
]) as ReadonlySet<string>

export const SIDEBAR_ROUTE_SECTION: Readonly<Record<string, string>> = {
  '/getting-started': 'onboarding',
  '/open-source-bounties': 'forge',
  '/public-relay': 'forge',
  '/challenges': 'forge',
  '/rankings': 'forge',
  '/chat-management': 'chat',
  '/dashboard/overview': 'general',
  '/dashboard/models': 'general',
  '/status': 'general',
  '/keys': 'general',
  '/drawing': 'general',
  '/usage-logs/common': 'general',
  '/usage-logs/task': 'general',
  '/wallet': 'personal',
  '/company': 'personal',
  '/profile': 'personal',
  '/support': 'personal',
  '/todos': 'personal',
  '/channels': 'admin',
  '/models/metadata': 'admin',
  '/users': 'admin',
  '/redemption-codes': 'admin',
  '/discount-codes': 'admin',
  '/subscriptions': 'admin',
  '/system-info': 'admin',
  '/system-settings/site': 'admin',
}

/**
 * Maps a selectable landing page to the same configuration switch used by
 * the sidebar. Routes without an entry are intentionally configuration-free.
 */
export const SIDEBAR_ROUTE_MODULE: Readonly<
  Record<string, { section: string; module: string }>
> = {
  '/dashboard': { section: 'console', module: 'detail' },
  '/dashboard/overview': { section: 'console', module: 'detail' },
  '/dashboard/models': { section: 'console', module: 'detail' },
  '/chat-management': { section: 'chat', module: 'chat' },
  '/keys': { section: 'console', module: 'token' },
  '/drawing': { section: 'console', module: 'midjourney' },
  '/usage-logs/common': { section: 'console', module: 'log' },
  '/usage-logs/task': { section: 'console', module: 'task' },
  '/wallet': { section: 'personal', module: 'topup' },
  '/company': { section: 'personal', module: 'company' },
  '/profile': { section: 'personal', module: 'personal' },
  '/open-source-bounties': { section: 'personal', module: 'personal' },
  '/public-relay': { section: 'personal', module: 'personal' },
  '/channels': { section: 'admin', module: 'channel' },
  '/models/metadata': { section: 'admin', module: 'models' },
  '/users': { section: 'admin', module: 'user' },
  '/redemption-codes': { section: 'admin', module: 'redemption' },
  '/subscriptions': { section: 'admin', module: 'subscription' },
  '/system-info': { section: 'admin', module: 'setting' },
  '/system-settings/site': { section: 'admin', module: 'setting' },
}

function parseSidebarModuleConfig(
  value: unknown
): Record<string, Record<string, unknown>> | null {
  if (typeof value === 'string') {
    try {
      return parseSidebarModuleConfig(JSON.parse(value))
    } catch {
      return null
    }
  }
  if (!isRecord(value)) return null

  const result: Record<string, Record<string, unknown>> = {}
  for (const [section, sectionValue] of Object.entries(value)) {
    if (isRecord(sectionValue)) result[section] = sectionValue
  }
  return result
}

/**
 * Returns whether a configured sidebar route remains enabled by a module
 * layer. It intentionally treats missing or malformed configuration as
 * non-restrictive, matching the sidebar's backwards-compatible behaviour.
 * Only explicit `false` closes a section or module.
 */
export function isSidebarRouteEnabledByModules(
  route: string,
  value: unknown
): boolean {
  const mapping = SIDEBAR_ROUTE_MODULE[route]
  if (!mapping) return true

  return isSidebarModuleEnabledByModules(mapping.section, mapping.module, value)
}

/** Pure module-layer check shared by route and legacy section/module callers. */
export function isSidebarModuleEnabledByModules(
  sectionName: string,
  moduleName: string,
  value: unknown
): boolean {
  const config = parseSidebarModuleConfig(value)
  const section = config?.[sectionName]
  if (!section) return true
  return section.enabled !== false && section[moduleName] !== false
}

export function isSidebarRouteHidden(
  route: string,
  preferences: SidebarPreferences
): boolean {
  return (
    preferences.hidden.includes(route) ||
    (SIDEBAR_ROUTE_SECTION[route] !== undefined &&
      preferences.hidden_sections.includes(SIDEBAR_ROUTE_SECTION[route]))
  )
}

/**
 * Resolve a user-selected landing page using an internal allowlist only.
 * Invalid, external, or explicitly hidden routes use the normal fallback.
 */
export function resolveSidebarDefaultRoute(
  value: unknown,
  fallback = '/dashboard'
): string {
  const settings = parseSidebarUserSettings(value)
  const preferences = settings?.preferences ?? SIDEBAR_DEFAULT_PREFERENCES
  const route = preferences.default_route
  if (
    !SIDEBAR_DEFAULT_ROUTE_ALLOWLIST.has(route) ||
    isSidebarRouteHidden(route, preferences)
  ) {
    return fallback
  }
  return route
}
