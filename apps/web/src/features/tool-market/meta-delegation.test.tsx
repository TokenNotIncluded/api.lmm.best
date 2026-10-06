/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'

const dom = new Window({ url: 'https://console.example.test/tool-market' })
Object.defineProperty(dom.document, 'compatMode', { value: 'CSS1Compat' })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLInputElement',
  'HTMLSelectElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: dom[key],
  })
}

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { useAuthStore } = await import('@/stores/auth-store')
const { metaDelegationAPI } = await import('./meta-delegation-api')
const { MetaDelegationSettings } = await import('./meta-delegation')
const { configureIssuedMetaDelegation } =
  await import('./meta-delegation-setup')
const { metaDelegationCopy } = await import('./meta-delegation-copy')

;(
  globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }
).IS_REACT_ACT_ENVIRONMENT = true
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const original = { ...metaDelegationAPI }
const rendered: {
  root: ReturnType<typeof createRoot>
  cache: InstanceType<typeof QueryClient>
}[] = []
const target = { kind: 'personal' as const, id: 'safe-token-id' }
const initial = {
  enabled: false,
  max_total_quota: 0,
  expires_at: 0,
  updated_at: 1,
}

async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 5))
}
async function waitFor(predicate: () => boolean) {
  for (let i = 0; i < 60 && !predicate(); i++) {
    await act(flush)
  }
  assert.ok(predicate(), 'expected state did not appear')
}
function button(text: string) {
  const item = [...document.querySelectorAll<HTMLButtonElement>('button')].find(
    (value) => value.textContent === text
  )
  assert.ok(item, `Missing button: ${text}`)
  return item
}
async function click(item: HTMLElement) {
  await act(async () => {
    item.click()
    await flush()
  })
}
async function inputValue(item: HTMLInputElement, value: string) {
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      'value'
    )?.set
    assert.ok(setter)
    setter.call(item, value)
    item.dispatchEvent(new Event('input', { bubbles: true }))
    item.dispatchEvent(new Event('change', { bubbles: true }))
    await flush()
  })
}
async function mount(permitted = true) {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'test', role: 1 })
  const cache = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: Infinity } },
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  rendered.push({ root, cache })
  await act(async () => {
    root.render(
      <QueryClientProvider client={cache}>
        <I18nextProvider i18n={i18n}>
          <MetaDelegationSettings target={target} permitted={permitted} />
        </I18nextProvider>
      </QueryClientProvider>
    )
    await flush()
  })
  await click(button(metaDelegationCopy.title))
  return cache
}
afterEach(async () => {
  for (const { root, cache } of rendered.splice(0)) {
    await act(async () => root.unmount())
    cache.clear()
  }
  Object.assign(metaDelegationAPI, original)
  useAuthStore.getState().auth.setUser(null)
  document.body.innerHTML = ''
})
after(() => dom.close())

test('meta settings require persisted connection permissions and never silently delegate', async () => {
  metaDelegationAPI.get = async () =>
    assert.fail('restricted connection must not query a delegation')
  metaDelegationAPI.set = async () =>
    assert.fail('restricted connection must not write a delegation')
  await mount(false)
  assert.ok(document.body.textContent?.includes(metaDelegationCopy.permissions))
  assert.equal(button('Save').disabled, true)
  assert.equal(
    document.querySelector<HTMLInputElement>('input[type=checkbox]')?.checked,
    false
  )
})

test('meta settings never save an empty default after a failed current-policy read', async () => {
  metaDelegationAPI.get = async () => {
    throw new Error('private-server-error')
  }
  metaDelegationAPI.set = async () =>
    assert.fail('failed read must not overwrite current policy')
  await mount()
  await waitFor(
    () =>
      document.body.textContent?.includes(metaDelegationCopy.failed) === true
  )
  assert.equal(button('Save').disabled, true)
  assert.ok(button('Retry'))
  assert.equal(
    document.body.textContent?.includes('private-server-error'),
    false
  )
})

test('meta settings submit exact credits and show the server stricter cap', async () => {
  metaDelegationAPI.get = async () => initial
  const bodies: unknown[] = []
  metaDelegationAPI.set = async (_target, body) => {
    bodies.push(body)
    return {
      ...initial,
      enabled: true,
      max_total_quota: 150,
      expires_at: 2000000000,
    }
  }
  await mount()
  await waitFor(() => button('Save').disabled === false)
  const checkbox = document.querySelector<HTMLInputElement>(
    'input[type=checkbox]'
  )
  assert.ok(checkbox)
  await click(checkbox)
  const amount = document.querySelector<HTMLInputElement>(
    'input[inputmode="numeric"]'
  )
  assert.ok(amount)
  await inputValue(amount, '500')
  await click(button('Save'))
  await waitFor(() => amount.value === '150')
  assert.deepEqual(bodies, [
    { enabled: true, max_total_quota: 500, expires_at: 0 },
  ])
  assert.ok(
    document.body.textContent?.includes('The saved limit is 150 Credits.')
  )
})

test('issued connection delegation is explicit, finite and does not claim success on API failure', async () => {
  let calls = 0
  metaDelegationAPI.set = async () => {
    calls++
    throw new Error('delegation rejected')
  }
  assert.equal(
    await configureIssuedMetaDelegation(
      target,
      { enabled: false, quota: '0' },
      true,
      1000
    ),
    undefined
  )
  await assert.rejects(
    configureIssuedMetaDelegation(
      target,
      { enabled: true, quota: '0' },
      false,
      1000
    )
  )
  await assert.rejects(
    configureIssuedMetaDelegation(
      target,
      { enabled: true, quota: '0.1' },
      true,
      1000
    )
  )
  assert.equal(calls, 0)
  await assert.rejects(
    configureIssuedMetaDelegation(
      target,
      { enabled: true, quota: '0' },
      true,
      1000
    ),
    /delegation rejected/
  )
  assert.equal(calls, 1)
})
