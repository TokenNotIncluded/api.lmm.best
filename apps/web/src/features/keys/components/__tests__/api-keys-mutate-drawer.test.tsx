/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import assert from 'node:assert/strict'
import { after, afterEach, describe, test } from 'node:test'

import { Window } from 'happy-dom'

const domWindow = new Window()
const domGlobals = [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLInputElement',
  'HTMLFormElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'KeyboardEvent',
  'PointerEvent',
  'MouseEvent',
  'FocusEvent',
  'CustomEvent',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
] as const

for (const key of domGlobals) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { useSystemConfigStore, DEFAULT_CURRENCY_CONFIG } =
  await import('@/stores/system-config-store')
const { useWalletCurrencyPreferenceStore } =
  await import('@/stores/wallet-currency-preference-store')
const { api } = await import('@/lib/api')
const { ApiKeysProvider } = await import('../api-keys-provider')
const { ApiKeysMutateDrawer } = await import('../api-keys-mutate-drawer')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

type ApiMethod = (url: string, data?: unknown) => Promise<{ data: unknown }>
type MockableApi = {
  get: ApiMethod
  post: ApiMethod
}
type RenderedDrawer = {
  host: HTMLDivElement
  queryClient: InstanceType<typeof QueryClient>
  root: ReturnType<typeof createRoot>
}

const apiClient = api as unknown as MockableApi
const originalGet = apiClient.get
const originalPost = apiClient.post
let renderedDrawer: RenderedDrawer | null = null

function installApiFixtures(createdPayloads: Array<Record<string, unknown>>) {
  apiClient.get = async (url) => {
    switch (url) {
      case '/api/status':
        return {
          data: {
            data: {
              default_use_auto_group: true,
              currency_unit: 'credit',
              credits_per_usd: 3000000,
              cny_per_usd: 7.2,
              legacy_pricing_units_per_usd: 6,
              quota_per_unit: 500000,
            },
          },
        }
      case '/api/user/models':
        return { data: { success: true, data: [] } }
      case '/api/user/self/groups':
        return {
          data: {
            success: true,
            data: {
              auto: { desc: 'Automatic routing', ratio: 'auto' },
              default: { desc: 'Standard access', ratio: 1 },
              vip: { desc: 'Priority access', ratio: 2 },
            },
          },
        }
      case '/api/token/auto-groups':
        return {
          data: {
            success: true,
            data: { groups: ['vip', 'default'], max_count: 3 },
          },
        }
      default:
        throw new Error(`Unexpected GET ${url}`)
    }
  }
  apiClient.post = async (url, data) => {
    assert.equal(url, '/api/token/')
    assert.ok(data && typeof data === 'object')
    createdPayloads.push(data as Record<string, unknown>)
    return { data: { success: true, data: {} } }
  }
}

async function waitForCondition(
  condition: () => boolean,
  failureMessage: string
): Promise<void> {
  if (condition()) return

  await new Promise<void>((resolve, reject) => {
    const observer = new MutationObserver(() => {
      if (!condition()) return
      clearTimeout(timeoutId)
      observer.disconnect()
      resolve()
    })
    const timeoutId = setTimeout(() => {
      observer.disconnect()
      reject(new Error(`${failureMessage}: ${document.body.textContent}`))
    }, 1500)

    observer.observe(document, {
      attributes: true,
      childList: true,
      characterData: true,
      subtree: true,
    })
  })
}

async function renderCreateDrawer(): Promise<void> {
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const freshAt = Date.now() + 60_000
  queryClient.setQueryData(
    ['status'],
    { default_use_auto_group: true },
    { updatedAt: freshAt }
  )
  queryClient.setQueryData(
    ['user-models'],
    { success: true, data: [] },
    { updatedAt: freshAt }
  )
  queryClient.setQueryData(
    ['user-groups'],
    {
      success: true,
      data: {
        auto: { desc: 'Automatic routing', ratio: 'auto' },
        default: { desc: 'Standard access', ratio: 1 },
        vip: { desc: 'Priority access', ratio: 2 },
      },
    },
    { updatedAt: freshAt }
  )
  queryClient.setQueryData(
    ['token-auto-groups'],
    {
      success: true,
      data: { groups: ['vip', 'default'], max_count: 3 },
    },
    { updatedAt: freshAt }
  )
  renderedDrawer = { host, queryClient, root }

  await act(async () =>
    root.render(
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>
          <ApiKeysProvider>
            <ApiKeysMutateDrawer open onOpenChange={() => undefined} />
          </ApiKeysProvider>
        </I18nextProvider>
      </QueryClientProvider>
    )
  )
  await act(async () =>
    waitForCondition(() => {
      return findButton('Select a group', false) !== null
    }, 'API key drawer did not finish initializing')
  )
}

