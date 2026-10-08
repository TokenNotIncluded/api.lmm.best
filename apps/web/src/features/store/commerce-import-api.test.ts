/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { afterEach, test } from 'node:test'

import { api } from '@/lib/api'

import {
  commerceImportApi,
  CommerceImportAPIError,
} from './commerce-import-api'
import { STORE_COMMERCE_IMPORT_COPY as copy } from './commerce-import-copy'

const original = { get: api.get, post: api.post }
const authScope = { userId: 19, sessionId: 'local-session' }
const envelope = (data: unknown) => ({ data: { success: true, data } })
afterEach(() => Object.assign(api, original))

test('product reading and new-card permissions are separate local-server requests', async () => {
  const calls: { url: string; body: unknown; options: unknown }[] = []
  api.post = (async (url: string, body: unknown, options: unknown) => {
    calls.push({ url, body, options })
    return envelope({
      authorization_url: 'https://external.example/oauth/authorize',
    })
  }) as typeof api.post
  await commerceImportApi.authorize('connection/id', false, authScope)
  await commerceImportApi.authorize('connection/id', true, authScope)
  assert.deepEqual(
    calls.map(({ url, body }) => ({ url, body })),
    [
      {
        url: '/api/store/commerce-import/connections/connection%2Fid/authorize',
        body: { cards_issue: false },
      },
      {
        url: '/api/store/commerce-import/connections/connection%2Fid/authorize',
        body: { cards_issue: true },
      },
    ]
  )
  assert.deepEqual(
    (calls[0].options as { authScope: unknown }).authScope,
    authScope
  )
})
test('recovery identifies the saved server request without sending a new issuance body or key', async () => {
  let captured: unknown
  api.post = (async (url: string, body: unknown) => {
    captured = { url, body }
    return envelope({ id: 'request/id', status: 'imported' })
  }) as typeof api.post
  await commerceImportApi.recover('connection', 'request/id', authScope)
  assert.deepEqual(captured, {
    url: '/api/store/commerce-import/connections/connection/requests/request%2Fid/recover',
    body: {},
  })
})
test('upstream error text and arbitrary codes never reach user-facing errors', async () => {
  api.post = (async () => {
    throw {
      response: {
        status: 500,
        data: {
          success: false,
          code: 'secret-in-code',
          request_id: 'secret-in-request-id',
          message: 'secret-in-message',
          detail: 'secret-in-detail',
        },
      },
    }
  }) as typeof api.post
  await assert.rejects(
    commerceImportApi.authorize('connection', false, authScope),
    (issue: unknown) => {
      assert.ok(issue instanceof CommerceImportAPIError)
      assert.equal(issue.message, copy.requestFailed)
      assert.equal(issue.code, 'request_failed')
      assert.equal(issue.httpStatus, 500)
      assert.equal(issue.requestId, undefined)
      assert.doesNotMatch(String(issue), /secret/)
      return true
    }
  )
})

test('fixed protocol failures expose only their safe saved request UUID', async () => {
  const requestId = '12345678-1234-4234-a234-123456789abc'
  api.post = (async () => {
    throw {
      response: {
        status: 409,
        data: {
          success: false,
          code: 'COMMERCE_IMPORT_quota_exceeded',
          error: 'quota_exceeded',
          request_id: requestId,
          message: 'never reflect this message',
        },
      },
    }
  }) as typeof api.post
  await assert.rejects(
    commerceImportApi.restock(
      'connection',
      {
        product_id: 'original-product',
        variant_id: 'original-variant',
        count: 1,
        expected_revision: 'a'.repeat(64),
        label: '',
      },
      authScope
    ),
    (issue: unknown) => {
      assert.ok(issue instanceof CommerceImportAPIError)
      assert.equal(issue.code, 'quota_exceeded')
      assert.equal(issue.message, copy.quotaExceeded)
      assert.equal(issue.requestId, requestId)
      assert.doesNotMatch(String(issue), /never reflect/)
      return true
    }
  )
})
test('catalog validation refuses unknown schemas before previewing product fields', async () => {
  api.get = (async () =>
    envelope({ schema: 'other.catalog', products: [] })) as typeof api.get
  await assert.rejects(commerceImportApi.catalog('connection', authScope), {
    message: copy.invalidCatalog,
  })
})
