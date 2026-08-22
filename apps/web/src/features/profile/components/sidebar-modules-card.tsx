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
import { ArrowDown, ArrowUp, LayoutDashboard } from 'lucide-react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { IconBadge } from '@/components/ui/icon-badge'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import { api } from '@/lib/api'
import { isConsoleActivated } from '@/lib/console-activation'
import { ROLE } from '@/lib/roles'
import {
  normalizeSidebarPreferences,
  parseSidebarUserSettings,
  serializeSidebarUserSettings,
  SIDEBAR_DEFAULT_PREFERENCES,
  SIDEBAR_DEFAULT_ROUTE_ALLOWLIST,
  isSidebarRouteHidden,
  type SidebarPreferences,
} from '@/lib/sidebar-preferences'
import { useAuthStore } from '@/stores/auth-store'

type SidebarModuleConfig = Record<string, boolean>
type SidebarModulesConfig = Record<string, SidebarModuleConfig>

type ModuleDef = {
  id: string
  key: string
  title: string
  description: string
  config?: { section: string; key: string }
  requiredRole?: number
}

type SectionDef = {
  id: string
  title: string
  description: string
  configSection?: string
  modules: ModuleDef[]
  requiresConsole?: boolean
  requiredRole?: number
}

type DefaultRouteDef = {
  route: string
  title: string
  requiredRole?: number
  requiresConsole?: boolean
}

