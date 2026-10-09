/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import * as z from 'zod'

export const DEFAULT_ASSISTANT_TOOL_POLICY =
  '{"version":1,"groups":{},"tools":{}}'

export type AssistantToolPolicy = {
  version: 1
  groups: Record<string, boolean>
  tools: Record<string, boolean>
  rules?: Record<string, AssistantToolRule>
}
export type AssistantToolRule = {
  min_level: number
  max_level: number
  discount_percent_by_level?: Record<string, number>
  market_service_ids?: string[]
  default_visibility?: 'user' | 'admin'
}

export type AssistantToolEffect =
  | 'read_only'
  | 'navigation'
  | 'confirmation'
  | 'server_guarded'
export type AssistantToolAccess =
  | 'user'
  | 'l0'
  | 'l1'
  | 'admin'
  | 'root'
  | 'mixed'
export type AssistantCatalogTool = {
  name: string
  label: string
  description: string
  effect: AssistantToolEffect
  access: AssistantToolAccess
}
export type AssistantCatalogGroup = {
  id: string
  label: string
  tools: AssistantCatalogTool[]
}

const effects = new Set([
  'read_only',
  'navigation',
  'confirmation',
  'server_guarded',
])
const accessLevels = new Set(['user', 'l0', 'l1', 'admin', 'root', 'mixed'])
const identifier = /^[a-z][a-z0-9_]{0,127}$/

function record(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null
}

// JSON.parse discards duplicate keys. Detect them before accepting a policy,
// so the displayed switches cannot disagree with the server's strict parser.
function hasDuplicateKeys(raw: string): boolean {
  const tokens =
    /\s*("(?:\\[\s\S]|[^"\\])*"|[{}[\],:]|true|false|null|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)/gy
  const stack: Array<{ keys: Set<string>; nextKey: boolean } | null> = []
  let token: RegExpExecArray | null
  while ((token = tokens.exec(raw)) !== null) {
    const value = token[1]
    const top = stack.at(-1)
    if (value === '{') stack.push({ keys: new Set(), nextKey: true })
    else if (value === '[') stack.push(null)
    else if (value === '}' || value === ']') stack.pop()
    else if (value === ',' && top) top.nextKey = true
    else if (value === ':' && top) top.nextKey = false
    else if (value.startsWith('"') && top?.nextKey) {
      const key = JSON.parse(value) as string
      if (top.keys.has(key)) return true
      top.keys.add(key)
      top.nextKey = false
    }
  }
  return false
}

function booleanMap(value: unknown): Record<string, boolean> | null {
  if (value === undefined) return {}
  const source = record(value)
  if (!source) return null
  const entries = Object.entries(source)
  if (
    entries.some(
      ([key, item]) => !identifier.test(key) || typeof item !== 'boolean'
    )
  ) {
    return null
  }
  return Object.fromEntries(entries) as Record<string, boolean>
}

export function parseAssistantToolPolicy(
  raw: string
): AssistantToolPolicy | null {
  if (new TextEncoder().encode(raw).length > 16_384) return null
  if (!raw.trim()) return { version: 1, groups: {}, tools: {} }
  try {
    const value = record(JSON.parse(raw))
    if (
      !value ||
      value.version !== 1 ||
      Object.keys(value).some(
        (key) => !['version', 'groups', 'tools', 'rules'].includes(key)
      ) ||
      hasDuplicateKeys(raw)
    ) {
      return null
    }
    const groups = booleanMap(value.groups)
    const tools = booleanMap(value.tools)
    const rules = parseAssistantToolRules(value.rules)
    if (!groups || !tools || rules === null) return null
    return {
      version: 1,
      groups,
      tools,
      ...(rules === undefined ? {} : { rules }),
    }
  } catch {
    return null
  }
}

export const assistantToolPolicySchema = z
  .string()
  .refine((raw) => parseAssistantToolPolicy(raw) !== null, {
    message: 'Invalid assistant tool policy',
  })

export function parseAssistantToolCatalog(
  payload: unknown
): AssistantCatalogGroup[] | null {
  const envelope = record(payload)
  const data = record(envelope?.data)
  if (envelope?.success !== true || !Array.isArray(data?.groups)) return null
  const groupIDs = new Set<string>()
  const toolNames = new Set<string>()
  const groups: AssistantCatalogGroup[] = []
  for (const source of data.groups) {
    const group = record(source)
    if (
      !group ||
      typeof group.id !== 'string' ||
      !identifier.test(group.id) ||
      groupIDs.has(group.id) ||
      typeof group.label !== 'string' ||
      !group.label.trim() ||
      !Array.isArray(group.tools)
    ) {
      return null
    }
    groupIDs.add(group.id)
    const tools: AssistantCatalogTool[] = []
    for (const sourceTool of group.tools) {
      const tool = record(sourceTool)
      if (
        !tool ||
        typeof tool.name !== 'string' ||
        !identifier.test(tool.name) ||
        toolNames.has(tool.name) ||
        typeof tool.label !== 'string' ||
        !tool.label.trim() ||
        typeof tool.description !== 'string' ||
        typeof tool.effect !== 'string' ||
        !effects.has(tool.effect) ||
        typeof tool.access !== 'string' ||
        !accessLevels.has(tool.access)
      ) {
        return null
      }
      toolNames.add(tool.name)
      tools.push(tool as AssistantCatalogTool)
    }
    groups.push({ id: group.id, label: group.label, tools })
  }
  return groups
}

