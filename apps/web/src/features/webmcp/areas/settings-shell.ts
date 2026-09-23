/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { getShellBridge } from '@/components/layout/lib/shell-bridge'
import { CONSOLE_SHORTCUTS } from '@/components/layout/lib/shortcuts'
import { listSystemInstances } from '@/features/system-info/api'
import { listSystemTasks } from '@/features/system-settings/api'
import {
  buildSettingsSearchIndex,
  searchSettings,
} from '@/features/system-settings/utils/settings-search-index'
import appI18n from '@/i18n/config'
import { normalizeInterfaceLanguage } from '@/i18n/languages'
import { ROLE } from '@/lib/roles'

import {
  clip,
  EMPTY_INPUT_SCHEMA,
  ensureNotAborted,
  ensureObject,
  optionalEnum,
  optionalInteger,
  optionalString,
  requiredString,
  requireAdmin,
  requireSignedIn,
  type WebMcpToolFactory,
} from '../tool-kit'

/** Match the existing root-only settings and system-info route guards. */
function requireSettingsAccess() {
  const user = requireAdmin()
  if (user.role !== ROLE.SUPER_ADMIN) {
    throw new Error('This page requires a root administrator account')
  }
  return user
}

function settingsIndex() {
  return buildSettingsSearchIndex(appI18n.getFixedT(appI18n.language))
}