function buildSectionDefs(t: (key: string) => string): SectionDef[] {
  return [
    {
      id: 'onboarding',
      title: t('Getting started'),
      description: t('Start here and review available work'),
      modules: [
        {
          id: '/getting-started',
          key: 'getting-started',
          title: t('Getting started'),
          description: t('Service guide and onboarding'),
        },
        {
          id: '/open-source-bounties',
          key: 'open-source-bounties',
          title: t('Open-source bounties'),
          description: t('Browse open-source work'),
        },
        {
          id: '/public-relay',
          key: 'public-relay',
          title: t('Channel marketplace'),
          description: t('Browse shared channels'),
        },
        {
          id: '/todos',
          key: 'todos',
          title: t('To-dos'),
          description: t('Review pending tasks and notices'),
        },
      ],
    },
    {
      id: 'forge',
      title: t('Open-source bounties'),
      description: t('Challenges and community work'),
      requiresConsole: true,
      modules: [
        {
          id: '/open-source-bounties',
          key: 'open-source-bounties',
          title: t('Open-source bounties'),
          description: t('Browse open-source work'),
        },
        {
          id: '/public-relay',
          key: 'public-relay',
          title: t('Channel marketplace'),
          description: t('Browse shared channels'),
        },
        {
          id: '/challenges',
          key: 'challenges',
          title: t('Challenges'),
          description: t('Open challenges'),
        },
        {
          id: '/rankings',
          key: 'rankings',
          title: t('Rankings'),
          description: t('Community rankings'),
        },
      ],
    },
    {
      id: 'chat',
      title: t('Conversations'),
      description: t('Conversation records and tools'),
      configSection: 'chat',
      requiresConsole: true,
      modules: [
        {
          id: '/chat-management',
          key: 'chat-management',
          title: t('Conversation records'),
          description: t('Review and manage conversations'),
          config: { section: 'chat', key: 'chat' },
        },
      ],
    },
    {
      id: 'general',
      title: t('General'),
      description: t('Data management and log viewing'),
      configSection: 'console',
      requiresConsole: true,
      modules: [
        {
          id: '/dashboard/overview',
          key: 'overview',
          title: t('Overview'),
          description: t('System overview'),
          config: { section: 'console', key: 'detail' },
        },
        {
          id: '/dashboard/models',
          key: 'dashboard',
          title: t('Dashboard'),
          description: t('System data statistics'),
          config: { section: 'console', key: 'detail' },
        },
        {
          id: '/keys',
          key: 'keys',
          title: t('API Keys'),
          description: t('API token management'),
          config: { section: 'console', key: 'token' },
        },
        {
          id: '/drawing',
          key: 'drawing',
          title: t('Drawing studio'),
          description: t('Create and review images'),
          config: { section: 'console', key: 'midjourney' },
        },
        {
          id: '/usage-logs/common',
          key: 'usage-logs',
          title: t('Usage Logs'),
          description: t('API usage records'),
          config: { section: 'console', key: 'log' },
        },
        {
          id: '/usage-logs/task',
          key: 'task-logs',
          title: t('Task Logs'),
          description: t('System task records'),
          config: { section: 'console', key: 'task' },
        },
      ],
    },
    {
      id: 'personal',
      title: t('Personal'),
      description: t('User personal functions'),
      requiresConsole: true,
      modules: [
        {
          id: '/wallet',
          key: 'wallet',
          title: t('Wallet'),
          description: t('Balance and payment management'),
          config: { section: 'personal', key: 'topup' },
        },
        {
          id: '/email-activations',
          key: 'email-activations',
          title: t('Email Activations'),
          description: t('Temporary email codes and order history'),
          config: { section: 'personal', key: 'topup' },
        },
        {
          id: '/profile',
          key: 'profile',
          title: t('Profile'),
          description: t('Personal info settings'),
          config: { section: 'personal', key: 'personal' },
        },
        {
          id: '/support',
          key: 'support',
          title: t('Submit a ticket'),
          description: t('Contact support'),
        },
        {
          id: '/todos',
          key: 'todos',
          title: t('To-dos'),
          description: t('Review pending tasks and notices'),
        },
      ],
    },
    {
      id: 'admin',
      title: t('Admin'),
      description: t('Administrative tools'),
      configSection: 'admin',
      requiresConsole: true,
      requiredRole: ROLE.ADMIN,
      modules: [
        {
          id: '/channels',
          key: 'channels',
          title: t('Channels'),
          description: t('Manage channels'),
          config: { section: 'admin', key: 'channel' },
        },
        {
          id: '/models/metadata',
          key: 'models',
          title: t('Models'),
          description: t('Manage models'),
          config: { section: 'admin', key: 'models' },
        },
        {
          id: '/users',
          key: 'users',
          title: t('Users'),
          description: t('Manage users'),
          config: { section: 'admin', key: 'user' },
        },
        {
          id: '/redemption-codes',
          key: 'redemption-codes',
          title: t('Redemption Codes'),
          description: t('Manage redemption codes'),
          config: { section: 'admin', key: 'redemption' },
        },
        {
          id: '/discount-codes',
          key: 'discount-codes',
          title: t('Discount Codes'),
          description: t('Manage discount codes'),
        },
        {
          id: '/subscriptions',
          key: 'subscriptions',
          title: t('Subscriptions'),
          description: t('Manage subscriptions'),
          config: { section: 'admin', key: 'subscription' },
        },
        {
          id: '/system-info',
          key: 'system-info',
          title: t('System Info'),
          description: t('Inspect system information'),
          requiredRole: ROLE.SUPER_ADMIN,
        },
        {
          id: '/system-settings/site',
          key: 'system-settings',
          title: t('System Settings'),
          description: t('Configure the service'),
          config: { section: 'admin', key: 'setting' },
        },
      ],
    },
  ]
}