function findButton(text: string, required: true): HTMLButtonElement
function findButton(text: string, required: false): HTMLButtonElement | null
function findButton(text: string, required = true): HTMLButtonElement | null {
  const button = [
    ...document.querySelectorAll<HTMLButtonElement>('button'),
  ].find((candidate) => candidate.textContent?.includes(text))
  if (required) assert.ok(button, `Expected button containing "${text}"`)
  return button ?? null
}

function getControlByLabel<T extends HTMLElement>(labelText: string): T {
  const label = [...document.querySelectorAll<HTMLLabelElement>('label')].find(
    (candidate) => candidate.textContent?.trim() === labelText
  )
  assert.ok(label, `Expected label "${labelText}"`)
  assert.ok(label.htmlFor)
  const control =
    label.control ??
    label
      .closest('[data-slot="form-item"]')
      ?.querySelector<HTMLElement>(
        '[data-slot="form-control"], input, textarea, button[role="combobox"], [role="group"]'
      )
  assert.ok(control)
  return control as T
}

async function changeInput(input: HTMLInputElement, value: string) {
  await act(async () => {
    const valueSetter = Object.getOwnPropertyDescriptor(
      domWindow.HTMLInputElement.prototype,
      'value'
    )?.set
    assert.ok(valueSetter)
    valueSetter.call(input, value)
    input.dispatchEvent(
      new domWindow.Event('input', { bubbles: true }) as unknown as Event
    )
  })
}

async function selectComboboxOption(
  trigger: HTMLButtonElement,
  optionDescription: string
) {
  await act(async () => trigger.click())
  const option = [
    ...document.querySelectorAll<HTMLElement>('[data-slot="command-item"]'),
  ].find((candidate) => candidate.textContent?.includes(optionDescription))
  assert.ok(option, `Expected option containing "${optionDescription}"`)
  await act(async () => option.click())
}

test('copies the public Base URL and a read-only quota curl with only a placeholder key', async () => {
  const payloads: Array<Record<string, unknown>> = []
  installApiFixtures(payloads)
  const fixtureGet = apiClient.get
  apiClient.get = async (url) =>
    url === '/api/status'
      ? {
          data: {
            data: {
              server_address: 'https://public.example.test/gateway/v1/',
              default_use_auto_group: true,
            },
          },
        }
      : fixtureGet(url)
  const copied: string[] = []
  const originalWrite = navigator.clipboard.writeText
  navigator.clipboard.writeText = async (text) => {
    copied.push(text)
  }
  try {
    await renderCreateDrawer()
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 30))
    })
    assert.equal(
      findButton('Copy Base URL', true).disabled,
      false,
      document.body.textContent ?? ''
    )
    await act(async () => {
      findButton('Copy Base URL', true).click()
    })
    assert.equal(copied[0], 'https://public.example.test/gateway/v1')
    const details = document.querySelector('details')
    assert.ok(details)
    details.open = true
    await act(async () => {
      findButton('Copy quota query', true).click()
    })
    assert.equal(
      copied[1],
      "curl 'https://public.example.test/gateway/v1/usage' \\\n  -H 'Authorization: Bearer API_KEY'"
    )
    assert.match(details.textContent ?? '', /scope=token, currency=USD/)
    assert.match(details.textContent ?? '', /GET \/v1\/pricing/)
    assert.match(details.textContent ?? '', /used_today uses retained UTC logs/)
    assert.equal(payloads.length, 0)
  } finally {
    navigator.clipboard.writeText = originalWrite
  }
})

