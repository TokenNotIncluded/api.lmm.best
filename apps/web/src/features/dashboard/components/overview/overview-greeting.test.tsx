/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { AxiosError, type AxiosAdapter } from 'axios'
import { Window } from 'happy-dom'
import { toast } from 'sonner'

const dom = new Window({ url: 'http://localhost/' })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLInputElement',
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
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const { OverviewGreeting } = await import('./overview-greeting')
const originalAdapter = api.defaults.adapter
const originalToast = toast.error
const originalUser = useAuthStore.getState().auth.user
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const user = {
  id: 905,
  username: 'greeting-user',
  display_name: 'Ada',
  role: 1,
  quota: 500000,
} as NonNullable<typeof originalUser>
const emptyPreference = { templates: {}, revision: 0, updated_at: 0 }
const queryKey = ['overview-greeting', user.id]
let dispose: (() => Promise<void>) | undefined
afterEach(async () => {
  await dispose?.()
  dispose = undefined
  api.defaults.adapter = originalAdapter
  toast.error = originalToast
  useAuthStore.getState().auth.setUser(originalUser)
  document.body.replaceChildren()
})
after(() => dom.close())

function respond(
  config: Parameters<AxiosAdapter>[0],
  status: number,
  data: unknown
) {
  const response = { config, status, data, statusText: '', headers: {} }
  // Apply the same status settlement as Axios's HTTP/browser transports.
  if (config.validateStatus && !config.validateStatus(status)) {
    throw new AxiosError(
      'Request failed',
      'ERR_BAD_RESPONSE',
      config,
      null,
      response
    )
  }
  return response
}
async function mount() {
  useAuthStore.getState().auth.setUser(user)
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(
      <QueryClientProvider client={client}>
        <I18nextProvider i18n={i18n}>
          <OverviewGreeting />
        </I18nextProvider>
      </QueryClientProvider>
    )
  })
  dispose = async () => {
    await act(async () => root.unmount())
    client.clear()
  }
  return { container, client }
}
async function until(check: () => boolean) {
  for (let i = 0; i < 100 && !check(); i++) {
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 10))
    })
  }
  assert.ok(check(), 'Expected overview greeting UI state')
}
function editButton(container: HTMLElement) {
  const button = container.querySelector<HTMLButtonElement>(
    '[aria-label="Edit overview greeting"]'
  )
  assert.ok(button)
  return button
}

test('a missing greeting API silently uses the default and cannot edit or write', async () => {
  const errors: unknown[] = []
  toast.error = ((message: unknown) =>
    errors.push(message)) as typeof toast.error
  const requests: string[] = []
  api.defaults.adapter = async (config) => {
    requests.push(config.method ?? '')
    assert.equal(config.url, '/api/assistant/workspace/greeting')
    return respond(config, 404, { success: false, message: 'Not found' })
  }
  const { container, client } = await mount()
  await until(() => client.getQueryState(queryKey)?.status === 'success')
  assert.equal(client.getQueryData(queryKey), null)
  assert.match(container.textContent ?? '', /HI,Ada, it is/)
  assert.equal(container.querySelector('[role="alert"]'), null)
  assert.equal(editButton(container).disabled, true)
  await act(async () => editButton(container).click())
  assert.equal(document.querySelector('[role="dialog"]'), null)
  assert.deepEqual(errors, [])
  assert.deepEqual(requests, ['get'])
})

test('a valid empty preference supports editing and preserves revisioned saves', async () => {
  let saved: unknown
  api.defaults.adapter = async (config) => {
    if (config.method === 'put') {
      saved = JSON.parse(String(config.data))
      return respond(config, 200, {
        success: true,
        data: { ...emptyPreference, revision: 1, updated_at: 1 },
      })
    }
    return respond(config, 200, { success: true, data: emptyPreference })
  }
  const { container, client } = await mount()
  await until(() => !editButton(container).disabled)
  await act(async () => editButton(container).click())
  await until(() => document.querySelector('[role="dialog"] textarea') !== null)
  const save = [
    ...document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button'),
  ].find((button) => button.textContent === 'Save')
  assert.ok(save)
  assert.equal(save.disabled, false)
  await act(async () => save.click())
  await until(() => saved !== undefined)
  await until(
    () => client.getQueryData<{ revision: number }>(queryKey)?.revision === 1
  )
  assert.deepEqual(saved, { language: 'en', template: '', revision: 0 })
})

test('a later 404 clears cached customization and closes editing without a write', async () => {
  let supported = true
  let writes = 0
  api.defaults.adapter = async (config) => {
    if (config.method === 'put') writes++
    return supported
      ? respond(config, 200, {
          success: true,
          data: {
            templates: { en: 'Welcome,$name; $$' },
            revision: 2,
            updated_at: 2,
          },
        })
      : respond(config, 404, { error: { message: 'Unknown request' } })
  }
  const { container, client } = await mount()
  await until(() => container.textContent?.includes('Welcome,Ada; $') === true)
  await act(async () => editButton(container).click())
  await until(() => document.querySelector('[role="dialog"] textarea') !== null)
  supported = false
  await act(async () => {
    await client.refetchQueries({ queryKey })
  })
  await until(() => document.querySelector('[role="dialog"]') === null)
  assert.match(container.textContent ?? '', /HI,Ada, it is/)
  assert.doesNotMatch(container.textContent ?? '', /Welcome/)
  assert.equal(container.querySelector('[role="alert"]'), null)
  assert.equal(editButton(container).disabled, true)
  assert.equal(writes, 0)
  supported = true
  await act(async () => {
    await client.refetchQueries({ queryKey })
  })
  await until(() => !editButton(container).disabled)
  assert.equal(document.querySelector('[role="dialog"]'), null)
})

for (const [label, status, payload] of [
  ['HTTP failure', 500, { success: false }],
  ['business failure', 200, { success: false }],
  ['invalid success flag', 200, { success: 'true', data: emptyPreference }],
  ['malformed preference', 200, { success: true, data: {} }],
] as const) {
  test(`${label} stays a visible read failure and disables editing`, async () => {
    toast.error = (() => 0) as typeof toast.error
    api.defaults.adapter = async (config) => respond(config, status, payload)
    const { container, client } = await mount()
    await until(() => container.querySelector('[role="alert"]') !== null)
    assert.equal(client.getQueryState(queryKey)?.status, 'error')
    assert.match(
      container.querySelector('[role="alert"]')?.textContent ?? '',
      /could not be loaded/
    )
    assert.equal(editButton(container).disabled, true)
    assert.match(container.textContent ?? '', /HI,Ada, it is/)
  })
}
