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
  AxiosError,
  type AxiosAdapter,
  type AxiosResponse,
  type InternalAxiosRequestConfig,
} from 'axios'

import {
  applyAuthBundle,
  setDevelopmentAuthRefreshAdapter,
} from '@/lib/auth-session'
import { api } from '@/lib/http-client'
import { ROLE } from '@/lib/roles'
import type { AuthBundle, AuthUser, TrustLevelInfo } from '@/stores/auth-store'

export const DEBUG_PERSONA_IDS = ['l0', 'b', 'e', 'f', 'l1', 'admin'] as const
export type DebugPersonaId = (typeof DEBUG_PERSONA_IDS)[number]

type MockConversation = {
  id: number
  userId: number
  title: string
  preview: string
  createdAt: number
  messages: Array<{
    id: number
    role: 'user' | 'assistant'
    content: string
    created_at: number
  }>
}

type DebugState = {
  activePersona: DebugPersonaId
  conversations: MockConversation[]
  preferences?: Partial<
    Record<DebugPersonaId, Pick<AuthUser, 'language' | 'sidebar_modules'>>
  >
}

type DebugEvent = {
  persona: DebugPersonaId
}

const DEBUG_EVENT = 'lmm:persona-debug-change'
const BLOCKED_DEBUG_REQUEST = 'PERSONA_DEBUG_UNMOCKED_REQUEST'
const now = Math.floor(Date.now() / 1000)
const debugAnnouncementReads = new Set<string>()
const debugAssistantRuntimeKeys = new Set<DebugPersonaId>()
type DebugProfileShare = {
  enabled: boolean
  modelUsageEnabled: boolean
}
const debugProfileShares = new Map<DebugPersonaId, DebugProfileShare>()
const debugProfileShareToken = 'a'.repeat(48)

function activeDebugProfileShare(): DebugProfileShare {
  const existing = debugProfileShares.get(state.activePersona)
  if (existing) return existing
  const created = { enabled: true, modelUsageEnabled: false }
  debugProfileShares.set(state.activePersona, created)
  return created
}

function trustLevel(level: number): TrustLevelInfo {
  return {
    level,
    automatic_level: level,
    override_level: null,
    paid_amount: level === 0 ? 0 : 50,
    discount_ratio: level > 0 ? 0.98 : 1,
    discount_percent: level > 0 ? 2 : 0,
    next_level: level < 2 ? level + 1 : null,
    next_level_paid_amount: level < 2 ? 100 : null,
    amount_to_next_level: level < 2 ? 50 : null,
    inactivity_decay_steps: 0,
    decay_period_days: 90,
    overridden: false,
  }
}

const DEBUG_USERS: Record<DebugPersonaId, AuthUser> = {
  l0: {
    id: 1001,
    username: 'debug_l0_newcomer',
    display_name: 'L0 Newcomer',
    role: ROLE.USER,
    status: 1,
    group: 'default',
    quota: 0,
    used_quota: 0,
    request_count: 0,
    developer_access_granted: false,
    trust_level_info: trustLevel(0),
    onboarding: {
      paid_activation_enabled: true,
      paid_activation_min_amount: 1,
      activation_complete: false,
      credential_complete: false,
      first_request_complete: false,
      stage: 'activate',
    },
  },
  l1: {
    id: 1002,
    username: 'debug_l1_developer',
    display_name: 'L1 Developer',
    role: ROLE.USER,
    status: 1,
    group: 'default',
    quota: 500_000,
    used_quota: 125_000,
    request_count: 42,
    developer_access_granted: true,
    trust_level_info: trustLevel(1),
    onboarding: {
      activation_complete: true,
      credential_complete: true,
      first_request_complete: true,
      stage: 'complete',
    },
  },
  b: {
    id: 1003,
    username: 'debug_b_guided_buyer',
    display_name: 'B · Guided buyer',
    email: 'b-buyer@debug.invalid',
    role: ROLE.USER,
    status: 1,
    group: 'default',
    quota: 0,
    used_quota: 0,
    request_count: 3,
    developer_access_granted: false,
    trust_level_info: trustLevel(0),
    onboarding: {
      activation_complete: false,
      credential_complete: false,
      first_request_complete: false,
      stage: 'activate',
    },
  },
  e: {
    id: 1004,
    username: 'debug_e_normal_user',
    display_name: 'E · Normal user',
    email: 'e-user@debug.invalid',
    role: ROLE.USER,
    status: 1,
    group: 'default',
    quota: 750_000,
    used_quota: 190_000,
    request_count: 88,
    developer_access_granted: true,
    trust_level_info: trustLevel(1),
    onboarding: {
      activation_complete: true,
      credential_complete: true,
      first_request_complete: true,
      stage: 'complete',
    },
  },
  f: {
    id: 1005,
    username: 'debug_f_enterprise_operator',
    display_name: 'F · Enterprise operator',
    email: 'f-operator@debug.invalid',
    role: ROLE.USER,
    status: 1,
    group: 'enterprise',
    quota: 2_000_000,
    used_quota: 340_000,
    request_count: 120,
    developer_access_granted: true,
    trust_level_info: trustLevel(2),
    onboarding: {
      activation_complete: true,
      credential_complete: true,
      first_request_complete: true,
      stage: 'complete',
    },
  },
  admin: {
    id: 1099,
    username: 'debug_administrator',
    display_name: 'Administrator',
    role: ROLE.SUPER_ADMIN,
    status: 1,
    group: 'admin',
    quota: 10_000_000,
    used_quota: 2_500_000,
    request_count: 390,
    developer_access_granted: true,
    trust_level_info: trustLevel(4),
    onboarding: {
      activation_complete: true,
      credential_complete: true,
      first_request_complete: true,
      stage: 'complete',
    },
  },
}

