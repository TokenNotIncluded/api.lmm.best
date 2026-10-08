/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, afterEach, beforeEach, test } from 'node:test'

import type { AxiosAdapter, AxiosResponse } from 'axios'
import { Window } from 'happy-dom'
import type { ReactNode } from 'react'
import type { Root } from 'react-dom/client'

const dom = new Window({
  url: 'https://console.example.test/system-settings/operations/commerce-import',
})
dom.document.write('<!doctype html><html><head></head><body></body></html>')
Object.defineProperty(dom.document, 'compatMode', {
  configurable: true,
  value: 'CSS1Compat',
})
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLInputElement',
  'HTMLTextAreaElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
  'PointerEvent',
  'FocusEvent',
  'CustomEvent',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
  'matchMedia',
  'customElements',
  'CSSStyleSheet',
  'localStorage',
  'sessionStorage',
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
const { QueryClient, QueryClientProvider, notifyManager } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const { CommerceImportSettingsSection, CommerceImportSettingsForm } =
  await import('./commerce-import-settings-section')
const { COMMERCE_IMPORT_SETTINGS_COPY: copy } =
  await import('./commerce-import-settings-copy')
notifyManager.setScheduler(queueMicrotask)
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })

type Request = {
  method: string
  url: string
  values?: Record<string, string>
}
const originalAdapter = api.defaults.adapter
const originalAuth = useAuthStore.getState().auth
const requests: Request[] = []
let stored: Record<string, string>
let override = false
let rejectSave = false
let container: HTMLDivElement
let root: Root
let queryClient: InstanceType<typeof QueryClient>
const effectiveOrigins = ['https://environment.example.test']

function response(
  config: Parameters<AxiosAdapter>[0],
  data: unknown
): AxiosResponse {
  return { config, data, status: 200, statusText: 'OK', headers: {} }
}

function signIn(role: number) {
  const now = Math.floor(Date.now() / 1000)
  useAuthStore.getState().auth.setBundle({
    access_token: 'local-settings-fixture',
    token_type: 'Bearer',
    access_expires_at: now + 3600,
    user: { id: 19, username: 'settings-fixture', role, status: 1 },
    session: {
      sid: 'commerce-import-settings-session',
      current: true,
      login_method: 'password',
      ip: '',
      user_agent: '',
      created_at: now,
      last_active_at: now,
      expires_at: now + 3600,
    },
  })
}

async function mount(node: ReactNode = <CommerceImportSettingsSection />) {
  await act(async () => {
    root.render(
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>{node}</I18nextProvider>
      </QueryClientProvider>
    )
  })
}

async function until(check: () => boolean, label: string) {
  for (let attempt = 0; attempt < 100; attempt += 1) {
    if (check()) return
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 5))
    })
  }
  assert.ok(check(), label)
}

function originsInput() {
  const input = container.querySelector<HTMLTextAreaElement>('textarea')
  assert.ok(input, 'trusted origins editor')
  return input
}

function enabledSwitch() {
  const control = container.querySelector<HTMLButtonElement>('[role="switch"]')
  assert.ok(control, 'external shop imports switch')
  return control
}

function saveButton() {
  const button = container.querySelector<HTMLButtonElement>(
    'button[type="submit"]'
  )
  assert.ok(button, 'save external shop import settings button')
  return button
}

async function editOrigins(value: string) {
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(
      dom.HTMLTextAreaElement.prototype,
      'value'
    )?.set
    assert.ok(setter)
    const input = originsInput()
    setter.call(input, value)
    input.dispatchEvent(new Event('input', { bubbles: true }))
    input.dispatchEvent(new Event('change', { bubbles: true }))
  })
}

async function submit() {
  const form = container.querySelector('form')
  assert.ok(form)
  await act(async () => {
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
  })
}

function writes() {
  return requests.filter((request) => request.method === 'post')
}

function optionReads() {
  return requests.filter(
    (request) => request.method === 'get' && request.url === '/api/option/'
  )
}

beforeEach(() => {
  requests.length = 0
  stored = {
    MerchantStoreCommerceImportEnabled: 'false',
    MerchantStoreCommerceImportTrustedOrigins: '[]',
  }
  override = false
  rejectSave = false
  signIn(100)
  api.defaults.adapter = async (config) => {
    const method = config.method ?? 'get'
    const url = config.url ?? ''
    const body = config.data ? JSON.parse(String(config.data)) : undefined
    requests.push({ method, url, values: body?.values })
    if (method === 'get' && url === '/api/option/') {
      return response(config, {
        success: true,
        data: Object.entries(stored).map(([key, value]) => ({ key, value })),
      })
    }
    if (method === 'get' && url === '/api/store/commerce-import/config') {
      return response(config, {
        success: true,
        data: {
          enabled:
            override || stored.MerchantStoreCommerceImportEnabled === 'true',
          trusted_origins: override
            ? effectiveOrigins
            : JSON.parse(stored.MerchantStoreCommerceImportTrustedOrigins),
          redirect_uri:
            'https://console.example.test/api/store/commerce-import/callback',
          environment_override: override,
        },
      })
    }
    if (method === 'post' && url === '/api/option/bulk') {
      if (rejectSave) {
        return response(config, {
          success: false,
          message: 'Settings could not be saved.',
        })
      }
      stored = { ...stored, ...body.values }
      return response(config, { success: true, message: '' })
    }
    throw new Error(`Unexpected request: ${method} ${url}`)
  }
  queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: Infinity },
      mutations: { retry: false },
    },
  })
  container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)
})

