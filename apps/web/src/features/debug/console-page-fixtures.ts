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
import type { AxiosAdapter, InternalAxiosRequestConfig } from 'axios'

import { useAuthStore } from '@/stores/auth-store'

const stamp = 1790035200
const page = { items: [], total: 0, page: 1, page_size: 20, size: 20 }
const modelNames = ['gpt-5-mini', 'claude-sonnet']

// Explicit read-only fixtures, not production fallbacks. Unknown requests still
// reach the persona adapter and fail closed; no payment or administrative write
// is added here. Activated only by console_review=1 in the development entry.
const reads: Record<string, unknown> = {
  '/api/user/company-billing-profile': null,
  '/api/user/topup/info': {
    enable_online_topup: false,
    enable_stripe_topup: false,
    enable_creem_topup: false,
    enable_waffo_topup: false,
    pay_methods: [],
    amount_options: [10, 50, 100, 200],
    min_topup: 1,
    stripe_min_topup: 1,
    discount: {},
    developer_access_granted: true,
    payment_available: false,
  },
  '/api/user/aff': 'local-preview-referral',
  '/api/user/2fa/status': {
    enabled: false,
    locked: false,
    backup_codes_remaining: 0,
  },
  '/api/user/gift': [],
  '/api/user/oauth/bindings': [],
  '/api/user/passkey': { enabled: false, credentials: [] },
  '/api/user/checkin': { enabled: false, checked_in_today: false, records: [] },
  '/api/subscription/self': {
    billing_preference: 'wallet_only',
    subscriptions: [],
    all_subscriptions: [],
  },
  '/api/subscription/self/reset-vouchers': [],
  '/api/subscription/plans': [],
  '/api/subscription/admin/plans': [],
  '/api/subscription/admin/reset/eligible': page,
  '/api/log/self/stat': { quota: 15000, rpm: 0, tpm: 0 },
  '/api/log/stat': { quota: 15000, rpm: 0, tpm: 0 },
  '/api/task/self': page,
  '/api/task/': page,
  '/api/remote-control/v1/pi/sessions': { sessions: [] },
  '/api/open-source-bounties/config': {
    rate_percent: 1,
    rate_basis_points: 100,
  },
  '/api/open-source-bounties': page,
  '/api/open-source-bounties/mine': [],
  '/api/open-source-bounties/accepted': [],
  '/api/open-source-bounties/disputes/mine': [],
  '/api/open-source-bounties/tips/received': [],
  '/api/open-source-bounties/mcp-token': {
    configured: false,
    endpoint: '/mcp',
    token: null,
  },
  '/api/public-relays': { items: [], group: 'default' },
  '/api/public-relays/mine': { items: [], group: 'default' },
  '/api/public-relays/config': {
    enabled: true,
    group: 'default',
    commission_rate: 0,
    fee_bps: 100,
  },
  '/api/prefill_group': { default: 1 },
  '/api/channel/models': modelNames,
  '/api/group/': ['default'],
  '/api/user/groups': ['default'],
  '/api/tool-market/config': {
    enabled: true,
    fee_bps: 100,
    recipient_id: 0,
    quota_per_unit: 500000,
    web_client_id: 'console-preview',
    mcp_path: '/mcp',
  },
  '/api/tool-market': [],
  '/api/tool-market/mine/grants': [],
  '/api/tool-market/mine/installations': [],
  '/api/tool-market/mine/favorites': [],
  '/api/tool-market/mine/services': [],
  '/api/tool-market/mine/calls': [],
  '/api/tool-market/mine/tokens': [],
  '/api/tool-market/mine/income': [],
  '/api/tool-market/mine/budgets': [],
  '/api/rankings': {
    models: [],
    vendors: [],
    top_movers: [],
    top_droppers: [],
    models_history: { points: [], models: [], buckets: 0 },
    vendor_share_history: { points: [], vendors: [], buckets: 0 },
  },
  '/api/assistant/models': modelNames,
  '/api/assistant/weekly-discount': null,
  '/api/assistant/support/eligibility': {
    eligible: false,
    reason: 'Local preview',
  },
  '/api/assistant/support/self': [],
  '/api/channel/ops': { retry_times: 0 },
  '/api/channel': page,
  '/api/models/': page,
  '/api/vendors/': page,
  '/api/deployments/settings': { enabled: false },
  '/api/deployments/': page,
  '/api/user/': page,
  '/api/redemption/': page,
  '/api/discount-code/': page,
  '/api/red-packet/admin': [],
  '/api/system-info/instances': [],
  '/api/system-task/list': [],
  '/api/system-task/current': null,
  '/api/custom-oauth-provider/': [],
  '/api/option/channel_affinity_cache': { entries: [], total: 0, size: 0 },
  '/api/security/policy': {
    policy_version: 'preview',
    reference_effective_date: '',
    reference_url: '',
    alignment: '',
    enforcement: { enabled: false, on_prompt: false, action: 'audit' },
    risk_categories: [],
    rules: [],
    violation_fees: [],
  },
  '/api/security/stats': {
    total_matches: 0,
    blocked_matches: 0,
    audited_matches: 0,
    affected_requests: 0,
    affected_users: 0,
    by_category: [],
  },
  '/api/security/admin/stats': {
    total_matches: 0,
    blocked_matches: 0,
    audited_matches: 0,
    affected_requests: 0,
    affected_users: 0,
    by_category: [],
    by_rule: [],
    ai_review: {
      total: 0,
      completed: 0,
      violations: 0,
      abuses: 0,
      failed: 0,
      by_group: [],
    },
  },
  '/api/security/admin/ai-reviews': page,
  '/api/security/admin/events': page,
  '/api/security/admin/review-runs': [],
  '/api/security/admin/policy': {
    settings: { enabled: false, on_prompt: false, action: 'audit' },
    rules: [],
  },
  '/api/assistant/admin/registration-events': [],
  '/api/option/hero-sms': {
    enabled: false,
    email_enabled: false,
    sms_enabled: false,
    api_key_configured: false,
    pending_work: false,
    currency: 'USD',
    currency_code: 840,
    price_multiplier: 1,
  },
  '/api/performance/logs': [],
  '/api/performance/stats': {
    enabled: false,
    total: 0,
    requests: 0,
    cache: {},
    system: {},
  },
  '/api/scripts/repository': {
    repository_url: '',
    branch: 'main',
    github_key_set: false,
  },
  '/api/scripts': [],
  '/api/hero-sms/email/products': { items: [] },
  '/api/hero-sms/email/activations': page,
  '/api/hero-sms/email/activations/current': null,
  '/api/drawing/self/settings': { enabled: false },
  '/api/option/': [
    { key: 'SystemName', value: 'LMM Best' },
    { key: 'QuotaPerUnit', value: '500000' },
  ],
}

