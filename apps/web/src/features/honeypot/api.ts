/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import type { ZodType } from 'zod'

import { api, type ApiRequestConfig } from '@/lib/api'

import {
  configSchema,
  hashHoneypotConfig,
  honeypotUserIDSchema,
  operationIDSchema,
  policiesSchema,
  previewMatchesRequest,
  previewRequestSchema,
  previewSchema,
  resultSchema,
  userResultSchema,
  userListSchema,
  userDetailSchema,
  type HoneypotConfig,
  type HoneypotPolicies,
  samePolicyCapabilities,
  validatePolicyCapabilities,
  type HoneypotPreview,
  type HoneypotPreviewRequest,
  type HoneypotResult,
} from './contract'

const prefix = '/core/v1/admin/abuse'
const usersPrefix = '/core/v2/admin/abuse'
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

function coreErrorCode(error: unknown): string | undefined {
  if (typeof error === 'object' && error !== null && 'response' in error) {
    const response = error.response
    if (
      typeof response === 'object' &&
      response !== null &&
      'data' in response
    ) {
      const data = response.data
      if (
        typeof data === 'object' &&
        data !== null &&
        Object.keys(data).length === 1 &&
        'error' in data
      ) {
        const body = data.error
        if (
          typeof body === 'object' &&
          body !== null &&
          Object.keys(body).length === 1 &&
          'code' in body &&
          typeof body.code === 'string'
        ) {
          return body.code
        }
      }
    }
  }
  return undefined
}

/** A dispatched write needs the exact Core rejection; an HTTP status alone is ambiguous. */
function dispatchedFailure(
  error: unknown,
  operationID: string
): HoneypotFailure {
  if (error instanceof HoneypotFailure) return error
  const knownRejections: Record<string, HoneypotFailureKind> = {
    '400:ADMIN_ABUSE_INVALID': 'invalid',
    '401:ADMIN_ABUSE_UNAUTHORIZED': 'forbidden',
    '403:ADMIN_ABUSE_FORBIDDEN': 'forbidden',
    '404:ADMIN_ABUSE_NOT_FOUND': 'not_found',
    '409:ADMIN_ABUSE_CONFLICT': 'conflict',
    '410:ADMIN_ABUSE_PREVIEW_EXPIRED': 'expired',
    '429:ADMIN_ABUSE_RATE_LIMITED': 'invalid',
  }
  return new HoneypotFailure(
    knownRejections[`${statusOf(error)}:${coreErrorCode(error)}`] ?? 'unknown',
    operationID
  )
}

