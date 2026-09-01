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

import type { NavGroup, NavItem } from '@/components/layout/types'
import { useStatus } from '@/hooks/use-status'
import {
  parseSidebarUserSettings,
  SIDEBAR_DEFAULT_PREFERENCES,
  type SidebarDensity,
  type SidebarPreferences,
  type SidebarUserSettings,
} from '@/lib/sidebar-preferences'
import { useAuthStore } from '@/stores/auth-store'

type SidebarSectionConfig = {
  enabled: boolean
  [key: string]: boolean
}

type SidebarModulesAdminConfig = Record<string, SidebarSectionConfig>

// User-layer config is a narrower overlay. Legacy section-only JSON is
// normalized by parseSidebarUserConfig so existing accounts keep working.
type SidebarModulesUserConfig = SidebarUserSettings | null

/**
 * Default sidebar modules configuration
 */
const DEFAULT_SIDEBAR_MODULES: SidebarModulesAdminConfig = {
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
    company: true,
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

const mergeWithDefaultSidebarModules = (
  config: SidebarModulesAdminConfig
): SidebarModulesAdminConfig => {
  const merged: SidebarModulesAdminConfig = { ...config }

  Object.entries(DEFAULT_SIDEBAR_MODULES).forEach(
    ([sectionKey, defaultSection]) => {
      const existingSection = merged[sectionKey]
      if (!existingSection) {
        merged[sectionKey] = { ...defaultSection }
        return
      }

      merged[sectionKey] = { ...defaultSection, ...existingSection }
      Object.keys(defaultSection).forEach((moduleKey) => {
        if (merged[sectionKey][moduleKey] === undefined) {
          merged[sectionKey][moduleKey] = defaultSection[moduleKey]
        }
      })
    }
  )

  return merged
}

/**
 * Mapping from URL to configuration keys
 */
const URL_TO_CONFIG_MAP: Record<string, { section: string; module: string }> = {
  '/dashboard': { section: 'console', module: 'detail' },
  '/dashboard/overview': { section: 'console', module: 'detail' },
  '/dashboard/models': { section: 'console', module: 'detail' },
  '/dashboard/users': { section: 'console', module: 'detail' },
  '/chat-management': { section: 'chat', module: 'chat' },
  '/keys': { section: 'console', module: 'token' },
  '/drawing': { section: 'console', module: 'midjourney' },
  '/usage-logs': { section: 'console', module: 'log' },
  '/usage-logs/common': { section: 'console', module: 'log' },
  '/usage-logs/drawing': { section: 'console', module: 'midjourney' },
  '/usage-logs/task': { section: 'console', module: 'task' },
  '/wallet': { section: 'personal', module: 'topup' },
  '/company': { section: 'personal', module: 'company' },
  '/profile': { section: 'personal', module: 'personal' },
  '/open-source-bounties': { section: 'personal', module: 'personal' },
  '/public-relay': { section: 'personal', module: 'personal' },
  '/channels': { section: 'admin', module: 'channel' },
  '/models': { section: 'admin', module: 'models' },
  '/models/metadata': { section: 'admin', module: 'models' },
  '/models/deployments': { section: 'admin', module: 'models' },
  '/users': { section: 'admin', module: 'user' },
  '/redemption-codes': { section: 'admin', module: 'redemption' },
  '/subscriptions': { section: 'admin', module: 'subscription' },
  '/system-settings': { section: 'admin', module: 'setting' },
  '/system-settings/site': { section: 'admin', module: 'setting' },
}

/**
 * Parse backend SidebarModulesAdmin configuration
 */
function parseSidebarConfig(
  value: string | null | undefined
): SidebarModulesAdminConfig {
  // If empty string, null, or undefined, use default config
  if (!value || value.trim() === '') {
    return DEFAULT_SIDEBAR_MODULES
  }

  try {
    const parsed = JSON.parse(value) as SidebarModulesAdminConfig
    return mergeWithDefaultSidebarModules(parsed)
  } catch {
    // eslint-disable-next-line no-console
    console.error('Failed to parse sidebar modules configuration')
    return DEFAULT_SIDEBAR_MODULES
  }
}

/**
 * Parse user-level sidebar_modules. Returns null when the value is empty,
 * invalid, or otherwise unusable — the caller treats null as "do not narrow",
 * so legacy users with an empty sidebar_modules field keep the full admin view.
 */
function parseUserSidebarConfig(
  value: string | null | undefined
): SidebarModulesUserConfig {
  return parseSidebarUserSettings(value)
}

function getNavItemKey(item: NavItem): string | null {
  if ('type' in item && item.type === 'chat-presets') return 'chat-presets'
  if ('url' in item && item.url) return String(item.url)
  return null
}

function isUserItemHidden(
  item: NavItem,
  userConfig: SidebarModulesUserConfig
): boolean {
  const key = getNavItemKey(item)
  return Boolean(key && userConfig?.preferences.hidden.includes(key))
}

/**
 * Check if a module is enabled. Admin config is the first (authoritative)
 * layer: if admin disables a section/module it is always hidden. User config
 * is a second narrower layer: it can only further hide what admin allowed.
 * A null user config means "do not narrow" (legacy/empty users).
 */
function isModuleEnabled(
  url: string,
  adminConfig: SidebarModulesAdminConfig,
  userConfig: SidebarModulesUserConfig
): boolean {
  const mapping = URL_TO_CONFIG_MAP[url]
  if (!mapping) {
    // No mapping config, default to visible (e.g. system settings and new features)
    return true
  }

  const { section, module } = mapping
  const adminSection = adminConfig[section]
  const adminAllowed = Boolean(
    adminSection && adminSection.enabled && adminSection[module] === true
  )
  if (!adminAllowed) return false

  if (!userConfig) return true

  const userSection = userConfig.modules[section]
  if (!userSection) return true
  if (userSection.enabled === false) return false
  return userSection[module] !== false
}

/**
 * Check if a navigation item should be visible
 */
function isNavItemVisible(
  item: NavItem,
  adminConfig: SidebarModulesAdminConfig,
  userConfig: SidebarModulesUserConfig
): boolean {
  // Handle dynamic chat presets type — also runs the admin × user AND gate
  if ('type' in item && item.type === 'chat-presets') {
    const adminChat = adminConfig.chat
    const adminAllowed = Boolean(adminChat?.enabled && adminChat.chat === true)
    if (!adminAllowed) return false
    if (!userConfig) return true
    const userChat = userConfig.modules.chat
    if (!userChat) return true
    if (userChat.enabled === false) return false
    return userChat.chat !== false
  }

  // Handle direct link type
  if ('url' in item && item.url) {
    const configUrls = item.configUrls ?? [item.url]
    return configUrls.some((url) =>
      isModuleEnabled(url as string, adminConfig, userConfig)
    )
  }

  // Handle collapsible type (with sub-items)
  if ('items' in item && item.items) {
    // If has sub-items, show this collapsible item if at least one sub-item is visible
    return item.items.some(
      (subItem) =>
        !isUserItemHidden(subItem as NavItem, userConfig) &&
        isModuleEnabled(subItem.url as string, adminConfig, userConfig)
    )
  }

  return true
}

/**
 * Filter navigation items
 */
function filterNavItems(
  items: NavItem[],
  sectionKey: string,
  adminConfig: SidebarModulesAdminConfig,
  userConfig: SidebarModulesUserConfig
): NavItem[] {
  const filtered = items
    .map((item) => {
      // If collapsible item, also filter its sub-items
      if ('items' in item && item.items) {
        const filteredSubItems = item.items.filter(
          (subItem) =>
            !isUserItemHidden(subItem as NavItem, userConfig) &&
            isModuleEnabled(subItem.url as string, adminConfig, userConfig)
        )

        return {
          ...item,
          items: filteredSubItems,
        }
      }
      return item
    })
    .filter(
      (item) =>
        !isUserItemHidden(item, userConfig) &&
        isNavItemVisible(item, adminConfig, userConfig)
    )

  const order = userConfig?.preferences.module_order[sectionKey]
  if (!order?.length) return filtered

  const rank = new Map(order.map((key, index) => [key, index]))
  return filtered
    .map((item, index) => ({ item, index }))
    .sort((left, right) => {
      const leftRank = rank.get(getNavItemKey(left.item) ?? '')
      const rightRank = rank.get(getNavItemKey(right.item) ?? '')
      return (
        (leftRank ?? order.length + left.index) -
        (rightRank ?? order.length + right.index)
      )
    })
    .map(({ item }) => item)
}

function orderNavGroups(
  groups: NavGroup[],
  preferences: SidebarPreferences
): NavGroup[] {
  const order = preferences.section_order
  if (!order.length) return groups
  const rank = new Map(order.map((key, index) => [key, index]))
  return groups
    .map((group, index) => ({ group, index }))
    .sort((left, right) => {
      const leftRank = rank.get(left.group.id ?? '')
      const rightRank = rank.get(right.group.id ?? '')
      return (
        (leftRank ?? order.length + left.index) -
        (rightRank ?? order.length + right.index)
      )
    })
    .map(({ group }) => group)
}

/**
 * Filter sidebar navigation groups by admin × user sidebar_modules config.
 *
 * Two layers, AND-combined:
 *   1. Admin (status.SidebarModulesAdmin) — authoritative, falls back to
 *      DEFAULT_SIDEBAR_MODULES when empty/invalid. Disabling here hides the
 *      item for everyone regardless of user preference.
 *   2. User (auth.user.sidebar_modules) — narrower overlay, null sentinel
 *      means "don't narrow". A section/module is only hidden if the user
 *      explicitly set it to false; undefined fields default to visible so
 *      legacy users with empty sidebar_modules keep the full admin view.
 *      The overlay is also skipped entirely when the backend tells us the
 *      user cannot configure sidebar_settings (e.g. root accounts), so a
 *      stale historical value cannot lock them out of entries they have no
 *      UI to restore.
 */
export function useSidebarConfig(navGroups: NavGroup[]): NavGroup[] {
  const { status } = useStatus()
  const { auth } = useAuthStore()

  const adminConfig = useMemo(
    () =>
      parseSidebarConfig(
        status?.SidebarModulesAdmin as string | null | undefined
      ),
    [status?.SidebarModulesAdmin]
  )

  const userConfig = useMemo(() => {
    // If the backend marks the user as unable to configure the sidebar
    // (e.g. root accounts), skip the user overlay entirely — a stale
    // historical sidebar_modules value from a previous role would otherwise
    // hide admin entries for someone who has no in-product UI to restore
    // them.
    if (auth?.user?.permissions?.sidebar_settings === false) {
      return null
    }
    return parseUserSidebarConfig(auth?.user?.sidebar_modules)
  }, [auth?.user?.permissions?.sidebar_settings, auth?.user?.sidebar_modules])

  const filteredNavGroups = useMemo(
    () =>
      navGroups
        .map((group) => ({
          ...group,
          items: filterNavItems(
            group.items,
            group.id ?? group.title,
            adminConfig,
            userConfig
          ),
        }))
        .filter(
          (group) =>
            !userConfig?.preferences.hidden_sections.includes(
              group.id ?? group.title
            ) && group.items.length > 0
        ), // Only show navigation groups with visible items
    [navGroups, adminConfig, userConfig]
  )

  return orderNavGroups(
    filteredNavGroups,
    userConfig?.preferences ?? SIDEBAR_DEFAULT_PREFERENCES
  )
}

export function useSidebarDensity(): SidebarDensity {
  const { auth } = useAuthStore()
  const userConfig =
    auth?.user?.permissions?.sidebar_settings === false
      ? null
      : parseUserSidebarConfig(auth?.user?.sidebar_modules)
  return userConfig?.preferences.density ?? 'comfortable'
}

/**
 * Check whether a single route is visible under the current sidebar_modules
 * config. Used by entries living outside the sidebar (e.g. the profile
 * dropdown's wallet link) so they honour the same "wallet display" toggle.
 */
export function useIsSidebarModuleVisible(url: string): boolean {
  const { status } = useStatus()
  const { auth } = useAuthStore()

  const adminConfig = parseSidebarConfig(
    status?.SidebarModulesAdmin as string | null | undefined
  )
  const userConfig =
    auth?.user?.permissions?.sidebar_settings === false
      ? null
      : parseUserSidebarConfig(auth?.user?.sidebar_modules)

  return isModuleEnabled(url, adminConfig, userConfig)
}
