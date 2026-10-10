/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import type { NavGroup } from '../types'

/**
 * Resolve a visited pathname to the navigation label a user would recognise.
 *
 * Matching mirrors `getConsolePageTitle`: the longest exact/prefix hit wins, so
 * `/subscriptions/reset` does not inherit the `/subscriptions` label.
 */
export function resolveRecentRouteLabel(
  groups: NavGroup[],
  pathname: string
): string | null {
  const target = pathname.split(/[?#]/)[0].replace(/\/+$/, '') || '/'

  const matches: { title: string; url: string }[] = []
  for (const group of groups) {
    for (const item of group.items) {
      const candidates = [
        ...(item.url ? [item.url] : []),
        ...(item.items ?? []).flatMap((sub) => (sub.url ? [sub.url] : [])),
      ]
      for (const candidate of candidates) {
        if (typeof candidate !== 'string') continue
        const base = candidate.split(/[?#]/)[0].replace(/\/+$/, '') || '/'
        if (
          target === base ||
          (base !== '/' && target.startsWith(`${base}/`))
        ) {
          matches.push({ title: item.title, url: base })
        }
      }
    }
  }

  return matches.sort((a, b) => b.url.length - a.url.length)[0]?.title ?? null
}
