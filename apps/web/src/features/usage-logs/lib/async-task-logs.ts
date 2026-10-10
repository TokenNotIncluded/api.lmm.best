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
export type AsyncLogCategory = 'drawing' | 'task'

export interface AsyncLogParams {
  p: number
  page_size: number
  mj_id?: string
  task_id?: string
  channel_id?: string
  status?: string
  action?: string
  platform?: string
  start_timestamp?: number
  end_timestamp?: number
}

function text(value: unknown): string | undefined {
  return typeof value === 'string' ? value.trim() || undefined : undefined
}

function timestamp(value: unknown, milliseconds: boolean): number | undefined {
  if (typeof value !== 'number' || !Number.isFinite(value) || value < 0) {
    return undefined
  }
  return Math.floor(milliseconds ? value : value / 1000)
}

/** Async history has no implicit date cutoff; an ID search must find old jobs. */
export function buildAsyncLogParams({
  logCategory,
  isAdmin,
  page,
  pageSize,
  searchParams,
}: {
  logCategory: AsyncLogCategory
  isAdmin: boolean
  page: number
  pageSize: number
  searchParams: Record<string, unknown>
}): AsyncLogParams {
  const milliseconds = logCategory === 'drawing'
  const start = timestamp(searchParams.startTime, milliseconds)
  const end = timestamp(searchParams.endTime, milliseconds)
  if (start !== undefined && end !== undefined && start > end) {
    throw new RangeError('Start time must be before end time')
  }

  const id = text(searchParams.filter)
  const status = text(searchParams.status)?.toUpperCase()
  // Video actions use camelCase. Do not uppercase them like status values.
  const action = text(searchParams.action)
  const platform =
    logCategory === 'task' ? text(searchParams.platform) : undefined
  const channel = isAdmin ? text(searchParams.channel) : undefined

  return {
    p: page,
    page_size: pageSize,
    ...(id && (logCategory === 'drawing' ? { mj_id: id } : { task_id: id })),
    ...(status && { status }),
    ...(action && { action }),
    ...(platform && { platform }),
    ...(channel && { channel_id: channel }),
    ...(start !== undefined && { start_timestamp: start }),
    ...(end !== undefined && { end_timestamp: end }),
  }
}

function record(value: unknown): Record<string, unknown> | undefined {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined
}

export function summarizeAsyncLogs(rows: readonly unknown[]) {
  const summary = {
    total: rows.length,
    queued: 0,
    running: 0,
    success: 0,
    failed: 0,
    other: 0,
  }
  for (const row of rows) {
    const status = text(record(row)?.status)?.toUpperCase()
    switch (status) {
      case 'NOT_START':
      case 'SUBMITTED':
      case 'QUEUED':
        summary.queued++
        break
      case 'IN_PROGRESS':
        summary.running++
        break
      case 'SUCCESS':
        summary.success++
        break
      case 'FAILURE':
        summary.failed++
        break
      default:
        summary.other++
    }
  }
  return summary
}

/** Only poll visible, active work. Unknown/MODAL states need user attention. */
export function getAsyncLogRefreshInterval(
  category: 'common' | AsyncLogCategory,
  rows: readonly unknown[],
  enabled: boolean,
  isError = false
): number | false {
  if (category === 'common' || !enabled || isError) return false
  const { queued, running } = summarizeAsyncLogs(rows)
  return queued + running > 0 ? 5000 : false
}

/** Never show an all-users page as the placeholder for the self-only scope. */
export function canKeepPreviousLogData(
  previousKey: readonly unknown[] | undefined,
  category: string,
  isAdmin: boolean
): boolean {
  return previousKey?.[1] === category && previousKey?.[2] === isAdmin
}

/** Allow web media and same-origin paths, never executable or credential URLs. */
export function safeMediaUrl(value: unknown): string | undefined {
  const candidate = text(value)
  if (!candidate) return undefined
  for (const character of candidate) {
    const code = character.charCodeAt(0)
    if (code <= 0x1f || code === 0x7f || character === '\\') {
      return undefined
    }
  }
  if (candidate.startsWith('/') && !candidate.startsWith('//')) return candidate
  try {
    const url = new URL(candidate)
    if (
      !['https:', 'http:'].includes(url.protocol) ||
      url.username ||
      url.password
    ) {
      return undefined
    }
    return url.href
  } catch {
    return undefined
  }
}

/** The API returns JSON values; older installations can return a JSON string. */
export function parseTaskRecords(data: unknown): Record<string, unknown>[] {
  let value = data
  if (typeof value === 'string') {
    try {
      value = JSON.parse(value)
    } catch {
      return []
    }
  }
  const entries = Array.isArray(value) ? value : [value]
  return entries.flatMap((entry) => {
    const item = record(entry)
    return item ? [item] : []
  })
}

export function getTaskResultUrl(log: {
  status?: string
  result_url?: string
  fail_reason?: string
}): string | undefined {
  if (log.status !== 'SUCCESS') return undefined
  return safeMediaUrl(log.result_url) ?? safeMediaUrl(log.fail_reason)
}

export function getDrawingVideoUrls(log: {
  video_url?: string
  video_urls?: unknown
}): string[] {
  let urls: unknown = log.video_urls
  if (typeof urls === 'string') {
    try {
      urls = JSON.parse(urls)
    } catch {
      urls = []
    }
  }
  const candidates = [log.video_url, ...(Array.isArray(urls) ? urls : [])]
  return [
    ...new Set(
      candidates.flatMap((candidate) => {
        const url = safeMediaUrl(candidate)
        return url ? [url] : []
      })
    ),
  ]
}

/** Match the audio dialog's HTTPS-only policy and discard malformed metadata. */
export function getTaskAudioClips(data: unknown) {
  const finiteDuration = (value: unknown) =>
    typeof value === 'number' && Number.isFinite(value) && value >= 0
      ? value
      : undefined
  return parseTaskRecords(data).flatMap((item) => {
    const audioUrl = safeMediaUrl(item.audio_url)
    if (!audioUrl?.startsWith('https://')) return []
    const metadata = record(item.metadata)
    return [
      {
        id: text(item.id),
        clip_id: text(item.clip_id),
        title: text(item.title),
        tags: text(item.tags) ?? text(metadata?.tags),
        duration:
          finiteDuration(item.duration) ?? finiteDuration(metadata?.duration),
        audio_url: audioUrl,
        image_url: safeMediaUrl(item.image_url),
        image_large_url: safeMediaUrl(item.image_large_url),
      },
    ]
  })
}
