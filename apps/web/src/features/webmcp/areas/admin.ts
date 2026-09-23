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
import { t } from 'i18next'

/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import {
  getChannelOps,
  getChannels,
  searchChannels,
} from '@/features/channels/api'
import { CHANNEL_TYPES } from '@/features/channels/constants'
import { formatBalance } from '@/features/channels/lib/channel-utils'
import { getCompanyBillingProfile } from '@/features/company/api'
import { listDiscountCodes } from '@/features/discount-codes/api'
import { getDiscountCodeAvailability } from '@/features/discount-codes/availability'
import { listHeroSmsActivations } from '@/features/email-activations/api'
import { getHeroSmsStatusPresentation } from '@/features/email-activations/status-meta'
import {
  getRedemptions,
  searchRedemptions,
} from '@/features/redemption-codes/api'
import { listSystemInstances } from '@/features/system-info/api'
import { searchUsers } from '@/features/users/api'
import { USER_STATUSES } from '@/features/users/constants'
import { api } from '@/lib/api'
import { getRoleLabel } from '@/lib/roles'

import {
  clip,
  EMPTY_INPUT_SCHEMA,
  ensureNotAborted,
  ensureObject,
  type ModelContextTool,
  optionalEnum,
  optionalInteger,
  optionalString,
  requireAdmin,
  type WebMcpRouter,
  type WebMcpToolFactory,
} from '../tool-kit'

/** Result rows any admin tool may return, kept small for agent contexts. */
const MAX_ROWS = 25

const CHANNEL_STATUS_IDS = ['0', '1', '2', '3', 'all'] as const
const CHANNEL_STATUS_FILTERS = ['enabled', 'disabled', 'all'] as const
const USER_ROLE_FILTERS = ['1', '10', '100'] as const
const USER_STATUS_FILTERS = ['-1', '1', '2'] as const
const REDEMPTION_STATUS_FILTERS = ['1', '2', '3', 'expired'] as const
const DISCOUNT_STATUS_FILTERS = ['1', '2'] as const

type ChannelRow = {
  id: number
  name: string
  type: number
  type_label: string
  status: number
  status_label: string
  response_time_ms: number
  balance: number
  used_quota: number
  groups: string[]
  tag: string | null
  priority: number | null
  weight: number | null
  tested_at: number
  /** Count only; model names are not secrets but are large. */
  model_count: number
}

type ChannelLike = {
  id: number
  name: string
  type: number
  status: number
  response_time: number
  balance: number
  used_quota: number
  group: string
  tag?: string | null
  priority?: number | null
  weight?: number | null
  test_time: number
  models: string
}

/** Channel status id -> admin-facing label (mirrors CHANNEL_STATUS_CONFIG). */
const CHANNEL_STATUS_LABELS: Record<number, string> = {
  0: 'Unknown',
  1: 'Enabled',
  2: 'Disabled',
  3: 'Auto Disabled',
}

/** Redemption status id -> admin-facing label (mirrors REDEMPTION_STATUSES). */
const REDEMPTION_STATUS_LABELS: Record<number, string> = {
  1: 'Unused',
  2: 'Disabled',
  3: 'Used',
}

/** `a@b.com` -> `a•••@b.com`. Admin tools never return a usable address. */
export function maskAdminEmail(
  email: string | null | undefined
): string | null {
  if (!email) return null
  const at = email.indexOf('@')
  if (at <= 0) return `${email.slice(0, 1)}•••`
  const local = email.slice(0, at)
  const domain = email.slice(at)
  const head = local.slice(0, 1)
  return `${head}${'•'.repeat(Math.max(3, local.length - 1))}${domain}`
}

/**
 * Redemption and discount codes are never returned in full: only a short
 * non-reversible prefix plus the trailing suffix characters are useful for
 * telling two rows apart in a list, and neither reconstructs the code.
 */
