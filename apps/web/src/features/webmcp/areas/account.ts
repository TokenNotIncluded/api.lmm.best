/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import {
  buildModelUsageQueryRanges,
  buildModelUsageReport,
  getModelUsageRange,
  type ModelUsageRow,
} from '@/features/profile/lib/model-usage'
import type {
  SelfSubscriptionData,
  PlanRecord,
} from '@/features/subscriptions/types'
import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'

import {
  clip,
  EMPTY_INPUT_SCHEMA,
  ensureNotAborted,
  ensureObject,
  optionalEnum,
  optionalInteger,
  optionalString,
  requireAdmin,
  requireSignedIn,
  type WebMcpToolFactory,
  type ModelContextTool,
} from '../tool-kit'

type RecordData = Record<string, unknown>
type Envelope<T> = { success: boolean; data?: T }

/** Only explicitly selected fields leave the account APIs. */
async function read<T>(
  path: string,
  signal: AbortSignal,
  params?: Record<string, unknown>
): Promise<T> {
  const accountId = requireSignedIn().id
  ensureNotAborted(signal)
  const response = await api.get<Envelope<T>>(path, { params, signal })
  ensureNotAborted(signal)
  if (requireSignedIn().id !== accountId) {
    throw new Error('The signed-in account changed. Retry this request.')
  }
  // Server error text can contain request content; keep tool failures generic.
  if (!response.data.success || response.data.data === undefined) {
    throw new Error('Account data could not be loaded. Retry this request.')
  }
  return response.data.data
}

