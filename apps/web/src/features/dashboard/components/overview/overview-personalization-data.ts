/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import type { QuotaDataItem } from '@/features/dashboard/types'

export const GREETING_LANGUAGES = [
  'en',
  'zh',
  'zh-TW',
  'fr',
  'ja',
  'ru',
  'vi',
] as const
export const GREETING_VARIABLES = [
  '$name',
  '$time',
  '$date',
  '$weekday',
  '$level',
  '$site',
  '$balance',
  '$$',
] as const
export type GreetingLanguage = (typeof GREETING_LANGUAGES)[number]
export type OverviewGreetingPreference = {
  templates: Partial<Record<GreetingLanguage, string>>
  revision: number
  updated_at: number
}

export function overviewLanguage(language: string): GreetingLanguage {
  const normalized = language.replaceAll('_', '-').toLowerCase()
  if (/^zh-(tw|hk|mo|hant)(-|$)/.test(normalized)) return 'zh-TW'
  const base = normalized.split('-')[0]
  return GREETING_LANGUAGES.includes(base as GreetingLanguage)
    ? (base as GreetingLanguage)
    : 'en'
}
export function validGreetingTemplate(template: string): boolean {
  return (
    [...template].length <= 512 &&
    !template.includes('\0') &&
    (template.match(/\$\$|\$[A-Za-z_][A-Za-z0-9_]*/g) ?? []).every((variable) =>
      GREETING_VARIABLES.includes(
        variable as (typeof GREETING_VARIABLES)[number]
      )
    )
  )
}
// Literal substitution only: neither templates nor variable values are evaluated.
// Replacement values are not parsed again, so names containing "$time" stay names.
export function expandOverviewGreeting(
  template: string,
  values: Record<string, string>
): string {
  return template.replaceAll(/\$\$|\$[A-Za-z_][A-Za-z0-9_]*/g, (key) =>
    key === '$$' ? '$' : (values[key] ?? key)
  )
}
export function parseOverviewPreference(
  value: unknown
): OverviewGreetingPreference {
  if (!value || typeof value !== 'object') {
    throw new Error('Invalid greeting response')
  }
  const row = value as Record<string, unknown>
  if (
    !Number.isSafeInteger(row.revision) ||
    Number(row.revision) < 0 ||
    !Number.isSafeInteger(row.updated_at) ||
    !row.templates ||
    typeof row.templates !== 'object' ||
    Array.isArray(row.templates)
  ) {
    throw new Error('Invalid greeting response')
  }
  for (const [language, template] of Object.entries(row.templates)) {
    if (
      !GREETING_LANGUAGES.includes(language as GreetingLanguage) ||
      typeof template !== 'string' ||
      !validGreetingTemplate(template)
    ) {
      throw new Error('Invalid greeting response')
    }
  }
  return {
    templates: row.templates as OverviewGreetingPreference['templates'],
    revision: Number(row.revision),
    updated_at: Number(row.updated_at),
  }
}

export type OverviewMetric = 'requests' | 'tokens' | 'quota'
const metricFields = {
  requests: 'count',
  tokens: 'token_used',
  quota: 'quota',
} as const
function localDay(date: Date): string {
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
}
export function overviewUsageRange(days: number, now = new Date()) {
  const start = new Date(now)
  start.setDate(start.getDate() - days + 1)
  start.setHours(0, 0, 0, 0)
  return {
    start_timestamp: Math.floor(start.getTime() / 1000),
    end_timestamp: Math.floor(now.getTime() / 1000),
  }
}
export function buildOverviewUsage(
  rows: QuotaDataItem[],
  metric: OverviewMetric,
  start: number,
  end: number,
  locale: string,
  otherLabel: string,
  unknownLabel: string
) {
  const days = new Map<string, { label: string; value: number }>()
  const cursor = new Date(start * 1000)
  cursor.setHours(0, 0, 0, 0)
  const formatter = new Intl.DateTimeFormat(locale, {
    month: 'short',
    day: 'numeric',
  })
  while (cursor.getTime() / 1000 <= end && days.size < 31) {
    days.set(localDay(cursor), { label: formatter.format(cursor), value: 0 })
    cursor.setDate(cursor.getDate() + 1)
  }
  const models = new Map<string, number>()
  for (const row of rows) {
    if (!Number.isFinite(row.created_at)) {
      throw new Error('Usage timestamps unavailable')
    }
    if (row.created_at < start || row.created_at > end) continue
    const value = row[metricFields[metric]]
    if (typeof value !== 'number' || !Number.isFinite(value) || value < 0) {
      throw new Error('Usage metric unavailable')
    }
    const bucket = days.get(localDay(new Date(row.created_at * 1000)))
    if (bucket) bucket.value += value
    const name = row.model_name || unknownLabel
    models.set(name, (models.get(name) ?? 0) + value)
  }
  const sorted = [...models].sort((a, b) => b[1] - a[1])
  const top = sorted.slice(0, 5)
  if (sorted.length > 5) {
    top.push([
      otherLabel,
      sorted.slice(5).reduce((total, row) => total + row[1], 0),
    ])
  }
  const values = [...days.values()]
  return {
    labels: values.map((row) => row.label),
    values: values.map((row) => row.value),
    models: top,
    total: values.reduce((sum, row) => sum + row.value, 0),
  }
}