export function maskAdminCode(code: string | null | undefined): string | null {
  if (!code) return null
  if (code.length <= 4) return '••••'
  return `${code.slice(0, 2)}••••${code.slice(-2)}`
}

function channelTypeLabel(type: number): string {
  return (
    (CHANNEL_TYPES as Record<number, string | undefined>)[type] ??
    `Type ${type}`
  )
}

function groupList(group: string | null | undefined): string[] {
  if (!group) return []
  return group
    .split(',')
    .map((entry) => entry.trim())
    .filter(Boolean)
    .slice(0, 8)
}

function modelCount(models: string | null | undefined): number {
  if (!models) return 0
  return models.split(',').filter((entry) => entry.trim()).length
}

/**
 * Reduce a channel record to the fields an agent needs. The upstream key, the
 * base URL (which may embed credentials in its userinfo), headers, mappings and
 * settings are intentionally omitted.
 */
export function toChannelRow(channel: ChannelLike): ChannelRow {
  return {
    id: channel.id,
    name: clip(channel.name, 96) ?? `Channel ${channel.id}`,
    type: channel.type,
    type_label: channelTypeLabel(channel.type),
    status: channel.status,
    status_label: CHANNEL_STATUS_LABELS[channel.status] ?? 'Unknown',
    response_time_ms: channel.response_time,
    balance: channel.balance,
    used_quota: channel.used_quota,
    groups: groupList(channel.group),
    tag: channel.tag ?? null,
    priority: channel.priority ?? null,
    weight: channel.weight ?? null,
    tested_at: channel.test_time,
    model_count: modelCount(channel.models),
  }
}

type ChannelHealth = {
  sampled: number
  total: number
  enabled: number
  manual_disabled: number
  auto_disabled: number
  unknown_status: number
  never_tested: number
  slow_over_5s: number
  zero_balance: number
  total_balance: number
  average_response_time_ms: number | null
  type_counts: Record<string, number>
  slowest: { id: number; name: string; response_time_ms: number }[]
}

export function summarizeChannelHealth(
  rows: ChannelRow[],
  total: number,
  typeCounts: Record<string, number> | undefined,
  retryTimes?: number
): ChannelHealth & { retry_times?: number } {
  const tested = rows.filter((row) => row.response_time_ms > 0)
  const summary: ChannelHealth = {
    sampled: rows.length,
    total,
    enabled: rows.filter((row) => row.status === 1).length,
    manual_disabled: rows.filter((row) => row.status === 2).length,
    auto_disabled: rows.filter((row) => row.status === 3).length,
    unknown_status: rows.filter((row) => row.status === 0).length,
    never_tested: rows.filter((row) => row.response_time_ms === 0).length,
    slow_over_5s: tested.filter((row) => row.response_time_ms > 5000).length,
    zero_balance: rows.filter((row) => row.balance <= 0).length,
    total_balance: Number(
      rows.reduce((sum, row) => sum + row.balance, 0).toFixed(4)
    ),
    average_response_time_ms: tested.length
      ? Math.round(
          tested.reduce((sum, row) => sum + row.response_time_ms, 0) /
            tested.length
        )
      : null,
    type_counts: typeCounts ?? {},
    slowest: [...tested]
      .sort((a, b) => b.response_time_ms - a.response_time_ms)
      .slice(0, 5)
      .map((row) => ({
        id: row.id,
        name: row.name,
        response_time_ms: row.response_time_ms,
      })),
  }
  return retryTimes === undefined
    ? summary
    : { ...summary, retry_times: retryTimes }
}

/** Pages the admin area may navigate to, with the filters each page accepts. */
const ADMIN_PAGES = {
  '/channels': 'Upstream channel management',
  '/users': 'User management',
  '/redemption-codes': 'Redemption codes',
  '/discount-codes': 'Discount codes',
  '/temporary-activations': 'Email and temporary activations',
  '/chat-management': 'Chat presets and assistant history',
  '/company': 'Company billing profile',
  '/operations/sources': 'Acquisition sources',
  '/system-info': 'System information',
  '/system-settings': 'System settings',
} as const

