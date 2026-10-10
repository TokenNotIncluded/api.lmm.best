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
  cloneHeaderNavDefaults,
  isHeaderNavAccessModule,
  parseHeaderNavAccess,
  parseHeaderNavBoolean,
  type HeaderNavModules as HeaderNavModulesConfig,
} from '@/lib/header-nav-config'

export type {
  HeaderNavModules as HeaderNavModulesConfig,
  ModuleAccess as HeaderNavAccessConfig,
} from '@/lib/header-nav-config'

export type SidebarSectionConfig = {
  enabled: boolean
  [key: string]: boolean
}

export type SidebarModulesAdminConfig = Record<string, SidebarSectionConfig>

export const HEADER_NAV_DEFAULT: HeaderNavModulesConfig =
  cloneHeaderNavDefaults()

export const SIDEBAR_MODULES_DEFAULT: SidebarModulesAdminConfig = {
  chat: {
    enabled: true,
    chat: true,
  },
  console: {
    enabled: true,
    detail: true,
    token: true,
    log: true,
    midjourney: true,
    task: true,
  },
  personal: {
    enabled: true,
    topup: true,
    personal: true,
  },
  admin: {
    enabled: true,
    channel: true,
    models: true,
    redemption: true,
    user: true,
    setting: true,
    subscription: true,
  },
}

const toBoolean = (value: unknown, fallback: boolean): boolean => {
  // Settings retain their existing rule: only the number 1 is true.
  return typeof value === 'number'
    ? value === 1
    : parseHeaderNavBoolean(value, fallback)
}

const cloneSidebarDefault = (): SidebarModulesAdminConfig =>
  Object.entries(SIDEBAR_MODULES_DEFAULT).reduce<SidebarModulesAdminConfig>(
    (acc, [section, config]) => {
      acc[section] = { ...config }
      return acc
    },
    {}
  )

export function parseHeaderNavModules(
  value: string | null | undefined
): HeaderNavModulesConfig {
  const base = cloneHeaderNavDefaults(HEADER_NAV_DEFAULT)
  if (!value) {
    return base
  }
  try {
    const parsed = JSON.parse(value) as Record<string, unknown>
    const result = cloneHeaderNavDefaults(base)

    Object.entries(parsed).forEach(([key, raw]) => {
      if (isHeaderNavAccessModule(key)) {
        result[key] = parseHeaderNavAccess(raw, base[key], toBoolean)
        return
      }

      if (typeof raw === 'boolean') {
        result[key] = raw
        return
      }
      if (typeof raw === 'string' || typeof raw === 'number') {
        result[key] = toBoolean(raw, Boolean(base[key]))
        return
      }
    })

    return result
  } catch {
    return base
  }
}

export function serializeHeaderNavModules(
  config: HeaderNavModulesConfig
): string {
  return JSON.stringify(config)
}

export function parseSidebarModulesAdmin(
  value: string | null | undefined
): SidebarModulesAdminConfig {
  const defaults = cloneSidebarDefault()
  // If empty string, null, or undefined, use default config
  if (!value || value.trim() === '') return defaults

  try {
    const parsed = JSON.parse(value) as Record<string, unknown>
    const result: SidebarModulesAdminConfig = {}

    Object.entries(parsed).forEach(([sectionKey, raw]) => {
      if (!raw || typeof raw !== 'object') return

      const defaultSection = defaults[sectionKey] ?? { enabled: true }
      const sectionConfig: SidebarSectionConfig = {
        enabled: toBoolean(
          (raw as Record<string, unknown>).enabled,
          defaultSection.enabled ?? true
        ),
      }

      Object.entries(raw as Record<string, unknown>).forEach(
        ([moduleKey, moduleValue]) => {
          if (moduleKey === 'enabled') return
          if (sectionKey === 'chat' && moduleKey === 'playground') return
          sectionConfig[moduleKey] = toBoolean(
            moduleValue,
            defaultSection[moduleKey] ?? true
          )
        }
      )

      result[sectionKey] = sectionConfig
    })

    // Merge defaults to ensure expected sections exist
    Object.entries(defaults).forEach(([sectionKey, config]) => {
      if (!result[sectionKey]) {
        result[sectionKey] = { ...config }
        return
      }

      Object.entries(config).forEach(([moduleKey, moduleValue]) => {
        if (!(moduleKey in result[sectionKey])) {
          result[sectionKey][moduleKey] = moduleValue
        }
      })
    })

    return result
  } catch {
    return defaults
  }
}

export function serializeSidebarModulesAdmin(
  config: SidebarModulesAdminConfig
): string {
  return JSON.stringify(config)
}
