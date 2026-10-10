/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'

const dom = new Window({ url: 'https://console.example.test/keys' })
for (const key of [
  'window',
  'document',
  'navigator',
  'localStorage',
  'sessionStorage',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLInputElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
  'CustomEvent',
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
Object.defineProperty(globalThis, 'IS_REACT_ACT_ENVIRONMENT', {
  configurable: true,
  value: true,
})

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const { CCSwitchDialog } = await import('../dialogs/cc-switch-dialog')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})
const originalFetch = globalThis.fetch
const originalAdapter = api.defaults.adapter
const originalOpen = window.open
const originalAuth = useAuthStore.getState().auth

type DialogProps = { open: boolean; tokenKey: string; tokenId?: number }
let current: {
  root: ReturnType<typeof createRoot>
  host: HTMLElement
  client: InstanceType<typeof QueryClient>
} | null = null
const catalogue = (ids: string[]) =>
  Response.json({
    object: 'list',
    data: ids.map((id) => ({ id, object: 'model' })),
  })

async function flush() {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 30))
  })
}
async function render(props: DialogProps) {
  return renderDialogs([props])
}
async function renderDialogs(dialogs: DialogProps[]) {
  if (!current) {
    const host = document.createElement('div')
    document.body.append(host)
    current = {
      host,
      root: createRoot(host),
      client: new QueryClient({
        defaultOptions: { queries: { retry: false } },
      }),
    }
  }
  const { root, client } = current
  await act(async () => {
    root.render(
      <QueryClientProvider client={client}>
        <I18nextProvider i18n={i18n}>
          {dialogs.map((props, index) => (
            <CCSwitchDialog key={index} {...props} onOpenChange={() => {}} />
          ))}
        </I18nextProvider>
      </QueryClientProvider>
    )
  })
  await flush()
  return client
}
function importButton() {
  const button = Array.from(document.querySelectorAll('button')).find(
    (item) => item.textContent?.trim() === 'Open CC Switch'
  )
  assert.ok(button)
  return button
}
async function options() {
  const input = document.querySelector<HTMLInputElement>(
    'input[placeholder="Select or enter model name"]'
  )
  assert.ok(input)
  await act(async () => {
    input.blur()
    input.focus()
  })
  return Array.from(
    document.querySelectorAll<HTMLElement>('[role="option"]')
  ).map((item) => item.textContent?.trim())
}
async function selectModel(id: string) {
  await options()
  const item = Array.from(
    document.querySelectorAll<HTMLElement>('[role="option"]')
  ).find((option) => option.textContent?.trim() === id)
  assert.ok(item)
  await act(async () =>
    item.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
  )
}
function mockAccountDirectory() {
  const requests: string[] = []
  api.defaults.adapter = async (config) => {
    requests.push(config.url ?? '')
    const data =
      config.url === '/api/user/models'
        ? { success: true, data: ['allowed-model', 'denied-model'] }
        : {
            success: true,
            data: {
              id: 42,
              group: 'default',
              model_limits_enabled: true,
              model_limits: 'allowed-model',
              account_balance_read: false,
            },
          }
    return { data, status: 200, statusText: 'OK', headers: {}, config }
  }
  return requests
}
afterEach(async () => {
  if (current) {
    const { root, host, client } = current
    await act(async () => root.unmount())
    client.clear()
    host.remove()
    current = null
  }
  globalThis.fetch = originalFetch
  api.defaults.adapter = originalAdapter
  window.open = originalOpen
  useAuthStore.setState({ auth: originalAuth })
  localStorage.clear()
  sessionStorage.clear()
})
after(() => dom.close())

