/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import type { DraftInput } from './api'

function rejectImpreciseNumbers(value: unknown): void {
  if (typeof value === 'number') {
    if (
      !Number.isFinite(value) ||
      (Number.isInteger(value) && !Number.isSafeInteger(value))
    ) {
      throw new Error('Schema requires its original JSON representation')
    }
  } else if (Array.isArray(value)) {
    value.forEach(rejectImpreciseNumbers)
  } else if (value && typeof value === 'object') {
    Object.values(value).forEach(rejectImpreciseNumbers)
  }
}

// Parsed schema copies are for presentation only. The original JSON is sent as
// an object value so large bounds/defaults retain their exact numeric spelling.
export function marketSchemaJSON(
  schema: Record<string, unknown> | string
): string {
  if (typeof schema !== 'string') rejectImpreciseNumbers(schema)
  const raw = typeof schema === 'string' ? schema : JSON.stringify(schema)
  const parsed: unknown = JSON.parse(raw)
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    throw new Error('Schema must be a JSON object')
  }
  return raw
}

export function marketDraftBody(input: DraftInput): string {
  const { tools, ...identity } = input
  const definitions = tools.map((tool) => {
    const {
      input_schema: inputSchema,
      output_schema: outputSchema,
      input_schema_json: inputRaw,
      output_schema_json: outputRaw,
      ...metadata
    } = tool
    const output = outputRaw ?? outputSchema
    const fields = JSON.stringify(metadata)
    const schema = marketSchemaJSON(inputRaw ?? inputSchema)
    return `${fields.slice(0, -1)}${fields.length > 2 ? ',' : ''}"input_schema":${schema}${output !== undefined && output !== '' ? `,"output_schema":${marketSchemaJSON(output)}` : ''}}`
  })
  const fields = JSON.stringify(identity)
  return `${fields.slice(0, -1)}${fields.length > 2 ? ',' : ''}"tools":[${definitions.join(',')}]}`
}