function initialConversations(): MockConversation[] {
  return [
    {
      id: 8101,
      userId: DEBUG_USERS.l0.id,
      title: 'Need help requesting L1 access',
      preview: 'My email is [REDACTED:EMAIL] and I need L1 access.',
      createdAt: now - 3_600,
      messages: [
        {
          id: 9101,
          role: 'user',
          content: 'My email is [REDACTED:EMAIL] and I need L1 access.',
          created_at: now - 3_600,
        },
        {
          id: 9102,
          role: 'assistant',
          content:
            'Tell me what you want to build. I can help prepare an access request.',
          created_at: now - 3_540,
        },
      ],
    },
    {
      id: 8102,
      userId: DEBUG_USERS.l1.id,
      title: 'SDK configuration help',
      preview: 'Show me the OpenAI-compatible client setup.',
      createdAt: now - 7_200,
      messages: [
        {
          id: 9201,
          role: 'user',
          content: 'Show me the OpenAI-compatible client setup.',
          created_at: now - 7_200,
        },
        {
          id: 9202,
          role: 'assistant',
          content: 'Open the setup guide and select your client platform.',
          created_at: now - 7_140,
        },
      ],
    },
    {
      id: 8103,
      userId: DEBUG_USERS.b.id,
      title: 'Need a guided client setup',
      preview: 'I need a stable API and step-by-step setup help.',
      createdAt: now - 10_800,
      messages: [
        {
          id: 9301,
          role: 'user',
          content: 'I need a stable API and step-by-step setup help.',
          created_at: now - 10_800,
        },
        {
          id: 9302,
          role: 'assistant',
          content: 'I will guide you through one setup step at a time.',
          created_at: now - 10_740,
        },
      ],
    },
    {
      id: 8104,
      userId: DEBUG_USERS.f.id,
      title: 'Enterprise stability review',
      preview: 'We need predictable latency and an operational setup.',
      createdAt: now - 14_400,
      messages: [
        {
          id: 9401,
          role: 'user',
          content: 'We need predictable latency and an operational setup.',
          created_at: now - 14_400,
        },
        {
          id: 9402,
          role: 'assistant',
          content:
            'I will focus on reliability, routing, and operational checks.',
          created_at: now - 14_340,
        },
      ],
    },
    {
      id: 8105,
      userId: DEBUG_USERS.e.id,
      title: 'Compare available model routes',
      preview: 'Which model route should I use for a document workflow?',
      createdAt: now - 18_000,
      messages: [
        {
          id: 9501,
          role: 'user',
          content: 'Which model route should I use for a document workflow?',
          created_at: now - 18_000,
        },
        {
          id: 9502,
          role: 'assistant',
          content:
            'Compare the live model catalog and choose the route by task.',
          created_at: now - 17_940,
        },
      ],
    },
  ]
}

// Development-only URL selection keeps local QA stable across HMR reloads.
function initialDebugPersona(): DebugPersonaId {
  if (typeof window === 'undefined') return 'l0'
  const value = new URLSearchParams(window.location.search).get('debug_persona')
  return DEBUG_PERSONA_IDS.includes(value as DebugPersonaId)
    ? (value as DebugPersonaId)
    : 'l0'
}
let state: DebugState = {
  activePersona: initialDebugPersona(),
  conversations: initialConversations(),
}