type AdminPage = keyof typeof ADMIN_PAGES

/**
 * Build the `search` payload for a page. Every value is validated here rather
 * than trusted from the caller, because a bad value would be rejected by the
 * route's zod schema and silently reset the filter.
 */
function buildSearch(
  page: AdminPage,
  input: Record<string, unknown>
): Record<string, unknown> | undefined {
  const keyword = optionalString(input, 'keyword', 128)
  const status = optionalString(input, 'status', 32)
  const group = optionalString(input, 'group', 64)
  const type = optionalInteger(input, 'type', 0, 999)
  const model = optionalString(input, 'model', 128)
  const pageNumber = optionalInteger(input, 'page_number', 1, 10_000)

  switch (page) {
    case '/channels': {
      return {
        ...(keyword ? { filter: keyword } : {}),
        ...(status && status !== 'all' ? { status: [status] } : {}),
        ...(type === undefined ? {} : { type: [String(type)] }),
        ...(group ? { group: [group] } : {}),
        ...(model ? { model } : {}),
        ...(pageNumber === undefined ? {} : { page: pageNumber }),
      }
    }
    case '/users': {
      return {
        ...(keyword ? { filter: keyword } : {}),
        ...(status && status !== 'all' ? { status: [status] } : {}),
        ...(group ? { group } : {}),
        ...(pageNumber === undefined ? {} : { page: pageNumber }),
      }
    }
    case '/redemption-codes': {
      return {
        ...(keyword ? { filter: keyword } : {}),
        ...(status && status !== 'all' ? { status: [status] } : {}),
        ...(pageNumber === undefined ? {} : { page: pageNumber }),
      }
    }
    case '/temporary-activations': {
      return {
        ...(status ? { status } : {}),
        ...(pageNumber === undefined ? {} : { page: pageNumber }),
      }
    }
    default: {
      const search: Record<string, unknown> = {}
      if (keyword && page === '/discount-codes') search.filter = keyword
      return Object.keys(search).length ? search : undefined
    }
  }
}

const ADMIN_PAGE_CASES: readonly [AdminPage, ...AdminPage[]] = [
  '/channels',
  '/users',
  '/redemption-codes',
  '/discount-codes',
  '/temporary-activations',
  '/chat-management',
  '/company',
  '/operations/sources',
  '/system-info',
  '/system-settings',
]

function navigationTool(router: WebMcpRouter): ModelContextTool {
  const properties: Record<string, unknown> = {
    page: {
      type: 'string',
      enum: [...ADMIN_PAGE_CASES],
      description: 'Which admin page to open.',
    },
    page_number: {
      type: 'integer',
      minimum: 1,
      maximum: 10_000,
      description: 'List page to open on that screen.',
    },
    keyword: { type: 'string', maxLength: 128 },
    status: { type: 'string', maxLength: 32 },
    group: { type: 'string', maxLength: 64 },
    model: { type: 'string', maxLength: 128 },
    type: { type: 'integer', minimum: 0, maximum: 999 },
  }
  return {
    name: 'lmm_admin_open',
    title: 'Open an admin page with filters',
    description:
      'Open one of the administrator pages and pre-filter its list. Requires an administrator account. Only channel, user, redemption-code, and temporary-activation lists honour filters; other pages just open. Accepted filters: keyword (free text search), status (channels: enabled/disabled/all, users: -1/1/2 or all, redemptions: 1/2/3/expired or all, activations: any status name), group (channel or user group name), model (channel model ID), type (channel type ID), page_number (list page number).',
    inputSchema: {
      type: 'object',
      properties,
      required: ['page'],
      additionalProperties: false,
    },
    annotations: { readOnlyHint: true },
    execute: async (rawInput, options) => {
      requireAdmin()
      const input = ensureObject(rawInput)
      const page = optionalEnum(input, 'page', ADMIN_PAGE_CASES)
      if (!page) throw new TypeError('page is required')
      ensureNotAborted(options.signal)
      const search = buildSearch(page, input)
      await router.navigate(search ? { to: page, search } : { to: page })
      return { page, filters: search ?? null, navigated: true }
    },
  }
}

