/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { api } from '@/lib/api'

import { STORE_COMMERCE_IMPORT_COPY as copy } from './commerce-import-copy'
import type {
  CommerceImportAuthScope,
  CommerceImportCatalog,
  CommerceImportConfig,
  CommerceImportConnection,
  CommerceImportDraft,
  CommerceImportRequest,
  CommerceImportRestock,
} from './commerce-import-types'

type Envelope<T> = {
  success: boolean
  code?: string
  error?: string
  request_id?: string
  data: T
}
const root = '/api/store/commerce-import'
const options = {
  skipErrorHandler: true,
  skipBusinessError: true,
  disableDuplicate: true,
}
const fixedErrors: Record<string, string> = {
  catalog_changed: copy.catalogChanged,
  product_unavailable: copy.unavailable,
  variant_unavailable: copy.unavailable,
  unsupported_product: copy.stockHelp,
  quota_exceeded: copy.quotaExceeded,
  insufficient_scope: copy.insufficientScope,
  access_denied: copy.insufficientScope,
  invalid_grant: copy.invalidGrant,
  invalid_token: copy.invalidGrant,
  idempotency_conflict: copy.manualError,
  issuance_expired: copy.manualError,
  manual_recovery: copy.manualError,
}
export class CommerceImportAPIError extends Error {
  readonly code: string
  readonly requestId?: string
  constructor(
    code: unknown,
    public readonly httpStatus?: number,
    requestId?: unknown
  ) {
    const safeCode =
      typeof code === 'string' && Object.hasOwn(fixedErrors, code)
        ? code
        : 'request_failed'
    super(fixedErrors[safeCode] || copy.requestFailed)
    this.name = 'CommerceImportAPIError'
    this.code = safeCode
    this.requestId =
      typeof requestId === 'string' &&
      /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(
        requestId
      )
        ? requestId
        : undefined
  }
}
async function unwrap<T>(request: Promise<{ data: Envelope<T> }>): Promise<T> {
  let response: { data: Envelope<T> }
  try {
    response = await request
  } catch (issue) {
    const error = issue as {
      name?: string
      code?: string
      response?: { status?: number; data?: Envelope<T> }
    }
    if (
      error.name === 'AbortError' ||
      error.name === 'CanceledError' ||
      error.code === 'ERR_CANCELED'
    ) {
      throw issue
    }
    throw new CommerceImportAPIError(
      error.response?.data?.error ?? error.response?.data?.code,
      error.response?.status,
      error.response?.data?.request_id
    )
  }
  if (response.data.success !== true) {
    throw new CommerceImportAPIError(
      response.data.error ?? response.data.code,
      undefined,
      response.data.request_id
    )
  }
  return response.data.data
}
function connectionPath(id: string) {
  return `${root}/connections/${encodeURIComponent(id)}`
}
export const commerceImportApi = {
  config: (authScope: CommerceImportAuthScope, signal?: AbortSignal) =>
    unwrap<CommerceImportConfig>(
      api.get(`${root}/config`, { ...options, authScope, signal })
    ),
  connections: (authScope: CommerceImportAuthScope, signal?: AbortSignal) =>
    unwrap<CommerceImportConnection[]>(
      api.get(`${root}/connections`, { ...options, authScope, signal })
    ),
  connect: (
    body: { origin: string; client_id: string },
    authScope: CommerceImportAuthScope
  ) =>
    unwrap<CommerceImportConnection>(
      api.post(`${root}/connections`, body, { ...options, authScope })
    ),
  authorize: (
    id: string,
    cards_issue: boolean,
    authScope: CommerceImportAuthScope
  ) =>
    unwrap<{ authorization_url: string }>(
      api.post(
        `${connectionPath(id)}/authorize`,
        { cards_issue },
        { ...options, authScope }
      )
    ),
  catalog: async (
    id: string,
    authScope: CommerceImportAuthScope,
    signal?: AbortSignal
  ) => {
    const result = await unwrap<CommerceImportCatalog>(
      api.get(`${connectionPath(id)}/catalog`, {
        ...options,
        authScope,
        signal,
      })
    )
    if (
      result.schema !== 'extore.commerce-catalog.v1' ||
      !Array.isArray(result.products) ||
      result.products.some(
        (product) =>
          product.schema !== 'extore.product-listing.v1' ||
          !Array.isArray(product.variants)
      )
    ) {
      throw new Error(copy.invalidCatalog)
    }
    return result
  },
  import: (
    id: string,
    body: CommerceImportDraft,
    authScope: CommerceImportAuthScope
  ) =>
    unwrap<{ product_id: string }>(
      api.post(`${connectionPath(id)}/import`, body, { ...options, authScope })
    ),
  requests: (
    id: string,
    authScope: CommerceImportAuthScope,
    signal?: AbortSignal
  ) =>
    unwrap<CommerceImportRequest[]>(
      api.get(`${connectionPath(id)}/requests`, {
        ...options,
        authScope,
        signal,
      })
    ),
  restock: (
    id: string,
    body: CommerceImportRestock,
    authScope: CommerceImportAuthScope
  ) =>
    unwrap<CommerceImportRequest>(
      api.post(`${connectionPath(id)}/restock`, body, { ...options, authScope })
    ),
  recover: (
    id: string,
    requestId: string,
    authScope: CommerceImportAuthScope
  ) =>
    unwrap<CommerceImportRequest>(
      api.post(
        `${connectionPath(id)}/requests/${encodeURIComponent(requestId)}/recover`,
        {},
        { ...options, authScope }
      )
    ),
  disconnect: (id: string, authScope: CommerceImportAuthScope) =>
    unwrap<unknown>(api.delete(connectionPath(id), { ...options, authScope })),
}