function installBlockedDebugFetch(): void {
  const originalFetch = globalThis.fetch.bind(globalThis)
  globalThis.fetch = async (input, init) => {
    const request = input instanceof Request ? input : new Request(input, init)
    const url = new URL(request.url, window.location.origin)
    if (
      url.origin === window.location.origin &&
      url.pathname === '/api/status'
    ) {
      return new Response(
        JSON.stringify(
          envelope({
            system_name: 'LMM Persona Lab',
            logo: '/logo.png',
            assistant: { enabled: true },
            announcements_enabled: false,
          })
        ),
        { status: 200, headers: { 'content-type': 'application/json' } }
      )
    }
    if (url.origin !== window.location.origin) {
      throw new Error(`PERSONA_DEBUG_EXTERNAL_REQUEST: ${url.origin}`)
    }
    if (isBackendPath(url.pathname)) {
      throw new Error(
        `${BLOCKED_DEBUG_REQUEST}: ${request.method} ${url.pathname}`
      )
    }
    return originalFetch(request)
  }
}

function isBackendPath(pathname: string): boolean {
  return ['/api', '/mj', '/pg'].some(
    (prefix) => pathname === prefix || pathname.startsWith(`${prefix}/`)
  )
}

function cloneUser(persona: DebugPersonaId): AuthUser {
  return {
    ...structuredClone(DEBUG_USERS[persona]),
    ...state.preferences?.[persona],
  }
}

function authBundle(persona: DebugPersonaId): AuthBundle {
  const issuedAt = Math.floor(Date.now() / 1000)
  return {
    access_token: `debug-persona-${persona}`,
    token_type: 'Bearer',
    access_expires_at: issuedAt + 86_400,
    user: cloneUser(persona),
    session: {
      sid: `debug-persona-${persona}`,
      current: true,
      login_method: 'development fixture',
      ip: '127.0.0.1',
      user_agent: 'LMM persona debug runtime',
      created_at: issuedAt,
      last_active_at: issuedAt,
      expires_at: issuedAt + 86_400,
    },
  }
}

function envelope<T>(data: T) {
  return { success: true, data }
}

function response<T>(
  config: InternalAxiosRequestConfig,
  data: T,
  status = 200
): AxiosResponse<T> {
  return {
    data,
    status,
    statusText: status === 200 ? 'OK' : 'Mock response',
    headers: {},
    config,
  }
}

function requestPath(config: InternalAxiosRequestConfig): URL {
  return new URL(config.url ?? '/', window.location.origin)
}

function activeUser(): AuthUser {
  return cloneUser(state.activePersona)
}

function conversationSummary(conversation: MockConversation) {
  return {
    id: conversation.id,
    title: conversation.title,
    last_message_preview: conversation.preview,
    created_at: conversation.createdAt,
    updated_at: conversation.createdAt + 60,
    archived_at: 0,
    owner:
      conversation.userId === activeUser().id ? 'self' : 'lower_level_user',
    privacy_notice:
      'Debug fixture: higher-access users can review lower-access conversations.',
  }
}

function parseRequestBody(config: InternalAxiosRequestConfig): unknown {
  if (typeof config.data !== 'string') return config.data
  try {
    return JSON.parse(config.data)
  } catch {
    return config.data
  }
}

function assistantReply(content: string) {
  return {
    choices: [{ message: { content } }],
  }
}

function preConversationPresets() {
  return {
    generation: now,
    version: 'persona-fixture-v1',
    presets: [
      {
        id: 'models-and-pricing',
        label: 'Browse models and pricing',
        prompt: 'Show me the available model IDs and their current prices.',
      },
      {
        id: 'client-setup',
        label: 'Connect my client',
        prompt: 'Help me connect my current AI client step by step.',
      },
      {
        id: 'l1-access',
        label: 'Request L1 access',
        prompt: 'Help me prepare one clear recommendation for L1 access.',
      },
      {
        id: 'bounty',
        label: 'Explore open-source bounties',
        prompt: 'Show me how to join an open-source bounty challenge.',
      },
    ],
  }
}

function personaTodoPage(category = 'all') {
  const categories = [
    'security_incident',
    'security_review',
    'open_source_bounty_review',
    'open_source_bounty',
    'developer_access',
    'account_action',
  ].map((key) => ({ key, total: 0, unread: 0 }))

  return {
    items: [],
    page: 1,
    page_size: 50,
    total: 0,
    category,
    unread_count: 0,
    total_unread_count: 0,
    unread_by_category: Object.fromEntries(
      categories.map(({ key }) => [key, 0])
    ),
    categories,
  }
}

