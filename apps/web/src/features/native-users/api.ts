/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { api } from '@/lib/api'

import {
  isCurrentNativeUsersScope,
  NativeUsersFailure,
  nativeUsersFailure,
  nativeUsersOptions,
  type NativeUsersScope,
} from './access'
import {
  nativeGroupsResponseSchema,
  nativeUsersQuery,
  nativeUsersResponseSchema,
  type NativeGroupsResponse,
  type NativeUsersQuery,
  type NativeUsersResponse,
} from './contract'

function assertScope(scope: NativeUsersScope, signal?: AbortSignal) {
  if (!isCurrentNativeUsersScope(scope) || signal?.aborted) {
    throw new NativeUsersFailure('session_changed')
  }
}

export async function readNativeUsers(
  scope: NativeUsersScope,
  query: NativeUsersQuery,
  signal?: AbortSignal
): Promise<NativeUsersResponse> {
  assertScope(scope, signal)
  const params = nativeUsersQuery(query)
  // Core's optional integer cannot parse an empty query value; absence means no level filter.
  const { service_level, ...rest } = params
  const requestParams = service_level === '' ? rest : { ...rest, service_level }
  try {
    const response = await api.get<unknown>('/core/v1/admin/users', {
      ...nativeUsersOptions(scope, signal),
      params: requestParams,
    })
    assertScope(scope, signal)
    const parsed = nativeUsersResponseSchema.safeParse(response.data)
    if (
      !parsed.success ||
      parsed.data.page !== params.page ||
      parsed.data.page_size !== params.page_size
    ) {
      throw new NativeUsersFailure('invalid_response')
    }
    return parsed.data
  } catch (error) {
    assertScope(scope, signal)
    throw nativeUsersFailure(error)
  }
}

export async function readNativeUserGroups(
  scope: NativeUsersScope,
  page: number,
  signal?: AbortSignal
): Promise<NativeGroupsResponse> {
  assertScope(scope, signal)
  if (!Number.isSafeInteger(page) || page < 1 || page > 100000) {
    throw new NativeUsersFailure('invalid_query')
  }
  try {
    const response = await api.get<unknown>('/core/v1/admin/users/groups', {
      ...nativeUsersOptions(scope, signal),
      params: { page, page_size: 20 },
    })
    assertScope(scope, signal)
    const parsed = nativeGroupsResponseSchema.safeParse(response.data)
    if (
      !parsed.success ||
      parsed.data.page !== page ||
      parsed.data.page_size !== 20
    ) {
      throw new NativeUsersFailure('invalid_response')
    }
    return parsed.data
  } catch (error) {
    assertScope(scope, signal)
    throw nativeUsersFailure(error)
  }
}
