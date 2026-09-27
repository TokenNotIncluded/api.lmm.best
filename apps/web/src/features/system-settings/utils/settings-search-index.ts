/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import type { TFunction } from 'i18next'

import { SYSTEM_SETTINGS_VIEW } from '@/components/layout/config/system-settings.config'
import type { NavGroup } from '@/components/layout/types'

/**
 * One jumpable destination inside `/system-settings/*`.
 *
 * `group` is the admin section shown in the sidebar ("Billing & Payment") and
 * `section` is the page inside it ("Quota Settings"). Both are localized
 * strings, so the index has to be rebuilt when the interface language changes.
 */
export type SettingsSearchEntry = {
  group: string
  section: string
  url: string
  /** Extra search terms, lowercased. */
  keywords: string[]
}

export type SettingsSearchableGroup = SettingsSearchEntry & {
  /** Entries of the same admin section, for grouped rendering. */
  entries: SettingsSearchEntry[]
}

/**
 * Alternate terms an administrator is likely to type instead of the exact
 * label. Keyed by the stable `group/section` ids rather than the translated
 * titles so the index survives copy and translation changes.
 */
const SETTINGS_SYNONYMS: Record<string, readonly string[]> = {
  // Site & Branding
  'site/system-info': ['version', 'build', 'about', 'license'],
  'site/notice': ['banner', 'alert bar', 'maintenance notice'],
  'site/header-navigation': ['top nav', 'menu', 'links'],
  'site/sidebar-modules': ['modules', 'feature toggle', 'console modules'],
  'site/ai-directory': ['directory', 'listing', 'catalog'],
  'site/ai-directory-ads': ['ads', 'sponsored', 'advertising'],
  // Authentication
  'auth/basic-auth': ['login', 'signup', 'register', 'email verification'],
  'auth/oauth': ['github', 'google', 'sso', 'oidc', 'third party login'],
  'auth/passkey': ['webauthn', 'biometric', 'security key'],
  'auth/bot-protection': ['captcha', 'turnstile', 'spam', 'abuse'],
  'auth/custom-oauth': ['oidc', 'openid', 'identity provider', 'idp'],
  // Billing & Payment
  'billing/quota': ['credit', 'balance', 'new user gift', 'initial quota'],
  'billing/currency': [
    'exchange rate',
    'usd',
    'price display',
    'quota per unit',
  ],
  'billing/model-pricing': ['price', 'cost', 'per token', 'pricing'],
  'billing/group-pricing': ['rate', 'multiplier', 'group discount'],
  'billing/payment': [
    'stripe',
    'epay',
    'alipay',
    'wechat',
    'top up',
    'recharge',
  ],
  'billing/checkin': ['daily reward', 'check in', 'bonus'],
  // Models & Routing
  'models/global': ['model list', 'default model', 'global config'],
  'models/routing-reliability': ['failover', 'retry', 'fallback', 'timeout'],
  'models/gemini': ['google', 'vertex'],
  'models/claude': ['anthropic'],
  'models/grok': ['xai'],
  'models/channel-affinity': ['sticky', 'session', 'load balance'],
  'models/model-deployment': ['deploy', 'publish', 'endpoint'],
  // Security & Limits
  'security/rate-limit': ['throttle', 'rpm', 'tpm', 'request limit', 'burst'],
  'security/sensitive-words': [
    'blocklist',
    'filter',
    'moderation',
    'banned words',
  ],
  'security/advanced-security': ['hardening', 'csrf', 'security headers'],
  'security/ip-access-routing': ['geo', 'country', 'allowlist', 'denylist'],
  'security/anti-relay': ['proxy', 'relay', 'referer', 'origin check'],
  'security/ssrf': [
    'internal network',
    'url validation',
    'server side request forgery',
  ],
  'security/token-limits': ['max tokens', 'context window', 'output limit'],
  // Console Content
  'content/dashboard': ['stats', 'charts', 'metrics'],
  'content/announcements': ['broadcast', 'message', 'release notes'],
  'content/api-info': ['base url', 'endpoint', 'api url'],
  'content/faq': ['help', 'questions', 'docs'],
  'content/uptime-kuma': ['status page', 'monitoring status'],
  'content/assistant': ['ai', 'copilot', 'chatbot'],
  'content/chat': ['presets', 'chat models', 'playground'],
  'content/drawing': ['image', 'midjourney', 'generation'],
  // Operations
  'operations/behavior': ['system settings', 'general', 'defaults'],
  'operations/alerts': ['monitoring', 'notification', 'webhook', 'incident'],
  'operations/email': ['smtp', 'mailer', 'email delivery'],
  'operations/worker': ['worker proxy', 'egress proxy'],
  'operations/hero-sms': ['sms', 'activation', 'phone verification'],
  'operations/logs': ['log retention', 'cleanup', 'purge'],
  'operations/performance': ['cache', 'tuning', 'throughput'],
  'operations/raw-json': ['json', 'advanced', 'raw config'],
  'operations/update-checker': ['upgrade', 'version check', 'self update'],
  'operations/scripts': ['automation', 'custom code', 'tasks'],
}

