/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { AxiosError, type InternalAxiosRequestConfig } from 'axios'
import { Window } from 'happy-dom'

import type { HoneypotPolicies } from './contract'

const dom = new Window({
  url: 'https://console.example.test/system-settings/security/honeypot',
})
dom.document.write('<!doctype html><html><head></head><body></body></html>')
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLInputElement',
  'HTMLSelectElement',
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
const { HoneypotWorkspace } = await import('./index')
const { DEFAULT_LIMITS } = await import('./contract')
const { hashHoneypotConfig } = await import('./contract')
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const previousAuth = useAuthStore.getState().auth
const originalAdapter = api.defaults.adapter
const mounts: Array<() => void> = []
const scope = { userId: 901, sessionId: 'synthetic-interaction-session' }
const policies: HoneypotPolicies = {
  version: 1,
  revision: 4,
  config: {
    version: 1,
    policies: [
      {
        id: 'fixed',
        name: 'Fixed',
        mode: 'fixed_text',
        public_models: ['public'],
        fixed_text: 'Example',
        clients: {},
        limits: DEFAULT_LIMITS,
      },
    ],
  },
  capabilities: {
    client_protocols: ['openai_chat', 'openai_responses'],
    upstream_protocols: [
      'openai_chat',
      'openai_responses',
      'anthropic_messages',
      'gemini',
    ],
    modes: ['fixed_text', 'custom_upstream'],
    numeric_loopback_only: true,
    credential_references: ['host-alias'],
  },
}
const response = (config: InternalAxiosRequestConfig, data: unknown) => ({
  status: 200,
  statusText: 'OK',
  headers: {},
  config,
  data,
})
function login(role = 100) {
  useAuthStore.getState().auth.setBundle({
    access_token: 'private-synthetic-unit-token',
    token_type: 'Bearer',
    access_expires_at: 2000000000,
    user: { id: 901, username: 'synthetic-root', role },
    session: {
      sid: scope.sessionId,
      current: true,
      login_method: 'password',
      ip: '127.0.0.1',
      user_agent: 'synthetic-unit',
      created_at: 1,
      last_active_at: 1,
      expires_at: 2000000000,
    },
  })
}
async function mount(element: React.ReactNode) {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  })
  await act(async () =>
    root.render(
      <QueryClientProvider client={client}>
        <I18nextProvider i18n={i18n}>{element}</I18nextProvider>
      </QueryClientProvider>
    )
  )
  mounts.push(() => {
    root.unmount()
    client.clear()
    container.remove()
  })
  return { container, client }
}
async function settle() {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 20))
  })
}
function findButton(label: string) {
  const button = [...document.querySelectorAll('button')].find(
    (item) => item.textContent?.trim() === label
  )
  assert.ok(button, `button ${label}`)
  return button
}
async function click(label: string) {
  const button = [...document.querySelectorAll('button')].find(
    (item) => item.textContent?.trim() === label
  )
  assert.ok(button, `button ${label}`)
  assert.equal(button.disabled, false, `button ${label} enabled`)
  await act(async () => button.click())
  await settle()
}
async function edit(label: string, value: string) {
  const wrapper = [...document.querySelectorAll('label')].find((item) =>
    item.textContent?.startsWith(label)
  )
  const input = wrapper?.querySelector('input,textarea,select') as
    | HTMLInputElement
    | HTMLTextAreaElement
    | HTMLSelectElement
  assert.ok(input, `field ${label}`)
  await act(async () => {
    const prototype =
      input.tagName === 'TEXTAREA'
        ? HTMLTextAreaElement.prototype
        : input.tagName === 'SELECT'
          ? HTMLSelectElement.prototype
          : HTMLInputElement.prototype
    const setter = Object.getOwnPropertyDescriptor(prototype, 'value')?.set
    assert.ok(setter)
    setter.call(input, value)
    input.dispatchEvent(
      new Event(input.tagName === 'SELECT' ? 'change' : 'input', {
        bubbles: true,
      })
    )
  })
}
afterEach(async () => {
  await act(async () => {
    for (const cleanup of mounts.splice(0)) cleanup()
  })
  api.defaults.adapter = originalAdapter
  useAuthStore.setState({ auth: previousAuth })
})
after(() => dom.close())