export function consolePageFixture(
  config: InternalAxiosRequestConfig
): unknown {
  if ((config.method ?? 'get').toUpperCase() !== 'GET') return undefined
  const url = new URL(config.url ?? '', window.location.origin)
  if (url.origin !== window.location.origin) return undefined
  const user = useAuthStore.getState().auth.user
  const path = url.pathname
  if (path === '/api/todos') {
    const category = url.searchParams.get('category') || 'all'
    return {
      success: true,
      data: {
        ...page,
        category,
        page_size: 50,
        unread_count: 0,
        total_unread_count: 0,
        unread_by_category: {},
        categories: [],
      },
    }
  }
  if (path === '/api/pricing') {
    return {
      success: true,
      data: [
        {
          id: 1,
          model_name: 'gpt-5-mini',
          quota_type: 0,
          model_ratio: 0.5,
          completion_ratio: 2,
          enable_groups: ['default'],
          supported_endpoint_types: ['openai'],
        },
        {
          id: 2,
          model_name: 'image-2',
          quota_type: 1,
          model_ratio: 1,
          completion_ratio: 1,
          model_price: 0.1,
          enable_groups: ['default'],
          supported_endpoint_types: ['image-generation'],
        },
      ],
      vendors: [],
      group_ratio: { default: 1 },
      usable_group: { default: { desc: 'Preview', ratio: 1 } },
      supported_endpoint: {},
      auto_groups: [],
    }
  }
  if (path === '/api/pricing/runtime') {
    return { success: true, data: { models: [] } }
  }
  if (path === '/api/token/') {
    const key = {
      id: 8001,
      name: '开发环境 · Preview',
      key: 'PREVIEW_NOT_A_SECRET',
      status: 1,
      remain_quota: 500000,
      used_quota: 125000,
      unlimited_quota: false,
      expired_time: -1,
      created_time: stamp - 86400,
      accessed_time: stamp,
      group: 'default',
      model_limits_enabled: false,
      model_limits: '',
      allow_ips: '',
      creation_source: 'manual',
    }
    return { success: true, data: { ...page, items: [key], total: 1 } }
  }
  if (path === '/api/user/sessions') {
    return {
      success: true,
      session_auto_logout: true,
      data: [
        {
          sid: 'preview-session',
          current: true,
          login_method: 'oauth',
          ip: '127.0.0.1',
          user_agent: 'Local browser preview',
          created_at: stamp - 3600,
          last_active_at: stamp,
          expires_at: stamp + 86400,
        },
      ],
    }
  }
  if (['/api/data/self', '/api/data', '/api/data/users'].includes(path)) {
    return {
      success: true,
      data: Array.from({ length: 7 }, (_, index) => ({
        id: index + 1,
        user_id: user?.id ?? 1002,
        username: 'preview',
        model_name: modelNames[index % 2],
        created_at: stamp - (6 - index) * 86400,
        token_used: 2400 + index * 380,
        count: 8 + index * 2,
        quota: 3000 + index * 80,
      })),
    }
  }
  if (['/api/data/flow/self', '/api/data/flow'].includes(path)) {
    return {
      success: true,
      data: [
        {
          user_id: user?.id ?? 1002,
          username: 'preview',
          node_name: 'local-preview',
          use_group: 'default',
          token_id: 1,
          token_name: 'Preview key',
          channel_id: 1,
          channel_name: 'Preview',
          model_name: modelNames[0],
          token_used: 3600,
          count: 12,
          quota: 600,
        },
      ],
    }
  }
  if (Object.hasOwn(reads, path)) {
    return { success: true, data: structuredClone(reads[path]) }
  }
  return undefined
}

export function withConsolePageFixtures(fallback: AxiosAdapter): AxiosAdapter {
  return async (config) => {
    const data = consolePageFixture(config)
    if (data === undefined) return fallback(config)
    return {
      data,
      status: 200,
      statusText: 'Local fixture',
      headers: {},
      config,
    }
  }
}
