/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/

export type ConnectionPermissions = {
  can_invoke: boolean
  can_manage: boolean
  expires_in_days: number
}

export const defaultConnectionPermissions: ConnectionPermissions = {
  can_invoke: true,
  can_manage: false,
  expires_in_days: 7,
}

export function isPersonalMarketClient(client: string): boolean {
  return (
    client.length > 0 &&
    client === client.trim() &&
    new TextEncoder().encode(client).length <= 128 &&
    !/[\u0000-\u001f\u007f]/.test(client) &&
    client !== 'web-market' &&
    !client.startsWith('oauth:')
  )
}

export function connectionTokenInput(
  client: string,
  permissions: ConnectionPermissions = defaultConnectionPermissions,
  now = Date.now()
) {
  const clientID = client.trim()
  if (
    !isPersonalMarketClient(clientID) ||
    typeof permissions.can_invoke !== 'boolean' ||
    typeof permissions.can_manage !== 'boolean' ||
    !Number.isInteger(permissions.expires_in_days) ||
    permissions.expires_in_days < 1 ||
    permissions.expires_in_days > 90 ||
    !Number.isFinite(now)
  ) {
    throw new Error('Invalid connection settings')
  }
  return {
    client_id: clientID,
    can_invoke: permissions.can_invoke,
    can_manage: permissions.can_manage,
    expires_at: Math.floor(now / 1000) + permissions.expires_in_days * 86400,
  }
}

export function marketEndpoint(origin: string, path: string): string {
  const base = new URL(origin)
  const endpoint = new URL(path, base)
  if (
    !path.startsWith('/') ||
    path.startsWith('//') ||
    endpoint.origin !== base.origin ||
    !['https:', 'http:'].includes(endpoint.protocol) ||
    endpoint.username ||
    endpoint.password ||
    endpoint.search ||
    endpoint.hash
  ) {
    throw new Error('Invalid MCP endpoint')
  }
  return endpoint.href
}

// The preview uses a placeholder; inserting the actual secret happens only after
// an explicit copy action. Never put connection secrets into query caches or URLs.
export function buildMarketClientConfig(
  endpoint: string,
  client: string,
  token = 'YOUR_CONNECTION_TOKEN'
): string {
  const url = new URL(endpoint)
  const safeEndpoint = marketEndpoint(url.origin, url.pathname)
  if (url.href !== safeEndpoint || !isPersonalMarketClient(client)) {
    throw new Error('Invalid connection settings')
  }
  return JSON.stringify(
    {
      mcpServers: {
        [client]: {
          type: 'http',
          url: safeEndpoint,
          headers: { Authorization: `Bearer ${token}` },
        },
      },
    },
    null,
    2
  )
}

export function connectionStatus(
  record: { revoked_at: number; expires_at: number },
  now = Date.now()
): 'active' | 'expired' | 'revoked' {
  if (record.revoked_at) return 'revoked'
  return record.expires_at * 1000 <= now ? 'expired' : 'active'
}

// The existing API caps each page at 100 records. Do not treat page one as a
// complete authorization snapshot, and never return a successful partial result.
export async function collectMarketPages<T>(
  fetchPage: (offset: number, limit: number) => Promise<T[]>,
  pageSize = 100,
  maxPages = 101
): Promise<T[]> {
  if (
    !Number.isInteger(pageSize) ||
    pageSize < 1 ||
    pageSize > 100 ||
    !Number.isInteger(maxPages) ||
    maxPages < 1 ||
    (maxPages - 1) * pageSize > 10000
  ) {
    throw new Error('Invalid pagination')
  }
  const result: T[] = []
  for (let page = 0; page < maxPages; page++) {
    const rows = await fetchPage(page * pageSize, pageSize)
    if (!Array.isArray(rows) || rows.length > pageSize) {
      throw new Error('Invalid account resource response')
    }
    result.push(...rows)
    if (rows.length < pageSize) return result
  }
  throw new Error('Account resource pagination limit reached')
}
