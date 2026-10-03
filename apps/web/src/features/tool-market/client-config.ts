/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/

export const marketClientProfiles = [
  'codex',
  'claude-code',
  'cursor',
  'http',
] as const
export type MarketClientProfile = (typeof marketClientProfiles)[number]

// Claude Code only accepts letters, numbers, hyphens and underscores in server
// names. The token, not this local display name, binds the market client ID.
// Hex encoding is deterministic and cannot collide with another encoded ID.
function claudeServerName(client: string): string {
  return /^[A-Za-z0-9_-]+$/.test(client)
    ? client
    : `lmm-market-${Array.from(new TextEncoder().encode(client), (byte) =>
        byte.toString(16).padStart(2, '0')
      ).join('')}`
}

// A copied CLI command must preserve every argument literally, including URLs
// with quotes, backticks or dollar signs. This is a POSIX-shell command.
function shellArgument(value: string): string {
  return `'${value.replaceAll("'", "'\\''")}'`
}

// Called only after endpoint, client ID and bearer-token validation by
// buildMarketClientConfig. JSON escaping also encodes TOML basic strings safely
// because validated inputs have no lone surrogates or forbidden controls.
export function formatMarketClientConfig(
  endpoint: string,
  client: string,
  token: string,
  profile: MarketClientProfile
): string {
  const headers = { Authorization: `Bearer ${token}` }
  if (profile === 'codex') {
    return [
      `[mcp_servers.${JSON.stringify(client)}]`,
      `url = ${JSON.stringify(endpoint)}`,
      `http_headers = { Authorization = ${JSON.stringify(headers.Authorization)} }`,
    ].join('\n')
  }
  if (profile === 'claude-code') {
    const server = JSON.stringify({ type: 'http', url: endpoint, headers })
    return `claude mcp add-json --scope user ${shellArgument(claudeServerName(client))} ${shellArgument(server)}`
  }
  if (profile === 'cursor' || profile === 'http') {
    return JSON.stringify(
      {
        mcpServers: {
          [client]: {
            ...(profile === 'http' ? { type: 'http' } : {}),
            url: endpoint,
            headers,
          },
        },
      },
      null,
      2
    )
  }
  throw new Error('Invalid client profile')
}