afterEach(async () => {
  apiClient.get = originalGet
  apiClient.post = originalPost
  domWindow.localStorage.clear()
  if (renderedDrawer) {
    await act(async () => renderedDrawer?.root.unmount())
    renderedDrawer.queryClient.clear()
    renderedDrawer.host.remove()
    renderedDrawer = null
  }
  document.body.replaceChildren()
})

after(() => {
  domWindow.close()
})

describe('API keys mutate drawer Auto group integration', () => {
  test('inherits the root Auto order and sends an empty override for every batch-created key', async () => {
    const createdPayloads: Array<Record<string, unknown>> = []
    installApiFixtures(createdPayloads)
    await renderCreateDrawer()

    const groupTrigger = getControlByLabel<HTMLButtonElement>('Group')
    await selectComboboxOption(groupTrigger, 'Automatic routing')
    assert.equal(groupTrigger.textContent?.includes('auto'), true)
    assert.equal(
      document.body.textContent?.includes(
        'Using the complete global Auto order (2 groups)'
      ),
      true
    )
    assert.deepEqual(
      [
        ...document.querySelectorAll('[data-slot="global-auto-order-name"]'),
      ].map((item) => item.textContent),
      ['vip', 'default']
    )
    assert.equal(findButton('Restore global Auto', true).disabled, true)

    await changeInput(getControlByLabel<HTMLInputElement>('Name'), 'batch')
    await changeInput(getControlByLabel<HTMLInputElement>('Quantity'), '2')
    await act(async () => findButton('Create API Key', true).click())
    await act(async () =>
      waitForCondition(
        () => createdPayloads.length === 2,
        'batch API keys were not created'
      )
    )

    assert.equal(createdPayloads.length, 2)
    assert.equal(createdPayloads[0]?.name, 'batch')
    for (const payload of createdPayloads) {
      assert.equal(payload.group, 'auto')
      assert.deepEqual(payload.auto_groups, [])
      assert.equal(payload.cross_group_retry, true)
    }
  })

  test('preserves an unsaved custom order and mode after Auto to ordinary to Auto changes', async () => {
    const createdPayloads: Array<Record<string, unknown>> = []
    installApiFixtures(createdPayloads)
    await renderCreateDrawer()

    await selectComboboxOption(
      getControlByLabel<HTMLButtonElement>('Group'),
      'Automatic routing'
    )
    const autoOrderControl = getControlByLabel<HTMLElement>('Auto group order')
    const addGroupTrigger = autoOrderControl.querySelector<HTMLButtonElement>(
      'button[role="combobox"]'
    )
    assert.ok(addGroupTrigger)
    await selectComboboxOption(addGroupTrigger, 'Priority access')

    assert.ok(document.querySelector('button[aria-label="Remove vip"]'))
    assert.equal(
      document.body.textContent?.includes('1 / 3 groups selected'),
      true
    )
    assert.equal(findButton('Restore global Auto', true).disabled, false)

    const groupTrigger = getControlByLabel<HTMLButtonElement>('Group')
    await selectComboboxOption(groupTrigger, 'Standard access')
    assert.equal(
      document.querySelector('button[aria-label="Remove vip"]'),
      null
    )
    await selectComboboxOption(groupTrigger, 'Automatic routing')

    assert.ok(document.querySelector('button[aria-label="Remove vip"]'))
    assert.equal(
      document.body.textContent?.includes('1 / 3 groups selected'),
      true
    )
    assert.equal(findButton('Restore global Auto', true).disabled, false)

    await changeInput(getControlByLabel<HTMLInputElement>('Name'), 'custom')
    await act(async () => findButton('Create API Key', true).click())
    await act(async () =>
      waitForCondition(
        () => createdPayloads.length === 1,
        'custom-order API key was not created'
      )
    )
    assert.deepEqual(createdPayloads[0]?.auto_groups, ['vip'])
  })
})

test('creation waits for an explicit group even when Auto is the server default', async () => {
  const payloads: Array<Record<string, unknown>> = []
  installApiFixtures(payloads)
  await renderCreateDrawer()
  await changeInput(
    getControlByLabel<HTMLInputElement>('Name'),
    'Explicit group'
  )
  const submit = document.querySelector<HTMLButtonElement>(
    '[data-slot="sheet-footer"] button:disabled'
  )
  assert.ok(submit)
  assert.equal(submit.disabled, true)
  await act(async () => submit.click())
  assert.equal(payloads.length, 0)
  await selectComboboxOption(
    getControlByLabel<HTMLButtonElement>('Group'),
    'Standard access'
  )
  assert.equal(findButton('Create API Key', true).disabled, false)
})