const DEFAULT_ROUTES: DefaultRouteDef[] = [
  {
    route: '/dashboard/overview',
    title: 'Overview',
    requiresConsole: true,
  },
  { route: '/dashboard/models', title: 'Dashboard', requiresConsole: true },
  { route: '/getting-started', title: 'Getting started' },
  {
    route: '/chat-management',
    title: 'Conversation records',
    requiresConsole: true,
  },
  { route: '/keys', title: 'API Keys', requiresConsole: true },
  { route: '/wallet', title: 'Wallet', requiresConsole: true },
  {
    route: '/email-activations',
    title: 'Email Activations',
    requiresConsole: true,
  },
  { route: '/drawing', title: 'Drawing studio', requiresConsole: true },
  { route: '/todos', title: 'To-dos', requiresConsole: true },
  { route: '/profile', title: 'Profile', requiresConsole: true },
  {
    route: '/open-source-bounties',
    title: 'Open-source bounties',
    requiresConsole: true,
  },
  {
    route: '/public-relay',
    title: 'Channel marketplace',
    requiresConsole: true,
  },
  { route: '/challenges', title: 'Challenges', requiresConsole: true },
  { route: '/rankings', title: 'Rankings', requiresConsole: true },
  { route: '/usage-logs/common', title: 'Usage Logs', requiresConsole: true },
  { route: '/usage-logs/task', title: 'Task Logs', requiresConsole: true },
  { route: '/support', title: 'Submit a ticket', requiresConsole: true },
  {
    route: '/channels',
    title: 'Channels',
    requiredRole: ROLE.ADMIN,
    requiresConsole: true,
  },
  {
    route: '/models/metadata',
    title: 'Models',
    requiredRole: ROLE.ADMIN,
    requiresConsole: true,
  },
  {
    route: '/users',
    title: 'Users',
    requiredRole: ROLE.ADMIN,
    requiresConsole: true,
  },
  {
    route: '/redemption-codes',
    title: 'Redemption Codes',
    requiredRole: ROLE.ADMIN,
    requiresConsole: true,
  },
  {
    route: '/discount-codes',
    title: 'Discount Codes',
    requiredRole: ROLE.ADMIN,
    requiresConsole: true,
  },
  {
    route: '/subscriptions',
    title: 'Subscriptions',
    requiredRole: ROLE.ADMIN,
    requiresConsole: true,
  },
  {
    route: '/system-info',
    title: 'System Info',
    requiredRole: ROLE.SUPER_ADMIN,
    requiresConsole: true,
  },
  {
    route: '/system-settings/site',
    title: 'System Settings',
    requiredRole: ROLE.SUPER_ADMIN,
    requiresConsole: true,
  },
]

function isVisibleRoute(
  route: string,
  sectionDefs: SectionDef[],
  preferences: SidebarPreferences
): boolean {
  return sectionDefs.some(
    (section) =>
      !preferences.hidden_sections.includes(section.id) &&
      section.modules.some(
        (module) =>
          module.id === route && !preferences.hidden.includes(module.id)
      )
  )
}

function createDefaultModules(sectionDefs: SectionDef[]): SidebarModulesConfig {
  const modules: SidebarModulesConfig = {}
  for (const section of sectionDefs) {
    if (!section.configSection) continue
    const sectionConfig = modules[section.configSection] ?? { enabled: true }
    for (const module of section.modules) {
      if (module.config) sectionConfig[module.config.key] = true
    }
    modules[section.configSection] = sectionConfig
  }
  return modules
}

function mergeModules(
  defaults: SidebarModulesConfig,
  value: SidebarModulesConfig
): SidebarModulesConfig {
  const merged: SidebarModulesConfig = {}
  for (const [sectionKey, section] of Object.entries(defaults)) {
    const override = value[sectionKey]
    merged[sectionKey] = override ? { ...section, ...override } : { ...section }
  }
  for (const [sectionKey, section] of Object.entries(value)) {
    if (!merged[sectionKey]) merged[sectionKey] = { ...section }
  }
  return merged
}

function ordered<T>(items: T[], keys: string[], getKey: (item: T) => string) {
  const rank = new Map(keys.map((key, index) => [key, index]))
  return items
    .map((item, index) => ({ item, index }))
    .sort((left, right) => {
      const leftRank = rank.get(getKey(left.item))
      const rightRank = rank.get(getKey(right.item))
      return (
        (leftRank ?? keys.length + left.index) -
        (rightRank ?? keys.length + right.index)
      )
    })
    .map(({ item }) => item)
}

function sanitizePreferences(
  preferences: SidebarPreferences,
  sectionDefs: SectionDef[]
): SidebarPreferences {
  const sectionKeys = new Set(sectionDefs.map((section) => section.id))
  const moduleKeys = new Set(
    sectionDefs.flatMap((section) => section.modules.map((module) => module.id))
  )
  const moduleOrder: Record<string, string[]> = {}
  for (const section of sectionDefs) {
    const allowed = new Set(section.modules.map((module) => module.id))
    const configured = preferences.module_order[section.id] ?? []
    const order = configured.filter((key) => allowed.has(key))
    if (order.length > 0) moduleOrder[section.id] = order
  }
  const normalized = normalizeSidebarPreferences(preferences)
  return {
    ...normalized,
    section_order: normalized.section_order.filter((key) =>
      sectionKeys.has(key)
    ),
    module_order: moduleOrder,
    hidden_sections: normalized.hidden_sections.filter((key) =>
      sectionKeys.has(key)
    ),
    hidden: normalized.hidden.filter((key) => moduleKeys.has(key)),
    default_route:
      SIDEBAR_DEFAULT_ROUTE_ALLOWLIST.has(normalized.default_route) &&
      isVisibleRoute(normalized.default_route, sectionDefs, normalized) &&
      !isSidebarRouteHidden(normalized.default_route, normalized)
        ? normalized.default_route
        : '',
  }
}

