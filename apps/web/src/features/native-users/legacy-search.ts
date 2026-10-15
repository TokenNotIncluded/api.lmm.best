/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import {
  DEFAULT_NATIVE_USERS_QUERY,
  nativeUsersQuery,
  type NativeUsersQuery,
} from './contract'

// The retained route validates legacy search keys and can coerce unknown values.
// Read the raw query before that coercion so saved unsupported filters never disappear.
export function nativeQueryFromLegacySearch(
  search: string
): NativeUsersQuery | undefined {
  const params = new URLSearchParams(search)
  const allowed = new Set([
    'page',
    'pageSize',
    'filter',
    'status',
    'role',
    'group',
    'l0Only',
    'sortBy',
    'sortOrder',
    'risk',
    'transfers',
    'usage',
    'funding',
    'checkin',
  ])
  const seen = new Set<string>()
  const query = { ...DEFAULT_NATIVE_USERS_QUERY }
  try {
    for (const [key, raw] of params) {
      if (!allowed.has(key) || seen.has(key)) return undefined
      seen.add(key)
      // TanStack's JSON search encoding accepts strings directly, and arrays/numbers as JSON.
      let value: unknown = raw
      try {
        value = JSON.parse(raw)
      } catch {
        /* Plain string is the expected text encoding. */
      }
      if (key === 'page' || key === 'pageSize') {
        if (typeof value !== 'number') return undefined
        if (key === 'page') query.page = value
        else query.page_size = value
      } else if (key === 'filter' || key === 'group') {
        if (typeof value !== 'string') return undefined
        if (key === 'filter') query.keyword = value
        else query.group = value
      } else if (key === 'role' || key === 'status') {
        if (!Array.isArray(value) || value.length > 1) return undefined
        if (value.length === 0) continue
        const options =
          key === 'role'
            ? { '1': 'user', '10': 'admin', '100': 'superadmin' }
            : { '1': 'active', '2': 'disabled' }
        if (typeof value[0] !== 'string' || !Object.hasOwn(options, value[0])) {
          return undefined
        }
        if (key === 'role') {
          query.role = options[
            value[0] as keyof typeof options
          ] as NativeUsersQuery['role']
        } else {
          query.status = options[
            value[0] as keyof typeof options
          ] as NativeUsersQuery['status']
        }
      } else if (key === 'sortBy') {
        if (value !== 'id' && value !== 'username') return undefined
        query.sort_by = value
      } else if (key === 'sortOrder') {
        if (value !== 'asc' && value !== 'desc') return undefined
        query.sort_order = value
      } else if (key === 'l0Only') {
        if (value !== false) return undefined
      } else if (value !== 'all') return undefined
    }
    return nativeUsersQuery(query)
  } catch {
    return undefined
  }
}