function transport(initial = policies, ambiguous = false) {
  let remote = structuredClone(initial)
  let writes = 0
  let operationID = ''
  let operationReads = 0
  api.defaults.adapter = async (config) => {
    if (config.method === 'put') {
      writes++
      const body = JSON.parse(config.data)
      operationID = body.operation_id
      remote = { ...remote, revision: remote.revision + 1, config: body.config }
      if (ambiguous) {
        throw new AxiosError('synthetic transport timeout', 'ETIMEDOUT', config)
      }
      return response(config, {
        version: 1,
        status: 'applied',
        operation_id: operationID,
        kind: 'policies',
        policy_revision: remote.revision,
        users: [],
        applied_config_hash: await hashHoneypotConfig(remote.config),
      })
    }
    if (config.url?.includes('/operations/')) {
      operationReads++
      assert.equal(config.url, `/core/v1/admin/abuse/operations/${operationID}`)
      return response(config, {
        version: 1,
        status: 'applied',
        operation_id: operationID,
        kind: 'policies',
        policy_revision: remote.revision,
        users: [],
        applied_config_hash: await hashHoneypotConfig(remote.config),
      })
    }
    if (config.url?.endsWith('/policies')) return response(config, remote)
    if (config.url?.endsWith('/users')) {
      return response(config, { version: 2, users: [], next_after: null })
    }
    throw new Error('Unexpected synthetic request')
  }
  return { writes: () => writes, operationReads: () => operationReads }
}
async function cycleTabs() {
  await click('Users')
  await click('Policies')
}

test('empty policy save updates the parent cache and survives switching Users then Policies', async () => {
  login()
  const server = transport({
    ...policies,
    revision: 0,
    config: { version: 1, policies: [] },
  })
  const { client } = await mount(<HoneypotWorkspace scope={scope} />)
  await settle()
  await click('Add policy')
  await edit('Policy ID', 'persisted-policy')
  await edit('Name', 'Persisted policy')
  await edit('Public models', 'public')
  await edit('Fixed text', 'Actual saved text')
  await edit('Audit reason', 'Reviewed synthetic policy')
  await click('Review changes')
  await click('Save changes')
  assert.equal(server.writes(), 1)
  const cached = client.getQueryData<HoneypotPolicies>([
    'honeypot-policies',
    scope.userId,
    scope.sessionId,
  ])
  assert.equal(cached?.revision, 1)
  assert.equal(cached?.config.policies[0]?.id, 'persisted-policy')
  await cycleTabs()
  assert.ok(
    [...document.querySelectorAll('input')].some(
      (input) => input.value === 'persisted-policy'
    )
  )
  assert.ok(
    [...document.querySelectorAll('textarea')].some(
      (input) => input.value === 'Actual saved text'
    )
  )
  assert.equal(server.writes(), 1)
})
test('a policy draft remains unchanged across tab switches without a write', async () => {
  login()
  const server = transport()
  await mount(<HoneypotWorkspace scope={scope} />)
  await settle()
  await edit('Fixed text', 'Unsaved draft')
  await edit('Audit reason', 'Draft reason')
  await cycleTabs()
  assert.ok(
    [...document.querySelectorAll('textarea')].some(
      (input) => input.value === 'Unsaved draft'
    )
  )
  assert.ok(
    [...document.querySelectorAll('textarea')].some(
      (input) => input.value === 'Draft reason'
    )
  )
  assert.equal(server.writes(), 0)
})
test('an unknown policy operation remains locked across tabs and recovers only the original operation', async () => {
  login()
  const server = transport(policies, true)
  await mount(<HoneypotWorkspace scope={scope} />)
  await settle()
  await edit('Fixed text', 'Unknown committed draft')
  await edit('Audit reason', 'Reviewed ambiguous synthetic write')
  await click('Review changes')
  await click('Save changes')
  assert.equal(server.writes(), 1)
  await cycleTabs()
  assert.equal(findButton('Save changes').disabled, true)
  assert.match(document.body.textContent ?? '', /Save result is unknown/)
  await click('Check original operation')
  assert.equal(server.writes(), 1)
  assert.equal(server.operationReads(), 1)
  assert.match(document.body.textContent ?? '', /Applied at revision 5/)
})
