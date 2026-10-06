/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import type { ToolInput } from './api'
import { marketSchemaJSON } from './draft-body'

export type EditableTools = {
  tools: ToolInput[]
  selected: string[]
  prices: Record<string, string>
}

export type ToolDefinitionChanges = {
  added: string[]
  removed: string[]
  changed: string[]
  endpointChanged: boolean
}

function normalizedDefinition(value: unknown): string {
  return JSON.stringify(value, (_key, item: unknown) => {
    if (!item || typeof item !== 'object' || Array.isArray(item)) return item
    return Object.fromEntries(
      Object.entries(item).sort(([left], [right]) => left.localeCompare(right))
    )
  })
}

function exactSchema(
  value: ToolInput['input_schema'] | undefined,
  raw: string | undefined
): ToolInput['input_schema'] {
  const source = raw ?? value
  if (source === undefined) throw new Error('Missing tool schema')
  // Validation may parse a read-only copy. Raw JSON is never replaced with
  // that copy; change detection and submission keep its exact numeric spelling.
  const exact = marketSchemaJSON(source)
  return typeof source === 'string' ? exact : source
}

export function exactEditorTool(tool: ToolInput): ToolInput {
  const { input_schema_json, output_schema_json, ...definition } = tool
  return {
    ...definition,
    input_schema: exactSchema(tool.input_schema, input_schema_json),
    ...(tool.output_schema !== undefined || output_schema_json !== undefined
      ? { output_schema: exactSchema(tool.output_schema, output_schema_json) }
      : {}),
  }
}

// A refresh updates the remote definition, not the owner's publication policy.
export function refreshToolDefinitions(
  previous: EditableTools,
  discovered: ToolInput[],
  options: { firstDiscovery: boolean; endpointChanged: boolean }
): EditableTools & { changes: ToolDefinitionChanges } {
  const exactDefinitions = discovered.map(exactEditorTool)
  const previousByName = new Map(
    previous.tools.map((tool) => [tool.name, tool])
  )
  const discoveredNames = new Set(discovered.map((tool) => tool.name))
  const changes: ToolDefinitionChanges = {
    added: [],
    removed: previous.tools
      .filter((tool) => !discoveredNames.has(tool.name))
      .map((tool) => tool.name),
    changed: [],
    endpointChanged: options.endpointChanged,
  }
  const tools = exactDefinitions.map((tool) => {
    const existing = previousByName.get(tool.name)
    if (!existing) {
      changes.added.push(tool.name)
      return { ...tool, permissions: [...tool.permissions] }
    }
    if (
      normalizedDefinition({
        description: existing.description,
        input_schema: existing.input_schema,
        output_schema: existing.output_schema,
      }) !==
      normalizedDefinition({
        description: tool.description,
        input_schema: tool.input_schema,
        output_schema: tool.output_schema,
      })
    ) {
      changes.changed.push(tool.name)
    }
    return {
      ...tool,
      price_quota: existing.price_quota,
      billing_mode: existing.billing_mode,
      input_token_price_quota: existing.input_token_price_quota,
      max_input_tokens: existing.max_input_tokens,
      billing_rules: existing.billing_rules,
      available_metering_metrics: options.endpointChanged
        ? []
        : existing.available_metering_metrics,
      permissions: [...existing.permissions],
    }
  })
  return {
    tools,
    selected: options.firstDiscovery
      ? tools.map((tool) => tool.name)
      : previous.selected.filter((name) => discoveredNames.has(name)),
    prices: Object.fromEntries(
      tools.map((tool) => [tool.name, previous.prices[tool.name] ?? '0'])
    ),
    changes,
  }
}

type CredentialMode = 'none' | 'bearer' | 'api_key'
export type StoredEditorCredentials = {
  mode: CredentialMode
  configured: boolean
}
type CredentialChoice = {
  mode: CredentialMode
  secret: string
  stored: StoredEditorCredentials | undefined
  storedVersionID: string | undefined
  sameEndpoint: boolean
}

// Only this short-lived request contains a replacement secret. Never put it in
// a draft, query key, cached mutation, or generated client configuration.
export function editorCredentialWrite(choice: CredentialChoice): {
  mode: CredentialMode
  secret?: string
  copy_from_version_id?: string
} {
  if (choice.mode === 'none') return { mode: 'none' }
  if (choice.secret) {
    if (
      new TextEncoder().encode(choice.secret).byteLength > 4096 ||
      /\p{Cc}/u.test(choice.secret) ||
      (choice.mode === 'bearer' && /\p{White_Space}/u.test(choice.secret))
    ) {
      throw new Error('Invalid credential')
    }
    return { mode: choice.mode, secret: choice.secret }
  }
  if (
    choice.sameEndpoint &&
    choice.storedVersionID &&
    choice.stored?.configured &&
    choice.stored.mode === choice.mode
  ) {
    return {
      mode: choice.mode,
      copy_from_version_id: choice.storedVersionID,
    }
  }
  throw new Error('Credential required')
}
