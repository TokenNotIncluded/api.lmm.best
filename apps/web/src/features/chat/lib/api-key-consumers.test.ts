/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'

import { api } from '@/lib/api'

import { fetchActiveChatKey } from '../hooks/use-active-chat-key'
import { resolveChatUrl } from './chat-links'
import { sendToFluent } from './send-to-fluent'

const dom = new Window({ url: 'https://console.example.test/' })
const previous = ['window', 'document', 'CustomEvent'].map(
  (key) => [key, Object.getOwnPropertyDescriptor(globalThis, key)] as const
)
for (const key of ['window', 'document', 'CustomEvent'] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: dom[key],
  })
}
const originalGet = api.get
const originalPost = api.post
afterEach(() => {
  api.get = originalGet
  api.post = originalPost
  document.body.replaceChildren()
})
after(() => {
  for (const [key, descriptor] of previous) {
    if (descriptor) Object.defineProperty(globalThis, key, descriptor)
    else Reflect.deleteProperty(globalThis, key)
  }
  dom.close()
})

for (const [format, issued, expected] of [
  ['native', `lmmk_${'a1'.repeat(32)}`, `lmmk_${'a1'.repeat(32)}`],
  ['prefixed legacy', 'sk-chat-secret', 'sk-chat-secret'],
  ['bare legacy', 'chat-secret', 'sk-chat-secret'],
]) {
  test(`${format}: active key, chat links and Fluent retain the usable credential`, async () => {
    api.get = (async (url: string) => {
      assert.equal(url, '/api/token/?p=1&size=50')
      return {
        data: { success: true, data: { items: [{ id: 42, status: 1 }] } },
      }
    }) as typeof api.get
    api.post = (async (url: string) => {
      assert.equal(url, '/api/token/42/key')
      return { data: { success: true, data: { key: issued } } }
    }) as typeof api.post
    const active = await fetchActiveChatKey()
    assert.equal(active, expected)
    assert.equal(
      resolveChatUrl({
        template: 'chat://open?key={key}',
        apiKey: active,
        serverAddress: '',
      }),
      `chat://open?key=${expected}`
    )
    for (const config of ['cherryConfig', 'aionuiConfig', 'deepchatConfig']) {
      const result = resolveChatUrl({
        template: `chat://open?config={${config}}`,
        apiKey: ` ${active} `,
        serverAddress: 'https://console.example.test',
      })
      const encoded = new URL(result).searchParams.get('config')
      assert.ok(encoded)
      assert.equal(
        JSON.parse(Buffer.from(encoded, 'base64').toString('utf8')).apiKey,
        expected
      )
    }
    const container = document.createElement('div')
    container.id = 'fluent-new-api-container'
    document.body.append(container)
    let detail: unknown
    container.addEventListener('fluent:prefill', (event) => {
      detail = (event as CustomEvent).detail
    })
    assert.equal(sendToFluent(active), true)
    assert.deepEqual(detail, {
      id: 'new-api',
      baseUrl: 'https://console.example.test',
      apiKey: expected,
    })
    // Callers may supply the DTO secret directly rather than the resolved active key.
    assert.equal(sendToFluent(issued), true)
    assert.equal((detail as { apiKey: string }).apiKey, expected)
  })
}
