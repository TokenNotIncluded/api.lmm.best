/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
// Pure helpers shared by the editor, policy parser and regression tests.
export type AssistantToolTextFields = {
  description?: string
  parameter_descriptions?: Record<string, string>
}
export type AssistantToolTextSchema = {
  description: string
  parameters: Array<{ path: string; description: string }>
  variables: Record<string, string>
}

export const ASSISTANT_TOOL_DESCRIPTION_MAX_BYTES = 4096
export const ASSISTANT_TOOL_FIELD_DESCRIPTION_MAX_BYTES = 2048
export const ASSISTANT_TOOL_TEXT_VARIABLES = [
  'max_reward_credits', 'reward_unit', 'min_level', 'max_level',
] as const
const variables = new Set<string>(ASSISTANT_TOOL_TEXT_VARIABLES)
const byteLength = (value: string) => new TextEncoder().encode(value).length
const variablePattern = () => /\{\{\s*([a-z_]+)\s*\}\}/g

function object(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? value as Record<string, unknown>
    : null
}

export function validAssistantToolDescription(value: unknown, maximum = ASSISTANT_TOOL_DESCRIPTION_MAX_BYTES): value is string {
  if (typeof value !== 'string' || value.includes('\0') || byteLength(value) > maximum) return false
  for (const match of value.matchAll(variablePattern())) {
    if (!variables.has(match[1])) return false
  }
  const remaining = value.replace(variablePattern(), '')
  return !remaining.includes('{{') && !remaining.includes('}}')
}

export function validAssistantToolParameterPath(path: string): boolean {
  if (path.includes('\0') || byteLength(path) > 512) return false
  if (path !== '' && !path.startsWith('/')) return false
  return !/~(?:[^01]|$)/.test(path)
}

export function validAssistantToolTextFields(value: Record<string, unknown>): boolean {
  if ('description' in value && value.description !== undefined && !validAssistantToolDescription(value.description)) return false
  if (!('parameter_descriptions' in value) || value.parameter_descriptions === undefined) return true
  const fields = object(value.parameter_descriptions)
  return fields !== null && Object.keys(fields).length <= 128 && Object.entries(fields).every(
    ([path, description]) => validAssistantToolParameterPath(path) &&
      validAssistantToolDescription(description, ASSISTANT_TOOL_FIELD_DESCRIPTION_MAX_BYTES)
  )
}

export function validAssistantToolTextSchema(value: unknown): value is AssistantToolTextSchema {
  const schema = object(value)
  if (!schema || typeof schema.description !== 'string' || !Array.isArray(schema.parameters)) return false
  const names = new Set<string>()
  for (const entry of schema.parameters) {
    const field = object(entry)
    if (!field || typeof field.path !== 'string' || typeof field.description !== 'string' ||
      !validAssistantToolParameterPath(field.path) || names.has(field.path)) return false
    names.add(field.path)
  }
  const values = object(schema.variables)
  return values !== null && Object.entries(values).every(([name, value]) => variables.has(name) && typeof value === 'string')
}

export function renderAssistantToolDescription(text: string, values: Record<string, string>): string {
  return text.replace(variablePattern(), (_match, name: string) =>
    variables.has(name) && Object.hasOwn(values, name) ? values[name] : 'unavailable'
  )
}

// A description edit must not be lost when another administrator changes a
// level rule. Independent paths merge separately; a local text edit wins a
// same-field conflict for explicit review by the existing settings save flow.
export function mergeAssistantToolText(
  before?: AssistantToolTextFields,
  local?: AssistantToolTextFields,
  remote?: AssistantToolTextFields,
): AssistantToolTextFields {
  const result: AssistantToolTextFields = {}
  const description = local?.description !== before?.description ? local?.description : remote?.description
  if (description !== undefined) result.description = description
  const paths = new Set([
    ...Object.keys(before?.parameter_descriptions ?? {}),
    ...Object.keys(local?.parameter_descriptions ?? {}),
    ...Object.keys(remote?.parameter_descriptions ?? {}),
  ])
  const fields: Array<[string, string]> = []
  for (const path of [...paths].sort()) {
    const original = before?.parameter_descriptions?.[path]
    const draft = local?.parameter_descriptions?.[path]
    const value = draft !== original ? draft : remote?.parameter_descriptions?.[path]
    if (value !== undefined) fields.push([path, value])
  }
  if (fields.length) result.parameter_descriptions = Object.fromEntries(fields)
  return result
}
