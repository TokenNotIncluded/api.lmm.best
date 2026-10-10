/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import type { InternalAxiosRequestConfig } from 'axios'
import { Window } from 'happy-dom'

import type { ProfileShareState } from './api'

const dom = new Window({ url: 'http://127.0.0.1:4174/' })
dom.document.write('<!doctype html><html><body></body></html>')
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLInputElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
  'scrollTo',
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
const { RouterProvider, createMemoryHistory, createRootRoute, createRouter } =
  await import('@tanstack/react-router')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { ProfileSharePage } = await import('./share-page')
const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})
const originalAdapter = api.defaults.adapter
afterEach(() => {
  api.defaults.adapter = originalAdapter
  document.body.replaceChildren()
})
after(() => dom.close())

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((finish) => {
    resolve = finish
  })
  return { promise, resolve }
}
async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 0))
}
async function settled(predicate: () => boolean) {
  for (let index = 0; index < 50; index += 1) {
    if (predicate()) return
    await act(flush)
  }
  assert.ok(predicate(), 'The expected UI state did not settle')
}
function button(host: HTMLElement, name: string) {
  const result = [...host.querySelectorAll<HTMLButtonElement>('button')].find(
    (element) => element.textContent?.trim() === name
  )
  assert.ok(result, `Missing ${name}`)
  return result
}
function element<T extends Element>(host: ParentNode, selector: string): T {
  const result = host.querySelector<T>(selector)
  assert.ok(result, `Missing ${selector}`)
  return result
}
async function click(element: HTMLElement) {
  await act(async () => {
    element.click()
    await flush()
  })
}
async function input(element: HTMLInputElement, value: string) {
  await act(async () => {
    Object.getOwnPropertyDescriptor(
      dom.HTMLInputElement.prototype,
      'value'
    )?.set?.call(element, value)
    element.dispatchEvent(new Event('input', { bubbles: true }))
    element.dispatchEvent(new Event('change', { bubbles: true }))
  })
}
async function layout(host: HTMLElement, value: string) {
  const select = element<HTMLSelectElement>(host, '#badge-layout')
  await act(async () => {
    select.value = value
    select.dispatchEvent(new Event('change', { bubbles: true }))
  })
}
const old: ProfileShareState = {
  enabled: true,
  model_usage_enabled: false,
  aggregate_usage_enabled: true,
  token: 'a'.repeat(48),
  url: `http://127.0.0.1:4174/api/share/profile/${'a'.repeat(48)}.svg`,
  linked_profiles: [{ provider: 'cursor', url: 'https://cursor.com/@old' }],
}

