/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import assert from 'node:assert/strict'
import { afterEach, test } from 'node:test'

import {
  act, api, findButton, flushEffects, waitFor,
} from '@/features/assistant/assistant-key-tool-test-support'
import type { AssistantSupportRequest } from '@/features/assistant/assistant-support-api'

const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } = await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { useAuthStore } = await import('@/stores/auth-store')
const { SupportConversation } = await import('./support-conversation')
const i18n = createInstance()
await i18n.use(initReactI18next).init({ lng: 'en', resources: { en: { translation: {} } } })
const l0 = { id: 71, username: 'support-test', role: 1, developer_access_granted: false }
const request: AssistantSupportRequest = {
  id: 8, user_id: 71, conversation_id: 91, kind: 'handoff', status: 'pending',
  topic: '', preferred_time: '', scheduled_at: 0, assigned_admin_id: 0,
  assigned_admin_name: '', created_at: 1, updated_at: 1, accepted_at: 0, closed_at: 0,
}
function installReads(current: AssistantSupportRequest | null = null) {
  const reads: string[] = []
  api.get = (async (url: string) => {
    reads.push(url)
    if (url === '/api/assistant/support/self') {
      return { data: { success: true, data: { request: current } } }
    }
    if (url === '/api/assistant/support/eligibility') {
      return { data: { success: true, data: { eligible: false } } }
    }
    assert.equal(url, '/api/assistant/support/8')
    return { data: { success: true, data: { request, messages: [] } } }
  }) as typeof api.get
  return reads
}
async function mount() {
  useAuthStore.getState().auth.setUser(l0)
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(<QueryClientProvider client={client}><I18nextProvider i18n={i18n}><SupportConversation /></I18nextProvider></QueryClientProvider>)
    await flushEffects()
  })
  return {
    container,
    async cleanup() {
      await act(async () => root.unmount())
      client.clear()
      container.remove()
    },
  }
}
afterEach(() => useAuthStore.getState().auth.reset('complete'))

test('L0 can request human support without an AI call or an existing conversation', async () => {
  const reads = installReads()
  const writes: unknown[][] = []
  api.post = (async (url: string, body: unknown) => {
    writes.push([url, body])
    return { data: { success: true, data: { request, created: true } } }
  }) as typeof api.post
  const view = await mount()
  try {
    assert.equal(writes.length, 0, 'Opening the page does not send a request')
    await act(async () => { findButton('Transfer to human').click(); await flushEffects() })
    await waitFor(() => view.container.textContent?.includes('Waiting for an administrator') === true, 'Show a real pending receipt')
    assert.deepEqual(writes, [['/api/assistant/support', { kind: 'handoff', conversation_id: 0 }]])
    assert.ok(view.container.querySelector('textarea'))
    assert.ok(reads.every((url) => url.startsWith('/api/assistant/support')))
    assert.equal(useAuthStore.getState().auth.user?.developer_access_granted, false)
  } finally { await view.cleanup() }
})

test('a failed handoff shows an error and permits a deliberate retry', async () => {
  installReads()
  let writes = 0
  api.post = (async () => {
    writes += 1
    if (writes === 1) throw new Error('temporary service failure')
    return { data: { success: true, data: { request, created: true } } }
  }) as typeof api.post
  const view = await mount()
  try {
    await act(async () => { findButton('Transfer to human').click(); await flushEffects() })
    assert.match(view.container.querySelector('[role="alert"]')?.textContent ?? '', /Unable to update human support/)
    assert.equal(findButton('Transfer to human').disabled, false)
    await act(async () => { findButton('Transfer to human').click(); await flushEffects() })
    await waitFor(() => Boolean(view.container.querySelector('textarea')), 'Retry creates a support conversation')
    assert.equal(writes, 2)
    assert.equal(view.container.querySelector('[role="alert"]'), null)
  } finally { await view.cleanup() }
})

test('failed support messages keep their draft and reuse the same retry identifier', async () => {
  installReads(request)
  const bodies: Array<{ content: string; client_turn_id: string }> = []
  api.post = (async (url: string, body: { content: string; client_turn_id: string }) => {
    assert.equal(url, '/api/assistant/support/8/messages')
    bodies.push(body)
    if (bodies.length === 1) throw new Error('temporary delivery failure')
    return { data: { success: true, data: { message: { id: 1, role: 'user', content: body.content } } } }
  }) as typeof api.post
  const view = await mount()
  try {
    await waitFor(() => Boolean(view.container.querySelector('textarea')), 'Load active support')
    const textarea = view.container.querySelector('textarea')!
    await act(async () => {
      Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, 'value')!.set!.call(textarea, 'I cannot enable L1')
      textarea.dispatchEvent(new Event('input', { bubbles: true }))
      await flushEffects()
    })
    assert.equal(findButton('Send').disabled, false)
    await act(async () => { view.container.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); await flushEffects() })
    assert.equal(textarea.value, 'I cannot enable L1')
    assert.ok(view.container.querySelector('[role="alert"]'))
    await act(async () => { view.container.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); await flushEffects() })
    assert.equal(textarea.value, '')
    assert.equal(bodies.length, 2)
    assert.ok(bodies[0].client_turn_id)
    assert.deepEqual(bodies[0], bodies[1])
  } finally { await view.cleanup() }
})

test('a handoff response from the previous account cannot enter a new account', async () => {
  installReads()
  let resolve!: (value: unknown) => void
  api.post = (() => new Promise((done) => { resolve = done })) as typeof api.post
  const view = await mount()
  try {
    await act(async () => { findButton('Transfer to human').click(); await flushEffects() })
    await act(async () => {
      useAuthStore.getState().auth.setUser({ ...l0, id: 72 })
      await flushEffects()
      resolve({ data: { success: true, data: { request, created: true } } })
      await flushEffects()
    })
    assert.equal(view.container.querySelector('textarea'), null)
    assert.equal(findButton('Transfer to human').disabled, false)
    assert.equal(useAuthStore.getState().auth.user?.id, 72)
  } finally { await view.cleanup() }
})