function operationFailure(
  error: unknown,
  operationID: string
): HoneypotFailure {
  if (error instanceof HoneypotFailure) return error
  const status = statusOf(error),
    code = coreErrorCode(error)
  const kind =
    (status === 401 && code === 'ADMIN_ABUSE_UNAUTHORIZED') ||
    (status === 403 && code === 'ADMIN_ABUSE_FORBIDDEN')
      ? 'forbidden'
      : status === 404 && code === 'ADMIN_ABUSE_OPERATION_NOT_FOUND'
        ? 'not_found'
        : 'unknown'
  return new HoneypotFailure(kind, operationID)
}
async function read<T>(
  path: string,
  schema: ZodType<T>,
  scope: HoneypotScope,
  params?: Record<string, string | number>,
  apiPrefix = prefix
) {
  try {
    const response = await api.get<unknown>(`${apiPrefix}${path}`, {
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
export async function getHoneypotUser(id: string, scope: HoneypotScope) {
  if (!honeypotUserIDSchema.safeParse(id).success) {
    throw new HoneypotFailure('invalid')
  }
  const user = await read(
    `/users/${id}`,
    userDetailSchema,
    scope,
    undefined,
    usersPrefix
  )
  if (user.user_id !== id) throw new HoneypotFailure('invalid')
  return user
}
export async function getHoneypotUsers(
  scope: HoneypotScope,
  params: { after: string | null; limit: number; query: string; state: string }
) {
  const { after, ...rest } = params
  if (
    (after !== null && !honeypotUserIDSchema.safeParse(after).success) ||
    !Number.isSafeInteger(params.limit) ||
    params.limit < 1 ||
    params.limit > 200
  ) {
    throw new HoneypotFailure('invalid')
  }
  const result = await read(
    '/users',
    userListSchema,
    scope,
    after === null ? rest : { ...rest, after },
    usersPrefix
  )
  if (
    result.users.length > params.limit ||
    (after !== null &&
      result.users.some((user) => BigInt(user.user_id) <= BigInt(after)))
  ) {
    throw new HoneypotFailure('invalid')
  }
  return result
}

async function readOperation(
  id: string,
  scope: HoneypotScope,
  kind: 'users' | 'policies'
) {
  const operationID = operationIDSchema.parse(id)
  try {
    const response = await api.get<unknown>(
      `${kind === 'users' ? usersPrefix : prefix}/operations/${encodeURIComponent(operationID)}`,
      options(scope)
    )
    if (
      typeof response.data !== 'object' ||
      response.data === null ||
      !('kind' in response.data) ||
      response.data.kind !== kind
    ) {
      throw new HoneypotFailure('unknown', operationID)
    }
    return resultSchema.parse(response.data)
  } catch (error) {
    throw operationFailure(error, operationID)
  }
}
/** Original v1 reader for a known policy operation; never guesses another protocol. */
export const getHoneypotOperation = (id: string, scope: HoneypotScope) =>
  readOperation(id, scope, 'policies')
export const getHoneypotUserOperation = (id: string, scope: HoneypotScope) =>
  readOperation(id, scope, 'users')

export async function previewHoneypotUsers(
  request: HoneypotPreviewRequest,
  scope: HoneypotScope
) {
  const parsed = previewRequestSchema.safeParse(request)
  if (!parsed.success) throw new HoneypotFailure('invalid')
  const body = parsed.data
  try {
    const response = await api.post<unknown>(
      `${usersPrefix}/users/preview`,
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
  if (
    !userResultSchema.safeParse(result).success ||
    !previewSchema.safeParse(preview).success
  ) {
    return false
  }
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
    const reviewed = previewSchema.parse(preview)
    const response = await api.post<unknown>(
      `${usersPrefix}/users/commit`,
      {
        operation_id: reviewed.operation_id,
        preview_id: reviewed.preview_id,
        preview_hash: reviewed.preview_hash,
      },
      options(scope)
    )
    const result = resultSchema.parse(response.data)
    if (!userResultMatchesPreview(result, preview)) {
      throw new HoneypotFailure('unknown', preview.operation_id)
    }
    return result
  } catch (error) {
    throw dispatchedFailure(error, preview.operation_id)
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
    throw dispatchedFailure(error, request.operation_id)
  }
}

/** Recheck the reviewed host registry before dispatch; Core retains final authority. */
export async function saveReviewedHoneypotPolicies(
  request: Parameters<typeof saveHoneypotPolicies>[0],
  scope: HoneypotScope,
  reviewed: HoneypotPolicies
) {
  const parsed = configSchema.safeParse(request.config)
  if (!parsed.success || !validatePolicyCapabilities(parsed.data, reviewed)) {
    throw new HoneypotFailure('invalid')
  }
  const current = await getHoneypotPolicies(scope)
  if (
    reviewed.revision !== request.expected_revision ||
    current.revision !== request.expected_revision ||
    !samePolicyCapabilities(reviewed.capabilities, current.capabilities)
  ) {
    throw new HoneypotFailure('conflict')
  }
  if (!validatePolicyCapabilities(parsed.data, current)) {
    throw new HoneypotFailure('invalid')
  }
  return saveHoneypotPolicies({ ...request, config: parsed.data }, scope)
}
