/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'

const domWindow = new Window({
  url: 'https://console.example.test/admin/users',
})
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLInputElement',
  'HTMLTextAreaElement',
  'HTMLFormElement',
  'HTMLLabelElement',
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
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}
Object.defineProperties(globalThis, {
  requestAnimationFrame: {
    configurable: true,
    value: (callback: FrameRequestCallback) => setTimeout(() => callback(0), 0),
  },
  cancelAnimationFrame: {
    configurable: true,
    value: (handle: number) => clearTimeout(handle),
  },
  getComputedStyle: {
    configurable: true,
    value: domWindow.getComputedStyle.bind(domWindow),
  },
})

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { useSystemConfigStore, DEFAULT_CURRENCY_CONFIG } =
  await import('@/stores/system-config-store')
const { useWalletCurrencyPreferenceStore } =
  await import('@/stores/wallet-currency-preference-store')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { SubscriptionsProvider } =
  await import('@/features/subscriptions/components/subscriptions-provider')
const { SubscriptionsMutateDrawer } =
  await import('@/features/subscriptions/components/subscriptions-mutate-drawer')
const { OpenSourceBounties } = await import('@/features/open-source-bounties')
const globals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
globals.IS_REACT_ACT_ENVIRONMENT = true
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const originalGet = api.get,
  originalPost = api.post
after(() => domWindow.close())
afterEach(() => {
  api.get = originalGet
  api.post = originalPost
  document.body.replaceChildren()
})
async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 30))
}
async function change(
  input: HTMLInputElement | HTMLTextAreaElement,
  text: string
) {
  const prototype =
    input.tagName === 'TEXTAREA'
      ? domWindow.HTMLTextAreaElement.prototype
      : HTMLInputElement.prototype
  const setter = Object.getOwnPropertyDescriptor(prototype, 'value')!.set!
  await act(async () => {
    setter.call(input, text)
    input.dispatchEvent(new Event('input', { bubbles: true }))
    await flush()
  })
}
function inputFor(labelText: string) {
  const label = [...document.querySelectorAll<HTMLLabelElement>('label')].find(
    (candidate) => candidate.textContent?.trim() === labelText
  )
  assert.ok(label, labelText)
  assert.ok(label.control)
  return label.control as HTMLInputElement
}
function button(text: string) {
  const element = [
    ...document.querySelectorAll<HTMLButtonElement>('button'),
  ].find((candidate) => candidate.textContent?.trim() === text)
  assert.ok(element, text)
  return element
}
async function render(component: 'subscription' | 'bounty') {
  const requests: Array<{ url: string; data: Record<string, unknown> }> = []
  useSystemConfigStore
    .getState()
    .setConfig({
      currency: {
        ...DEFAULT_CURRENCY_CONFIG,
        currencyUnit: 'credit',
        creditsPerUsd: 3000000,
        cnyPerUsd: 7.2,
        legacyPricingUnitsPerUsd: 6,
      },
    })
  useWalletCurrencyPreferenceStore.getState().setPreference('USD')
  api.get = (async (url: string) => {
    let data: unknown = []
    if (url === '/api/status')
      data = {
        currency_unit: 'credit',
        credits_per_usd: 3000000,
        cny_per_usd: 7.2,
        legacy_pricing_units_per_usd: 6,
        backend_capabilities: { bounty_public_read: true },
      }
    else if (url.includes('open-source-bounties?'))
      data = { items: [], total: 0, page: 1, page_size: 50 }
    else if (url.includes('open-source-bounties/config'))
      data = { rate_percent: 10, rate_basis_points: 1000 }
    return { data: { success: true, data } }
  }) as typeof api.get
  api.post = (async (url: string, data: unknown) => {
    requests.push({ url, data: data as Record<string, unknown> })
    return { data: { success: true, data: {} } }
  }) as typeof api.post
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  await act(async () => {
    root.render(
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>
          {component === 'subscription' ? (
            <SubscriptionsProvider>
              <SubscriptionsMutateDrawer open onOpenChange={() => {}} />
            </SubscriptionsProvider>
          ) : (
            <OpenSourceBounties />
          )}
        </I18nextProvider>
      </QueryClientProvider>
    )
    await flush()
  })
  return { requests, root, queryClient }
}
test('subscription submit keeps fiat list price and exact raw quota after USD to CNY display change', async () => {
  const rendered = await render('subscription')
  try {
    await change(inputFor('Plan Title'), 'Plan')
    await change(inputFor('Plan Price'), '6.8')
    await change(inputFor('Quota (USD)'), '1.25')
    await act(async () => {
      useWalletCurrencyPreferenceStore.getState().setPreference('CNY')
      await flush()
    })
    assert.equal(inputFor('Quota (CNY)').value, '9')
    const form = document.querySelector<HTMLFormElement>('#subscription-form')!
    await act(async () => {
      form.dispatchEvent(
        new Event('submit', { bubbles: true, cancelable: true })
      )
      await flush()
    })
    assert.equal(rendered.requests[0]?.url, '/api/subscription/admin/plans')
    const plan = rendered.requests[0]?.data.plan as Record<string, unknown>
    assert.equal(plan.total_amount, 3750000)
    assert.equal(plan.price_amount, 6.8)
    assert.equal(plan.currency, 'CNY')
  } finally {
    await act(async () => rendered.root.unmount())
    rendered.queryClient.clear()
  }
})
test('bounty draft submit keeps its literal raw reward after USD to CNY display change', async () => {
  const rendered = await render('bounty')
  try {
    await act(async () => {
      button('Create bounty').click()
      await flush()
    })
    await change(
      document.querySelector<HTMLInputElement>('#bounty-repository')!,
      'https://github.com/owner/repository'
    )
    await change(
      document.querySelector<HTMLInputElement>('#bounty-title')!,
      'Repair quota conversion'
    )
    await change(
      document.querySelector<HTMLTextAreaElement>('#bounty-description')!,
      'A detailed defect scope with enough characters.'
    )
    await change(
      document.querySelector<HTMLTextAreaElement>('#bounty-rules')!,
      'Require focused tests and exact quota preservation.'
    )
    await change(
      document.querySelector<HTMLInputElement>('#bounty-reward')!,
      '1.25'
    )
    await act(async () => {
      useWalletCurrencyPreferenceStore.getState().setPreference('CNY')
      await flush()
    })
    assert.equal(
      document.querySelector<HTMLInputElement>('#bounty-reward')?.value,
      '9'
    )
    await act(async () => {
      button('Save draft').click()
      await flush()
    })
    assert.equal(rendered.requests[0]?.url, '/api/open-source-bounties')
    assert.equal(rendered.requests[0]?.data.reward_quota, 3750000)
    assert.equal(rendered.requests[0]?.data.reward_slots, 1)
  } finally {
    await act(async () => rendered.root.unmount())
    rendered.queryClient.clear()
  }
})