export function assistantPolicyMatchesCatalog(
  policy: AssistantToolPolicy,
  groups: AssistantCatalogGroup[]
): boolean {
  const groupIDs = new Set(groups.map((group) => group.id))
  const toolNames = new Set(
    groups.flatMap((group) => group.tools.map((tool) => tool.name))
  )
  return (
    Object.keys(policy.groups).every((id) => groupIDs.has(id)) &&
    Object.keys(policy.tools).every((name) => toolNames.has(name)) &&
    Object.entries(policy.rules ?? {}).every(([name, rule]) => {
      const tool = groups
        .flatMap((group) => group.tools)
        .find((tool) => tool.name === name)
      if (!tool) return false
      const defaults = defaultAssistantToolRule(tool)
      return (
        rule.min_level >= defaults.min_level &&
        rule.max_level <= defaults.max_level
      )
    })
  )
}

export function isAssistantToolEnabled(
  policy: AssistantToolPolicy,
  group: string,
  tool: string
): boolean {
  return policy.groups[group] !== false && policy.tools[tool] !== false
}

export function updateAssistantToolPolicy(
  policy: AssistantToolPolicy,
  scope: 'groups' | 'tools',
  name: string,
  enabled: boolean
): string {
  return JSON.stringify({
    ...policy,
    version: 1,
    groups: {
      ...policy.groups,
      ...(scope === 'groups' ? { [name]: enabled } : {}),
    },
    tools: {
      ...policy.tools,
      ...(scope === 'tools' ? { [name]: enabled } : {}),
    },
  })
}

/** Merge independent settings; absence inherits enabled, rather than acting as a deletion. */
export function mergeAssistantToolPolicy(
  previous: string,
  draft: string,
  incoming: string
): string {
  if (draft === previous) return incoming
  if (incoming === previous) return draft
  const before = parseAssistantToolPolicy(previous)
  const local = parseAssistantToolPolicy(draft)
  const remote = parseAssistantToolPolicy(incoming)
  // Keep malformed local input visible and unsaved, rather than enabling defaults.
  if (!before || !local || !remote) return draft
  const mergeMap = (scope: 'groups' | 'tools') => {
    const names = new Set([
      ...Object.keys(before[scope]),
      ...Object.keys(local[scope]),
      ...Object.keys(remote[scope]),
    ])
    const merged: Record<string, boolean> = {}
    for (const name of [...names].sort()) {
      const baselineEnabled = before[scope][name] !== false
      const localEnabled = local[scope][name] !== false
      const remoteEnabled = remote[scope][name] !== false
      const localChanged = localEnabled !== baselineEnabled
      const remoteChanged = remoteEnabled !== baselineEnabled
      let choice = remote[scope][name]
      if (localChanged && remoteChanged) {
        // A concurrent disable cannot be replaced with an enabled choice.
        choice = localEnabled && remoteEnabled
      } else if (localChanged) {
        choice = local[scope][name]
      }
      if (choice !== undefined) merged[name] = choice
    }
    return merged
  }
  return JSON.stringify({
    version: 1,
    groups: mergeMap('groups'),
    tools: mergeMap('tools'),
    ...mergeRuleMaps(before.rules, local.rules, remote.rules),
  })
}