async function mount() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  const rootRoute = createRootRoute({
    component: () => (
      <QueryClientProvider client={client}>
        <I18nextProvider i18n={i18n}>
          <ProfileSharePage />
        </I18nextProvider>
      </QueryClientProvider>
    ),
  })
  const router = createRouter({
    routeTree: rootRoute,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  await act(async () => {
    root.render(<RouterProvider router={router} />)
    await flush()
  })
  await settled(() => !!host.querySelector('input[type="url"]'))
  return {
    host,
    client,
    close: async () => {
      await act(async () => root.unmount())
      client.clear()
    },
  }
}

function adapter() {
  const delayedRead = deferred<void>()
  const delayedWrite = deferred<void>()
  const writes: Record<string, unknown>[] = []
  let state = structuredClone(old)
  let readSignal: InternalAxiosRequestConfig['signal']
  let holdRead = false
  let holdWrite = false
  let failWrite = false
  api.defaults.adapter = async (config) => {
    const method = config.method
    let data: unknown
    if (config.url === '/api/user/self') {
      data = { username: 'review-account' }
    } else {
      assert.equal(config.url, '/api/user/self/profile-share')
      if (method === 'get') {
        data = structuredClone(state)
        if (holdRead) {
          readSignal = config.signal
          await delayedRead.promise // Deliberately ignores abort to test stale result isolation.
        }
      } else {
        const body = config.data ? JSON.parse(config.data) : {}
        writes.push(body)
        if (holdWrite) await delayedWrite.promise
        if (!failWrite) {
          state =
            method === 'delete'
              ? {
                  ...state,
                  enabled: false,
                  aggregate_usage_enabled: false,
                  model_usage_enabled: false,
                }
              : { ...state, ...body }
        }
        data = structuredClone(state)
      }
    }
    return {
      config,
      headers: {},
      status: 200,
      statusText: 'OK',
      data:
        failWrite && method === 'post'
          ? { success: false, message: 'private upstream detail' }
          : { success: true, data },
    }
  }
  return {
    writes,
    delayedRead,
    delayedWrite,
    get signal() {
      return readSignal
    },
    holdRead: () => {
      holdRead = true
    },
    holdWrite: () => {
      holdWrite = true
    },
    failWrite: () => {
      failWrite = true
    },
  }
}

test('confirmed aggregate save survives an older GET and the next edit keeps confirmed consent and account', async () => {
  const requests = adapter()
  const view = await mount()
  try {
    const url = element<HTMLInputElement>(view.host, 'input[type="url"]')
    await input(url, 'https://cursor.com/@confirmed')
    await click(element<HTMLElement>(view.host, '[data-slot="switch"]'))
    requests.holdRead()
    let read!: Promise<unknown>
    await act(async () => {
      read = view.client.refetchQueries({
        queryKey: ['profile-share'],
        exact: true,
      })
    })
    await click(button(view.host, 'Save linked accounts'))
    await settled(
      () => view.host.textContent?.includes('Linked accounts saved.') === true
    )
    assert.equal(
      view.client.getQueryData<ProfileShareState>(['profile-share'])
        ?.aggregate_usage_enabled,
      false
    )
    await act(async () => {
      requests.delayedRead.resolve()
      await read
      await flush()
    })
    assert.equal(
      view.client.getQueryData<ProfileShareState>(['profile-share'])
        ?.aggregate_usage_enabled,
      false
    )
    assert.equal(
      view.host.querySelector<HTMLInputElement>('input[type="url"]')?.value,
      'https://cursor.com/@confirmed'
    )
    assert.equal(
      view.host
        .querySelector('[data-slot="switch"]')
        ?.hasAttribute('data-checked'),
      false
    )
    assert.equal(requests.signal?.aborted, true)
    const label = element<HTMLInputElement>(view.host, 'input[id$="-label"]')
    await input(label, 'Confirmed label')
    await click(button(view.host, 'Save linked accounts'))
    await settled(() => requests.writes.length === 2)
    assert.deepEqual(requests.writes[1], {
      aggregate_usage_enabled: false,
      linked_profiles: [
        {
          provider: 'cursor',
          url: 'https://cursor.com/@confirmed',
          label: 'Confirmed label',
        },
      ],
    })
  } finally {
    requests.delayedRead.resolve()
    await act(flush)
    await view.close()
  }
})

for (const operation of ['enable', 'disable'] as const) {
  test(`confirmed ${operation} cancels old sharing reads before updating the cache`, async () => {
    const requests = adapter()
    const view = await mount()
    try {
      requests.holdRead()
      let read!: Promise<unknown>
      await act(async () => {
        read = view.client.refetchQueries({
          queryKey: ['profile-share'],
          exact: true,
        })
      })
      await layout(view.host, operation === 'enable' ? 'models' : 'profile')
      const sharing = element<HTMLElement>(
        view.host,
        '[data-testid="badge-sharing"]'
      )
      await click(
        button(
          sharing,
          operation === 'enable'
            ? 'Turn on model sharing'
            : 'Turn off public badge'
        )
      )
      await settled(() => {
        const state = view.client.getQueryData<ProfileShareState>([
          'profile-share',
        ])
        return operation === 'enable'
          ? state?.model_usage_enabled === true
          : state?.enabled === false
      })
      await act(async () => {
        requests.delayedRead.resolve()
        await read
        await flush()
      })
      const state = view.client.getQueryData<ProfileShareState>([
        'profile-share',
      ])
      assert.equal(
        operation === 'enable' ? state?.model_usage_enabled : state?.enabled,
        operation === 'enable'
      )
      assert.equal(requests.signal?.aborted, true)
    } finally {
      requests.delayedRead.resolve()
      await act(flush)
      await view.close()
    }
  })
}

test('failed save keeps edited accounts and consent, while pending aggregate save blocks legacy revoke', async () => {
  const requests = adapter()
  requests.holdWrite()
  requests.failWrite()
  const view = await mount()
  try {
    const url = element<HTMLInputElement>(view.host, 'input[type="url"]')
    await input(url, 'https://cursor.com/@unsaved')
    await click(element<HTMLElement>(view.host, '[data-slot="switch"]'))
    await click(button(view.host, 'Save linked accounts'))
    await layout(view.host, 'profile')
    assert.equal(
      button(
        element<HTMLElement>(view.host, '[data-testid="badge-sharing"]'),
        'Turn off public badge'
      ).disabled,
      true
    )
    await act(async () => {
      requests.delayedWrite.resolve()
      await flush()
    })
    await settled(
      () =>
        view.host.textContent?.includes('Could not save linked accounts') ===
        true
    )
    assert.equal(
      view.host.querySelector<HTMLInputElement>('input[type="url"]')?.value,
      'https://cursor.com/@unsaved'
    )
    assert.equal(
      view.host
        .querySelector('[data-slot="switch"]')
        ?.hasAttribute('data-checked'),
      false
    )
    assert.deepEqual(view.client.getQueryData(['profile-share']), old)
  } finally {
    requests.delayedWrite.resolve()
    await view.close()
  }
})

test('the top linked sharing toggle sends only consent and preserves configured accounts', async () => {
  const requests = adapter()
  const view = await mount()
  try {
    await click(
      button(
        element<HTMLElement>(view.host, '[data-testid="badge-sharing"]'),
        'Turn off linked sharing'
      )
    )
    assert.deepEqual(requests.writes, [{ aggregate_usage_enabled: false }])
    assert.equal(
      view.host.querySelector<HTMLInputElement>('input[type="url"]')?.value,
      'https://cursor.com/@old'
    )
  } finally {
    await view.close()
  }
})
