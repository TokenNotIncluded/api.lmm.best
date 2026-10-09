/* Copyright (C) 2026 LIghtJUNction; licensed under AGPL-3.0-or-later. */
export const OPENUI_MAX_CHARS = 16000
export const OPENUI_MAX_LINES = 128
export const OPENUI_PAGES = ['usage', 'pricing', 'keys', 'wallet', 'tools', 'setup', 'profile', 'support'] as const
const pagePaths = {
  usage: '/usage-logs', pricing: '/pricing', keys: '/keys', wallet: '/wallet',
  tools: '/tool-market', setup: '/getting-started', profile: '/profile', support: '/support',
} satisfies Record<typeof OPENUI_PAGES[number], string>

export function resolveOpenUIPage(page: unknown): string | null {
  return typeof page === 'string' && Object.hasOwn(pagePaths, page)
    ? pagePaths[page as keyof typeof pagePaths] : null
}

export function isBoundedOpenUI(code: string): boolean {
  return code.length <= OPENUI_MAX_CHARS && code.split('\n').length <= OPENUI_MAX_LINES && /^\s*root\s*=/.test(code)
}

export function compareOpenUICells(left: string | number | undefined, right: string | number | undefined): number {
  if (left === right) return 0
  if (left === undefined) return 1
  if (right === undefined) return -1
  if (typeof left === 'number' && typeof right === 'number') return left - right
  return String(left).localeCompare(String(right), undefined, { numeric: true })
}
