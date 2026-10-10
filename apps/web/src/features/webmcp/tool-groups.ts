/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import type { WebMcpToolSummary } from './index'

/** Bucket key used when a tool name carries no usable area segment. */
export const FALLBACK_TOOL_GROUP = 'site'

const GROUP_LABELS: Record<string, string> = {
  site: 'Site and navigation',
  page: 'Site and navigation',
  navigate: 'Site and navigation',
  model: 'Models and pricing',
  pricing: 'Models and pricing',
  rankings: 'Rankings and status',
  status: 'Rankings and status',
  directory: 'AI directory',
  ai: 'AI directory',
  red: 'Red packets',
  security: 'Security',
  scripts: 'Scripts',
  account: 'Account',
  auth: 'Account',
  workbench: 'Workspace',
  admin: 'Administration',
  signal: 'Signal game',
  settings: 'Settings',
}

const GROUP_ALIASES: Record<string, string> = {
  home: 'site',
  about: 'site',
  developers: 'site',
  guide: 'site',
  release: 'site',
  legal: 'site',
  challenges: 'site',
  challenge: 'site',
  page: 'site',
  navigate: 'site',
  source: 'site',
  public: 'site',
  model: 'pricing',
  rankings: 'status',
  directory: 'ai',
  auth: 'account',
  onboarding: 'account',
  setup: 'account',
  shell: 'settings',
  system: 'settings',
}

/** Tools that inspect data without consequential changes. */
export function isReadOnly(tool: WebMcpToolSummary) {
  return tool.readOnly && !tool.consequential
}

/** The area segment of a tool name: lmm_red_packet_read -> "red". */
export function toolGroupKey(name: string, fallback = FALLBACK_TOOL_GROUP) {
  const parts = name.split('_')
  if (parts.length < 2) return fallback
  const key = parts[1]
  return key && key.length > 0 ? (GROUP_ALIASES[key] ?? key) : fallback
}

export function toolGroupLabel(name: string) {
  const key = toolGroupKey(name)
  return GROUP_LABELS[key] ?? 'Other tools'
}

export type ToolGroup = {
  key: string
  label: string
  tools: WebMcpToolSummary[]
}

/** Group tools by area, keeping first-seen order and filtering by query. */
export function groupTools(
  tools: WebMcpToolSummary[],
  query = '',
  translate: (key: string) => string = (key) => key
): ToolGroup[] {
  const needle = query.trim().toLowerCase()
  const matched = needle
    ? tools.filter((tool) =>
        [tool.name, translate(tool.title), translate(tool.description)]
          .join(' ')
          .toLowerCase()
          .includes(needle)
      )
    : tools
  const groups = new Map<string, ToolGroup>()
  for (const tool of matched) {
    const key = toolGroupKey(tool.name)
    const group = groups.get(key) ?? {
      key,
      label: toolGroupLabel(tool.name),
      tools: [],
    }
    group.tools.push(tool)
    groups.set(key, group)
  }
  return [...groups.values()]
}
