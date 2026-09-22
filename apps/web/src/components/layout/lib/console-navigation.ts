/*
Copyright (C) 2026 LIghtJUNction

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

*/
import type { NavGroup, NavItem, TopNavLink } from '../types'

// Promote only links that have already passed role and sidebar-module filters.
// Do not reconstruct entries here: badges, aliases and panel interactions matter.
const PRIMARY_URLS = [
  '/dashboard/overview',
  '/getting-started',
  '/pricing',
  '/keys',
  '/usage-logs/common',
  '/wallet',
] as const

export function organizeConsoleNavigation(groups: NavGroup[], title: string) {
  // First-run navigation is intentionally separate from the activated console.
  if (groups.some((group) => group.id === 'onboarding')) return groups

  const primary: NavItem[] = []
  const promoted = new Set<NavItem>()
  for (const url of PRIMARY_URLS) {
    const item = groups
      .flatMap((group) => group.items)
      .find((entry) => entry.url === url)
    if (item) {
      primary.push(item)
      promoted.add(item)
    }
  }

  const remaining = groups
    .map((group) => ({
      ...group,
      items: group.items.filter((item) => !promoted.has(item)),
    }))
    .filter((group) => group.items.length > 0)

  return primary.length
    ? [{ id: 'console-primary', title, items: primary }, ...remaining]
    : remaining
}

function leafItems(groups: NavGroup[]) {
  return groups.flatMap((group) =>
    group.items.flatMap((item) => (item.items ? item.items : [item]))
  )
}

export function getConsolePageTitle(groups: NavGroup[], href: string) {
  const pathname = href.split(/[?#]/)[0].replace(/\/+$/, '') || '/'
  // Prefer the longest exact/prefix match, so /subscriptions/reset does not
  // accidentally inherit the /subscriptions title.
  return leafItems(groups)
    .filter((item) =>
      [item.url, ...(item.activeUrls ?? [])].some((url) => {
        if (typeof url !== 'string') return false
        const base = url.split(/[?#]/)[0].replace(/\/+$/, '') || '/'
        return (
          pathname === base || (base !== '/' && pathname.startsWith(`${base}/`))
        )
      })
    )
    .sort((a, b) => String(b.url ?? '').length - String(a.url ?? '').length)[0]
    ?.title
}

export function getConsoleSiteLinks(links: TopNavLink[], groups: NavGroup[]) {
  const destinations = new Set(leafItems(groups).map((item) => item.url))
  const seen = new Set<string>()
  return links.filter((link) => {
    // Keep explicitly external and disabled custom links with their semantics.
    if (!link.external && !link.disabled && destinations.has(link.href)) {
      return false
    }
    const key = `${Boolean(link.external)}:${link.href}`
    if (seen.has(key)) return false
    seen.add(key)
    return true
  })
}
