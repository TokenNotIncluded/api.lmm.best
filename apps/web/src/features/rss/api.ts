/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { api } from '@/lib/api'

export const RSS_MAX_FEEDS = 24

export type RSSFeedConfig = {
  id: string
  name: string
  url: string
  enabled: boolean
}

export type RSSItem = {
  id: string
  title: string
  url: string
  summary: string
  published_at?: string
}

export type RSSFeedResult = {
  id: string
  name: string
  url: string
  items: RSSItem[]
  error?: string
}

export function safeRSSFeedUrl(value: string): string | null {
  try {
    const parsed = new URL(value.trim())
    if (
      !['http:', 'https:'].includes(parsed.protocol) ||
      !parsed.hostname ||
      parsed.username ||
      parsed.password
    ) {
      return null
    }
    return parsed.toString()
  } catch {
    return null
  }
}

export function parseRSSFeeds(value: string): RSSFeedConfig[] | null {
  if (!value.trim()) return []
  try {
    const parsed: unknown = JSON.parse(value)
    if (!Array.isArray(parsed) || parsed.length > RSS_MAX_FEEDS) return null
    const seenIDs = new Set<string>()
    const seenURLs = new Set<string>()
    const feeds: RSSFeedConfig[] = []
    for (const item of parsed) {
      if (
        !item ||
        typeof item !== 'object' ||
        typeof item.id !== 'string' ||
        typeof item.name !== 'string' ||
        typeof item.url !== 'string' ||
        typeof item.enabled !== 'boolean'
      ) {
        return null
      }
      const id = item.id.trim()
      const name = item.name.trim()
      const url = safeRSSFeedUrl(item.url)
      if (
        !/^[a-zA-Z0-9_-]{1,64}$/.test(id) ||
        seenIDs.has(id) ||
        !name ||
        name.length > 80 ||
        !url ||
        item.url.length > 2048 ||
        seenURLs.has(url)
      ) {
        return null
      }
      seenIDs.add(id)
      seenURLs.add(url)
      feeds.push({ id, name, url: item.url.trim(), enabled: item.enabled })
    }
    return feeds
  } catch {
    return null
  }
}

export async function getRSSFeeds(): Promise<RSSFeedResult[]> {
  const response = await api.get<{
    success: boolean
    data: { feeds: RSSFeedResult[] | null }
  }>('/api/rss')
  if (!response.data.success) throw new Error('Unable to load RSS feeds')
  return response.data.data.feeds ?? []
}