export const adminTools: WebMcpToolFactory = ({ router }) => [
  {
    name: 'lmm_admin_channels_list',
    title: 'List upstream channels',
    description:
      'Administrator only. List upstream channels with name, type, status, response time, and balance. Never returns the channel key, base URL, headers, or any credential. Use keyword to search, status to filter by enabled/disabled, and type to filter by channel type ID. At most 25 rows per call.',
    inputSchema: {
      type: 'object',
      properties: {
        keyword: { type: 'string', maxLength: 128 },
        status: { type: 'string', enum: [...CHANNEL_STATUS_FILTERS] },
        status_id: { type: 'string', enum: [...CHANNEL_STATUS_IDS] },
        type: { type: 'integer', minimum: 0, maximum: 999 },
        group: { type: 'string', maxLength: 64 },
        model: { type: 'string', maxLength: 128 },
        sort_by: {
          type: 'string',
          enum: [
            'id',
            'name',
            'priority',
            'balance',
            'response_time',
            'test_time',
          ],
        },
        sort_order: { type: 'string', enum: ['asc', 'desc'] },
        page: { type: 'integer', minimum: 1, maximum: 1000 },
      },
      additionalProperties: false,
    },
    annotations: { readOnlyHint: true, untrustedContentHint: true },
    execute: async (rawInput, options) => {
      requireAdmin()
      const input = ensureObject(rawInput)
      const keyword = optionalString(input, 'keyword', 128)
      const statusFilter =
        optionalEnum(input, 'status', CHANNEL_STATUS_FILTERS) ?? 'all'
      const statusId = optionalEnum(input, 'status_id', CHANNEL_STATUS_IDS)
      const type = optionalInteger(input, 'type', 0, 999)
      const group = optionalString(input, 'group', 64)
      const model = optionalString(input, 'model', 128)
      const sortBy = optionalEnum(input, 'sort_by', [
        'id',
        'name',
        'priority',
        'balance',
        'response_time',
        'test_time',
      ] as const)
      const sortOrder = optionalEnum(input, 'sort_order', [
        'asc',
        'desc',
      ] as const)
      const page = optionalInteger(input, 'page', 1, 1000) ?? 1

      // A numeric status filter (0/1/2/3) narrows further than the
      // enabled/disabled switch the UI exposes, so it is sent as `status_id`.
      const status = statusId && statusId !== 'all' ? statusId : statusFilter

      ensureNotAborted(options.signal)
      const response =
        keyword || model
          ? await searchChannels({
              keyword: keyword ?? '',
              status,
              type,
              group,
              model,
              sort_by: sortBy,
              sort_order: sortOrder,
              p: page,
              page_size: MAX_ROWS,
            })
          : await getChannels({
              status,
              type,
              group,
              sort_by: sortBy,
              sort_order: sortOrder,
              p: page,
              page_size: MAX_ROWS,
            })
      ensureNotAborted(options.signal)

      const items = (response.data?.items ?? []) as unknown as ChannelLike[]
      const rows = items.slice(0, MAX_ROWS).map(toChannelRow)
      return {
        total: response.data?.total ?? rows.length,
        page,
        count: rows.length,
        type_counts: response.data?.type_counts ?? {},
        channels: rows.map((row) => ({
          ...row,
          balance_display: formatBalance(row.balance),
        })),
      }
    },
  },
  {
    name: 'lmm_admin_channels_health',
    title: 'Summarize channel health',
    description:
      'Administrator only. Summarize upstream channel health: enabled/disabled/auto-disabled counts, never-tested channels, channels slower than 5 seconds, zero-balance channels, total and average response time, per-type counts, and the upstream retry budget. Counts cover the sampled page, not the whole table — read total for the full size.',
    inputSchema: {
      type: 'object',
      properties: {
        status: { type: 'string', enum: [...CHANNEL_STATUS_FILTERS] },
        sample: { type: 'integer', minimum: 5, maximum: 25 },
      },
      additionalProperties: false,
    },
    annotations: { readOnlyHint: true, untrustedContentHint: true },
    execute: async (rawInput, options) => {
      requireAdmin()
      const input = ensureObject(rawInput)
      const status =
        optionalEnum(input, 'status', CHANNEL_STATUS_FILTERS) ?? 'all'
      const sample = optionalInteger(input, 'sample', 5, MAX_ROWS) ?? MAX_ROWS

      ensureNotAborted(options.signal)
      const [list, ops] = await Promise.all([
        getChannels({ status, p: 1, page_size: sample }),
        getChannelOps(),
      ])
      ensureNotAborted(options.signal)

      const items = (list.data?.items ?? []) as unknown as ChannelLike[]
      const rows = items.slice(0, sample).map(toChannelRow)
      const health = summarizeChannelHealth(
        rows,
        list.data?.total ?? rows.length,
        list.data?.type_counts,
        ops.data?.retry_times
      )
      return { ...health, status_filter: status }
    },
  },
  {
    name: 'lmm_admin_users_search',
    title: 'Search users',
    description:
      'Administrator only. Search accounts by keyword, group, role, or status. Returns id, username, display name, role, status, group, quota, used quota, and a masked email. No other personal data is returned and full email addresses are never returned.',
    inputSchema: {
      type: 'object',
      properties: {
        keyword: { type: 'string', maxLength: 128 },
        group: { type: 'string', maxLength: 64 },
        role: { type: 'string', enum: [...USER_ROLE_FILTERS] },
        status: { type: 'string', enum: [...USER_STATUS_FILTERS] },
        page: { type: 'integer', minimum: 1, maximum: 1000 },
      },
      additionalProperties: false,
    },
    annotations: { readOnlyHint: true, untrustedContentHint: true },
    execute: async (rawInput, options) => {
      requireAdmin()
      const input = ensureObject(rawInput)
      const keyword = optionalString(input, 'keyword', 128) ?? ''
      const group = optionalString(input, 'group', 64) ?? ''
      const role = optionalEnum(input, 'role', USER_ROLE_FILTERS) ?? ''
      const status = optionalEnum(input, 'status', USER_STATUS_FILTERS) ?? ''
      const page = optionalInteger(input, 'page', 1, 1000) ?? 1

      ensureNotAborted(options.signal)
      const response = await searchUsers({
        keyword,
        group,
        role,
        status,
        p: page,
        page_size: MAX_ROWS,
      })
      ensureNotAborted(options.signal)

      const items = response.data?.items ?? []
      return {
        total: response.data?.total ?? items.length,
        page,
        count: Math.min(items.length, MAX_ROWS),
        users: items.slice(0, MAX_ROWS).map((user) => ({
          id: user.id,
          username: clip(user.username, 64),
          display_name: clip(user.display_name, 64),
          role: user.role,
          role_label: getRoleLabel(user.role),
          status: user.status,
          status_label:
            USER_STATUSES[user.status as keyof typeof USER_STATUSES]
              ?.labelKey ?? 'Unknown',
          group: clip(user.group, 48),
          quota: user.quota,
          used_quota: user.used_quota,
          request_count: user.request_count,
          email_masked: maskAdminEmail(user.email),
        })),
      }
    },
  },
  {
    name: 'lmm_admin_redemptions_list',
    title: 'List redemption codes',
    description:
      'Administrator only. List redemption codes with name, status, quota, and used-by information. The code itself is never returned in plaintext — only a masked placeholder. Use keyword to search and status to filter.',
    inputSchema: {
      type: 'object',
      properties: {
        keyword: { type: 'string', maxLength: 128 },
        status: { type: 'string', enum: [...REDEMPTION_STATUS_FILTERS] },
        page: { type: 'integer', minimum: 1, maximum: 1000 },
      },
      additionalProperties: false,
    },
    annotations: { readOnlyHint: true, untrustedContentHint: true },
    execute: async (rawInput, options) => {
      requireAdmin()
      const input = ensureObject(rawInput)
      const keyword = optionalString(input, 'keyword', 128) ?? ''
      const status =
        optionalEnum(input, 'status', REDEMPTION_STATUS_FILTERS) ?? ''
      const page = optionalInteger(input, 'page', 1, 1000) ?? 1

      ensureNotAborted(options.signal)
      const response =
        keyword || status
          ? await searchRedemptions({
              keyword,
              status,
              p: page,
              page_size: MAX_ROWS,
            })
          : await getRedemptions({ p: page, page_size: MAX_ROWS })
      ensureNotAborted(options.signal)

      const items = response.data?.items ?? []
      return {
        total: response.data?.total ?? items.length,
        page,
        count: Math.min(items.length, MAX_ROWS),
        codes: items.slice(0, MAX_ROWS).map((code) => ({
          id: code.id,
          name: clip(code.name, 64),
          // Masked: the plaintext redemption code is never exposed to agents.
          code_masked: maskAdminCode(code.key),
          status: code.status,
          status_label: REDEMPTION_STATUS_LABELS[code.status] ?? 'Unknown',
          quota: code.quota,
          reward_type: code.reward_type ?? null,
          used_user_id: code.used_user_id || null,
          created_time: code.created_time,
          redeemed_time: code.redeemed_time,
          expired_time: code.expired_time,
        })),
      }
    },
  },
  {
    name: 'lmm_admin_discount_codes_list',
    title: 'List discount codes',
    description:
      'Administrator only. List discount codes with name, discount percent, minimum amount, usage counts, and availability. The code string is masked; share links are not generated.',
    inputSchema: {
      type: 'object',
      properties: {
        keyword: { type: 'string', maxLength: 128 },
        status: { type: 'string', enum: [...DISCOUNT_STATUS_FILTERS] },
        page: { type: 'integer', minimum: 1, maximum: 1000 },
      },
      additionalProperties: false,
    },
    annotations: { readOnlyHint: true, untrustedContentHint: true },
    execute: async (rawInput, options) => {
      requireAdmin()
      const input = ensureObject(rawInput)
      const keyword = optionalString(input, 'keyword', 128)
      const status =
        optionalEnum(input, 'status', DISCOUNT_STATUS_FILTERS) ?? ''
      const page = optionalInteger(input, 'page', 1, 1000) ?? 1

      ensureNotAborted(options.signal)
      const response = await listDiscountCodes({
        page,
        pageSize: MAX_ROWS,
        keyword,
        status,
      })
      ensureNotAborted(options.signal)

      const items = response.data?.items ?? []
      return {
        total: response.data?.total ?? items.length,
        page,
        count: Math.min(items.length, MAX_ROWS),
        codes: items.slice(0, MAX_ROWS).map((code) => ({
          id: code.id,
          name: clip(code.name, 64),
          code_masked: maskAdminCode(code.code),
          discount_percent: code.discount_percent,
          min_amount: code.min_amount,
          status: code.status,
          status_label: code.status === 1 ? 'Enabled' : 'Disabled',
          availability: getDiscountCodeAvailability(code),
          used_count: code.used_count,
          /** 0 is the server's "no cap" sentinel. */
          max_uses: code.max_uses,
          starts_time: code.starts_time,
          expired_time: code.expired_time,
        })),
      }
    },
  },
  {
    name: 'lmm_admin_activations_list',
    title: 'List temporary activations',
    description:
      'Administrator only. List email and temporary (SMS) activations with site, domain, status, and charged quota. The mailbox, the received verification code, and message contents are masked or omitted — only a masked address is returned.',
    inputSchema: {
      type: 'object',
      properties: {
        status: { type: 'string', maxLength: 32 },
        page: { type: 'integer', minimum: 1, maximum: 1000 },
        size: { type: 'integer', minimum: 1, maximum: 25 },
      },
      additionalProperties: false,
    },
    annotations: { readOnlyHint: true, untrustedContentHint: true },
    execute: async (rawInput, options) => {
      requireAdmin()
      const input = ensureObject(rawInput)
      const status = optionalString(input, 'status', 32)
      const page = optionalInteger(input, 'page', 1, 1000) ?? 1
      const size = optionalInteger(input, 'size', 1, MAX_ROWS) ?? MAX_ROWS

      ensureNotAborted(options.signal)
      const result = await listHeroSmsActivations({ page, size, status })
      ensureNotAborted(options.signal)

      return {
        total: result.total,
        page: result.page,
        count: Math.min(result.items.length, MAX_ROWS),
        activations: result.items.slice(0, MAX_ROWS).map((activation) => {
          const presentation = getHeroSmsStatusPresentation(
            activation.status,
            t
          )
          return {
            id: clip(activation.id, 32),
            order_id: clip(activation.order_id, 32),
            site: clip(activation.site, 48),
            domain: clip(activation.domain, 64),
            /** Masked: a usable mailbox is never returned to an agent. */
            email_masked: maskAdminEmail(activation.email),
            // `code` and `message` hold the provider's verification code and
            // message body, so they are dropped entirely.
            has_code: Boolean(activation.code),
            status: activation.status,
            status_label: presentation ? presentation.label : activation.status,
            charge_quota: activation.charge_quota,
            cancel_reason: clip(activation.cancel_reason, 120),
            created_at: clip(activation.created_at, 32),
            updated_at: clip(activation.updated_at, 32),
          }
        }),
      }
    },
  },
  {
    name: 'lmm_admin_acquisition_summary',
    title: 'Summarize acquisition sources',
    description:
      'Administrator only. Summarize the acquisition report for a time range: per-source registrations and identified registrations, plus activity summary counts. Payment amounts are not returned. Defaults to the last 30 days; the range may not exceed 366 days.',
    inputSchema: {
      type: 'object',
      properties: {
        from: {
          type: 'integer',
          minimum: 0,
          description: 'Unix seconds, inclusive. Defaults to 30 days ago.',
        },
        to: {
          type: 'integer',
          minimum: 0,
          description: 'Unix seconds, inclusive. Defaults to now.',
        },
      },
      additionalProperties: false,
    },
    annotations: { readOnlyHint: true, untrustedContentHint: true },
    execute: async (rawInput, options) => {
      requireAdmin()
      const input = ensureObject(rawInput)
      const now = Math.floor(Date.now() / 1000)
      const to = optionalInteger(input, 'to', 0, 4_102_444_800) ?? now
      const from =
        optionalInteger(input, 'from', 0, 4_102_444_800) ?? to - 30 * 86_400
      if (from > to) throw new TypeError('from must not be after to')
      if (to - from > 366 * 86_400) {
        throw new TypeError('Range must not exceed 366 days')
      }

      ensureNotAborted(options.signal)
      const response = await api.get(
        `/api/admin/acquisition/report?from=${from}&to=${to}`
      )
      ensureNotAborted(options.signal)

      const payload = response.data as {
        success?: boolean
        message?: string
        data?: {
          channels?: {
            source: string
            evidence: string
            registrations: number
            identified_registrations: number
          }[]
          activity?: { source: string; [key: string]: unknown }[]
          unclassified_payment_rows?: number
          lookback_days?: number
          applied_lookback_days?: number[]
        }
      }
      if (payload.success === false) {
        throw new Error(payload.message || 'Acquisition report failed')
      }
      const report = payload.data
      const channels = (report?.channels ?? []).slice(0, MAX_ROWS)
      return {
        from,
        to,
        lookback_days: report?.lookback_days ?? null,
        applied_lookback_days: report?.applied_lookback_days ?? [],
        unclassified_payment_rows: report?.unclassified_payment_rows ?? 0,
        source_count: report?.channels?.length ?? 0,
        total_registrations: (report?.channels ?? []).reduce(
          (sum, row) => sum + (row.registrations ?? 0),
          0
        ),
        sources: channels.map((row) => ({
          source: clip(row.source, 64),
          evidence: clip(row.evidence, 48),
          registrations: row.registrations,
          identified_registrations: row.identified_registrations,
        })),
        activity_count: report?.activity?.length ?? 0,
      }
    },
  },
  {
    name: 'lmm_admin_system_info',
    title: 'Read system instance info',
    description:
      'Administrator only. List backend instances with node name, status (online or stale), runtime version, platform, started-at time, and CPU, memory, and storage usage percentages. Hostnames are masked.',
    inputSchema: EMPTY_INPUT_SCHEMA,
    annotations: { readOnlyHint: true, untrustedContentHint: true },
    execute: async (_rawInput, options) => {
      requireAdmin()
      ensureNotAborted(options.signal)
      const response = await listSystemInstances()
      ensureNotAborted(options.signal)

      const instances = (response.data ?? []).slice(0, MAX_ROWS)
      return {
        total: response.data?.length ?? instances.length,
        online: instances.filter((instance) => instance.status === 'online')
          .length,
        stale: instances.filter((instance) => instance.status === 'stale')
          .length,
        instances: instances.map((instance) => {
          const info = instance.info ?? {}
          return {
            node_name: clip(instance.node_name, 64),
            reporter_id: clip(instance.reporter_id, 64),
            instance_slot: clip(instance.instance_slot, 32),
            status: instance.status,
            stale_after_seconds: instance.stale_after_seconds,
            started_at: instance.started_at,
            last_seen_at: instance.last_seen_at,
            uptime_seconds:
              instance.started_at > 0
                ? Math.max(
                    0,
                    Math.floor(Date.now() / 1000) - instance.started_at
                  )
                : null,
            version: clip(info.runtime?.version, 32),
            os: clip(info.runtime?.goos, 16),
            arch: clip(info.runtime?.goarch, 16),
            is_master: info.role?.is_master === true,
            cpu_usage_percent: info.resources?.cpu?.usage_percent ?? null,
            memory_usage_percent: info.resources?.memory?.usage_percent ?? null,
            storage_usage_percent:
              info.resources?.storage?.used_percent ?? null,
            // Hostnames are internal infrastructure identifiers: masked.
            hostname_masked: maskAdminEmail(info.host?.hostname ?? null),
          }
        }),
      }
    },
  },
  {
    name: 'lmm_admin_company_profile',
    title: 'Read the company billing profile',
    description:
      'Administrator only. Read the company billing profile used on invoices: country, business flag, business name, postcode, state, and whether it is used for invoices. The tax ID is masked. Read-only — updating the profile is not offered to agents.',
    inputSchema: EMPTY_INPUT_SCHEMA,
    annotations: { readOnlyHint: true, untrustedContentHint: true },
    execute: async (_rawInput, options) => {
      requireAdmin()
      ensureNotAborted(options.signal)
      const profile = await getCompanyBillingProfile(options.signal)
      ensureNotAborted(options.signal)
      if (!profile) return { configured: false }
      return {
        configured: true,
        country: clip(profile.country, 48),
        is_business: profile.isBusiness,
        business_name: clip(profile.businessName, 96),
        postcode: clip(profile.postcode, 32),
        state: clip(profile.state, 64),
        use_for_invoices: profile.useForInvoices,
        // Masked: a tax identifier is a sensitive business credential.
        tax_id_masked: maskAdminCode(profile.taxId),
        created_at: profile.createdAt,
        updated_at: profile.updatedAt,
      }
    },
  },
  navigationTool(router),
]