// Defaults mirror the catalogue's access floor. Policy ranges can tighten a
// capability, but cannot turn a member into an administrator.
export function defaultAssistantToolRule(
  tool: AssistantCatalogTool
): AssistantToolRule {
  return {
    min_level:
      tool.access === 'root'
        ? 6
        : tool.access === 'admin'
          ? 5
          : tool.access === 'l1'
            ? 1
            : 0,
    max_level: tool.access === 'l0' ? 0 : 6,
  }
}
export function assistantToolRule(
  policy: AssistantToolPolicy,
  tool: AssistantCatalogTool
): AssistantToolRule {
  return policy.rules?.[tool.name] ?? defaultAssistantToolRule(tool)
}
export function updateAssistantToolRule(
  policy: AssistantToolPolicy,
  name: string,
  rule?: AssistantToolRule
): string {
  const rules = { ...policy.rules }
  if (rule) rules[name] = rule
  else delete rules[name]
  const { rules: _previous, ...rest } = policy
  return JSON.stringify({
    ...rest,
    ...(Object.keys(rules).length ? { rules } : {}),
  })
}
export function weeklyDiscountLimit(
  rule: AssistantToolRule,
  level: number
): number {
  return (
    rule.discount_percent_by_level?.[String(level)] ?? (level >= 5 ? 0 : 10)
  )
}
function parseAssistantToolRules(
  value: unknown
): Record<string, AssistantToolRule> | null | undefined {
  if (value === undefined) return undefined
  const rules = record(value)
  if (!rules) return null
  for (const [name, source] of Object.entries(rules)) {
    const rule = record(source)
    if (
      !identifier.test(name) ||
      !rule ||
      !Number.isInteger(rule.min_level) ||
      !Number.isInteger(rule.max_level) ||
      Number(rule.min_level) < 0 ||
      Number(rule.max_level) > 6 ||
      Number(rule.min_level) > Number(rule.max_level)
    ) {
      return null
    }
    if (
      Object.keys(rule).some(
        (key) =>
          ![
            'min_level',
            'max_level',
            'discount_percent_by_level',
            'market_service_ids',
            'default_visibility',
          ].includes(key)
      )
    ) {
      return null
    }
    if ('discount_percent_by_level' in rule) {
      const limits = record(rule.discount_percent_by_level)
      if (
        name !== 'prepare_weekly_discount' ||
        !limits ||
        Object.entries(limits).some(
          ([level, limit]) =>
            !/^[0-6]$/.test(level) ||
            !Number.isInteger(limit) ||
            Number(limit) < 0 ||
            Number(limit) > 99 ||
            (Number(level) >= 5 && limit !== 0)
        )
      ) {
        return null
      }
    }
    if ('market_service_ids' in rule) {
      const ids = rule.market_service_ids
      if (
        name !== 'call_market_tool' ||
        !Array.isArray(ids) ||
        ids.length > 64 ||
        ids.some(
          (id) =>
            typeof id !== 'string' ||
            !id.trim() ||
            id.trim() !== id ||
            new TextEncoder().encode(id).length > 128
        ) ||
        new Set(ids).size !== ids.length
      ) {
        return null
      }
    }
    if (
      'default_visibility' in rule &&
      (name !== 'create_site_issue' ||
        !['user', 'admin'].includes(String(rule.default_visibility)))
    ) {
      return null
    }
  }
  return rules as Record<string, AssistantToolRule>
}
function stableValue(value: unknown): string {
  if (Array.isArray(value)) return JSON.stringify([...value].sort())
  if (value && typeof value === 'object') {
    return JSON.stringify(
      Object.fromEntries(
        Object.entries(value)
          .sort(([a], [b]) => a.localeCompare(b))
          .map(([key, item]) => [key, stableValue(item)])
      )
    )
  }
  return JSON.stringify(value) ?? ''
}
function mergeRuleMaps(
  before: AssistantToolPolicy['rules'],
  local: AssistantToolPolicy['rules'],
  remote: AssistantToolPolicy['rules']
): Pick<AssistantToolPolicy, 'rules'> {
  const rules: Record<string, AssistantToolRule> = {}
  const names = new Set([
    ...Object.keys(before ?? {}),
    ...Object.keys(local ?? {}),
    ...Object.keys(remote ?? {}),
  ])
  for (const name of [...names].sort()) {
    const b = before?.[name],
      l = local?.[name],
      r = remote?.[name]
    const localChanged = stableValue(l) !== stableValue(b),
      remoteChanged = stableValue(r) !== stableValue(b)
    const chosen = localChanged ? l : r
    if (!localChanged || !remoteChanged || stableValue(l) === stableValue(r)) {
      if (chosen) rules[name] = chosen
      continue
    }
    // Concurrent edits to one rule use the intersection. The save flow pauses
    // for review; an empty level intersection is deliberately invalid.
    const merged: AssistantToolRule = {
      min_level: Math.max(l?.min_level ?? 0, r?.min_level ?? 0),
      max_level: Math.min(l?.max_level ?? 6, r?.max_level ?? 6),
    }
    if (name === 'prepare_weekly_discount') {
      merged.discount_percent_by_level = Object.fromEntries(
        Array.from({ length: 7 }, (_, level) => [
          String(level),
          Math.min(
            weeklyDiscountLimit(l ?? { min_level: 0, max_level: 6 }, level),
            weeklyDiscountLimit(r ?? { min_level: 0, max_level: 6 }, level)
          ),
        ])
      )
    }
    if (name === 'call_market_tool') {
      merged.market_service_ids = (l?.market_service_ids ?? [])
        .filter((id) => r?.market_service_ids?.includes(id))
        .sort()
    }
    if (name === 'create_site_issue') {
      merged.default_visibility =
        l?.default_visibility === 'admin' || r?.default_visibility === 'admin'
          ? 'admin'
          : 'user'
    }
    rules[name] = merged
  }
  return Object.keys(rules).length ? { rules } : {}
}
