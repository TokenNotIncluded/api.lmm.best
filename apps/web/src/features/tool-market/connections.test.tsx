/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import assert from 'node:assert/strict'
import { after, afterEach, beforeEach, test } from 'node:test'

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
const { marketAPI } = await import('./api')
const { MarketConnections } = await import('./connections')
const { resetMarketCurrencyTest } = await import('./currency-test-support')

;(
  globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }
).IS_REACT_ACT_ENVIRONMENT = true
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const originals = {
  mine: marketAPI.mine,
  token: marketAPI.token,
  install: marketAPI.install,
  grant: marketAPI.grant,
  invoke: marketAPI.invoke,
  budget: marketAPI.budget,
}
const config = {
  enabled: false,
  fee_bps: 0,
  recipient_id: 0,
  quota_per_unit: 500000,
  web_client_id: 'web-market',
  mcp_path: '/mcp/market',
}
const record = {
  id: 'token-test',
  client_id: 'my-agent',
  can_invoke: true,
  can_manage: false,
  expires_at: Math.floor(Date.now() / 1000) + 86400,
  revoked_at: 0,
}
const secret = 'mkt_test_once_only'
const rendered: {
  root: ReturnType<typeof createRoot>
  cache: InstanceType<typeof QueryClient>
}[] = []

const flush = () => new Promise<void>((resolve) => setTimeout(resolve, 10))
async function waitFor(predicate: () => boolean) {
  for (let attempt = 0; attempt < 80; attempt++) {
    if (predicate()) return
    await act(flush)
  }
  assert.fail('Expected connection UI did not become ready')
}
function button(container: HTMLElement, text: string) {
  const result = [...container.querySelectorAll('button')].find(
    (item) => item.textContent === text
  )
  assert.ok(result, `Missing button: ${text}`)
  return result
}
function element<T extends Element>(container: HTMLElement, selector: string) {
  const result = container.querySelector<T>(selector)
  assert.ok(result, `Missing element: ${selector}`)
  return result
}
function previewText(container: HTMLElement) {
  return element<HTMLPreElement>(container, 'pre').textContent ?? ''
}
async function mount(onChooseClient: (clientID: string) => void) {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'test', role: 1 })
  marketAPI.mine = (async () => []) as typeof marketAPI.mine
  marketAPI.token = async (client) => ({
    token: secret,
    record: { ...record, client_id: client },
  })
  marketAPI.install = async () => assert.fail('Navigation must not load a tool')
  marketAPI.grant = async () =>
    assert.fail('Navigation must not authorize payment')
  marketAPI.invoke = async () => assert.fail('Navigation must not call a tool')
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
          <MarketConnections config={config} onChooseClient={onChooseClient} />
        </I18nextProvider>
      </QueryClientProvider>
    )
    await flush()
  })
  await waitFor(
    () => container.textContent?.includes('No connections yet') === true
  )
  return { container, cache }
}

beforeEach(async () => {
  resetMarketCurrencyTest()
  await i18n.changeLanguage('en')
})

afterEach(async () => {
  for (const { root, cache } of rendered.splice(0)) {
    await act(async () => root.unmount())
    cache.clear()
  }
  Object.assign(marketAPI, originals)
  useAuthStore.getState().auth.setUser(null)
  document.body.replaceChildren()
})
after(() => dom.close())

test('editing a one-credit budget preserves raw quota across account currency changes', async () => {
  const { container, cache } = await mount(() => {})
  let payload: Parameters<typeof marketAPI.budget>[0] | undefined
  marketAPI.budget = async (input) => {
    payload = input
    return null
  }
  await act(async () => {
    cache.setQueryData(
      ['tool-market', 1, 'budgets'],
      [
        {
          scope: 'account',
          scope_id: '',
          limit_quota: 1,
          spent_quota: 0,
          reserved_quota: 0,
        },
      ]
    )
    await flush()
  })
  await waitFor(() => container.textContent?.includes('Edit budget') === true)
  await act(async () => button(container, 'Edit budget').click())
  const input = element<HTMLInputElement>(container, '#budget-limit')
  assert.equal(input.value, '0.000002')
  for (const [currency, expected] of [
    ['CNY', '0.000014'],
    ['CREDIT', '1'],
    ['USD', '0.000002'],
  ]) {
    await act(async () => {
      const auth = useAuthStore.getState().auth
      assert.ok(auth.user)
      auth.setUser({
        ...auth.user,
        setting: JSON.stringify({ wallet_display_currency: currency }),
      })
    })
    assert.equal(input.value, expected)
    assert.match(
      container.textContent ?? '',
      new RegExp(currency === 'CREDIT' ? '1 Credits' : currency)
    )
  }
  await act(async () => {
    button(container, 'Save budget').click()
    await flush()
  })
  await waitFor(() => payload !== undefined)
  assert.deepEqual(payload, { scope: 'account', scope_id: '', limit_quota: 1 })
})

