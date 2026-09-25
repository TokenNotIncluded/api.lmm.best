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
/*
Copyright (C) 2026 LIghtJUNction
*/
import { getPricing } from '@/features/pricing/api'
import {
  fetchRepositoryStars,
  REPOSITORIES,
  repositoryUrl,
  type RepositoryKind,
} from '@/features/repositories/api'
/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { signalGameTools } from '@/features/signal-game/webmcp'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { accountTools } from './areas/account'
import { adminTools } from './areas/admin'
import { authOnboardingTools } from './areas/auth-onboarding'
import { publicSiteTools } from './areas/public-site'
import { publicToolsTools } from './areas/public-tools'
import { settingsShellTools } from './areas/settings-shell'
import { workbenchTools } from './areas/workbench'
import { readPageOutline } from './page-outline'
import {
  EMPTY_INPUT_SCHEMA,
  ensureNotAborted,
  ensureObject,
  requireAdmin,
  requireSignedIn,
  type ModelContextTool,
  type WebMcpRouter,
  type WebMcpToolFactory,
} from './tool-kit'

export type { ModelContextTool } from './tool-kit'

type ModelContext = {
  registerTool: (
    tool: ModelContextTool,
    options?: { signal?: AbortSignal }
  ) => void | Promise<void>
}

declare global {
  interface Document {
    modelContext?: ModelContext
  }
}

const AREA_TOOL_FACTORIES: readonly WebMcpToolFactory[] = [
  publicSiteTools,
  publicToolsTools,
  authOnboardingTools,
  accountTools,
  workbenchTools,
  adminTools,
  settingsShellTools,
]

/** Every static page an agent may open, with a short purpose for discovery. */
export const SITE_PAGES = {
  '/': 'Home',
  '/pricing': 'Public model prices and access plans',
  '/rankings': 'Model usage rankings',
  '/status': 'Service and channel status',
  '/guide': 'Getting started guide',
  '/developers': 'Developer documentation and API examples',
  '/scripts': 'Public installer scripts',
  '/security': 'Security policy and disclosure',
  '/about': 'About LMM',
  '/webmcp': 'WebMCP tools for browser agents',
  '/games/signal': 'Signal puzzle game and leaderboard',
  '/challenges': 'Open challenges',
  '/privacy-policy': 'Privacy policy',
  '/terms-of-service': 'Terms of service',
  '/user-agreement': 'User agreement',
  '/sign-in': 'Sign in',
  '/sign-up': 'Create an account',
  '/forgot-password': 'Reset a forgotten password',
  '/getting-started': 'Account onboarding',
  '/dashboard': 'Account dashboard overview',
  '/dashboard/overview': 'Account dashboard overview',
  '/wallet': 'Wallet balance and top-up',
  '/subscriptions': 'Subscription plans',
  '/usage-logs': 'API usage logs',
  '/profile': 'Profile and security settings',
  '/profile/share': 'Share account usage by model',
  '/keys': 'API keys',
  '/models': 'Available models',
  '/models/metadata': 'Model records',
  '/models/deployments': 'Model deployments',
  '/playground': 'Model playground chat',
  '/drawing': 'Image generation',
  '/tool-market': 'Tool market',
  '/todos': 'Personal to-do list',
  '/workspace': 'Workspace',
  '/remote-control': 'Remote control sessions',
  '/support': 'Support tickets',
  '/temporary-activations': 'Temporary access activations',
  '/ai-directory': 'AI product directory',
  '/rss': 'RSS and Atom reader',
  '/red-packets': 'Red packets',
  '/public-relay': 'Public relay',
  '/open-source-bounties': 'Open source bounties',
  '/channels': 'Admin: upstream channels',
  '/users': 'Admin: users',
  '/redemption-codes': 'Admin: redemption codes',
  '/discount-codes': 'Admin: discount codes',
  '/email-activations': 'Admin: email activations',
  '/chat-management': 'Admin: chat presets',
  '/company': 'Admin: company profile',
  '/operations/sources': 'Admin: acquisition sources',
  '/system-info': 'Admin: system information',
  '/system-settings': 'Admin: system settings',
  '/system-settings/auth': 'Admin: authentication settings',
  '/system-settings/billing': 'Admin: billing settings',
  '/system-settings/content': 'Admin: content settings',
  '/system-settings/models': 'Admin: model settings',
  '/system-settings/operations': 'Admin: operations settings',
  '/system-settings/security': 'Admin: security settings',
  '/system-settings/site': 'Admin: site settings',
  '/subscriptions/reset': 'Subscription reset options',
  '/usage-logs/common': 'API usage logs',
  '/usage-logs/drawing': 'Drawing usage logs',
  '/usage-logs/task': 'Task usage logs',
  '/setup': 'Initial site setup',
  '/401': 'Sign-in required',
  '/403': 'Access denied',
  '/404': 'Page not found',
  '/500': 'Server error',
  '/503': 'Maintenance',
} as const

