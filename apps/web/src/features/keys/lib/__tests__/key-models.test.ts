/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { afterEach, test } from 'node:test'

import { getKeyModels } from '../key-models'

const originalFetch = globalThis.fetch
afterEach(() => {
  globalThis.fetch = originalFetch
})
const signal = () => new AbortController().signal
const mockResponse = (response: Response) => {
  globalThis.fetch = (async () => response) as typeof fetch
}

test('relay catalogue keeps only unique IDs, omitting unknown metadata and echoed credentials', async () => {
  mockResponse(
    Response.json({
      object: 'list',
      ignored: 'sk-test-secret',
      data: [
        {
          id: 'allowed-model',
          key: 'sk-test-secret',
          provider_metadata: 'private',
        },
        { id: 'allowed-model' },
        { id: 'second-model' },
      ],
    })
  )
  assert.deepEqual(await getKeyModels('sk-test-secret', signal()), [
    'allowed-model',
    'second-model',
  ])
})

test('malformed or error catalogues fail closed with a fixed credential-free error', async () => {
  for (const body of [
    null,
    { data: 'wrong' },
    { data: [null] },
    { data: [[]] },
    { data: [{ id: 1 }] },
    { data: [{ id: '' }] },
    { data: [{ id: ' model ' }] },
    { data: [{ id: 'x'.repeat(513) }] },
    { data: [{ id: 'sk-test-secret' }] },
    { data: [{ id: 'model-with-sk-test-secret-echo' }] },
    { error: { message: 'sk-test-secret' }, data: [] },
    { success: false, data: [] },
  ]) {
    mockResponse(Response.json(body))
    await assert.rejects(
      getKeyModels('sk-test-secret', signal()),
      (error: unknown) => {
        assert.ok(error instanceof Error)
        assert.equal(error.message, 'Failed to fetch models')
        assert.equal(error.cause, undefined)
        assert.doesNotMatch(error.stack ?? '', /sk-test-secret/)
        return true
      }
    )
  }
  mockResponse(new Response('{broken sk-test-secret'))
  await assert.rejects(getKeyModels('sk-test-secret', signal()), {
    message: 'Failed to fetch models',
  })
})

test('an oversized streaming body is cancelled instead of being buffered or cached', async () => {
  let cancelled = false
  let pulls = 0
  mockResponse(
    new Response(
      new ReadableStream<Uint8Array>({
        pull(controller) {
          pulls += 1
          controller.enqueue(new Uint8Array(512 * 1024))
        },
        cancel() {
          cancelled = true
        },
      })
    )
  )
  await assert.rejects(getKeyModels('sk-test-secret', signal()), {
    message: 'Failed to fetch models',
  })
  assert.equal(cancelled, true)
  assert.ok(pulls <= 4)
})

test('the model count is bounded independently of small individual IDs', async () => {
  mockResponse(
    Response.json({
      data: Array.from({ length: 10_001 }, () => ({ id: 'model' })),
    })
  )
  await assert.rejects(getKeyModels('sk-test-secret', signal()), {
    message: 'Failed to fetch models',
  })
})

test('missing credentials never issue a request and abort reasons never become cached errors', async () => {
  let calls = 0
  const controller = new AbortController()
  controller.abort(new Error('raw sk-test-secret cancellation reason'))
  globalThis.fetch = (async (_url, init) => {
    calls += 1
    assert.equal(init?.signal?.aborted, true)
    throw init?.signal?.reason
  }) as typeof fetch
  await assert.rejects(getKeyModels('', signal()), {
    message: 'Failed to fetch models',
  })
  assert.equal(calls, 0)
  await assert.rejects(
    getKeyModels('sk-test-secret', controller.signal),
    (error: unknown) => {
      assert.ok(error instanceof Error)
      assert.equal(error.message, 'Failed to fetch models')
      assert.equal(error.cause, undefined)
      return true
    }
  )
  assert.equal(calls, 1)
})
