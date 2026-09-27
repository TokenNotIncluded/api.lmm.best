/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

type ToolExecuteOptions = { signal: AbortSignal }

export type ModelContextTool = {
  name: string
  title?: string
  description: string
  inputSchema: Record<string, unknown>
  annotations?: {
    readOnlyHint?: boolean
    consequentialHint?: boolean
    untrustedContentHint?: boolean
  }
  execute: (
    input: Record<string, unknown>,
    options: ToolExecuteOptions
  ) => Promise<unknown>
}

export type WebMcpRouter = {
  navigate: (options: {
    to: string
    search?: Record<string, unknown>
  }) => Promise<unknown>
  subscribe: (event: 'onResolved', listener: () => void) => () => void
}

/** Shared context handed to every area tool factory. */
export type WebMcpToolContext = { router: WebMcpRouter }

/** An area module contributes tools for the pages it owns. */
export type WebMcpToolFactory = (
  context: WebMcpToolContext
) => ModelContextTool[]

export const EMPTY_INPUT_SCHEMA = {
  type: 'object',
  properties: {},
  additionalProperties: false,
} as const

export function ensureObject(value: unknown): Record<string, unknown> {
  if (value === undefined || value === null) return {}
  if (typeof value !== 'object' || Array.isArray(value)) {
    throw new TypeError('Tool input must be an object')
  }
  return value as Record<string, unknown>
}

export function ensureNotAborted(signal: AbortSignal) {
  if (signal.aborted) {
    throw signal.reason ?? new DOMException('Aborted', 'AbortError')
  }
}

export function optionalString(
  input: Record<string, unknown>,
  key: string,
  max = 256
): string | undefined {
  const value = input[key]
  if (value === undefined || value === null || value === '') return undefined
  if (typeof value !== 'string' || value.length > max) {
    throw new TypeError(`${key} must be a string of at most ${max} characters`)
  }
  return value.trim()
}

export function requiredString(
  input: Record<string, unknown>,
  key: string,
  max = 256
): string {
  const value = optionalString(input, key, max)
  if (!value) throw new TypeError(`${key} is required`)
  return value
}

export function optionalInteger(
  input: Record<string, unknown>,
  key: string,
  min: number,
  max: number
): number | undefined {
  const value = input[key]
  if (value === undefined || value === null) return undefined
  if (
    typeof value !== 'number' ||
    !Number.isInteger(value) ||
    value < min ||
    value > max
  ) {
    throw new TypeError(`${key} must be an integer from ${min} to ${max}`)
  }
  return value
}

export function optionalEnum<T extends string>(
  input: Record<string, unknown>,
  key: string,
  values: readonly T[]
): T | undefined {
  const value = input[key]
  if (value === undefined || value === null) return undefined
  if (typeof value !== 'string' || !values.includes(value as T)) {
    throw new TypeError(`${key} must be one of: ${values.join(', ')}`)
  }
  return value as T
}

export function optionalBoolean(
  input: Record<string, unknown>,
  key: string
): boolean | undefined {
  const value = input[key]
  if (value === undefined || value === null) return undefined
  if (typeof value !== 'boolean') throw new TypeError(`${key} must be boolean`)
  return value
}

/** Throws unless a user is signed in; returns the stored user. */
export function requireSignedIn() {
  const user = useAuthStore.getState().auth.user
  if (!user) {
    throw new Error('Sign in first. Use lmm_navigate with path "/sign-in".')
  }
  return user
}

export function requireAdmin() {
  const user = requireSignedIn()
  if (typeof user.role !== 'number' || user.role < ROLE.ADMIN) {
    throw new Error('This tool requires an administrator account')
  }
  return user
}

/** Throws unless the current tab is on one of the given path prefixes. */
export function requirePath(prefixes: readonly string[], hint: string) {
  const path = typeof window === 'undefined' ? '' : window.location.pathname
  if (
    !prefixes.some(
      (prefix) =>
        path === prefix || path.startsWith(`${prefix.replace(/\/$/, '')}/`)
    )
  ) {
    throw new Error(`Open ${hint} with lmm_navigate first`)
  }
}

/** Clip long text so tool results stay small and predictable. */
export function clip(value: unknown, max = 280): string | null {
  if (value === undefined || value === null) return null
  const text = String(value)
  return text.length > max ? `${text.slice(0, max - 1)}…` : text
}
