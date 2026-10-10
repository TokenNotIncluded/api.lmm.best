/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/

// These tokens are read only after JSON.parse has validated the document.
// Retain the source of every value instead of converting numbers to doubles.
function jsonTokens(raw: string) {
  return [
    ...raw.matchAll(
      /"(?:[^"\\]|\\.)*"|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?|true|false|null|[{}[\]:,]/g
    ),
  ]
}

export function jsonObjectProperties(raw: string): Map<string, string> {
  const value: unknown = JSON.parse(raw)
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error('Arguments must be an object')
  }
  const tokens = jsonTokens(raw)
  const entries = new Map<string, string>()
  let cursor = 1
  while (tokens[cursor]?.[0] !== '}') {
    const key = JSON.parse(tokens[cursor][0]) as string
    cursor += 2 // Property name and colon.
    const start = tokens[cursor].index
    let depth = 0
    do {
      const token = tokens[cursor++][0]
      if (token === '{' || token === '[') depth++
      else if (token === '}' || token === ']') depth--
    } while (depth > 0)
    const last = tokens[cursor - 1]
    entries.set(key, raw.slice(start, last.index + last[0].length))
    if (tokens[cursor][0] === ',') cursor++
  }
  return entries
}

export function serializeJsonProperties(properties: Map<string, string>) {
  if (!properties.size) return '{}'
  return `{\n${[...properties].map(([key, raw]) => `  ${JSON.stringify(key)}: ${raw}`).join(',\n')}\n}`
}

function normalizedNumber(raw: string) {
  const match = /^([+-]?)(\d*)(?:\.(\d*))?(?:[eE]([+-]?\d+))?$/.exec(raw)
  if (!match) return null
  const fraction = match[3] ?? ''
  let digits = `${match[2]}${fraction}`.replace(/^0+/, '')
  if (!digits) return '0'
  const trimmed = digits.replace(/0+$/, '')
  const exponent =
    BigInt(match[4] ?? '0') -
    BigInt(fraction.length) +
    BigInt(digits.length - trimmed.length)
  digits = trimmed
  return `${match[1] === '-' ? '-' : ''}${digits}e${exponent}`
}

export function canRoundTripNumber(raw: string) {
  const number = Number(raw)
  return (
    Number.isFinite(number) &&
    (!Number.isInteger(number) || Number.isSafeInteger(number)) &&
    normalizedNumber(raw) === normalizedNumber(String(number))
  )
}

export function hasUnsafeJsonNumber(raw: string) {
  return jsonTokens(raw).some(
    ([token]) => /^[\d-]/.test(token) && !canRoundTripNumber(token)
  )
}
