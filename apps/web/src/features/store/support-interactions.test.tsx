/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'
import type React from 'react'

import type { SupportHistory } from './support-api'
import type { SupportDraft } from './support-helpers'

const dom = new Window({ url: 'https://store.example.test/store/support' })
dom.document.write('<!doctype html><html><body></body></html>')
Object.defineProperty(dom.document, 'compatMode', { configurable: true, value: 'CSS1Compat' })
for (const key of ['window', 'document', 'navigator', 'HTMLElement', 'HTMLInputElement', 'HTMLTextAreaElement', 'SVGElement', 'Node', 'Element', 'Event', 'MouseEvent', 'CustomEvent', 'FocusEvent', 'KeyboardEvent', 'MutationObserver', 'ResizeObserver', 'requestAnimationFrame', 'cancelAnimationFrame', 'getComputedStyle', 'matchMedia', 'customElements', 'CSSStyleSheet', 'localStorage'] as const) {
  Object.defineProperty(globalThis, key, { configurable: true, value: dom[key] })
}
Object.defineProperty(globalThis, 'IS_REACT_ACT_ENVIRONMENT', { configurable: true, value: true })
const { act, useState } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } = await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { useAuthStore } = await import('@/stores/auth-store')
const { consumeQueuedAssistantRequest } = await import('@/features/assistant/assistant-events')
const { supportApi } = await import('./support-api')
const { StoreSupportChat } = await import('./support-chat')
const { StoreSupportPage } = await import('./support-page')
const i18n = createInstance()
await i18n.use(initReactI18next).init({ lng: 'en', resources: { en: { translation: {} } } })
const originals = { ...supportApi }
const originalAuth = useAuthStore.getState().auth
let root: ReturnType<typeof createRoot> | undefined
let client: InstanceType<typeof QueryClient> | undefined
function authenticate(id = 11) {
  useAuthStore.setState({ auth: { ...originalAuth, user: { id, username: `buyer-${id}`, role: 1, status: 1 } as NonNullable<typeof originalAuth.user>, bootstrapState: 'complete' } })
}
function history(id = 'c1', subject = 'First product'): SupportHistory {
  return { conversation: { id, buyer_id: 11, seller_id: 22, product_id: 'p1', order_id: '', subject, status: 'open', last_message_id: 1, buyer_read_id: 1, seller_read_id: 0, created_at: 1, updated_at: 1 }, items: [{ id: 1, conversation_id: id, sender_id: 22, body: '<img src=x onerror=alert(1)> PRIVATE-CHAT-TEXT', created_at: 1 }], has_more: false }
}
async function settle() { await act(async () => { await new Promise((resolve) => setTimeout(resolve, 50)) }) }
async function render(node: React.ReactNode) {
  authenticate()
  document.body.innerHTML = '<main></main>'
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  root = createRoot(document.querySelector('main')!)
  await act(async () => root!.render(<QueryClientProvider client={client!}><I18nextProvider i18n={i18n}>{node}</I18nextProvider></QueryClientProvider>))
  await settle()
}
function button(text: string) {
  const result = [...document.querySelectorAll('button')].find((item) => item.textContent?.trim() === text)
  assert.ok(result, `missing button ${text}`)
  return result
}
async function click(element: HTMLElement) { await act(async () => element.click()); await settle() }
function Composer() {
  const [draft, setDraft] = useState<SupportDraft>({ text: 'Hello seller' })
  return <StoreSupportChat id='c1' userId={11} draft={draft} onDraft={(_id, update) => setDraft((current) => typeof update === 'function' ? update(current) : update)} onBack={() => undefined} />
}
afterEach(async () => {
  await act(async () => root?.unmount())
  root = undefined
  client?.clear()
  Object.assign(supportApi, originals)
  useAuthStore.setState({ auth: originalAuth })
  consumeQueuedAssistantRequest()
})
after(() => dom.happyDOM.abort())

test('conversation text is escaped and assistant sharing requires explicit review', async () => {
  supportApi.history = async () => history()
  supportApi.assistantContext = async () => ({ role: 'buyer', conversation_id: 'c1', subject: 'First product', messages: [{ role: 'seller', text: 'Useful advice' }] })
  await render(<Composer />)
  assert.equal(document.querySelector('img'), null)
  assert.ok(document.body.textContent?.includes('<img src=x'))
  assert.equal(consumeQueuedAssistantRequest(), undefined)
  await click(button('Assistant help'))
  const preview = document.querySelector('textarea[aria-label="Text to share with the assistant"]') as HTMLTextAreaElement
  assert.ok(preview.value.includes('Useful advice'))
  assert.equal(consumeQueuedAssistantRequest(), undefined)
  await click(button('Open in assistant'))
  const queued = consumeQueuedAssistantRequest()
  assert.equal(queued?.autoSend, false)
  assert.equal(queued?.preset, 'service')
  assert.ok(queued?.message?.includes('Useful advice'))
})

test('retry after a lost response reuses the request key and clears only a successful draft', async () => {
  const calls: { key: string; body: string }[] = []
  supportApi.history = async () => history()
  supportApi.send = async (_id, body, key) => {
    calls.push({ key, body })
    if (calls.length === 1) throw new Error('Simulated lost response')
    return { id: 2, conversation_id: 'c1', sender_id: 11, body, created_at: 2 }
  }
  await render(<Composer />)
  await click(button('Send message'))
  assert.equal((document.querySelector('textarea') as HTMLTextAreaElement).value, 'Hello seller')
  await click(button('Send message'))
  assert.equal(calls.length, 2)
  assert.equal(calls[0].key, calls[1].key)
  assert.equal((document.querySelector('textarea') as HTMLTextAreaElement).value, '')
})

test('switching conversations retains separate drafts and changing accounts clears private content', async () => {
  supportApi.conversations = async () => ({ items: [history('c1', 'First product'), history('c2', 'Second product')].map((item) => ({ ...item.conversation, buyer_name: 'Buyer', seller_name: 'Seller', unread_count: 0 })), has_more: false })
  supportApi.history = async (id) => history(id, id === 'c1' ? 'First product' : 'Second product')
  await render(<StoreSupportPage search={{ role: 'buyer', tab: 'messages', conversation: 'c1' }} />)
  const textarea = document.querySelector('textarea') as HTMLTextAreaElement
  await act(async () => {
    Object.getOwnPropertyDescriptor(dom.HTMLTextAreaElement.prototype, 'value')!.set!.call(textarea, 'Unsent draft')
    textarea.dispatchEvent(new dom.Event('input', { bubbles: true }))
  })
  const choose = (subject: string) => {
    const target = [...document.querySelectorAll('aside button')].find((item) => item.textContent?.includes(subject))
    assert.ok(target)
    return target as HTMLButtonElement
  }
  await click(choose('Second product'))
  assert.equal((document.querySelector('textarea') as HTMLTextAreaElement).value, '')
  await click(choose('First product'))
  assert.equal((document.querySelector('textarea') as HTMLTextAreaElement).value, 'Unsent draft')
  supportApi.history = async () => { throw new Error('Not a participant') }
  supportApi.conversations = async () => ({ items: [], has_more: false })
  await act(async () => authenticate(33))
  await settle()
  assert.equal(document.body.textContent?.includes('PRIVATE-CHAT-TEXT'), false)
  assert.equal(document.querySelector('textarea'), null)
})