test('a newly issued token chooses its own client without granting or invoking tools', async () => {
  const chosen: string[] = []
  const { container, cache } = await mount((client) => chosen.push(client))
  await act(async () => {
    button(container, 'Create connection token').click()
    await flush()
  })
  await waitFor(() => container.querySelector('#mcp-secret') !== null)
  const clientInput = element<HTMLInputElement>(container, '#mcp-client')
  const inputSetter = Object.getOwnPropertyDescriptor(
    dom.HTMLInputElement.prototype,
    'value'
  )?.set
  assert.ok(inputSetter)
  await act(async () => {
    inputSetter.call(clientInput, 'another-client')
    clientInput.dispatchEvent(new Event('input', { bubbles: true }))
    clientInput.dispatchEvent(new Event('change', { bubbles: true }))
  })
  assert.equal(clientInput.value, 'another-client')
  await act(async () =>
    button(container, 'Choose tools for this client').click()
  )
  assert.deepEqual(chosen, ['my-agent'])
  const pre = previewText(container)
  assert.ok(pre.includes('YOUR_CONNECTION_TOKEN'))
  assert.ok(pre.includes('my-agent'))
  assert.equal(pre.includes(secret), false)
  assert.equal(
    JSON.stringify(cache.getQueryCache().getAll()).includes(secret),
    false
  )
  assert.equal(
    JSON.stringify(
      cache
        .getMutationCache()
        .getAll()
        .map((row) => row.state)
    ).includes(secret),
    false
  )
})

test('existing client cards carry exact personal and OAuth IDs to the tool picker', async () => {
  const chosen: string[] = []
  const { container, cache } = await mount((client) => chosen.push(client))
  await act(async () => {
    cache.setQueryData(
      ['tool-market', 1, 'tokens'],
      [{ ...record, client_id: 'cursor-work' }]
    )
    cache.setQueryData(
      ['tool-market', 1, 'installations'],
      [{ client_id: 'oauth:lmm-pi', tool_id: 'tool-1', version_id: 'v-1' }]
    )
    await flush()
  })
  await waitFor(() => container.querySelectorAll('article').length === 2)
  const articles = [...container.querySelectorAll('article')]
  assert.equal(articles.length, 2)
  for (const article of articles) {
    await act(async () =>
      button(article, 'Choose tools for this client').click()
    )
  }
  assert.deepEqual(chosen, ['cursor-work', 'oauth:lmm-pi'])
})

test('profile switching updates placeholder preview and copies secrets only after explicit action', async () => {
  const copies: string[] = []
  Object.defineProperty(dom.navigator, 'clipboard', {
    configurable: true,
    value: {
      writeText: async (value: string) => {
        copies.push(value)
      },
    },
  })
  const { container } = await mount(() => {})
  const select = element<HTMLSelectElement>(container, '#mcp-client-profile')
  assert.equal(select.value, 'codex')
  assert.match(previewText(container), /\[mcp_servers\./)
  await act(async () => {
    select.value = 'cursor'
    select.dispatchEvent(new Event('change', { bubbles: true }))
  })
  assert.match(previewText(container), /"mcpServers"/)
  assert.equal(copies.length, 0)
  await act(async () => {
    button(container, 'Create connection token').click()
    await flush()
  })
  await waitFor(() => container.querySelector('#mcp-secret') !== null)
  assert.equal(copies.length, 0)
  await act(async () => {
    button(container, 'Copy configuration').click()
    await flush()
  })
  assert.equal(copies.length, 1)
  assert.equal(
    JSON.parse(copies[0]).mcpServers['my-agent'].headers.Authorization,
    `Bearer ${secret}`
  )
  assert.equal(previewText(container).includes(secret), false)
  await act(async () => {
    useAuthStore.getState().auth.setUser({ id: 2, username: 'next', role: 1 })
    await flush()
  })
  await waitFor(
    () => container.textContent?.includes('No connections yet') === true
  )
  assert.equal(container.querySelector('#mcp-secret'), null)
  assert.equal(container.innerHTML.includes(secret), false)
})
