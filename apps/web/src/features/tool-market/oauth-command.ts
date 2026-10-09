/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { marketEndpoint } from './connection-utils'

export function marketOAuthCommand(endpoint: string): string {
  const url = new URL(endpoint)
  const modes = url.searchParams.getAll('mode')
  if (
    !/^https?:\/\/[a-zA-Z0-9.:[\]-]+\/[a-zA-Z0-9/_-]+(?:\?mode=(?:compact|full))?$/.test(
      endpoint
    ) ||
    `${url.origin}${url.pathname}` !==
      marketEndpoint(url.origin, url.pathname) ||
    (url.search !== '' &&
      (modes.length !== 1 || !['compact', 'full'].includes(modes[0])))
  ) {
    throw new Error('Invalid MCP endpoint')
  }
  // Quote query punctuation and IPv6 brackets rather than exposing shell globs.
  const target =
    url.search || endpoint.includes('[') ? `'${endpoint}'` : endpoint
  return `codex mcp add lmm --url ${target}\n# Complete browser authorization. If needed, run:\ncodex mcp login lmm`
}
