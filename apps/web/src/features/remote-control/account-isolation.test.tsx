/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
// @ts-expect-error Bun supplies this module in the test runner.
import { mock } from 'bun:test'
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

import type { AuthUser } from '@/stores/auth-store'

const dom = new Window()
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: dom[key],
  })
}
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
mock.module('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}))
mock.module('@/components/layout', () => {
  const Section = Object.assign(
    ({ children }: { children?: React.ReactNode }) => <main>{children}</main>,
    {
      Title: ({ children }: { children?: React.ReactNode }) => (
        <h1>{children}</h1>
      ),
      Content: ({ children }: { children?: React.ReactNode }) => (
        <section>{children}</section>
      ),
    }
  )
  return { SectionPageLayout: Section }
})
mock.module('@/lib/format', () => ({
  formatTimestampToDate: (value: number) => String(value),
}))
mock.module('@/components/ui/markdown', () => ({
  Markdown: ({ children }: { children?: React.ReactNode }) => <p>{children}</p>,
}))
let sessions: unknown[] = [],
  messages: unknown[] = []
let reads = 0
let postedSignal: AbortSignal | undefined
mock.module('@/lib/api', () => ({
  api: {
    async get(path: string) {
      reads++
      return {
        data: {
          success: true,
          data: path.includes('/messages') ? { messages } : { sessions },
        },
      }
    },
    async post(
      _path: string,
      _body: unknown,
      options: { signal: AbortSignal }
    ) {
      postedSignal = options.signal
      return new Promise((_resolve, reject) =>
        options.signal.addEventListener(
          'abort',
          () => reject(new Error('Cancelled')),
          { once: true }
        )
      )
    },
  },
}))
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { useAuthStore } = await import('@/stores/auth-store')
const { derivePiRemoteKey } = await import('./crypto')
const { RemoteControl } = await import('./index')
const id = 'session_fixture',
  device = 'device_fixture'
const key = await derivePiRemoteKey('fixture-pin', id)
async function envelope(payload: unknown, aad: string) {
  const nonce = crypto.getRandomValues(new Uint8Array(12))
  const ciphertext = await crypto.subtle.encrypt(
    {
      name: 'AES-GCM',
      iv: nonce,
      additionalData: new TextEncoder().encode(aad),
    },
    key,
    new TextEncoder().encode(JSON.stringify(payload))
  )
  return {
    nonce: Buffer.from(nonce).toString('base64url'),
    ciphertext: Buffer.from(ciphertext).toString('base64url'),
  }
}
const user = (id: number) => ({ id }) as AuthUser
async function settle(until: () => boolean) {
  for (let attempt = 0; attempt < 100 && !until(); attempt++) {
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 10))
    })
  }
  assert.ok(until(), document.body.textContent ?? 'condition not met')
}
async function click(host: HTMLElement, label: string) {
  const button = [...host.querySelectorAll('button')].find(
    (node) => node.textContent === label
  )
  assert.ok(button, label)
  await act(async () => button.click())
}
async function unlock(host: HTMLElement) {
  const input = host.querySelector<HTMLInputElement>('input[type=password]')
  assert.ok(input)
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(
      dom.HTMLInputElement.prototype,
      'value'
    )?.set
    assert.ok(setter)
    setter.call(input, 'fixture-pin')
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })
  const form = host.querySelector('form')
  assert.ok(form)
  await act(async () =>
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
  )
  await settle(
    () => host.textContent?.includes('PRIVATE-FIRST-ACCOUNT') === true
  )
}
test('account switch/logout drop keys, plaintext query data and in-flight commands', async () => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  const now = Date.now()
  sessions = [
    {
      session_id: id,
      device_id: device,
      generation: 'generation-one',
      expires_at: Math.floor(now / 1000) + 120,
      updated_at: Math.floor(now / 1000),
      metadata: await envelope(
        {
          version: 1,
          summary: 'PRIVATE-FIRST-ACCOUNT',
          capabilities: ['prompt'],
        },
        `lmm-pi-remote:v1:metadata:${id}:${device}`
      ),
    },
  ]
  messages = [
    {
      sequence: 1,
      sender: 'plugin',
      created_at: now,
      ...(await envelope(
        { type: 'state', busy: false, created_at: now },
        `lmm-pi-remote:v1:message:${id}:plugin`
      )),
    },
  ]
  try {
    useAuthStore.getState().auth.reset()
    await act(async () =>
      root.render(
        <QueryClientProvider client={client}>
          <RemoteControl />
        </QueryClientProvider>
      )
    )
    assert.ok(host.textContent?.includes('Sign in to continue'))
    assert.equal(reads, 0)
    await act(async () => useAuthStore.getState().auth.setUser(user(1)))
    await settle(() => !!host.querySelector('input[type=password]'))
    await unlock(host)
    await settle(
      () =>
        host.querySelector<HTMLTextAreaElement>('textarea')?.disabled === false
    )
    const textarea = host.querySelector('textarea')
    assert.ok(textarea)
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(
        dom.HTMLTextAreaElement.prototype,
        'value'
      )?.set
      assert.ok(setter)
      setter.call(textarea, 'Private task')
      textarea.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await click(host, 'Send task')
    await settle(() => !!postedSignal)
    await act(async () => useAuthStore.getState().auth.setUser(user(2)))
    await settle(() => !!host.querySelector('input[type=password]'))
    assert.ok(!host.textContent?.includes('PRIVATE-FIRST-ACCOUNT'))
    assert.ok(postedSignal?.aborted)
    assert.equal(
      client
        .getQueryCache()
        .findAll({ queryKey: ['remote-control', 'pi', '1:browser'] }).length,
      0
    )
    await act(async () => useAuthStore.getState().auth.reset())
    assert.ok(host.textContent?.includes('Sign in to continue'))
    assert.equal(
      client.getQueryCache().findAll({ queryKey: ['remote-control', 'pi'] })
        .length,
      0
    )
  } finally {
    await act(async () => root.unmount())
    client.clear()
    useAuthStore.getState().auth.reset()
  }
})
after(() => dom.happyDOM.abort())
