/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
// Keep advanced JSON numbers in their original representation. Parsing an
// integer above JavaScript's safe range and serializing it changes the payload.
export function marketInvokeBody(input: {
  arguments: unknown
  arguments_json?: string
  [key: string]: unknown
}): string | Record<string, unknown> {
  const { arguments_json: raw, arguments: args, ...identity } = input
  if (raw === undefined) return { ...identity, arguments: args }
  if (new TextEncoder().encode(raw).byteLength > 128 * 1024) {
    throw new Error('Invalid arguments')
  }
  const parsed: unknown = JSON.parse(raw)
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    throw new Error('Arguments must be an object')
  }
  const fields = JSON.stringify(identity)
  return `${fields.slice(0, -1)}${fields.length > 2 ? ',' : ''}"arguments":${raw}}`
}