const PUBLIC_PATHS = new Set([
  '/',
  '/pricing',
  '/rankings',
  '/status',
  '/guide',
  '/developers',
  '/scripts',
  '/security',
  '/about',
  '/webmcp',
  '/games/signal',
  '/challenges',
  '/privacy-policy',
  '/terms-of-service',
  '/user-agreement',
  '/sign-in',
  '/sign-up',
  '/forgot-password',
  '/setup',
  '/401',
  '/403',
  '/404',
  '/500',
  '/503',
])

function requireNavigationAccess(path: keyof typeof SITE_PAGES) {
  if (PUBLIC_PATHS.has(path)) return
  if (
    SITE_PAGES[path].startsWith('Admin:') ||
    path === '/models' ||
    path.startsWith('/models/')
  ) {
    requireAdmin()
    return
  }
  requireSignedIn()
}

const NAVIGABLE_PATHS = Object.fromEntries(
  Object.keys(SITE_PAGES).map((path) => [path, path])
) as { [K in keyof typeof SITE_PAGES]: K }

function safeAccountStatus() {
  const user = useAuthStore.getState().auth.user
  if (!user) return { authenticated: false }
  return {
    authenticated: true,
    role: user.role,
    trust_level: user.trust_level_info?.level ?? null,
    developer_access_granted: user.developer_access_granted === true,
    onboarding_stage: user.onboarding?.stage ?? user.onboarding_stage ?? null,
  }
}