function buildNavGroups(t: TFunction): NavGroup[] {
  return SYSTEM_SETTINGS_VIEW.getNavGroups(t)
}

function sectionIdFromUrl(url: string): string | null {
  const query = url.split('?')[1]
  if (query) return new URLSearchParams(query).get('section')
  const segments = url.split('/').filter(Boolean)
  return segments.length > 2 ? (segments.at(-1) ?? null) : null
}

function normalize(value: string) {
  return value.trim().toLowerCase()
}

/** Flatten the localized settings navigation into a searchable index. */
export function buildSettingsSearchIndex(
  t: TFunction,
  groups: NavGroup[] = buildNavGroups(t)
): SettingsSearchableGroup[] {
  return groups.flatMap((group) =>
    group.items.map((section) => {
      const entries: SettingsSearchEntry[] = (section.items ?? []).flatMap(
        (item) => {
          if (!item.url) return []
          const id = sectionIdFromUrl(item.url)
          const category = item.url.split('/')[2]?.split('?')[0]
          const synonyms =
            (id ? SETTINGS_SYNONYMS[`${category}/${id}`] : undefined) ?? []
          return [
            {
              group: section.title,
              section: item.title,
              url: item.url,
              keywords: [section.title, item.title, ...synonyms].map(normalize),
            },
          ]
        }
      )
      return {
        group: section.title,
        section: section.title,
        url: entries[0]?.url ?? '',
        keywords: [section.title].map(normalize),
        entries,
      }
    })
  )
}

function scoreEntry(entry: SettingsSearchEntry, query: string): number {
  const needle = normalize(query)
  if (!needle) return 1
  const section = normalize(entry.section)
  const group = normalize(entry.group)
  if (section.startsWith(needle)) return 100
  if (section.includes(needle)) return 70
  if (group.includes(needle)) return 50
  if (entry.keywords.some((keyword) => keyword.includes(needle))) return 30
  if (entry.url.toLowerCase().includes(needle)) return 20
  return 0
}

/**
 * Rank the index for a free-text query, best match first.
 *
 * An empty query returns the whole index so the palette doubles as a browsable
 * table of contents.
 */
export function searchSettings(
  index: SettingsSearchableGroup[],
  query: string
): SettingsSearchableGroup[] {
  if (!query.trim()) return index
  return index
    .map((group) => {
      const scored = group.entries
        .map((entry) => ({ entry, score: scoreEntry(entry, query) }))
        .filter((item) => item.score > 0)
        .sort((a, b) => b.score - a.score)
      return { group, scored }
    })
    .filter((item) => item.scored.length > 0)
    .sort(
      (a, b) =>
        (b.scored[0]?.score ?? 0) - (a.scored[0]?.score ?? 0) ||
        a.group.section.localeCompare(b.group.section)
    )
    .map((item) => ({
      ...item.group,
      entries: item.scored.map((entry) => entry.entry),
    }))
}

/** Total number of jumpable settings destinations. */
export function countSettingsEntries(index: SettingsSearchableGroup[]) {
  return index.reduce((total, group) => total + group.entries.length, 0)
}
