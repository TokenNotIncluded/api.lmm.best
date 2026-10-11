/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { getRequestErrorToastId } from './request-error-toast.ts'

function httpError(status: number, message: string, code?: string) {
  return { response: { status, data: { message, code } } }
}

test('simultaneous identical HTTP errors share a notification ID', () => {
  const errors = Array.from({ length: 8 }, (_, i) => ({
    ...httpError(404, 'Not Found'),
    config: { url: `/synthetic/${i}` },
  }))
  const ids = errors.map(getRequestErrorToastId)
  assert.equal(new Set(ids).size, 1)
  assert.equal(typeof ids[0], 'string')
  assert.ok(!ids[0]?.includes('/synthetic/'))
})

test('different messages, status codes and business codes stay distinct', () => {
  const errors = [
    httpError(404, 'Not Found'),
    httpError(500, 'Not Found'),
    httpError(404, 'Other error'),
    httpError(404, 'Not Found', 'A'),
    httpError(404, 'Not Found', 'B'),
  ]
  assert.equal(new Set(errors.map(getRequestErrorToastId)).size, errors.length)
  assert.equal(
    getRequestErrorToastId(errors[3]),
    getRequestErrorToastId({
      response: {
        status: 404,
        data: { message: 'Not Found', error: { code: 'A' } },
      },
    })
  )
})

test('uses the HTTP client message when a response has no message', () => {
  assert.equal(
    getRequestErrorToastId({
      response: { status: 502 },
      message: 'Gateway failed',
    }),
    getRequestErrorToastId(httpError(502, 'Gateway failed'))
  )
})

test('unknown, malformed and successful responses do not share an error ID', () => {
  for (const error of [
    null,
    undefined,
    'error',
    {},
    { response: null },
    { response: { status: '404', data: { message: 'Not Found' } } },
    httpError(200, 'Not Found'),
    httpError(600, 'Not Found'),
    httpError(404.5, 'Not Found'),
    { response: { status: 503, data: { code: 'OTHER' } } },
    httpError(404, ' '),
    { response: { status: 404, data: { message: {} } } },
  ]) {
    assert.equal(getRequestErrorToastId(error), undefined)
  }
})

test('does not mutate frozen responses', () => {
  const error = Object.freeze({
    response: Object.freeze({
      status: 404,
      data: Object.freeze({ message: 'Not Found' }),
    }),
  })
  const snapshot = JSON.stringify(error)
  const first = getRequestErrorToastId(error)
  assert.equal(getRequestErrorToastId(error), first)
  assert.equal(JSON.stringify(error), snapshot)
})