test('CCSwitch offers only the target Key relay catalogue and keeps credentials out of caches', async () => {
  const accountRequests = mockAccountDirectory()
  useAuthStore.setState({
    auth: { ...originalAuth, accessToken: 'console-session-secret' },
  })
  localStorage.setItem(
    'status',
    JSON.stringify({ server_address: 'https://third-party.example/' })
  )
  const requests: Array<{ url: string; init: RequestInit | undefined }> = []
  globalThis.fetch = (async (url, init) => {
    requests.push({ url: String(url), init })
    return catalogue(['allowed-model'])
  }) as typeof fetch
  const client = await render({
    open: true,
    tokenId: 42,
    tokenKey: 'sk-target-key-secret',
  })
  assert.deepEqual(await options(), ['allowed-model'])
  assert.deepEqual(accountRequests, ['/api/token/42'])
  assert.equal(requests.length, 1)
  assert.equal(requests[0].url, '/v1/models')
  const init = requests[0].init
  assert.equal(
    new Headers(init?.headers).get('Authorization'),
    'Bearer sk-target-key-secret'
  )
  assert.equal(init?.credentials, 'omit')
  assert.equal(init?.cache, 'no-store')
  assert.equal(init?.redirect, 'error')
  assert.equal(init?.method, 'GET')
  assert.ok(init?.signal)
  assert.equal(importButton().disabled, true)
  await selectModel('allowed-model')
  assert.equal(importButton().disabled, false)
  const cache = client
    .getQueryCache()
    .getAll()
    .map(({ queryKey, state }) => ({ queryKey, state }))
  assert.doesNotMatch(
    JSON.stringify(cache),
    /target-key-secret|console-session-secret/
  )
  assert.doesNotMatch(
    document.body.textContent ?? '',
    /target-key-secret|console-session-secret/
  )
  assert.doesNotMatch(
    JSON.stringify([localStorage, sessionStorage]),
    /target-key-secret|console-session-secret/
  )
  const popup = {
    closed: false,
    location: { href: '' },
    opener: window,
    focus() {},
    close() {},
  }
  window.open = (() => popup) as unknown as typeof window.open
  await act(async () => importButton().click())
  const imported = new URL(popup.location.href)
  assert.equal(imported.protocol, 'ccswitch:')
  assert.equal(imported.searchParams.get('model'), 'allowed-model')
  assert.equal(imported.searchParams.get('apiKey'), 'sk-target-key-secret')
  assert.equal(
    imported.searchParams.get('endpoint'),
    'https://third-party.example/'
  )
  assert.equal(
    requests.length,
    1,
    'import does not send credentials to the configured address'
  )
})

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => {
    resolve = done
  })
  return { promise, resolve }
}

test('rotating a Key with the same ID clears selection and ignores its late catalogue', async () => {
  mockAccountDirectory()
  const old = deferred<Response>()
  const signals: AbortSignal[] = []
  globalThis.fetch = (async (_url, init) => {
    assert.ok(init?.signal)
    signals.push(init.signal)
    return new Headers(init.headers).get('Authorization') ===
      'Bearer sk-old-secret'
      ? old.promise
      : catalogue(['new-model'])
  }) as typeof fetch
  const client = await render({
    open: true,
    tokenId: 42,
    tokenKey: 'sk-old-secret',
  })
  assert.equal(importButton().disabled, true)
  await render({ open: true, tokenId: 42, tokenKey: 'sk-new-secret' })
  assert.deepEqual(await options(), ['new-model'])
  assert.equal(signals[0].aborted, true)
  await selectModel('new-model')
  assert.equal(importButton().disabled, false)
  await act(async () => old.resolve(catalogue(['old-model'])))
  await flush()
  assert.deepEqual(await options(), ['new-model'])
  assert.doesNotMatch(
    JSON.stringify(
      client
        .getQueryCache()
        .getAll()
        .map(({ queryKey, state }) => ({ queryKey, state }))
    ),
    /old-model|old-secret|new-secret/
  )
  await render({ open: true, tokenId: 42, tokenKey: 'sk-third-secret' })
  assert.equal(
    importButton().disabled,
    true,
    'a previous selection is not imported with a new Key'
  )
})

test('one-time Keys without IDs have separate catalogues and reopening requires fresh authorization', async () => {
  const accountRequests = mockAccountDirectory()
  const calls: string[] = []
  globalThis.fetch = (async (_url, init) => {
    const auth = new Headers(init?.headers).get('Authorization') ?? ''
    calls.push(auth)
    return catalogue([
      auth === 'Bearer sk-first-secret' ? 'first-model' : 'second-model',
    ])
  }) as typeof fetch
  const client = await render({ open: true, tokenKey: 'first-secret' })
  assert.deepEqual(await options(), ['first-model'])
  await selectModel('first-model')
  assert.equal(importButton().disabled, false)
  await render({ open: true, tokenKey: 'sk-second-secret' })
  assert.deepEqual(await options(), ['second-model'])
  assert.equal(importButton().disabled, true)
  await render({ open: false, tokenKey: 'sk-second-secret' })
  assert.equal(
    client
      .getQueryCache()
      .getAll()
      .filter((query) => query.queryKey[0] === 'user-models-ccswitch').length,
    0
  )
  await render({ open: true, tokenKey: 'sk-second-secret' })
  assert.deepEqual(await options(), ['second-model'])
  assert.equal(importButton().disabled, true)
  assert.deepEqual(calls, [
    'Bearer sk-first-secret',
    'Bearer sk-second-secret',
    'Bearer sk-second-secret',
  ])
  assert.deepEqual(
    accountRequests,
    [],
    'ID-less Keys do not use owner metadata or the account directory'
  )
})

