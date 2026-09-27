/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { getAIDirectory } from '@/features/ai-directory/api'
import { getPricing } from '@/features/pricing/api'
import { estimateRequestCost } from '@/features/pricing/lib/request-estimate'
import type { PricingData, PricingModel } from '@/features/pricing/types'
import { getRankings } from '@/features/rankings/api'
import { claimRedPacket, getRedPacket } from '@/features/red-packets/api'
import { getSecurityPolicy, getSecurityStats } from '@/features/security/api'
import {
  getStatusDetectionMetrics,
  getStatusDetectionSummary,
} from '@/features/status-detection/api'
import { aggregateStatusGroups } from '@/features/status-detection/lib/aggregate'

import {
  clip,
  EMPTY_INPUT_SCHEMA,
  ensureNotAborted,
  ensureObject,
  optionalBoolean,
  optionalEnum,
  optionalString,
  requireSignedIn,
  type WebMcpToolFactory,
} from '../tool-kit'

const PERIODS = ['today', 'week', 'month', 'year'] as const
const DEFAULT_LIMIT = 20

function readLimit(input: Record<string, unknown>, fallback = DEFAULT_LIMIT) {
  const value = input.limit
  if (value === undefined || value === null) return fallback
  if (
    typeof value !== 'number' ||
    !Number.isInteger(value) ||
    value < 1 ||
    value > 50
  ) {
    throw new TypeError('limit must be an integer from 1 to 50')
  }
  return value
}

function readHours(input: Record<string, unknown>, fallback: number) {
  const value = input.hours
  if (value === undefined || value === null) return fallback
  if (
    typeof value !== 'number' ||
    !Number.isInteger(value) ||
    value < 1 ||
    value > 168
  ) {
    throw new TypeError('hours must be an integer from 1 to 168')
  }
  return value
}

function findModel(pricing: PricingData, name: string): PricingModel | null {
  const wanted = name.trim().toLowerCase()
  return (
    pricing.data.find((item) => item.model_name.toLowerCase() === wanted) ??
    pricing.data.find((item) =>
      item.model_name.toLowerCase().includes(wanted)
    ) ??
    null
  )
}