function finiteNumber(value: unknown) {
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

export const settingsShellTools: WebMcpToolFactory = ({ router }) => [
  {
    name: 'lmm_shell_status',
    title: 'Read interface preferences',
    description:
      'Read the signed-in interface language, displayed color scheme, reduced-motion preference, and wallet path. This does not change preferences or return account credentials.',
    inputSchema: EMPTY_INPUT_SCHEMA,
    annotations: { readOnlyHint: true },
    execute: async (_input, options) => {
      requireSignedIn()
      ensureNotAborted(options.signal)
      return {
        language: normalizeInterfaceLanguage(appI18n.language),
        color_scheme:
          typeof document === 'undefined'
            ? null
            : document.documentElement.classList.contains('dark')
              ? 'dark'
              : 'light',
        reduced_motion:
          typeof window === 'undefined'
            ? null
            : window.matchMedia('(prefers-reduced-motion: reduce)').matches,
        wallet_path: '/wallet',
      }
    },
  },
  {
    name: 'lmm_shell_shortcuts',
    title: 'List keyboard shortcuts',
    description:
      'List the console keyboard shortcuts and their destinations. Command also works as Control on Windows and Linux. Navigation still follows the existing account and route permissions.',
    inputSchema: EMPTY_INPUT_SCHEMA,
    annotations: { readOnlyHint: true },
    execute: async (_input, options) => {
      requireSignedIn()
      ensureNotAborted(options.signal)
      return {
        shortcuts: CONSOLE_SHORTCUTS.map((shortcut) => ({
          label: appI18n.t(shortcut.id),
          keys: shortcut.keys,
          path: shortcut.to ?? null,
        })),
      }
    },
  },
  {
    name: 'lmm_shell_open',
    title: 'Open console shortcuts or search',
    description:
      'Open the command palette or keyboard shortcut reference in the signed-in console. Only opens an interface panel; no command runs and no data changes.',
    inputSchema: {
      type: 'object',
      properties: {
        panel: { type: 'string', enum: ['commands', 'shortcuts'] },
      },
      required: ['panel'],
      additionalProperties: false,
    },
    annotations: { readOnlyHint: true },
    execute: async (rawInput, options) => {
      requireSignedIn()
      const input = ensureObject(rawInput)
      const panel = optionalEnum(input, 'panel', ['commands', 'shortcuts'])
      if (!panel) throw new TypeError('panel is required')
      ensureNotAborted(options.signal)
      const bridge = getShellBridge()
      const open =
        panel === 'commands' ? bridge.openPalette : bridge.openShortcutSheet
      if (!open) throw new Error('Open a signed-in console page first')
      open()
      return { panel, opened: true }
    },
  },
  {
    name: 'lmm_settings_search',
    title: 'Find system settings sections',
    description:
      'List or search every system settings section by title, keyword, or path. Root administrator only. Returns navigation labels and paths, never option values, secrets, keys, or configuration contents.',
    inputSchema: {
      type: 'object',
      properties: { query: { type: 'string', maxLength: 120 } },
      additionalProperties: false,
    },
    annotations: { readOnlyHint: true },
    execute: async (rawInput, options) => {
      requireSettingsAccess()
      const input = ensureObject(rawInput)
      const query = optionalString(input, 'query', 120) ?? ''
      ensureNotAborted(options.signal)
      const sections = searchSettings(settingsIndex(), query).flatMap((group) =>
        group.entries.map((entry) => ({
          group: entry.group,
          title: entry.section,
          path: entry.url,
        }))
      )
      return { sections, total: sections.length }
    },
  },
  {
    name: 'lmm_settings_open',
    title: 'Open a system settings section',
    description:
      'Open a path returned by lmm_settings_search or the system information page. Root administrator only. Navigation only: never saves settings, starts tasks, deletes data, or makes a payment.',
    inputSchema: {
      type: 'object',
      properties: { path: { type: 'string', maxLength: 180 } },
      required: ['path'],
      additionalProperties: false,
    },
    annotations: { readOnlyHint: true },
    execute: async (rawInput, options) => {
      requireSettingsAccess()
      const input = ensureObject(rawInput)
      const path = requiredString(input, 'path', 180)
      const known =
        path === '/system-info' ||
        settingsIndex().some((group) =>
          group.entries.some((entry) => entry.url === path)
        )
      if (!known) {
        throw new TypeError('Use a path returned by lmm_settings_search')
      }
      ensureNotAborted(options.signal)
      const [to, query] = path.split('?')
      await router.navigate({
        to,
        ...(query
          ? { search: Object.fromEntries(new URLSearchParams(query)) }
          : {}),
      })
      return { path, opened: true }
    },
  },
  {
    name: 'lmm_system_instances',
    title: 'Read system instance health',
    description:
      'Read a bounded system instance health summary for root administrators: instance slot, online or stale state, timestamps, runtime version, and CPU, memory, and storage percentages. Never returns raw diagnostic payloads, environment variables, connection strings, or credentials.',
    inputSchema: EMPTY_INPUT_SCHEMA,
    annotations: { readOnlyHint: true, untrustedContentHint: true },
    execute: async (_input, options) => {
      requireSettingsAccess()
      ensureNotAborted(options.signal)
      const response = await listSystemInstances()
      ensureNotAborted(options.signal)
      requireSettingsAccess()
      if (!response.success) throw new Error('Unable to load system instances')
      const rows = response.data ?? []
      return {
        total: rows.length,
        instances: rows.slice(0, 100).map((instance) => ({
          slot: clip(
            instance.instance_slot ?? instance.info?.runtime?.instance_slot,
            40
          ),
          status: instance.status === 'online' ? 'online' : 'stale',
          last_seen_at: finiteNumber(instance.last_seen_at),
          started_at: finiteNumber(instance.started_at),
          version: clip(instance.info?.runtime?.version, 40),
          cpu_percent: finiteNumber(
            instance.info?.resources?.cpu?.usage_percent
          ),
          memory_percent: finiteNumber(
            instance.info?.resources?.memory?.usage_percent
          ),
          storage_percent: finiteNumber(
            instance.info?.resources?.storage?.used_percent
          ),
        })),
      }
    },
  },
  {
    name: 'lmm_system_tasks',
    title: 'Read background task status',
    description:
      'Read recent background task IDs, types, states, timestamps, and numeric progress for root administrators. Does not start or cancel tasks. Raw task payloads, results, logs, and error text are excluded because they may contain private data.',
    inputSchema: {
      type: 'object',
      properties: { limit: { type: 'integer', minimum: 1, maximum: 50 } },
      additionalProperties: false,
    },
    annotations: { readOnlyHint: true, untrustedContentHint: true },
    execute: async (rawInput, options) => {
      requireSettingsAccess()
      const input = ensureObject(rawInput)
      const limit = optionalInteger(input, 'limit', 1, 50) ?? 20
      ensureNotAborted(options.signal)
      const response = await listSystemTasks(limit)
      ensureNotAborted(options.signal)
      requireSettingsAccess()
      if (!response.success) throw new Error('Unable to load background tasks')
      return {
        tasks: (response.data ?? []).slice(0, limit).map((task) => ({
          id: finiteNumber(task.id),
          type: clip(task.type, 80),
          status: ['pending', 'running', 'succeeded', 'failed'].includes(
            task.status
          )
            ? task.status
            : 'unknown',
          progress: finiteNumber(task.state?.progress),
          processed: finiteNumber(task.state?.processed),
          total: finiteNumber(task.state?.total),
          created_at: finiteNumber(task.created_at),
          updated_at: finiteNumber(task.updated_at),
          has_error: Boolean(task.error),
        })),
      }
    },
  },
]
