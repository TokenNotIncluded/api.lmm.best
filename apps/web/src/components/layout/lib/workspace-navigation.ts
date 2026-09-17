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
import type { NavGroup, NavItem, NavLink } from '../types'

function normalize(value: string): string {
  return value.normalize('NFKD').replace(/\p{M}/gu, '').toLowerCase()
}

/** Search the supplied, permission-filtered groups without fetching more links. */
export function filterWorkspaceNavigation(
  groups: NavGroup[],
  query: string
): NavGroup[] {
  const words = normalize(query).trim().split(/\s+/u).filter(Boolean)
  if (words.length === 0) return groups
  const matches = (...labels: string[]) => {
    const text = normalize(labels.join(' '))
    return words.every((word) => text.includes(word))
  }
  return groups.flatMap((group) => {
    const items = group.items.flatMap<NavItem>((item) => {
      if (item.disabled) return []
      // Flatten children so a match cannot remain in a closed accordion.
      // Preserve each destination's original URL, icon and interaction.
      if (item.items) {
        return item.items
          .filter(
            (child) =>
              !child.disabled &&
              matches(group.title, item.title, child.title, child.url ?? '')
          )
          .map((child) => ({
            ...child,
            title: `${item.title} / ${child.title}`,
          }))
      }
      return matches(group.title, item.title, item.url ?? '') ? [item] : []
    })
    return items.length ? [{ ...group, items }] : []
  })
}

const SHORTCUT_ROUTES = [
  '/keys',
  '/pricing',
  '/wallet',
  '/usage-logs/common',
] as const

/** A priority list, not authorization: absent links remain absent. */
export function selectWorkspaceShortcuts(
  groups: NavGroup[],
  role = 0
): NavLink[] {
  const links: NavLink[] = []
  for (const group of groups) {
    for (const item of group.items) {
      if (
        item.disabled ||
        (item.requiredRole !== undefined && role < item.requiredRole)
      ) {
        continue
      }
      if (item.items) {
        for (const child of item.items) {
          if (
            !child.disabled &&
            (child.requiredRole === undefined || role >= child.requiredRole)
          ) {
            links.push(child)
          }
        }
      } else if (item.url) {
        links.push(item)
      }
    }
  }
  return SHORTCUT_ROUTES.flatMap((url) => {
    const link = links.find((item) => item.url === url)
    return link ? [link] : []
  })
}