export const publicToolsTools: WebMcpToolFactory = ({ router }) => [
  {
    name: 'lmm_rankings_read',
    title: 'Read model and vendor usage rankings',
    description:
      'Read the public LMM usage leaderboard for a period: top models with token volume, share and growth, plus leading vendors. Read-only.',
    inputSchema: {
      type: 'object',
      properties: {
        period: { type: 'string', enum: PERIODS },
        limit: { type: 'integer', minimum: 1, maximum: 50 },
      },
      additionalProperties: false,
    },
    annotations: { readOnlyHint: true },
    execute: async (rawInput, options) => {
      const input = ensureObject(rawInput)
      const period = optionalEnum(input, 'period', PERIODS) ?? 'week'
      const limit = readLimit(input)
      ensureNotAborted(options.signal)
      const response = await getRankings(period)
      ensureNotAborted(options.signal)
      if (!response.success) {
        throw new Error(response.message || 'Rankings unavailable')
      }
      const data = response.data
      return {
        page: '/rankings',
        period,
        models: data.models.slice(0, limit).map((model) => ({
          rank: model.rank,
          model: model.model_name,
          vendor: model.vendor,
          category: model.category,
          total_tokens: model.total_tokens,
          share: Number(model.share.toFixed(4)),
          growth_pct: model.growth_pct,
        })),
        vendors: data.vendors.slice(0, limit).map((vendor) => ({
          rank: vendor.rank,
          vendor: vendor.vendor,
          total_tokens: vendor.total_tokens,
          share: Number(vendor.share.toFixed(4)),
          growth_pct: vendor.growth_pct,
          models_count: vendor.models_count,
          top_model: vendor.top_model,
        })),
      }
    },
  },
  {
    name: 'lmm_status_read',
    title: 'Read service and channel status',
    description:
      'Read the public service status summary: per group success rate, average time to first token and model coverage for a recent window. Read-only.',
    inputSchema: {
      type: 'object',
      properties: {
        hours: { type: 'integer', minimum: 1, maximum: 168 },
        limit: { type: 'integer', minimum: 1, maximum: 50 },
      },
      additionalProperties: false,
    },
    annotations: { readOnlyHint: true },
    execute: async (rawInput, options) => {
      const input = ensureObject(rawInput)
      const hours = readHours(input, 24)
      const limit = readLimit(input, 15)
      ensureNotAborted(options.signal)
      const summary = await getStatusDetectionSummary(hours)
      ensureNotAborted(options.signal)
      const modelNames = (summary.data.models ?? [])
        .map((model) => model.model_name.trim())
        .filter(Boolean)
        .slice(0, 24)
      const metrics =
        modelNames.length > 0
          ? await getStatusDetectionMetrics(modelNames, hours).catch(() => ({
              entries: [],
              failedModels: modelNames,
            }))
          : { entries: [], failedModels: [] }
      ensureNotAborted(options.signal)
      const groups = aggregateStatusGroups(metrics.entries)
      return {
        page: '/status',
        hours,
        group_count: groups.length,
        groups: groups.slice(0, limit).map((group) => ({
          group: group.group,
          models: group.modelCount,
          success_rate: Number.isFinite(group.successRate)
            ? Number(group.successRate.toFixed(4))
            : null,
          avg_ttft_ms: Number.isFinite(group.avgTtftMs)
            ? Math.round(group.avgTtftMs)
            : null,
          avg_tps: Number.isFinite(group.avgTps)
            ? Number(group.avgTps.toFixed(2))
            : null,
        })),
      }
    },
  },
  {
    name: 'lmm_ai_directory_search',
    title: 'Search the AI product directory',
    description:
      'Search the public AI product directory by keyword and category. Returns listed websites with their URLs and summaries. Read-only.',
    inputSchema: {
      type: 'object',
      properties: {
        query: { type: 'string', maxLength: 120 },
        category: { type: 'string', maxLength: 60 },
        limit: { type: 'integer', minimum: 1, maximum: 50 },
      },
      additionalProperties: false,
    },
    annotations: { readOnlyHint: true, untrustedContentHint: true },
    execute: async (rawInput, options) => {
      const input = ensureObject(rawInput)
      const query = optionalString(input, 'query', 120)?.toLowerCase()
      const category = optionalString(input, 'category', 60)?.toLowerCase()
      const limit = readLimit(input)
      ensureNotAborted(options.signal)
      const links = await getAIDirectory()
      ensureNotAborted(options.signal)
      const entries = links
        .filter(
          (entry) =>
            (!category || entry.category === category) &&
            (!query ||
              entry.name.toLowerCase().includes(query) ||
              entry.summary.toLowerCase().includes(query) ||
              entry.url.toLowerCase().includes(query))
        )
        .slice(0, limit)
        .map((entry) => ({
          name: entry.name,
          category: entry.category,
          url: entry.url,
          summary: clip(entry.summary, 200),
        }))
      return { page: '/ai-directory', match_count: entries.length, entries }
    },
  },
  {
    name: 'lmm_pricing_open_model',
    title: 'Open a model on the pricing page',
    description:
      'Open the public pricing page filtered to one model, so the user can inspect its card and cost estimator. Navigates the current tab.',
    inputSchema: {
      type: 'object',
      properties: { model: { type: 'string', maxLength: 128 } },
      required: ['model'],
      additionalProperties: false,
    },
    annotations: { readOnlyHint: true },
    execute: async (rawInput, options) => {
      const input = ensureObject(rawInput)
      const model = optionalString(input, 'model', 128)
      if (!model) throw new TypeError('model is required')
      ensureNotAborted(options.signal)
      await router.navigate({ to: '/pricing', search: { search: model } })
      return { page: '/pricing', model_query: model, navigated: true }
    },
  },
  {
    name: 'lmm_pricing_estimate',
    title: 'Estimate the cost of one request',
    description:
      'Estimate the platform credit cost of a single text request for one model, given input, output and cached token counts. Uses the same calculation as the pricing page estimator. Not a fiat checkout price.',
    inputSchema: {
      type: 'object',
      properties: {
        model: { type: 'string', maxLength: 128 },
        input_tokens: { type: 'integer', minimum: 0, maximum: 20000000 },
        output_tokens: { type: 'integer', minimum: 0, maximum: 20000000 },
        cached_tokens: { type: 'integer', minimum: 0, maximum: 20000000 },
        group: { type: 'string', maxLength: 60 },
      },
      required: ['model', 'input_tokens', 'output_tokens'],
      additionalProperties: false,
    },
    annotations: { readOnlyHint: true },
    execute: async (rawInput, options) => {
      const input = ensureObject(rawInput)
      const name = optionalString(input, 'model', 128)
      if (!name) throw new TypeError('model is required')
      const readTokens = (key: string, required: boolean) => {
        const value = input[key]
        if (value === undefined || value === null) {
          if (required) throw new TypeError(`${key} is required`)
          return 0
        }
        if (
          typeof value !== 'number' ||
          !Number.isInteger(value) ||
          value < 0 ||
          value > 20_000_000
        ) {
          throw new TypeError(`${key} must be an integer from 0 to 20000000`)
        }
        return value
      }
      const inputTokens = readTokens('input_tokens', true)
      const outputTokens = readTokens('output_tokens', true)
      const cachedTokens = readTokens('cached_tokens', false)
      if (cachedTokens > inputTokens) {
        throw new TypeError('cached_tokens cannot exceed input_tokens')
      }
      const group = optionalString(input, 'group', 60)
      ensureNotAborted(options.signal)
      const pricing = await getPricing()
      ensureNotAborted(options.signal)
      const model = findModel(pricing, name)
      if (!model) throw new Error(`Unknown model: ${clip(name, 80)}`)
      const groupRatio = pricing.group_ratio ?? {}
      const chosenGroup = group ?? pricing.auto_groups?.[0] ?? null
      const ratio =
        chosenGroup === null
          ? undefined
          : (groupRatio[chosenGroup] ?? model.group_ratio?.[chosenGroup] ?? 1)
      const amount = estimateRequestCost(
        model,
        ratio,
        inputTokens,
        outputTokens,
        cachedTokens
      )
      return {
        page: '/pricing',
        model: model.model_name,
        vendor: model.vendor_name ?? null,
        group: chosenGroup,
        group_multiplier: ratio ?? null,
        input_tokens: inputTokens,
        output_tokens: outputTokens,
        cached_tokens: cachedTokens,
        estimated_credit: amount === null ? null : Number(amount.toFixed(6)),
        estimable: amount !== null,
        billing_basis:
          'Platform credit; the pricing page shows the same figure.',
      }
    },
  },
  {
    name: 'lmm_red_packet_read',
    title: 'Read a public red packet',
    description:
      'Read the public description of a red packet by its slug: title, description, draw mode, remaining rewards, per-user limit and the exact time window. Read-only; opening it is a separate tool.',
    inputSchema: {
      type: 'object',
      properties: { slug: { type: 'string', maxLength: 128 } },
      required: ['slug'],
      additionalProperties: false,
    },
    annotations: { readOnlyHint: true, untrustedContentHint: true },
    execute: async (rawInput, options) => {
      const input = ensureObject(rawInput)
      const slug = optionalString(input, 'slug', 128)
      if (!slug) throw new TypeError('slug is required')
      ensureNotAborted(options.signal)
      const response = await getRedPacket(slug)
      ensureNotAborted(options.signal)
      if (!response.success || !response.data) {
        throw new Error(response.message || 'Red packet not found')
      }
      const packet = response.data
      return {
        page: `/red-packet/${packet.slug}`,
        slug: packet.slug,
        title: clip(packet.title, 160),
        description: clip(packet.description, 320),
        draw_mode: packet.draw_mode,
        enabled: packet.enabled,
        remaining_items: packet.remaining_items,
        total_items: packet.total_items,
        claim_count: packet.claim_count,
        per_user_limit: packet.per_user_limit,
        starts_at:
          packet.start_at > 0
            ? new Date(packet.start_at * 1000).toISOString()
            : null,
        ends_at:
          packet.end_at > 0
            ? new Date(packet.end_at * 1000).toISOString()
            : null,
        open:
          packet.enabled &&
          packet.remaining_items > 0 &&
          (packet.start_at <= 0 || Date.now() >= packet.start_at * 1000) &&
          (packet.end_at <= 0 || Date.now() < packet.end_at * 1000),
      }
    },
  },
  {
    name: 'lmm_red_packet_open',
    title: 'Open a red packet for the signed-in account',
    description:
      'Claim one draw from a public red packet for the signed-in account. Consumes a real draw against the per-user limit and cannot be undone. Requires confirm: true and a signed-in account; it never signs in or registers one.',
    inputSchema: {
      type: 'object',
      properties: {
        slug: { type: 'string', maxLength: 128 },
        confirm: { type: 'boolean' },
      },
      required: ['slug', 'confirm'],
      additionalProperties: false,
    },
    annotations: { consequentialHint: true, untrustedContentHint: true },
    execute: async (rawInput, options) => {
      const input = ensureObject(rawInput)
      const slug = optionalString(input, 'slug', 128)
      if (!slug) throw new TypeError('slug is required')
      if (optionalBoolean(input, 'confirm') !== true) {
        throw new Error(
          'Set confirm: true to open this red packet. It uses a real draw from your account.'
        )
      }
      ensureNotAborted(options.signal)
      const user = requireSignedIn()
      const response = await claimRedPacket(slug)
      ensureNotAborted(options.signal)
      if (!response.success || !response.data) {
        throw new Error(response.message || 'Red packet draw failed')
      }
      const reward = response.data
      return {
        page: `/red-packet/${slug}`,
        opened: true,
        account: user.username,
        reward: {
          type: reward.item_type,
          name: clip(reward.name, 160),
          code_masked: reward.code ? '••••' : null,
          quota: reward.quota ?? null,
          discount_percent: reward.discount_percent ?? null,
        },
        note: 'Open the red packet page to see the unwrap animation and copy the code.',
      }
    },
  },
  {
    name: 'lmm_security_read',
    title: 'Read the LMM security policy and statistics',
    description:
      'Read the public security summary: policy version, enforcement mode, protected groups, risk categories, rules and aggregate match statistics. Read-only and public.',
    inputSchema: EMPTY_INPUT_SCHEMA,
    annotations: { readOnlyHint: true },
    execute: async (_rawInput, options) => {
      ensureNotAborted(options.signal)
      const [policy, stats] = await Promise.all([
        getSecurityPolicy().catch(() => null),
        getSecurityStats().catch(() => null),
      ])
      ensureNotAborted(options.signal)
      if (!policy?.data && !stats?.data) {
        throw new Error('Security information unavailable')
      }
      return {
        page: '/security',
        policy: policy?.data
          ? {
              version: policy.data.policy_version,
              alignment: policy.data.alignment,
              enforcement_enabled: policy.data.enforcement?.enabled ?? false,
              enforcement_action: policy.data.enforcement?.action ?? null,
              protected_groups: (policy.data.protected_groups ?? [])
                .slice(0, 30)
                .map((group) => clip(group, 80)),
              risk_categories: (policy.data.risk_categories ?? [])
                .slice(0, 30)
                .map((category) => ({
                  id: category.id,
                  name: clip(category.name, 120),
                  severity: category.severity,
                })),
              rules: (policy.data.rules ?? []).slice(0, 30).map((rule) => ({
                id: rule.id,
                name: clip(rule.name, 120),
                severity: rule.severity,
              })),
            }
          : null,
        stats: stats?.data
          ? {
              total_matches: stats.data.total_matches,
              blocked_matches: stats.data.blocked_matches,
              audited_matches: stats.data.audited_matches,
              affected_requests: stats.data.affected_requests,
              affected_users: stats.data.affected_users,
            }
          : null,
      }
    },
  },
  {
    name: 'lmm_public_tools_navigate',
    title: 'Open a public tools page',
    description:
      'Open one of the public pages covered by this tool group: pricing, rankings, status, AI directory, security, scripts, WebMCP or the signal game. Navigates the current tab.',
    inputSchema: {
      type: 'object',
      properties: {
        page: {
          type: 'string',
          enum: [
            '/pricing',
            '/rankings',
            '/status',
            '/ai-directory',
            '/security',
            '/scripts',
            '/webmcp',
            '/games/signal',
            '/red-packets',
          ],
        },
      },
      required: ['page'],
      additionalProperties: false,
    },
    annotations: { readOnlyHint: true },
    execute: async (rawInput, options) => {
      const input = ensureObject(rawInput)
      const page = optionalEnum(input, 'page', [
        '/pricing',
        '/rankings',
        '/status',
        '/ai-directory',
        '/security',
        '/scripts',
        '/webmcp',
        '/games/signal',
        '/red-packets',
      ] as const)
      if (!page) throw new TypeError('page is required')
      ensureNotAborted(options.signal)
      await router.navigate({ to: page })
      return { page, navigated: true }
    },
  },
]
