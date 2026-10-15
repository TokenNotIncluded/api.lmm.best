/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import type { ZodType } from 'zod'

import { api, type ApiRequestConfig } from '@/lib/api'

import {
  configSchema,
  hashHoneypotConfig,
  operationIDSchema,
  policiesSchema,
  previewMatchesRequest,
  previewRequestSchema,
  previewSchema,
  resultSchema,
  userListSchema,
  userViewSchema,
  type HoneypotConfig,
  type HoneypotPreview,
  type HoneypotPreviewRequest,
  type HoneypotResult,
} from './contract'

const prefix = '/core/v1/admin/abuse'
export type HoneypotScope = NonNullable<ApiRequestConfig['authScope']>
export type HoneypotFailureKind =
  | 'invalid'
  | 'unavailable'
  | 'forbidden'
  | 'conflict'
  | 'expired'
  | 'unknown'
  | 'not_found'

export class HoneypotFailure extends Error {
  constructor(
    readonly kind: HoneypotFailureKind,
    readonly operationID?: string
  ) {
    // Backend text is intentionally not forwarded: diagnostics may contain private data.
    super(`Honeypot operation: ${kind}`)
  }
}

const options = (authScope: HoneypotScope): ApiRequestConfig => ({
  authScope,
  disableDuplicate: true,
  skipBusinessError: true,
  skipErrorHandler: true,
  timeout: 10000,
})
function statusOf(error: unknown): number | undefined {
  if (typeof error !== 'object' || error === null || !('response' in error)) {
    return undefined
  }
  const response = error.response
  return typeof response === 'object' &&
    response !== null &&
    'status' in response &&
    typeof response.status === 'number'
    ? response.status
    : undefined
}
function failure(error: unknown, operationID?: string): HoneypotFailure {
  if (error instanceof HoneypotFailure) return error
  const status = statusOf(error)
  const kind =
    status === 409
      ? 'conflict'
      : status === 410
        ? 'expired'
        : status === 401 || status === 403
          ? 'forbidden'
          : status === 404
            ? 'not_found'
            : status === 400 || status === 429
              ? 'invalid'
              : operationID
                ? 'unknown'
                : 'unavailable'
  return new HoneypotFailure(kind, operationID)
}
async function read<T>(
  path: string,
  schema: ZodType<T>,
  scope: HoneypotScope,
  params?: Record<string, string | number>
) {
  try {
    const response = await api.get<unknown>(`${prefix}${path}`, {
      ...options(scope),
      params,
    })
    return schema.parse(response.data)
  } catch (error) {
    throw failure(error)
  }
}

export const getHoneypotPolicies = (scope: HoneypotScope) =>
  read('/policies', policiesSchema, scope)
export const getHoneypotUser = (id: number, scope: HoneypotScope) =>
  read(`/users/${id}`, userViewSchema, scope)
export const getHoneypotUsers = (
  scope: HoneypotScope,
  params: { after: number; limit: number; query: string; state: string }
) => read('/users', userListSchema, scope, params)
export async function getHoneypotOperation(id: string, scope: HoneypotScope) {
  const operationID = operationIDSchema.parse(id)
  try {
    const response = await api.get<unknown>(
      `${prefix}/operations/${encodeURIComponent(operationID)}`,
      options(scope)
    )
    return resultSchema.parse(response.data)
  } catch (error) {
    throw failure(error, operationID)
  }
}

export async function previewHoneypotUsers(
  request: HoneypotPreviewRequest,
  scope: HoneypotScope
) {
  const body = previewRequestSchema.parse(request)
  try {
    const response = await api.post<unknown>(
      `${prefix}/users/preview`,
      body,
      options(scope)
    )
    const preview = previewSchema.parse(response.data)
    if (!previewMatchesRequest(preview, body)) {
      throw new HoneypotFailure('invalid')
    }
    return preview
  } catch (error) {
    throw failure(error)
  }
}

export function userResultMatchesPreview(
  result: HoneypotResult,
  preview: HoneypotPreview
): boolean {
  const targets = new Map(
    preview.targets.map((target) => [target.user_id, target])
  )
  return (
    result.kind === 'users' &&
    result.operation_id === preview.operation_id &&
    result.policy_revision === preview.policy_revision &&
    result.users.length === targets.size &&
    new Set(result.users.map((user) => user.user_id)).size === targets.size &&
    result.users.every((user) => {
      const target = targets.get(user.user_id)
      return (
        target !== undefined &&
        user.revision === target.expected_revision + 1 &&
        user.marked === target.after.marked &&
        user.policy_id === target.after.policy_id &&
        user.active === target.after.active
      )
    })
  )
}

export async function commitHoneypotUsers(
  preview: HoneypotPreview,
  scope: HoneypotScope
) {
  try {
    const response = await api.post<unknown>(
      `${prefix}/users/commit`,
      {
        operation_id: preview.operation_id,
        preview_id: preview.preview_id,
        preview_hash: preview.preview_hash,
      },
      options(scope)
    )
    const result = resultSchema.parse(response.data)
    if (!userResultMatchesPreview(result, preview)) {
      throw new HoneypotFailure('unknown', preview.operation_id)
    }
    return result
  } catch (error) {
    throw failure(error, preview.operation_id)
  }
}

export async function saveHoneypotPolicies(
  request: {
    operation_id: string
    reason: string
    expected_revision: number
    config: HoneypotConfig
  },
  scope: HoneypotScope
) {
  const config = configSchema.parse(request.config)
  const configHash = await hashHoneypotConfig(config)
  try {
    const response = await api.put<unknown>(
      `${prefix}/policies`,
      { ...request, config },
      options(scope)
    )
    const result = resultSchema.parse(response.data)
    if (
      result.kind !== 'policies' ||
      result.operation_id !== request.operation_id ||
      result.policy_revision !== request.expected_revision + 1 ||
      result.users.length !== 0
    ) {
      throw new HoneypotFailure('unknown', request.operation_id)
    }
    if (result.applied_config_hash !== configHash) {
      throw new HoneypotFailure('conflict', request.operation_id)
    }
    return result
  } catch (error) {
    throw failure(error, request.operation_id)
  }
}
