/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import {
  canRoundTripNumber,
  jsonObjectProperties,
  serializeJsonProperties,
} from './schema-form-json'

export type ParameterSchema = Record<string, unknown>

export function schemaObject(value: unknown): value is ParameterSchema {
  return !!value && typeof value === 'object' && !Array.isArray(value)
}

export function readParameterSchema(raw: string): ParameterSchema | null {
  try {
    const value: unknown = JSON.parse(raw)
    return schemaObject(value) ? value : null
  } catch {
    return null
  }
}

export function guidedParameters(schema: ParameterSchema | null) {
  if (
    !schema ||
    ['$ref', 'allOf', 'anyOf', 'oneOf', 'if', 'dependentSchemas'].some(
      (key) => key in schema
    ) ||
    !schemaObject(schema.properties)
  ) {
    return []
  }
  return Object.entries(schema.properties).filter(
    (entry): entry is [string, ParameterSchema] => schemaObject(entry[1])
  )
}

export function initialArguments(raw: string): string {
  const schema = readParameterSchema(raw)
  const fields = guidedParameters(schema)
  if (!fields.length) return '{}'
  const properties = jsonObjectProperties(
    jsonObjectProperties(raw).get('properties') ?? '{}'
  )
  const defaults = new Map<string, string>()
  for (const [name, field] of fields) {
    // Confirming an action always belongs to the explicit confirmation step.
    if (
      Object.hasOwn(field, 'default') &&
      !/confirm|approve|accept/i.test(name)
    ) {
      const source = properties.get(name)
      if (source) {
        const defaultValue = jsonObjectProperties(source).get('default')
        if (defaultValue !== undefined) defaults.set(name, defaultValue)
      }
    }
  }
  return serializeJsonProperties(defaults)
}

export function parameterValue(field: ParameterSchema, raw: string): unknown {
  if (raw === '') return undefined
  if (field.type === 'number' || field.type === 'integer') {
    // Keep partial and invalid numeric input visible as a string. It then fails
    // parameter validation instead of silently dropping the user's value.
    if (!/^[+-]?(?:\d+(?:\.\d+)?|\.\d+)(?:[eE][+-]?\d+)?$/.test(raw)) return raw
    const number = Number(raw)
    if (!canRoundTripNumber(raw)) return raw
    if (field.type === 'integer' && !Number.isSafeInteger(number)) return raw
    return Number.isFinite(number) ? number : raw
  }
  if (field.type === 'object' || field.type === 'array') {
    return JSON.parse(raw) as unknown
  }
  return raw
}

export function updateArgument(raw: string, key: string, value: unknown) {
  const next = jsonObjectProperties(raw)
  if (value === undefined) next.delete(key)
  else next.set(key, JSON.stringify(value))
  return serializeJsonProperties(next)
}

// This supplies immediate feedback for common fields. The backend still checks
// the complete JSON Schema, including references and composed constraints.
export function argumentIssue(raw: string, schemaRaw: string): string | null {
  let value: unknown
  try {
    value = JSON.parse(raw)
  } catch {
    return 'Enter valid JSON.'
  }
  if (!schemaObject(value)) return 'Arguments must be a JSON object.'
  const schema = readParameterSchema(schemaRaw)
  if (!schema) return null
  const required = Array.isArray(schema.required)
    ? schema.required.filter((name): name is string => typeof name === 'string')
    : []
  for (const name of required) {
    if (!Object.hasOwn(value, name)) return `Missing parameter: ${name}`
  }
  for (const [name, field] of guidedParameters(schema)) {
    if (!Object.hasOwn(value, name)) continue
    const item = value[name]
    if (
      (field.type === 'string' && typeof item !== 'string') ||
      (field.type === 'boolean' && typeof item !== 'boolean') ||
      (field.type === 'number' && typeof item !== 'number') ||
      (field.type === 'integer' && !Number.isInteger(item)) ||
      (field.type === 'array' && !Array.isArray(item)) ||
      (field.type === 'object' && !schemaObject(item))
    ) {
      return `Invalid parameter: ${name}`
    }
    if (
      Array.isArray(field.enum) &&
      !field.enum.some(
        (allowed) => JSON.stringify(allowed) === JSON.stringify(item)
      )
    ) {
      return `Invalid parameter: ${name}`
    }
    if (
      typeof item === 'number' &&
      ((typeof field.minimum === 'number' && item < field.minimum) ||
        (typeof field.maximum === 'number' && item > field.maximum))
    ) {
      return `Invalid parameter: ${name}`
    }
    if (
      typeof item === 'string' &&
      ((typeof field.minLength === 'number' &&
        Array.from(item).length < field.minLength) ||
        (typeof field.maxLength === 'number' &&
          Array.from(item).length > field.maxLength))
    ) {
      return `Invalid parameter: ${name}`
    }
  }
  return null
}