test('single creation keeps the one-time secret outside query cache and clears it on close', async () => {
  const payloads: Array<Record<string, unknown>> = []
  installApiFixtures(payloads)
  apiClient.post = async (_url, data) => {
    payloads.push(data as Record<string, unknown>)
    return {
      data: {
        success: true,
        data: {
          id: 51,
          name: 'First device',
          key: 'one-time-test-secret',
          one_time_reveal: true,
        },
      },
    }
  }
  await renderCreateDrawer()
  await selectComboboxOption(
    getControlByLabel<HTMLButtonElement>('Group'),
    'Standard access'
  )
  await changeInput(getControlByLabel<HTMLInputElement>('Name'), 'First device')
  await act(async () => findButton('Create API Key', true).click())
  await act(async () =>
    waitForCondition(
      () => !!document.querySelector('input[aria-label="API Key"]'),
      'one-time result not shown'
    )
  )
  assert.equal(payloads.length, 1)
  assert.equal(payloads[0]?.one_time_reveal, true)
  assert.equal(
    document.querySelector<HTMLInputElement>('input[aria-label="API Key"]')
      ?.value,
    'sk-one-time-test-secret'
  )
  assert.doesNotMatch(
    JSON.stringify(
      renderedDrawer?.queryClient
        .getQueryCache()
        .getAll()
        .map((query) => query.state.data)
    ),
    /one-time-test-secret/
  )
  await act(async () => findButton('I saved the key, close', true).click())
  assert.equal(document.querySelector('input[aria-label="API Key"]'), null)
})

test('an ambiguous creation failure cannot silently create a second key', async () => {
  const payloads: Array<Record<string, unknown>> = []
  installApiFixtures(payloads)
  apiClient.post = async (_url, data) => {
    payloads.push(data as Record<string, unknown>)
    throw new Error('connection lost')
  }
  await renderCreateDrawer()
  await selectComboboxOption(
    getControlByLabel<HTMLButtonElement>('Group'),
    'Standard access'
  )
  await changeInput(getControlByLabel<HTMLInputElement>('Name'), 'First device')
  await act(async () => findButton('Create API Key', true).click())
  await act(async () =>
    waitForCondition(
      () => !!document.querySelector('[role="alert"]'),
      'uncertain creation message missing'
    )
  )
  assert.equal(findButton('Create API Key', true).disabled, true)
  assert.equal(payloads.length, 1)
})

test('a limited key uses a fixed Credit basis after changing the display currency', async () => {
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...DEFAULT_CURRENCY_CONFIG,
      currencyUnit: 'credit',
      creditsPerUsd: 3000000,
      cnyPerUsd: 7.2,
      legacyPricingUnitsPerUsd: 6,
    },
  })
  useWalletCurrencyPreferenceStore.getState().setPreference('USD')
  const created: Array<Record<string, unknown>> = []
  installApiFixtures(created)
  await renderCreateDrawer()
  await selectComboboxOption(
    getControlByLabel<HTMLButtonElement>('Group'),
    'Standard access'
  )
  await changeInput(getControlByLabel<HTMLInputElement>('Name'), 'fixed')
  await act(async () =>
    getControlByLabel<HTMLButtonElement>('Unlimited Quota').click()
  )
  await changeInput(getControlByLabel<HTMLInputElement>('Quota (USD)'), '1.25')
  await act(async () =>
    useWalletCurrencyPreferenceStore.getState().setPreference('CNY')
  )
  assert.equal(getControlByLabel<HTMLInputElement>('Quota (CNY)').value, '9')
  await act(async () => findButton('Create API Key', true).click())
  await act(async () =>
    waitForCondition(() => created.length === 1, 'limited key not submitted')
  )
  assert.equal(created[0]?.remain_quota, 3750000)
  assert.equal(created[0]?.unlimited_quota, false)
})