export function SidebarModulesCard() {
  const { t } = useTranslation()
  const [loading, setLoading] = useState(false)
  const [config, setConfig] = useState<SidebarModulesConfig>({})
  const [preferences, setPreferences] = useState<SidebarPreferences>(
    SIDEBAR_DEFAULT_PREFERENCES
  )
  const currentUser = useAuthStore((s) => s.auth.user)
  const setUser = useAuthStore((s) => s.auth.setUser)

  const allSectionDefs = useMemo(() => buildSectionDefs(t), [t])
  const sectionDefs = useMemo(() => {
    const role = currentUser?.role ?? ROLE.GUEST
    const consoleActivated = isConsoleActivated(currentUser)
    return allSectionDefs
      .filter((section) => {
        if (section.requiresConsole && !consoleActivated) return false
        if (section.id === 'onboarding' && consoleActivated) return false
        return (
          section.requiredRole === undefined || role >= section.requiredRole
        )
      })
      .map((section) => ({
        ...section,
        modules: section.modules.filter(
          (module) =>
            module.requiredRole === undefined || role >= module.requiredRole
        ),
      }))
      .filter((section) => section.modules.length > 0)
  }, [allSectionDefs, currentUser])

  const routeOptions = useMemo(() => {
    const role = currentUser?.role ?? ROLE.GUEST
    const consoleActivated = isConsoleActivated(currentUser)
    const visibleRoutes = new Set(
      sectionDefs.flatMap((section) =>
        section.modules.map((module) => module.id)
      )
    )
    return DEFAULT_ROUTES.filter(
      (option) =>
        SIDEBAR_DEFAULT_ROUTE_ALLOWLIST.has(option.route) &&
        visibleRoutes.has(option.route) &&
        isVisibleRoute(option.route, sectionDefs, preferences) &&
        !isSidebarRouteHidden(option.route, preferences) &&
        (!option.requiresConsole || consoleActivated) &&
        (option.requiredRole === undefined || role >= option.requiredRole)
    )
  }, [currentUser, preferences, sectionDefs])

  const loadConfig = useCallback(async () => {
    try {
      const res = await api.get('/api/user/self')
      const raw = res.data.success ? res.data.data?.sidebar_modules : null
      let rawValue = ''
      if (typeof raw === 'string') rawValue = raw
      else if (raw) rawValue = JSON.stringify(raw)
      const parsed = parseSidebarUserSettings(rawValue)
      if (parsed) {
        setConfig(
          mergeModules(createDefaultModules(sectionDefs), parsed.modules)
        )
        setPreferences(parsed.preferences)
      } else {
        setConfig(createDefaultModules(sectionDefs))
        setPreferences(SIDEBAR_DEFAULT_PREFERENCES)
      }
    } catch {
      setConfig(createDefaultModules(sectionDefs))
      setPreferences(SIDEBAR_DEFAULT_PREFERENCES)
    }
  }, [sectionDefs])

  useEffect(() => {
    void loadConfig()
  }, [loadConfig])

  const orderedSections = useMemo(
    () =>
      ordered(sectionDefs, preferences.section_order, (section) => section.id),
    [preferences.section_order, sectionDefs]
  )

  const isHidden = (moduleId: string) => preferences.hidden.includes(moduleId)

  const isModuleEnabled = (module: ModuleDef) => {
    if (isHidden(module.id)) return false
    if (!module.config) return true
    return config[module.config.section]?.[module.config.key] !== false
  }

  const isSectionEnabled = (section: SectionDef) => {
    if (preferences.hidden_sections.includes(section.id)) return false
    if (
      section.configSection &&
      config[section.configSection]?.enabled === false
    ) {
      return false
    }
    return true
  }

  const toggleModule = (module: ModuleDef, visible: boolean) => {
    setPreferences((previous) => ({
      ...previous,
      hidden: visible
        ? previous.hidden.filter((id) => id !== module.id)
        : [...new Set([...previous.hidden, module.id])],
    }))
    const config = module.config
    if (config) {
      setConfig((previous) => ({
        ...previous,
        [config.section]: {
          ...(previous[config.section] ?? { enabled: true }),
          [config.key]: visible,
        },
      }))
    }
  }

  const toggleSection = (section: SectionDef, enabled: boolean) => {
    const ids = section.modules.map((module) => module.id)
    setPreferences((previous) => ({
      ...previous,
      hidden_sections: enabled
        ? previous.hidden_sections.filter((id) => id !== section.id)
        : [...new Set([...previous.hidden_sections, section.id])],
      hidden: enabled
        ? previous.hidden.filter((id) => !ids.includes(id))
        : [...new Set([...previous.hidden, ...ids])],
    }))
    const configSection = section.configSection
    if (configSection) {
      setConfig((previous) => ({
        ...previous,
        [configSection]: {
          ...previous[configSection],
          enabled,
        },
      }))
    }
  }

  const moveSection = (sectionId: string, offset: number) => {
    const current = orderedSections.map((section) => section.id)
    const index = current.indexOf(sectionId)
    const next = index + offset
    if (index < 0 || next < 0 || next >= current.length) return
    ;[current[index], current[next]] = [current[next], current[index]]
    setPreferences((previous) => ({ ...previous, section_order: current }))
  }

  const moveModule = (
    section: SectionDef,
    moduleId: string,
    offset: number
  ) => {
    const current = ordered(
      section.modules,
      preferences.module_order[section.id] ?? [],
      (module) => module.id
    ).map((module) => module.id)
    const index = current.indexOf(moduleId)
    const next = index + offset
    if (index < 0 || next < 0 || next >= current.length) return
    ;[current[index], current[next]] = [current[next], current[index]]
    setPreferences((previous) => ({
      ...previous,
      module_order: { ...previous.module_order, [section.id]: current },
    }))
  }

  const handleSave = async () => {
    setLoading(true)
    try {
      const safePreferences = sanitizePreferences(preferences, sectionDefs)
      const serialized = serializeSidebarUserSettings(config, safePreferences)
      const res = await api.put('/api/user/self', {
        sidebar_modules: serialized,
      })
      if (res.data.success) {
        setPreferences(safePreferences)
        if (currentUser) {
          setUser({ ...currentUser, sidebar_modules: serialized })
        }
        toast.success(t('Saved successfully'))
      } else {
        toast.error(res.data.message || t('Save failed'))
      }
    } catch {
      toast.error(t('Save failed, please retry'))
    } finally {
      setLoading(false)
    }
  }

  const handleReset = () => {
    setConfig(createDefaultModules(sectionDefs))
    setPreferences(SIDEBAR_DEFAULT_PREFERENCES)
    toast.success(t('Reset to default configuration'))
  }

  const selectedDefaultRoute = routeOptions.some(
    (option) => option.route === preferences.default_route
  )
    ? preferences.default_route
    : ''

  return (
    <Card data-card-hover='false' className='gap-0 overflow-hidden py-0'>
      <CardHeader className='border-b p-3 !pb-3 sm:p-5 sm:!pb-5'>
        <div className='flex items-center gap-3'>
          <IconBadge tone='info' size='title'>
            <LayoutDashboard />
          </IconBadge>
          <div className='min-w-0'>
            <CardTitle className='text-lg tracking-tight sm:text-xl'>
              {t('Sidebar Personal Settings')}
            </CardTitle>
            <CardDescription className='text-xs sm:text-sm'>
              {t('Customize sidebar display content')}
            </CardDescription>
          </div>
        </div>
      </CardHeader>
      <CardContent className='space-y-4 p-3 sm:space-y-5 sm:p-5'>
        <div className='grid gap-3 rounded-lg border p-3 sm:grid-cols-2'>
          <div className='grid gap-1.5'>
            <Label htmlFor='sidebar-density'>{t('Sidebar density')}</Label>
            <NativeSelect
              id='sidebar-density'
              value={preferences.density}
              onChange={(event) =>
                setPreferences((previous) => ({
                  ...previous,
                  density:
                    event.target.value === 'compact'
                      ? 'compact'
                      : 'comfortable',
                }))
              }
            >
              <NativeSelectOption value='comfortable'>
                {t('Comfortable')}
              </NativeSelectOption>
              <NativeSelectOption value='compact'>
                {t('Compact')}
              </NativeSelectOption>
            </NativeSelect>
          </div>
          <div className='grid gap-1.5'>
            <Label htmlFor='sidebar-default-route'>{t('Default page')}</Label>
            <NativeSelect
              id='sidebar-default-route'
              value={selectedDefaultRoute}
              onChange={(event) =>
                setPreferences((previous) => ({
                  ...previous,
                  default_route: event.target.value,
                }))
              }
            >
              <NativeSelectOption value=''>
                {t('Use system default')}
              </NativeSelectOption>
              {routeOptions.map((option) => (
                <NativeSelectOption key={option.route} value={option.route}>
                  {t(option.title)}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </div>
        </div>

        {orderedSections.map((section, sectionIndex) => {
          const sectionEnabled = isSectionEnabled(section)
          const modules = ordered(
            section.modules,
            preferences.module_order[section.id] ?? [],
            (module) => module.id
          )
          return (
            <div
              key={section.id}
              className='bg-background/60 rounded-lg border p-3'
            >
              <div className='flex items-start justify-between gap-3'>
                <div className='min-w-0'>
                  <p className='text-sm font-medium'>{section.title}</p>
                  <p className='text-muted-foreground text-xs'>
                    {section.description}
                  </p>
                </div>
                <div className='flex shrink-0 items-center gap-1'>
                  <Button
                    type='button'
                    variant='ghost'
                    size='icon-sm'
                    aria-label={t('Move section up')}
                    disabled={sectionIndex === 0}
                    onClick={() => moveSection(section.id, -1)}
                  >
                    <ArrowUp aria-hidden='true' />
                  </Button>
                  <Button
                    type='button'
                    variant='ghost'
                    size='icon-sm'
                    aria-label={t('Move section down')}
                    disabled={sectionIndex === orderedSections.length - 1}
                    onClick={() => moveSection(section.id, 1)}
                  >
                    <ArrowDown aria-hidden='true' />
                  </Button>
                  <Switch
                    checked={sectionEnabled}
                    onCheckedChange={(value) => toggleSection(section, value)}
                  />
                </div>
              </div>
              <div className='mt-3 grid grid-cols-1 gap-2 sm:grid-cols-2'>
                {modules.map((module, moduleIndex) => {
                  const visible = isModuleEnabled(module)
                  return (
                    <div
                      key={module.id}
                      className={`flex min-h-16 items-center gap-2 rounded-lg border p-3 ${
                        visible ? '' : 'opacity-50'
                      }`}
                    >
                      <div className='flex shrink-0 flex-col'>
                        <Button
                          type='button'
                          variant='ghost'
                          size='icon-sm'
                          aria-label={t('Move item up')}
                          disabled={moduleIndex === 0}
                          onClick={() => moveModule(section, module.id, -1)}
                        >
                          <ArrowUp aria-hidden='true' />
                        </Button>
                        <Button
                          type='button'
                          variant='ghost'
                          size='icon-sm'
                          aria-label={t('Move item down')}
                          disabled={moduleIndex === modules.length - 1}
                          onClick={() => moveModule(section, module.id, 1)}
                        >
                          <ArrowDown aria-hidden='true' />
                        </Button>
                      </div>
                      <div className='mr-2 min-w-0 flex-1'>
                        <p className='truncate text-sm font-medium'>
                          {module.title}
                        </p>
                        <p className='text-muted-foreground truncate text-xs'>
                          {module.description}
                        </p>
                      </div>
                      <Switch
                        checked={visible}
                        onCheckedChange={(value) => toggleModule(module, value)}
                        disabled={!sectionEnabled}
                        aria-label={`${module.title}: ${t('Visible')}`}
                      />
                    </div>
                  )
                })}
              </div>
            </div>
          )
        })}

        <div className='flex flex-col-reverse gap-2 border-t pt-4 sm:flex-row sm:justify-end'>
          <Button variant='outline' onClick={handleReset}>
            {t('Reset to Default')}
          </Button>
          <Button onClick={handleSave} disabled={loading}>
            {loading ? t('Saving...') : t('Save Changes')}
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}