function coreTools(router: WebMcpRouter): ModelContextTool[] {
  return [
    ...signalGameTools(),
    {
      name: 'lmm_site_info',
      title: 'LMM site information',
      description:
        'Return public LMM site information and the current page path.',
      inputSchema: {
        type: 'object',
        properties: {},
        additionalProperties: false,
      },
      annotations: { readOnlyHint: true },
      execute: async (_input, options) => {
        ensureNotAborted(options.signal)
        return {
          site: 'api.lmm.best',
          current_path: window.location.pathname,
          pricing_path: '/pricing',
          guide_path: '/guide',
          developers_path: '/developers',
          scripts_path: '/scripts',
          webmcp_path: '/webmcp',
          pi_oauth: true,
        }
      },
    },
    {
      name: 'lmm_site_map',
      title: 'List LMM pages',
      description:
        'List every page lmm_navigate can open, with a short purpose. Admin pages require an administrator account.',
      inputSchema: EMPTY_INPUT_SCHEMA,
      annotations: { readOnlyHint: true },
      execute: async (_input, options) => {
        ensureNotAborted(options.signal)
        return {
          current_path: window.location.pathname,
          pages: Object.entries(SITE_PAGES).map(([path, purpose]) => ({
            path,
            purpose,
          })),
        }
      },
    },
    {
      name: 'lmm_page_outline',
      title: 'Read the current page outline',
      description:
        'Read the current page title, headings, and visible buttons, links, and tabs. Works on every page; use it to orient before acting.',
      inputSchema: EMPTY_INPUT_SCHEMA,
      annotations: { readOnlyHint: true, untrustedContentHint: true },
      execute: async (_input, options) => {
        ensureNotAborted(options.signal)
        return readPageOutline(SITE_PAGES)
      },
    },
    {
      name: 'lmm_navigate',
      title: 'Open an LMM page',
      description:
        'Navigate the current tab to one of the listed public or account pages. Call lmm_site_map for descriptions.',
      inputSchema: {
        type: 'object',
        properties: {
          path: { type: 'string', enum: Object.keys(NAVIGABLE_PATHS) },
        },
        required: ['path'],
        additionalProperties: false,
      },
      execute: async (rawInput, options) => {
        const input = ensureObject(rawInput)
        const path = input.path
        if (typeof path !== 'string' || !Object.hasOwn(NAVIGABLE_PATHS, path)) {
          throw new TypeError('Unknown navigation path')
        }
        ensureNotAborted(options.signal)
        requireNavigationAccess(path as keyof typeof SITE_PAGES)
        await router.navigate({
          to: NAVIGABLE_PATHS[path as keyof typeof NAVIGABLE_PATHS],
        })
        return { path, navigated: true }
      },
    },
    {
      name: 'lmm_model_prices',
      title: 'Read LMM model prices',
      description:
        'Read public model pricing. An optional exact model ID filters the result.',
      inputSchema: {
        type: 'object',
        properties: { model: { type: 'string', maxLength: 128 } },
        additionalProperties: false,
      },
      annotations: { readOnlyHint: true },
      execute: async (rawInput, options) => {
        const input = ensureObject(rawInput)
        const model = input.model
        if (
          model !== undefined &&
          (typeof model !== 'string' || model.length > 128)
        ) {
          throw new TypeError('model must be a short string')
        }
        ensureNotAborted(options.signal)
        const pricing = await getPricing(options.signal)
        ensureNotAborted(options.signal)
        const models = pricing.data
          .filter((item) => !model || item.model_name === model)
          .map((item) => {
            const inputRatio = Number(item.model_ratio)
            const outputRatio = inputRatio * Number(item.completion_ratio)
            const tokenBased = item.quota_type === 0
            return {
              model: item.model_name,
              vendor: item.vendor_name ?? null,
              quota_type: item.quota_type,
              billing_unit: tokenBased
                ? 'platform_credit_per_million_tokens'
                : 'platform_credit_per_request',
              input_platform_credit_per_million_tokens: tokenBased
                ? inputRatio * 2
                : null,
              output_platform_credit_per_million_tokens: tokenBased
                ? outputRatio * 2
                : null,
              cache_ratio_multiplier: tokenBased
                ? (item.cache_ratio ?? null)
                : null,
              request_platform_credit: tokenBased
                ? null
                : (item.model_price ?? null),
              group_multipliers: item.group_ratio ?? null,
              billing_mode: item.billing_mode ?? null,
            }
          })
        return {
          pricing_basis:
            'Base platform credit before group multipliers; not a fiat checkout price.',
          count: models.length,
          models,
        }
      },
    },
    {
      name: 'lmm_public_scripts',
      title: 'Read public LMM scripts',
      description:
        'List public installer filenames and download links. Does not download, install, or execute a script.',
      inputSchema: {
        type: 'object',
        properties: {},
        additionalProperties: false,
      },
      annotations: { readOnlyHint: true, untrustedContentHint: true },
      execute: async (_input, options) => {
        ensureNotAborted(options.signal)
        const response = await api.get<{
          success: boolean
          data?: { name: string; size?: number; updated?: string }[]
        }>('/api/scripts', { signal: options.signal })
        ensureNotAborted(options.signal)
        if (!response.data.success || !Array.isArray(response.data.data)) {
          throw new Error('Public scripts unavailable')
        }
        return {
          page: '/scripts',
          repository: repositoryUrl('scripts'),
          scripts: response.data.data.map((script) => ({
            name: script.name,
            download_url: `${window.location.origin}/scripts/${encodeURIComponent(script.name)}`,
            size: script.size ?? null,
            updated: script.updated ?? null,
          })),
        }
      },
    },
    {
      name: 'lmm_source_repositories',
      title: 'Read LMM source repositories',
      description:
        'Return the public project and installer repositories, with GitHub star counts when available. Does not star or modify repositories.',
      inputSchema: {
        type: 'object',
        properties: {},
        additionalProperties: false,
      },
      annotations: { readOnlyHint: true },
      execute: async (_input, options) => {
        ensureNotAborted(options.signal)
        const rows = await Promise.all(
          (['project', 'scripts'] as RepositoryKind[]).map(async (kind) => ({
            repository: REPOSITORIES[kind],
            url: repositoryUrl(kind),
            stars: await fetchRepositoryStars(kind, options.signal).catch(
              () => null
            ),
          }))
        )
        ensureNotAborted(options.signal)
        return { repositories: rows }
      },
    },
    {
      name: 'lmm_account_status',
      title: 'Read current LMM account status',
      description:
        'Read the current signed-in account access summary without credentials or private profile data.',
      inputSchema: {
        type: 'object',
        properties: {},
        additionalProperties: false,
      },
      annotations: { readOnlyHint: true },
      execute: async (_input, options) => {
        ensureNotAborted(options.signal)
        return safeAccountStatus()
      },
    },
  ]
}

