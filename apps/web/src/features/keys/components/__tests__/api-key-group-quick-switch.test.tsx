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
const { api } = await import('@/lib/api')
const { ApiKeysProvider, useApiKeys } = await import('../api-keys-provider')
const { ApiKeyGroupQuickSwitch } = await import('../api-key-group-quick-switch')

type ApiKey = {
  id: number
  name: string
  key: string
  status: number
  remain_quota: number
  used_quota: number
  unlimited_quota: boolean
  expired_time: number
  created_time: number
  accessed_time: number
  group: string
  auto_groups: string[]
  cross_group_retry: boolean
  model_limits_enabled: boolean
  model_limits: string
  allow_ips: string
}

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        Group: 'Group',
        'Cross-group': 'Cross-group',
        Ratio: 'Ratio',
        'Search...': 'Search...',
        'No group found.': 'No group found.',
        'API key updated successfully': 'API key updated successfully',
        'Failed to update API key': 'Failed to update API key',
        'Group warning': 'Group warning',
        'Confirmation {{current}} of {{total}}':
          'Confirmation {{current}} of {{total}}',
        Continue: 'Continue',
        'I understand, continue': 'I understand, continue',
        Cancel: 'Cancel',
      },
    },
  },
})

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

type PutCall = { url: string; data: Record<string, unknown> }
type MockableApi = {
  put: (url: string, data?: unknown) => Promise<{ data: unknown }>
}

const apiClient = api as unknown as MockableApi
const originalPut = apiClient.put

const baseApiKey: ApiKey = {
  id: 42,
  name: 'test-key',
  key: 'sk-masked',
  status: 1,
  remain_quota: 500,
  used_quota: 10,
  unlimited_quota: false,
  expired_time: -1,
  created_time: 1_700_000_000,
  accessed_time: 1_700_000_000,
  group: 'default',
  auto_groups: [],
  cross_group_retry: false,
  model_limits_enabled: false,
  model_limits: '',
  allow_ips: '',
}

const groupOptions = [
  { value: 'default', label: 'default', desc: 'Standard access', ratio: 1 },
  { value: 'vip', label: 'vip', desc: 'Priority access', ratio: 3 },
  {
    value: 'restricted',
    label: 'restricted',
    desc: 'Restricted access',
    ratio: 5,
    warning: {
      enabled: true,
      message: 'This group has extra charges.',
      mode: 'modal' as const,
      confirmations: 2,
    },
  },
]

type Rendered = {
  host: HTMLDivElement
  root: ReturnType<typeof createRoot>
}

let rendered: Rendered | null = null

function RefreshProbe() {
  const { refreshTrigger } = useApiKeys()
  return <output data-testid='refresh-trigger'>{refreshTrigger}</output>
}

async function renderQuickSwitch(apiKey: ApiKey): Promise<void> {
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  rendered = { host, root }

  await act(async () =>
    root.render(
      <I18nextProvider i18n={i18n}>
        <ApiKeysProvider>
          <ApiKeyGroupQuickSwitch
            apiKey={apiKey}
            options={groupOptions}
            optionsLoading={false}
            ratio={1}
            shouldReduceMotion={false}
          />
          <RefreshProbe />
        </ApiKeysProvider>
      </I18nextProvider>
    )
  )
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

function getTrigger(): HTMLButtonElement {
  const trigger = document.querySelector<HTMLButtonElement>(
    '[data-api-key-group-quick-switch]'
  )
  assert.ok(trigger)
  return trigger
}

function getRefreshTriggerValue(): number {
  const output = document.querySelector('[data-testid="refresh-trigger"]')
  assert.ok(output)
  return Number(output.textContent)
}

async function openAndSelect(optionText: string): Promise<void> {
  await act(async () => getTrigger().click())
  const option = [
    ...document.querySelectorAll<HTMLElement>('[data-slot="command-item"]'),
  ].find((candidate) => candidate.textContent?.includes(optionText))
  assert.ok(option, `Expected option containing "${optionText}"`)
  await act(async () => option.click())
}

afterEach(async () => {
  apiClient.put = originalPut
  if (rendered) {
    await act(async () => rendered?.root.unmount())
    rendered.host.remove()
    rendered = null
  }
  document.body.replaceChildren()
})

after(() => {
  domWindow.close()
})

describe('API key group quick switch', () => {
  test('clicking a new group in the dropdown calls the update endpoint and refreshes the table', async () => {
    const calls: PutCall[] = []
    apiClient.put = async (url, data) => {
      calls.push({ url, data: data as Record<string, unknown> })
      return { data: { success: true, data: {} } }
    }

    await renderQuickSwitch(baseApiKey)
    assert.equal(getRefreshTriggerValue(), 0)

    await openAndSelect('Priority access')

    await act(async () =>
      waitForCondition(
        () => calls.length === 1,
        'update API was not called after selecting a group'
      )
    )

    assert.equal(calls[0]?.url, '/api/token/')
    assert.equal(calls[0]?.data.id, 42)
    assert.equal(calls[0]?.data.name, 'test-key')
    assert.equal(calls[0]?.data.group, 'vip')
    assert.equal(calls[0]?.data.remain_quota, 500)
    assert.equal(calls[0]?.data.unlimited_quota, false)
    assert.equal(calls[0]?.data.cross_group_retry, false)
    assert.deepEqual(calls[0]?.data.auto_groups, [])
    assert.equal(calls[0]?.data.group_warning_confirmations, 0)

    await act(async () =>
      waitForCondition(
        () => getRefreshTriggerValue() === 1,
        'row refresh was not triggered after a successful group switch'
      )
    )
  })

  test('selecting a warning-gated group requires confirmation before calling the update endpoint', async () => {
    const calls: PutCall[] = []
    apiClient.put = async (url, data) => {
      calls.push({ url, data: data as Record<string, unknown> })
      return { data: { success: true, data: {} } }
    }

    await renderQuickSwitch(baseApiKey)
    await openAndSelect('Restricted access')

    await act(async () =>
      waitForCondition(
        () => document.body.textContent?.includes('Group warning') === true,
        'group warning dialog did not open'
      )
    )
    assert.equal(calls.length, 0)

    const findConfirmButton = () =>
      [...document.querySelectorAll<HTMLButtonElement>('button')].find(
        (button) =>
          button.textContent === 'Continue' ||
          button.textContent === 'I understand, continue'
      )

    const firstConfirm = findConfirmButton()
    assert.ok(firstConfirm)
    await act(async () => firstConfirm.click())
    assert.equal(calls.length, 0, 'first confirmation must not submit yet')

    const secondConfirm = findConfirmButton()
    assert.ok(secondConfirm)
    assert.equal(secondConfirm.textContent, 'I understand, continue')
    await act(async () => secondConfirm.click())

    await act(async () =>
      waitForCondition(
        () => calls.length === 1,
        'update API was not called after confirming the group warning'
      )
    )
    assert.equal(calls[0]?.data.group, 'restricted')
    assert.equal(calls[0]?.data.group_warning_confirmations, 2)
  })
})
