/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import type { LogOtherData } from '../types'

export type ResponseModelObservation = NonNullable<
  LogOtherData['response_model']
>

// Keep this allowlist aligned with the Go and Rust relay comparison rules.
const compatibleSuffix =
  /^-(?:\d{4}-\d{2}-\d{2}|\d{8}|latest|preview(?:-(?:\d{2}-\d{2}|\d{2}-\d{4}|\d{4}-\d{2}-\d{2}|\d{8}))?)$/

function canonicalModelName(model: string): string {
  return model.trim().toLowerCase().split('/').at(-1) ?? ''
}

/** Validate historical metadata without trusting a stored mismatch flag. */
export function getResponseModelObservation(
  value: unknown
): ResponseModelObservation | undefined {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return undefined
  }
  const observation = value as Record<string, unknown>
  if (
    typeof observation.requested_model !== 'string' ||
    typeof observation.upstream_model !== 'string' ||
    typeof observation.returned_model !== 'string' ||
    observation.returned_model.trim() === ''
  ) {
    return undefined
  }
  return {
    requested_model: observation.requested_model,
    upstream_model: observation.upstream_model,
    returned_model: observation.returned_model,
  }
}

/**
 * Compare raw provider names with the requested and selected model names.
 * Provider paths and case are ignored. Only explicit date, latest and preview
 * suffixes are compatible; model variants such as gpt-4o-mini remain distinct.
 * A shorter returned name does not establish the requested snapshot.
 */
export function isResponseModelMismatch(
  observation: ResponseModelObservation | undefined
): boolean {
  if (!observation) return false
  if (!observation.returned_model.trim()) return false
  const returned = canonicalModelName(observation.returned_model)
  return ![observation.requested_model, observation.upstream_model].some(
    (model) => {
      const expected = canonicalModelName(model)
      if (!expected) return false
      return (
        returned === expected ||
        (returned.startsWith(expected) &&
          compatibleSuffix.test(returned.slice(expected.length)))
      )
    }
  )
}