function toolsFor(router: WebMcpRouter): ModelContextTool[] {
  const seen = new Set<string>()
  const tools: ModelContextTool[] = []
  const add = (list: ModelContextTool[]) => {
    for (const tool of list) {
      if (seen.has(tool.name)) {
        throw new Error(`Duplicate WebMCP tool: ${tool.name}`)
      }
      seen.add(tool.name)
      tools.push(tool)
    }
  }
  add(coreTools(router))
  for (const factory of AREA_TOOL_FACTORIES) {
    add(factory({ router }))
  }
  return tools
}

export type WebMcpToolSummary = {
  name: string
  title: string
  description: string
  readOnly: boolean
  consequential: boolean
}

const IDLE_ROUTER: WebMcpRouter = {
  navigate: async () => undefined,
  subscribe: () => () => undefined,
}

/** Describe every registered tool for the /webmcp catalogue page. */
export function listWebMcpTools(): WebMcpToolSummary[] {
  return toolsFor(IDLE_ROUTER).map((tool) => ({
    name: tool.name,
    title: tool.title ?? tool.name,
    description: tool.description,
    readOnly: tool.annotations?.readOnlyHint === true,
    consequential: tool.annotations?.consequentialHint === true,
  }))
}

export function getWebMcpContext(): ModelContext | null {
  try {
    if (typeof document === 'undefined') return null
    const context = document.modelContext
    return typeof context?.registerTool === 'function' ? context : null
  } catch {
    return null
  }
}

export function installWebMcp(router: WebMcpRouter): () => void {
  const modelContext = getWebMcpContext()
  if (!modelContext) return () => undefined
  let controller = new AbortController()
  let disposed = false

  const register = () => {
    controller = new AbortController()
    const signal = controller.signal
    let tools: ModelContextTool[]
    try {
      tools = toolsFor(router)
    } catch {
      // A malformed optional integration must never block the application.
      return
    }
    for (const tool of tools) {
      try {
        void Promise.resolve(modelContext.registerTool(tool, { signal })).catch(
          () => undefined
        )
      } catch {
        // Optional browser integration must never prevent the app from mounting.
      }
    }
  }
  const refresh = () => {
    controller.abort()
    if (!disposed) register()
  }
  register()
  const unsubscribeAuth = useAuthStore.subscribe(refresh)
  const unsubscribeRouter = router.subscribe('onResolved', refresh)
  return () => {
    disposed = true
    unsubscribeAuth()
    unsubscribeRouter()
    controller.abort()
  }
}
