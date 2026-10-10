/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import type {
  LinkedProfileProvider,
  LinkedUsageProfile,
  ProfileSnapshotPeriod,
} from '../types'

export interface LinkedProfileDraft {
  id: string
  provider: LinkedProfileProvider
  url: string
  label: string
  importSnapshot: boolean
  tokens: string
  requests: string
  messages: string
  period: ProfileSnapshotPeriod
  start: string
  end: string
  observedAt: string
  approximate: boolean
  source: string
}

export const MAX_LINKED_PROFILES = 5

export function localDateTime(value: string) {
  const date = new Date(value)
  if (!Number.isFinite(date.getTime())) return ''
  const shifted = new Date(date.getTime() - date.getTimezoneOffset() * 60_000)
  return shifted.toISOString().slice(0, 19)
}

export function profileDraft(
  profile?: LinkedUsageProfile,
  now = new Date()
): LinkedProfileDraft {
  const snapshot = profile?.snapshot
  return {
    id: crypto.randomUUID(),
    provider: profile?.provider ?? 'cursor',
    url: profile?.url ?? '',
    label: profile?.label ?? '',
    importSnapshot: Boolean(snapshot),
    tokens: snapshot?.tokens === undefined ? '' : String(snapshot.tokens),
    requests: snapshot?.requests === undefined ? '' : String(snapshot.requests),
    messages: snapshot?.messages === undefined ? '' : String(snapshot.messages),
    period: snapshot?.period ?? 'all',
    start: snapshot?.period_start ?? '',
    end: snapshot?.period_end ?? '',
    observedAt: localDateTime(snapshot?.observed_at ?? now.toISOString()),
    approximate: snapshot?.approximate ?? false,
    source: snapshot?.source ?? '',
  }
}

export function validProfileURL(
  provider: LinkedProfileProvider,
  value: string
) {
  try {
    const url = new URL(value)
    if (
      url.protocol !== 'https:' ||
      url.username ||
      url.password ||
      url.port ||
      url.search ||
      url.hash ||
      value.includes('?') ||
      value.includes('#') ||
      url.href !== value ||
      new TextEncoder().encode(value).length > 512
    ) {
      return false
    }
    const host = url.hostname
    if (
      !/^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+$/.test(
        host
      ) ||
      /^\d+\.\d+\.\d+\.\d+$/.test(host) ||
      /\.(local|localhost|internal|lan|home|arpa|onion|test|invalid|example)$/.test(
        host
      )
    ) {
      return false
    }
    if (provider === 'custom') {
      const path = decodeURIComponent(url.pathname)
      return (
        path.length > 1 &&
        !path.endsWith('/') &&
        !/(?:\p{Cc}|[\\\s])/u.test(path) &&
        !path.includes('//') &&
        !/(^|\/)\.\.?($|\/)/.test(path) &&
        !/%(?:2e|2f|5c)/i.test(path) &&
        !/%(?:2e|2f|5c)/i.test(url.pathname)
      )
    }
    const prefixes = provider === 'cursor' ? ['/@'] : ['/u/']
    if (provider === 'cursor' && host !== 'cursor.com') return false
    if (provider === 'chatgpt' && host !== 'chatgpt.com') return false
    const prefix = prefixes.find((candidate) =>
      url.pathname.startsWith(candidate)
    )
    return Boolean(
      prefix &&
      /^[A-Za-z0-9][A-Za-z0-9_.-]{0,79}$/.test(
        url.pathname.slice(prefix.length)
      )
    )
  } catch {
    return false
  }
}

function validDay(value: string) {
  return (
    /^\d{4}-\d{2}-\d{2}$/.test(value) &&
    Number.isFinite(Date.parse(value)) &&
    new Date(value).toISOString().slice(0, 10) === value
  )
}

export function prepareLinkedProfiles(
  drafts: LinkedProfileDraft[],
  now = Date.now()
) {
  const errors: Record<string, string> = {}
  const profiles: LinkedUsageProfile[] = []
  const urls = new Set<string>()
  for (const draft of drafts) {
    const fail = (message: string) => {
      errors[draft.id] = message
    }
    const url = draft.url.trim()
    const label = draft.label.trim()
    if (!validProfileURL(draft.provider, url)) {
      fail('Enter a valid public HTTPS profile URL for this provider.')
      continue
    }
    if (urls.has(url)) {
      fail('This profile URL is already in the list.')
      continue
    }
    urls.add(url)
    if ([...label].length > 48 || /\p{Cc}/u.test(label)) {
      fail('Use a label of up to 48 characters without control characters.')
      continue
    }
    const profile: LinkedUsageProfile = { provider: draft.provider, url }
    if (label) profile.label = label
    if (draft.importSnapshot) {
      const metrics: { tokens?: number; requests?: number; messages?: number } =
        {}
      let invalid = false
      for (const key of ['tokens', 'requests', 'messages'] as const) {
        const raw = draft[key].trim()
        if (!raw) continue
        const value = Number(raw)
        if (!/^\d+$/.test(raw) || !Number.isSafeInteger(value) || value < 0) {
          invalid = true
        } else metrics[key] = value
      }
      if (invalid || !Object.keys(metrics).length) {
        fail('Enter at least one whole, non-negative usage number.')
        continue
      }
      const observed = new Date(draft.observedAt)
      if (
        !draft.observedAt ||
        !Number.isFinite(observed.getTime()) ||
        observed.getTime() < Date.UTC(2000, 0, 1) ||
        observed.getTime() > now + 300_000
      ) {
        fail('Enter when you observed these numbers, between 2000 and now.')
        continue
      }
      const hasDates = Boolean(draft.start || draft.end)
      if (
        (draft.period === 'custom' || hasDates) &&
        (draft.period === 'all' ||
          !validDay(draft.start) ||
          !validDay(draft.end) ||
          draft.start > draft.end ||
          draft.end > observed.toISOString().slice(0, 10) ||
          Date.parse(draft.end) - Date.parse(draft.start) >
            (draft.period === '7d' ? 7 : draft.period === '30d' ? 30 : 365) *
              86_400_000)
      ) {
        fail(
          'Use a valid date range of up to 366 days ending by the observation date.'
        )
        continue
      }
      const source = draft.source.trim()
      if ([...source].length > 80 || /\p{Cc}/u.test(source)) {
        fail(
          'Use a source note of up to 80 characters without control characters.'
        )
        continue
      }
      profile.snapshot = {
        ...metrics,
        period: draft.period,
        observed_at: observed.toISOString(),
        approximate: draft.approximate,
        ...(hasDates
          ? { period_start: draft.start, period_end: draft.end }
          : {}),
        ...(source ? { source } : {}),
      }
    }
    profiles.push(profile)
  }
  return {
    profiles,
    errors,
    valid: drafts.length <= MAX_LINKED_PROFILES && !Object.keys(errors).length,
  }
}