function numeric(value: unknown) {
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

function safeSubscription(
  record: SelfSubscriptionData['subscriptions'][number]
) {
  const subscription = record.subscription
  return {
    id: subscription.id,
    plan_id: subscription.plan_id,
    status: clip(subscription.status, 32),
    start_time: subscription.start_time,
    end_time: subscription.end_time,
    amount_total: subscription.amount_total,
    amount_used: subscription.amount_used,
    next_reset_time: subscription.next_reset_time ?? null,
  }
}

const NAVIGATION = [
  ['profile', 'Open profile', '/profile', 'signed-in'],
  ['share', 'Open usage sharing', '/profile/share', 'signed-in'],
  ['wallet', 'Open wallet', '/wallet', 'signed-in'],
  ['common_logs', 'Open usage logs', '/usage-logs/common', 'signed-in'],
  ['drawing_logs', 'Open drawing logs', '/usage-logs/drawing', 'signed-in'],
  ['task_logs', 'Open task logs', '/usage-logs/task', 'signed-in'],
  ['subscriptions', 'Open subscription plans', '/subscriptions', 'admin'],
  [
    'subscription_reset',
    'Open subscription reset',
    '/subscriptions/reset',
    'root',
  ],
] as const

export const accountTools: WebMcpToolFactory = ({ router }) => [
  {
    name: 'lmm_account_get_profile',
    title: 'Read my profile',
    description:
      'Read the signed-in account name, role, group and usage counters. Passwords, access tokens, OAuth identifiers, notification configuration and security codes are excluded.',
    inputSchema: EMPTY_INPUT_SCHEMA,
    annotations: { readOnlyHint: true, untrustedContentHint: true },
    execute: async (_, { signal }) => {
      const data = await read<RecordData>('/api/user/self', signal)
      return {
        id: numeric(data.id),
        username: clip(data.username, 64),
        display_name: clip(data.display_name, 64),
        role: numeric(data.role),
        group: clip(data.group, 64),
        quota: numeric(data.quota),
        used_quota: numeric(data.used_quota),
        request_count: numeric(data.request_count),
        created_time: numeric(data.created_time),
      }
    },
  },
  {
    name: 'lmm_account_get_wallet',
    title: 'Read my wallet',
    description:
      'Read current account balance and whether top-up methods are available. Returns platform quota units and availability flags only. Does not quote, start or confirm a payment.',
    inputSchema: EMPTY_INPUT_SCHEMA,
    annotations: { readOnlyHint: true },
    execute: async (_, { signal }) => {
      const [profile, info] = await Promise.all([
        read<RecordData>('/api/user/self', signal),
        read<RecordData>('/api/user/topup/info', signal),
      ])
      return {
        quota: numeric(profile.quota),
        used_quota: numeric(profile.used_quota),
        request_count: numeric(profile.request_count),
        redemption_enabled: info.enable_redemption !== false,
        online_topup_enabled: info.enable_online_topup === true,
        stripe_enabled: info.enable_stripe_topup === true,
        creem_enabled: info.enable_creem_topup === true,
        waffo_enabled: info.enable_waffo_topup === true,
        waffo_pancake_enabled: info.enable_waffo_pancake_topup === true,
        path: '/wallet',
      }
    },
  },
  {
    name: 'lmm_account_get_subscriptions',
    title: 'Read my subscriptions',
    description:
      'Read the signed-in account subscription periods, quota and status. Billing preference is reported without changing it. No purchase, reset or redemption is performed.',
    inputSchema: EMPTY_INPUT_SCHEMA,
    annotations: { readOnlyHint: true },
    execute: async (_, { signal }) => {
      const data = await read<SelfSubscriptionData>(
        '/api/subscription/self',
        signal
      )
      return {
        billing_preference: clip(data.billing_preference, 64),
        subscriptions: (data.all_subscriptions ?? data.subscriptions ?? []).map(
          safeSubscription
        ),
      }
    },
  },
  {
    name: 'lmm_account_get_share_status',
    title: 'Read usage sharing status',
    description:
      'Check whether the signed-in account public usage badge is enabled. Public share tokens and image URLs are excluded. Open /profile/share to change or copy the badge.',
    inputSchema: EMPTY_INPUT_SCHEMA,
    annotations: { readOnlyHint: true },
    execute: async (_, { signal }) => {
      const data = await read<RecordData>(
        '/api/user/self/profile-share',
        signal
      )
      return { enabled: data.enabled === true, path: '/profile/share' }
    },
  },
  {
    name: 'lmm_account_get_model_usage',
    title: 'Read usage by model',
    description:
      'Read all per-model request, token and quota totals for 7, 30 or 365 days. Uses bounded, non-overlapping self-usage windows. Returns the actual timestamps and raw quota units, never API keys or prompts.',
    inputSchema: {
      type: 'object',
      properties: { range: { type: 'string', enum: ['7d', '30d', '365d'] } },
      additionalProperties: false,
    },
    annotations: { readOnlyHint: true, untrustedContentHint: true },
    execute: async (rawInput, { signal }) => {
      requireSignedIn()
      const input = ensureObject(rawInput)
      const range = getModelUsageRange(
        optionalEnum(input, 'range', ['7d', '30d', '365d']) ?? '30d'
      )
      const rows = await Promise.all(
        buildModelUsageQueryRanges(range).map((window) =>
          read<ModelUsageRow[]>('/api/data/self', signal, {
            ...window,
            default_time: 'day',
          })
        )
      )
      return { range, ...buildModelUsageReport(rows.flat()) }
    },
  },
  {
    name: 'lmm_account_list_usage_logs',
    title: 'Read my usage logs',
    description:
      'Read one page of the signed-in account common, drawing or task logs. Returns status, model and usage metadata only. Excludes prompts, messages, API keys, IP addresses and raw log details.',
    inputSchema: {
      type: 'object',
      properties: {
        category: { type: 'string', enum: ['common', 'drawing', 'task'] },
        page: { type: 'integer', minimum: 1, maximum: 10000 },
        page_size: { type: 'integer', minimum: 1, maximum: 50 },
        model: { type: 'string', maxLength: 128 },
      },
      additionalProperties: false,
    },
    annotations: { readOnlyHint: true, untrustedContentHint: true },
    execute: async (rawInput, { signal }) => {
      requireSignedIn()
      const input = ensureObject(rawInput)
      const category =
        optionalEnum(input, 'category', ['common', 'drawing', 'task']) ??
        'common'
      const page = optionalInteger(input, 'page', 1, 10000) ?? 1
      const pageSize = optionalInteger(input, 'page_size', 1, 50) ?? 20
      const model = optionalString(input, 'model', 128)
      const path =
        category === 'common'
          ? '/api/log/self'
          : category === 'drawing'
            ? '/api/mj/self'
            : '/api/task/self'
      const data = await read<{ items?: RecordData[]; total?: number }>(
        path,
        signal,
        {
          p: page,
          page_size: pageSize,
          ...(category === 'common' && model ? { model_name: model } : {}),
        }
      )
      return {
        category,
        page,
        total: numeric(data.total),
        items: (data.items ?? []).slice(0, pageSize).map((item) => ({
          id: numeric(item.id),
          created_at: numeric(item.created_at),
          model_name: clip(item.model_name, 128),
          status:
            typeof item.status === 'number'
              ? item.status
              : clip(item.status, 32),
          type: numeric(item.type),
          quota: numeric(item.quota),
          prompt_tokens: numeric(item.prompt_tokens),
          completion_tokens: numeric(item.completion_tokens),
          use_time: numeric(item.use_time),
        })),
      }
    },
  },
  {
    name: 'lmm_account_list_subscription_plans',
    title: 'Read subscription plans',
    description:
      'Admin-only read of subscription plan prices, periods and enabled status. Gateway product identifiers and credentials are excluded. No plan changes are available.',
    inputSchema: EMPTY_INPUT_SCHEMA,
    annotations: { readOnlyHint: true, untrustedContentHint: true },
    execute: async (_, { signal }) => {
      requireAdmin()
      const data = await read<PlanRecord[]>(
        '/api/subscription/admin/plans',
        signal,
        { include_archived: '1' }
      )
      return {
        plans: data.map(({ plan }) => ({
          id: plan.id,
          title: clip(plan.title, 128),
          price_amount: plan.price_amount,
          currency: clip(plan.currency, 8),
          duration_unit: plan.duration_unit,
          duration_value: plan.duration_value,
          total_amount: plan.total_amount,
          enabled: plan.enabled,
          archived_at: plan.archived_at ?? null,
        })),
      }
    },
  },
  ...NAVIGATION.map(
    ([name, title, path, permission]): ModelContextTool => ({
      name: `lmm_account_open_${name}`,
      title,
      description: `Open ${path}. ${permission === 'root' ? 'Requires a root administrator.' : permission === 'admin' ? 'Requires an administrator.' : 'Requires a signed-in account.'} Navigation only; no account, payment or subscription changes.`,
      inputSchema: EMPTY_INPUT_SCHEMA,
      annotations: { readOnlyHint: true },
      execute: async (_, { signal }) => {
        const user =
          permission === 'signed-in' ? requireSignedIn() : requireAdmin()
        if (permission === 'root' && user.role < ROLE.SUPER_ADMIN) {
          throw new Error('This page requires a root administrator account')
        }
        ensureNotAborted(signal)
        await router.navigate({ to: path })
        return { path }
      },
    })
  ),
]