function readAuditUserId(config: InternalAxiosRequestConfig, url: URL): number {
  const raw =
    (config.params as { user_id?: unknown } | undefined)?.user_id ??
    url.searchParams.get('user_id')
  const id = Number(raw)
  return Number.isSafeInteger(id) && id > 0 ? id : activeUser().id
}

function userById(id: number): AuthUser | undefined {
  return Object.values(DEBUG_USERS).find((user) => user.id === id)
}

function canReadUserConversations(targetUserId: number): boolean {
  const viewer = activeUser()
  if (viewer.id === targetUserId) return true
  const target = userById(targetUserId)
  if (!target) return false
  if (viewer.role >= ROLE.ADMIN && viewer.role > target.role) return true
  return (
    (viewer.trust_level_info?.level ?? 0) >
    (target.trust_level_info?.level ?? 0)
  )
}

function rejectRequest(
  config: InternalAxiosRequestConfig,
  status: number,
  message: string
): never {
  const rejectedResponse = response(config, { success: false, message }, status)
  throw new AxiosError(
    message,
    AxiosError.ERR_BAD_REQUEST,
    config,
    undefined,
    rejectedResponse
  )
}

const debugAdapter: AxiosAdapter = async (config) => {
  const url = requestPath(config)
  const path = url.pathname
  const method = (config.method ?? 'get').toUpperCase()

  if (method === 'PUT' && path === '/api/user/self') {
    let data: Record<string, unknown>
    try {
      data =
        typeof config.data === 'string' ? JSON.parse(config.data) : config.data
    } catch {
      rejectRequest(config, 400, 'Invalid local preview preference')
    }
    if (
      !data ||
      Array.isArray(data) ||
      Object.keys(data).length === 0 ||
      Object.keys(data).some(
        (key) => !['language', 'sidebar_modules'].includes(key)
      )
    ) {
      rejectRequest(
        config,
        400,
        'This preview only saves language and sidebar preferences locally'
      )
    }
    const preferences: Pick<AuthUser, 'language' | 'sidebar_modules'> = {}
    if (data.language !== undefined) {
      if (
        typeof data.language !== 'string' ||
        !['en', 'zh', 'zh-TW', 'fr', 'ja', 'ru', 'vi'].includes(data.language)
      ) {
        rejectRequest(config, 400, 'Invalid local preview language')
      }
      preferences.language = data.language
    }
    if (data.sidebar_modules !== undefined) {
      if (
        typeof data.sidebar_modules !== 'string' ||
        data.sidebar_modules.length > 65536
      ) {
        rejectRequest(config, 400, 'Invalid local preview sidebar preference')
      }
      preferences.sidebar_modules = data.sidebar_modules
    }
    state = {
      ...state,
      preferences: {
        ...state.preferences,
        [state.activePersona]: {
          ...state.preferences?.[state.activePersona],
          ...preferences,
        },
      },
    }
    return response(config, envelope(activeUser()))
  }

  // Empty, read-only SMS fixtures let low-balance page entry be tested without
  // making a provider call. Purchase and other mutations remain unmocked.
  if (method === 'GET' && path === '/api/hero-sms/sms/orders/current-list') {
    return response(config, envelope({ items: [] }))
  }
  if (method === 'GET' && path === '/api/hero-sms/sms/orders') {
    return response(
      config,
      envelope({ items: [], total: 0, page: 1, size: 50 })
    )
  }
  if (
    method === 'GET' &&
    [
      '/api/hero-sms/sms/countries',
      '/api/hero-sms/sms/services',
      '/api/hero-sms/sms/operators',
    ].includes(path)
  ) {
    return response(config, envelope([]))
  }

  if (method === 'GET' && path === '/api/admin/acquisition/links') {
    return response(
      config,
      envelope({
        items: [
          {
            id: '0123456789abcdef0123456789abcdef',
            name: 'Local documentation example',
            source: 'documentation',
            medium: 'readme',
            campaign: 'local-preview',
            content: 'setup-guide',
            target: '/guide',
            archived: false,
          },
        ],
        total: 1,
        page: 1,
      })
    )
  }
  if (
    method === 'GET' &&
    /^\/api\/admin\/acquisition\/links\/[^/]+\/preview$/.test(path)
  ) {
    return response(
      config,
      envelope({
        link_id: '0123456789abcdef0123456789abcdef',
        target: '/guide',
        source: 'documentation',
        medium: 'readme',
        campaign: 'local-preview',
        content: 'setup-guide',
        evidence: 'promotion_link',
        referrer_host: '',
        excluded: true,
        archived: false,
      })
    )
  }
  if (method === 'GET' && path === '/api/acquisition/self-report') {
    return response(config, envelope(null))
  }
  if (method === 'GET' && path === '/api/log/self') {
    return response(
      config,
      envelope({ items: [], total: 0, page: 1, page_size: 1 })
    )
  }
  if (method === 'GET' && path === '/api/admin/acquisition/visitors') {
    return response(
      config,
      envelope({
        from: now - 30 * 86400,
        to: now,
        available_from: now - 20 * 86400,
        coverage_complete: false,
        observed_visitors: 5,
        channels: [
          { source: 'documentation', visitors: 3 },
          { source: 'unknown', visitors: 2 },
        ],
      })
    )
  }
  if (method === 'GET' && path === '/api/admin/acquisition/funnel') {
    return response(
      config,
      envelope({
        from: now - 30 * 86400,
        to: now,
        observed_until: now,
        observation_days: 30,
        attribution: 'registration',
        payment_snapshot_updated_at: now,
        payment_snapshot_status: 'ready',
        unavailable: ['historical_permission_events'],
        first_payment_sources: [
          {
            source: 'documentation',
            evidence: 'promotion_link',
            rule: 'last_external_before_payment_v1',
            lookback_days: 30,
            inferred: true,
            accounts: 1,
          },
        ],
        channels: [
          {
            source: 'documentation',
            registrations: 3,
            mature_accounts: 1,
            observing_accounts: 2,
            stages: [
              'application_submitted',
              'access_approved',
              'oauth_authorized',
              'api_key_created',
              'credential_ready',
              'client_configured',
              'first_successful_request',
              'first_payment',
              'repeat_payment',
            ].map((id) => ({
              id,
              observed: id === 'client_configured' ? 0 : 1,
              not_observed: 0,
              unknown: id === 'client_configured' ? 3 : 0,
              mature_observed: id === 'client_configured' ? 0 : 1,
              mature_known: id === 'client_configured' ? 0 : 1,
              observing: 2,
              conversion_rate: id === 'client_configured' ? null : 1,
              timing_accounts: id === 'client_configured' ? 0 : 1,
              mean_seconds_from_registration:
                id === 'client_configured' ? null : 3600,
            })),
          },
        ],
      })
    )
  }
  if (method === 'GET' && path === '/api/admin/acquisition/report') {
    return response(
      config,
      envelope({
        from: now - 30 * 86400,
        to: now,
        unclassified_payment_rows: 0,
        lookback_days: 30,
        applied_lookback_days: [30],
        started_at: now - 20 * 86400,
        observed_until: now,
        channels: [
          {
            source: 'documentation',
            evidence: 'promotion_link',
            registrations: 3,
            identified_registrations: 3,
          },
          {
            source: 'unknown',
            evidence: 'unavailable',
            registrations: 2,
            identified_registrations: 0,
          },
        ],
        payments: [
          {
            source: 'documentation',
            currency: 'USD',
            paid_micros: 10000000,
            refund_micros: 2000000,
            net_micros: 8000000,
            paying_accounts: 1,
          },
        ],
        activity_state: {
          started_at: now - 20 * 86400,
          scanned_through: now - 60,
          updated_at: now,
          status: 'ready',
          incomplete: false,
        },
        activity: [
          {
            source: 'documentation',
            eligible_accounts: 3,
            successful_accounts: 2,
            mature_accounts: 1,
            retained_accounts: 1,
            observing_accounts: 1,
            incomplete_accounts: 0,
            retention_rate: 1,
          },
        ],
      })
    )
  }
  if (method === 'GET' && path === '/api/admin/acquisition/users') {
    return response(
      config,
      envelope({
        total: 3,
        items: [1003, 1004, 1005].map((id, index) => ({
          user_id: id,
          registered_at: now - (15 - index * 5) * 86400,
          source: 'documentation',
          evidence: 'promotion_link',
          first_success_at: index < 2 ? now - (12 - index * 5) * 86400 : 0,
        })),
      })
    )
  }
  if (
    method === 'GET' &&
    /^\/api\/admin\/acquisition\/users\/\d+$/.test(path)
  ) {
    return response(
      config,
      envelope({
        user_id: Number(path.split('/').at(-1)),
        registered_at: now - 15 * 86400,
        historical: false,
        first_success_at: now - 12 * 86400,
        attribution: {
          first_source: 'documentation',
          first_observed_at: now - 15 * 86400 - 300,
          registration_source: 'documentation',
          registration_campaign: 'local-preview',
          registration_inferred: false,
          registration_visit_id: 1,
          lookback_days: 30,
        },
        recent: [
          {
            id: 1,
            source: 'documentation',
            evidence: 'promotion_link',
            created_at: now - 15 * 86400 - 300,
            referrer_host: 'github.com',
            landing: '/guide',
          },
        ],
      })
    )
  }
  if (
    path === '/api/acquisition/consent' &&
    ['POST', 'DELETE'].includes(method)
  ) {
    return response(config, envelope({ allowed: method === 'POST' }))
  }
  if (path === '/api/admin/acquisition/activity/rebuild' && method === 'POST') {
    return response(config, envelope({ scheduled: true }))
  }
  if (method === 'GET' && path === '/api/status') {
    return response(
      config,
      envelope({
        system_name: 'LMM Persona Lab',
        logo: '/logo.png',
        assistant: { enabled: true },
        announcements_enabled: false,
        registration_enabled: false,
        email_verification: false,
        turnstile_check: false,
      })
    )
  }
  if (method === 'GET' && path === '/api/setup') {
    return response(config, envelope({ status: true }))
  }
  if (method === 'POST' && path === '/api/user/auth/refresh') {
    return response(config, envelope(authBundle(state.activePersona)))
  }
  if (method === 'GET' && path === '/api/user/self/announcements') {
    const scenario = new URLSearchParams(window.location.search).get(
      'required-announcements'
    )
    if (scenario === 'unsupported') {
      rejectRequest(config, 404, 'Legacy backend has no announcement API')
    }
    if (scenario === 'unavailable') {
      rejectRequest(config, 503, 'Announcement service is unavailable')
    }
    const required = scenario === '1'
    return response(
      config,
      envelope(
        required
          ? [1, 2].map((id) => ({
              id,
              revision: `debug-announcement-${id}`,
              publishDate: '2026-09-01T00:00:00Z',
              content:
                id === 1
                  ? Array.from(
                      { length: 16 },
                      (_, index) =>
                        `### Reading section ${index + 1}\n\nThis is a local test announcement. Scroll through every section to enable confirmation.`
                    ).join('\n\n')
                  : 'This is the second local test announcement. Confirm it to enter the console.',
              read_at: debugAnnouncementReads.has(`${activeUser().id}:${id}`)
                ? now
                : 0,
            }))
          : []
      )
    )
  }
  if (method === 'POST' && path === '/api/user/self/announcements/read') {
    const data =
      typeof config.data === 'string' ? JSON.parse(config.data) : config.data
    debugAnnouncementReads.add(`${activeUser().id}:${data.id}`)
    return response(config, envelope([]))
  }
  if (method === 'GET' && path === '/api/user/self') {
    return response(config, envelope(activeUser()))
  }
  if (
    path === '/api/user/self/profile-share' &&
    ['GET', 'POST', 'DELETE'].includes(method)
  ) {
    const share = activeDebugProfileShare()
    if (method === 'POST') {
      const data =
        typeof config.data === 'string' ? JSON.parse(config.data) : config.data
      share.enabled = true
      if (typeof data?.model_usage_enabled === 'boolean') {
        share.modelUsageEnabled = data.model_usage_enabled
      }
    } else if (method === 'DELETE') {
      share.enabled = false
      share.modelUsageEnabled = false
    }
    return response(
      config,
      envelope(
        share.enabled
          ? {
              enabled: true,
              model_usage_enabled: share.modelUsageEnabled,
              token: debugProfileShareToken,
              url: `${window.location.origin}/api/share/profile/${debugProfileShareToken}.svg`,
            }
          : { enabled: false }
      )
    )
  }
  if (method === 'GET' && path === '/api/notice') {
    return response(config, envelope(''))
  }
  if (method === 'GET' && path === '/api/release-notes/latest') {
    return response(config, envelope(null))
  }
  if (method === 'GET' && path === '/api/user/models') {
    return response(config, envelope(['gpt-5-mini', 'claude-sonnet']))
  }
  if (method === 'GET' && path === '/api/user/self/groups') {
    const group = activeUser().group || 'default'
    return response(
      config,
      envelope({
        [group]: {
          desc: group === 'enterprise' ? 'Enterprise' : 'Default',
          ratio: 1,
        },
      })
    )
  }
  if (method === 'GET' && path === '/api/uptime/status') {
    return response(config, envelope([]))
  }
  if (method === 'GET' && path === '/api/ratio-notifications') {
    return response(config, envelope([]))
  }
  if (method === 'GET' && path === '/api/token/auto-groups') {
    return response(
      config,
      envelope({ groups: [activeUser().group || 'default'], max_count: 5 })
    )
  }
  if (method === 'POST' && path === '/api/assistant/runtime-key') {
    if (activeUser().role < ROLE.SUPER_ADMIN) {
      return response(
        config,
        { success: false, message: 'Root account required' },
        403
      )
    }
    const created = !debugAssistantRuntimeKeys.has(state.activePersona)
    debugAssistantRuntimeKeys.add(state.activePersona)
    return response(
      config,
      envelope({
        id: 8002,
        name: 'AI assistant runtime',
        group: 'default',
        created,
      })
    )
  }
  if (method === 'GET' && path === '/api/token/') {
    const developerAccessGranted =
      activeUser().developer_access_granted === true
    const runtimeKeys =
      url.searchParams.get('creation_mode') === 'automatic' &&
      debugAssistantRuntimeKeys.has(state.activePersona)
        ? [
            {
              id: 8002,
              name: 'AI assistant runtime',
              key: 'a1b2**********c3d4',
              one_time_reveal: true,
              status: 1,
              remain_quota: 0,
              used_quota: 0,
              unlimited_quota: true,
              expired_time: -1,
              created_time: now,
              accessed_time: now,
              group: 'default',
              model_limits_enabled: false,
              model_limits: '',
              allow_ips: '',
              creation_source: 'assistant_runtime',
            },
          ]
        : []
    return response(
      config,
      !developerAccessGranted
        ? { success: false, message: 'Developer access required' }
        : {
            success: true,
            data: {
              items: runtimeKeys,
              total: runtimeKeys.length,
              page: 1,
              page_size: 10,
            },
          },
      !developerAccessGranted ? 403 : 200
    )
  }
  if (method === 'GET' && (path === '/api/data/self' || path === '/api/data')) {
    return response(config, envelope([]))
  }
  if (method === 'GET' && path === '/api/perf-metrics/summary') {
    return response(config, envelope({ models: [] }))
  }
  if (method === 'GET' && path === '/api/assistant/status') {
    const user = activeUser()
    return response(
      config,
      envelope({
        enabled: true,
        model: 'debug-fixture',
        developer_access_granted: user.developer_access_granted === true,
        funding: { mode: 'super_administrator' },
        trust_level: user.trust_level_info?.level ?? 0,
        role: user.role,
        is_admin: user.role >= ROLE.ADMIN,
        is_root: user.role === ROLE.SUPER_ADMIN,
        capabilities: {
          public_assistant: true,
          account: true,
          developer_tools: user.developer_access_granted === true,
          admin_config: user.role >= ROLE.ADMIN,
        },
      })
    )
  }
  if (method === 'GET' && path === '/api/assistant/pre-conversation-presets') {
    return response(config, envelope(preConversationPresets()))
  }
  if (method === 'GET' && path === '/api/assistant/journey') {
    const access = activeUser().developer_access_granted === true
    return response(
      config,
      envelope({
        main: [
          { id: 'ask_ai', status: 'completed' },
          {
            id: 'get_recommendation',
            status: access ? 'completed' : 'pending',
          },
          { id: 'create_api_key', status: access ? 'completed' : 'pending' },
          { id: 'install_client', status: access ? 'completed' : 'pending' },
          { id: 'configure_client', status: access ? 'completed' : 'pending' },
          { id: 'first_api_call', status: access ? 'completed' : 'pending' },
        ],
        side: [
          {
            id: 'earn_ai_gift',
            status:
              activeUser().id === DEBUG_USERS.b.id ? 'pending' : 'completed',
          },
          { id: 'accept_bounty', status: 'pending' },
        ],
      })
    )
  }
  if (method === 'GET' && path === '/api/assistant/new-user-gift') {
    const offered = activeUser().id === DEBUG_USERS.b.id
    return response(
      config,
      envelope(
        offered
          ? {
              amount_cents: 300,
              quota: 3_000_000,
              status: 'offered',
              reason: 'Fixture: guided, concrete onboarding conversation.',
              created_at: now,
              claimed_at: 0,
            }
          : null
      )
    )
  }
  if (method === 'POST' && path === '/api/assistant/new-user-gift/claim') {
    if (activeUser().id !== DEBUG_USERS.b.id) {
      rejectRequest(config, 404, 'Welcome gift is unavailable')
    }
    return response(
      config,
      envelope({
        gift: {
          amount_cents: 300,
          quota: 3_000_000,
          status: 'claimed',
          reason: 'Fixture: guided, concrete onboarding conversation.',
          created_at: now,
          claimed_at: now,
        },
        already_claimed: false,
      })
    )
  }
  if (
    method === 'POST' &&
    /^\/api\/assistant\/pre-conversation-presets\/[^/]+\/click$/.test(path)
  ) {
    return response(config, envelope(null))
  }
  if (method === 'POST' && path === '/api/assistant/chat') {
    const body = parseRequestBody(config) as { message?: string }
    return response(
      config,
      assistantReply(
        `Debug assistant received: ${String(body?.message ?? '').trim()}`
      )
    )
  }
  if (method === 'GET' && path === '/api/assistant/conversations') {
    const requestedUserId = readAuditUserId(config, url)
    if (!canReadUserConversations(requestedUserId)) {
      rejectRequest(config, 404, 'Conversation history is unavailable')
    }
    const visible = state.conversations.filter(
      (conversation) => conversation.userId === requestedUserId
    )
    return response(
      config,
      envelope({
        conversations: visible.map(conversationSummary),
        privacy_notice: 'This is non-production fixture data.',
      })
    )
  }
  const detail = path.match(/^\/api\/assistant\/conversations\/(\d+)$/)
  if (method === 'GET' && detail) {
    const conversation = state.conversations.find(
      (item) => item.id === Number(detail[1])
    )
    if (conversation) {
      if (!canReadUserConversations(conversation.userId)) {
        rejectRequest(config, 404, 'Conversation history is unavailable')
      }
      return response(
        config,
        envelope({
          conversation: conversationSummary(conversation),
          messages: conversation.messages,
          privacy_notice: 'This is non-production fixture data.',
        })
      )
    }
    rejectRequest(config, 404, 'Conversation history is unavailable')
  }
  if (method === 'GET' && path === '/api/assistant/handoffs/self') {
    return response(config, envelope(null))
  }
  if (method === 'GET' && path === '/api/user/self/onboarding/todo') {
    const granted = activeUser().developer_access_granted === true
    return response(
      config,
      envelope({
        eligibility: {
          eligible: granted,
          developer_access_granted: granted,
          trust_level: activeUser().trust_level_info?.level ?? 0,
        },
        status: granted ? 'completed' : 'unavailable',
        steps: [],
      })
    )
  }
  if (method === 'GET' && path === '/api/user/developer-access/request') {
    return response(config, envelope(null))
  }
  if (method === 'GET' && path === '/api/todos') {
    return response(
      config,
      envelope(personaTodoPage(url.searchParams.get('category') || 'all'))
    )
  }
  if (method === 'POST' && path === '/api/todos/read') {
    return response(config, envelope({ marked: 0 }))
  }

  throw new Error(`${BLOCKED_DEBUG_REQUEST}: ${method} ${path}`)
}

export function installPersonaDebugRuntime(): void {
  api.defaults.adapter = debugAdapter
  setDevelopmentAuthRefreshAdapter(debugAdapter)
  installBlockedDebugFetch()
  applyAuthBundle(authBundle(state.activePersona), false)
  document.documentElement.dataset.personaDebug = 'true'
}

export function getActiveDebugPersona(): DebugPersonaId {
  return state.activePersona
}

export function setActiveDebugPersona(persona: DebugPersonaId): void {
  state = { ...state, activePersona: persona }
  applyAuthBundle(authBundle(persona), false)
  window.dispatchEvent(
    new CustomEvent<DebugEvent>(DEBUG_EVENT, { detail: { persona } })
  )
}

export function resetPersonaDebugRuntime(): void {
  state = { activePersona: 'l0', conversations: initialConversations() }
  setActiveDebugPersona('l0')
}

export function subscribeDebugPersona(
  listener: (persona: DebugPersonaId) => void
): () => void {
  const handleChange = (event: Event) => {
    listener((event as CustomEvent<DebugEvent>).detail.persona)
  }
  window.addEventListener(DEBUG_EVENT, handleChange)
  return () => window.removeEventListener(DEBUG_EVENT, handleChange)
}