afterEach(async () => {
  await act(async () => root.unmount())
  queryClient.clear()
  container.remove()
  api.defaults.adapter = originalAdapter
  useAuthStore.setState({ auth: originalAuth })
})
after(() => dom.close())

test('root saves the switch and origins JSON array in one acknowledged bulk write and refreshes options', async () => {
  await mount()
  await until(() => !!container.querySelector('textarea'), 'root settings load')
  assert.equal(optionReads().length, 1)
  assert.equal(enabledSwitch().getAttribute('aria-checked'), 'false')
  assert.equal(originsInput().value, '')
  assert.equal(saveButton().disabled, true)
  const input =
    '  https://shop.example.test  \n\nhttps://catalog.example.test:8443\n'
  await editOrigins(input)
  await act(async () => enabledSwitch().click())
  assert.equal(saveButton().disabled, false)
  await submit()
  await until(
    () => optionReads().length === 2 && saveButton().disabled,
    'an acknowledged save refreshes the persisted options'
  )
  const values = {
    MerchantStoreCommerceImportEnabled: 'true',
    MerchantStoreCommerceImportTrustedOrigins:
      '["https://shop.example.test","https://catalog.example.test:8443"]',
  }
  assert.deepEqual(writes(), [
    { method: 'post', url: '/api/option/bulk', values },
  ])
  assert.deepEqual(stored, values)
  assert.equal(enabledSwitch().getAttribute('aria-checked'), 'true')
  assert.equal(
    originsInput().value,
    'https://shop.example.test\nhttps://catalog.example.test:8443'
  )
  assert.deepEqual(queryClient.getQueryData(['system-options']), {
    success: true,
    data: Object.entries(values).map(([key, value]) => ({ key, value })),
  })
})

test('a seller cannot render administrator settings or request system options', async () => {
  signIn(1)
  await mount()
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 10))
  })
  assert.equal(container.textContent, '')
  assert.equal(container.querySelector('form'), null)
  assert.deepEqual(requests, [])
})

for (const [kind, value] of [
  ['internal address', 'https://inventory.internal'],
  ['HTTP origin', 'http://shop.example.test'],
  ['origin with a path', 'https://shop.example.test/catalog'],
  ['duplicate origins', 'https://shop.example.test\nhttps://shop.example.test'],
]) {
  test(`rejects ${kind} before submitting any options`, async () => {
    await mount(
      <CommerceImportSettingsForm
        enabled
        origins='https://saved.example.test'
      />
    )
    await editOrigins(value)
    assert.equal(originsInput().getAttribute('aria-invalid'), 'true')
    assert.equal(saveButton().disabled, true)
    assert.ok(container.textContent?.includes(copy.invalidOrigins))
    await submit()
    assert.deepEqual(writes(), [])
    assert.equal(originsInput().value, value)
  })
}

test('enabling imports requires at least one trusted origin', async () => {
  await mount(<CommerceImportSettingsForm enabled={false} origins='' />)
  await act(async () => enabledSwitch().click())
  assert.equal(originsInput().getAttribute('aria-invalid'), 'true')
  assert.ok(container.textContent?.includes(copy.enabledWithoutOrigins))
  assert.equal(saveButton().disabled, true)
  await submit()
  assert.deepEqual(writes(), [])
})

test('environment override explains stored settings and shows the effective origins', async () => {
  override = true
  await mount()
  await until(
    () => container.textContent?.includes(copy.environmentOverride) === true,
    'server environment override notice loads'
  )
  assert.ok(container.textContent?.includes(copy.effectiveOrigins))
  assert.ok(container.textContent?.includes(effectiveOrigins[0]))
  assert.equal(enabledSwitch().getAttribute('aria-checked'), 'false')
  assert.equal(originsInput().value, '')
  assert.equal(originsInput().disabled, false)
})

test('a rejected bulk save preserves the editable draft and allows a successful retry', async () => {
  rejectSave = true
  await mount()
  await until(() => !!container.querySelector('textarea'), 'root settings load')
  const input = 'https://draft.example.test'
  await editOrigins(input)
  await act(async () => enabledSwitch().click())
  await submit()
  await until(
    () => writes().length === 1 && !saveButton().disabled,
    'rejected save leaves the draft available for retry'
  )
  assert.equal(originsInput().value, input)
  assert.equal(originsInput().disabled, false)
  assert.equal(enabledSwitch().getAttribute('aria-checked'), 'true')
  assert.equal(optionReads().length, 1)
  assert.deepEqual(stored, {
    MerchantStoreCommerceImportEnabled: 'false',
    MerchantStoreCommerceImportTrustedOrigins: '[]',
  })
  assert.equal(container.textContent?.includes(copy.saved), false)
  rejectSave = false
  await submit()
  await until(
    () =>
      writes().length === 2 &&
      optionReads().length === 2 &&
      saveButton().disabled,
    'retry acknowledges the same draft and refreshes options'
  )
  assert.deepEqual(writes()[1].values, writes()[0].values)
  assert.equal(originsInput().value, input)
  assert.equal(stored.MerchantStoreCommerceImportEnabled, 'true')
  assert.equal(
    stored.MerchantStoreCommerceImportTrustedOrigins,
    JSON.stringify([input])
  )
})