test('simultaneous Key dialogs never share catalogue data or garbage collection', async () => {
  mockAccountDirectory()
  const calls: string[] = []
  globalThis.fetch = (async (_url, init) => {
    const auth = new Headers(init?.headers).get('Authorization') ?? ''
    calls.push(auth)
    return catalogue([
      auth === 'Bearer sk-first-secret' ? 'first-model' : 'second-model',
    ])
  }) as typeof fetch
  const dialogs = [
    { open: true, tokenKey: 'sk-first-secret' },
    { open: true, tokenKey: 'sk-second-secret' },
  ]
  const client = await renderDialogs(dialogs)
  const queries = () =>
    client
      .getQueryCache()
      .getAll()
      .filter((query) => query.queryKey[0] === 'user-models-ccswitch')
  assert.equal(queries().length, 2)
  assert.deepEqual(
    queries().map((query) => query.state.data),
    [['first-model'], ['second-model']]
  )
  assert.notDeepEqual(queries()[0].queryKey, queries()[1].queryKey)
  assert.doesNotMatch(
    JSON.stringify(
      queries().map(({ queryKey, state }) => ({ queryKey, state }))
    ),
    /first-secret|second-secret/
  )
  await renderDialogs([{ ...dialogs[0], open: false }, dialogs[1]])
  assert.equal(queries().length, 1)
  assert.deepEqual(queries()[0].state.data, ['second-model'])
  assert.deepEqual(await options(), ['second-model'])
  assert.equal(
    calls.length,
    2,
    'closing one dialog does not refetch or remove the other'
  )
})

test('Key configuration invalidation reflects the relay Auto-group result and blocks export while refreshing', async () => {
  mockAccountDirectory()
  const refreshed = deferred<Response>()
  let calls = 0
  globalThis.fetch = (async (_url, init) => {
    assert.equal(
      new Headers(init?.headers).get('Authorization'),
      'Bearer sk-auto-secret'
    )
    calls += 1
    return calls === 1 ? catalogue(['vip-allowed']) : refreshed.promise
  }) as typeof fetch
  const client = await render({ open: true, tokenKey: 'sk-auto-secret' })
  assert.deepEqual(await options(), ['vip-allowed'])
  await selectModel('vip-allowed')
  assert.equal(importButton().disabled, false)
  let refresh!: Promise<unknown>
  const popup = {
    closed: false,
    location: { href: '' },
    opener: window,
    focus() {},
    close() {},
  }
  window.open = (() => popup) as unknown as typeof window.open
  await act(async () => {
    refresh = client.invalidateQueries({ queryKey: ['user-models-ccswitch'] })
    importButton().click()
  })
  assert.equal(
    popup.location.href,
    '',
    'invalidation blocks import before its render notification'
  )
  await flush()
  assert.equal(calls, 2)
  assert.equal(importButton().disabled, true)
  assert.deepEqual(await options(), [])
  await act(async () => {
    refreshed.resolve(catalogue(['default-allowed']))
    await refresh
  })
  await flush()
  assert.deepEqual(await options(), ['default-allowed'])
  assert.equal(
    importButton().disabled,
    true,
    'a model outside the new resolved groups is cleared'
  )
  await selectModel('default-allowed')
  assert.equal(importButton().disabled, false)
})

test('expired or revoked Key errors cannot export a previous selection or retain raw errors', async () => {
  mockAccountDirectory()
  let calls = 0
  globalThis.fetch = (async () => {
    calls += 1
    if (calls === 1) return catalogue(['allowed-model'])
    if (calls === 2) {
      return Response.json(
        { error: { message: 'sk-expired-secret echoed by provider' } },
        { status: 401 }
      )
    }
    throw new Error('redirect or network failure with sk-expired-secret')
  }) as typeof fetch
  const client = await render({ open: true, tokenKey: 'sk-expired-secret' })
  await selectModel('allowed-model')
  assert.equal(importButton().disabled, false)
  await act(async () => {
    await client.invalidateQueries({ queryKey: ['user-models-ccswitch'] })
  })
  await flush()
  assert.equal(importButton().disabled, true)
  assert.deepEqual(await options(), [])
  assert.match(document.body.textContent ?? '', /Failed to fetch models/)
  const retry = Array.from(document.querySelectorAll('button')).find(
    (item) => item.textContent?.trim() === 'Retry'
  )
  assert.ok(retry)
  await act(async () => retry.click())
  await flush()
  assert.equal(importButton().disabled, true)
  const query = client
    .getQueryCache()
    .getAll()
    .find((item) => item.queryKey[0] === 'user-models-ccswitch')
  assert.ok(query)
  assert.equal((query.state.error as Error).message, 'Failed to fetch models')
  assert.equal((query.state.error as Error).cause, undefined)
  assert.doesNotMatch(
    JSON.stringify(query.state),
    /expired-secret|echoed by provider/
  )
  assert.doesNotMatch(
    document.body.textContent ?? '',
    /expired-secret|echoed by provider/
  )
})
