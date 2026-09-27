/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
export type ShortcutGroupId = 'global' | 'navigation' | 'appearance'

export type ShortcutDefinition = {
  /** Stable id, also used as the i18n key for the label. */
  id: string
  group: ShortcutGroupId
  /** Rendered as individual keycaps. */
  keys: string[]
  /** Locale-independent destination for navigation shortcuts. */
  to?: string
}

/**
 * Single source of truth for the console shortcut sheet.
 *
 * `labelKey`/`descriptionKey` are English source strings resolved through
 * `t()` so the sheet, the WebMCP tool and the palette stay in sync.
 */
export const CONSOLE_SHORTCUTS: readonly ShortcutDefinition[] = [
  { id: 'Open command palette', group: 'global', keys: ['⌘', 'K'] },
  { id: 'Show keyboard shortcuts', group: 'global', keys: ['?'] },
  { id: 'Close dialog or menu', group: 'global', keys: ['Esc'] },
  {
    id: 'Go to dashboard',
    group: 'navigation',
    keys: ['G', 'D'],
    to: '/dashboard/overview',
  },
  { id: 'Go to API keys', group: 'navigation', keys: ['G', 'K'], to: '/keys' },
  { id: 'Go to wallet', group: 'navigation', keys: ['G', 'W'], to: '/wallet' },
  {
    id: 'Go to usage logs',
    group: 'navigation',
    keys: ['G', 'L'],
    to: '/usage-logs/common',
  },
  {
    id: 'Go to pricing',
    group: 'navigation',
    keys: ['G', 'P'],
    to: '/pricing',
  },
  {
    id: 'Go to settings',
    group: 'navigation',
    keys: ['G', 'S'],
    to: '/settings',
  },
  { id: 'Toggle sidebar', group: 'appearance', keys: ['⌘', 'B'] },
]

export const SHORTCUT_GROUP_ORDER: readonly ShortcutGroupId[] = [
  'global',
  'navigation',
  'appearance',
]

export const SHORTCUT_GROUP_TITLES: Record<ShortcutGroupId, string> = {
  global: 'General',
  navigation: 'Go to',
  appearance: 'Appearance',
}

/** Lowercase key sequence for a navigation shortcut, e.g. `['g', 'd']`. */
export const NAVIGATION_SHORTCUTS = CONSOLE_SHORTCUTS.filter(
  (shortcut) => shortcut.group === 'navigation' && shortcut.to
).map((shortcut) => ({
  keys: shortcut.keys.map((key) => key.toLowerCase()),
  to: shortcut.to as string,
  label: shortcut.id,
}))
