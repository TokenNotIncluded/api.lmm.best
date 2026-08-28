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

const domWindow = new Window({ url: 'https://console.example.test/' })
for (const key of [
  'window',
  'document',
  'navigator',
  'history',
  'location',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLInputElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'MutationObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
  'scrollTo',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} = await import('@tanstack/react-router')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { AssistantPlanTool } = await import('./assistant-plan-tool')

const originalGet = api.get
const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

async function flushQueries() {
  await new Promise((resolve) => setTimeout(resolve, 20))
}

const assistantOffersFixture = {
  success: true,
  data: {
    ok: true,
    developer_access_granted: true,
    read_only: false,
    checkout_available: true,
    payment_hidden: false,
    payment_compliance_confirmed: true,
    topup_discounts: { 100: 0.8 },
    plans: [
      {
        plan: {
          id: 1,
          title: 'Starter',
          price_amount: 8,
          currency: 'USD',
          duration_unit: 'month',
          duration_value: 1,
          quota_reset_period: 'monthly',
          enabled: true,
          sort_order: 1,
          allow_balance_pay: true,
          allow_wallet_overflow: true,
          max_purchase_per_user: 0,
          total_amount: 5_000_000,
        },
      },
      {
        plan: {
          id: 2,
          title: 'Pro',
          price_amount: 15,
          currency: 'USD',
          duration_unit: 'month',
          duration_value: 1,
          quota_reset_period: 'monthly',
          enabled: true,
          sort_order: 2,
          allow_balance_pay: true,
          allow_wallet_overflow: true,
          max_purchase_per_user: 0,
          total_amount: 15_000_000,
        },
      },
    ],
  },
}

async function renderTool(
  developerAccessGranted: boolean,
  onRequestAccess = () => {}
) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const rootRoute = createRootRoute({
    component: () => (
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>
          <AssistantPlanTool
            developerAccessGranted={developerAccessGranted}
            onRequestAccess={onRequestAccess}
          />
        </I18nextProvider>
      </QueryClientProvider>
    ),
  })
  const indexRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/',
    component: () => null,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([indexRoute]),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  await act(async () => {
    root.render(<RouterProvider router={router} />)
    await flushQueries()
  })
  await act(flushQueries)
  return { container, queryClient, root }
}

function findRetryButton(alertTitle: string): HTMLButtonElement {
  const title = [
    ...document.querySelectorAll<HTMLElement>('[data-slot="alert-title"]'),
  ].find((candidate) => candidate.textContent?.includes(alertTitle))
  const button = title
    ?.closest('[data-slot="alert"]')
    ?.querySelector<HTMLButtonElement>('button')
  assert.ok(button, `Could not find retry button for ${alertTitle}`)
  return button
}

async function unmount(rendered: Awaited<ReturnType<typeof renderTool>>) {
  await act(async () => rendered.root.unmount())
  rendered.queryClient.clear()
  rendered.container.remove()
}

afterEach(() => {
  api.get = originalGet
  document.body.replaceChildren()
})

after(() => domWindow.close())

describe('AssistantPlanTool', () => {
  test('formats plan prices and discounts with internal Chinese locale codes', async () => {
    api.get = (async (url: string) => {
      assert.equal(url, '/api/assistant/offers')
      return { data: assistantOffersFixture }
    }) as typeof api.get

    await i18n.changeLanguage('zhTW')
    const rendered = await renderTool(true)
    try {
      assert.match(rendered.container.textContent ?? '', /8 USD/)
      assert.match(rendered.container.textContent ?? '', /save 20%/)
    } finally {
      await unmount(rendered)
      await i18n.changeLanguage('en')
    }
  })

  test('renders backend-authorized offers and checkout for an L0 user', async () => {
    let calls = 0
    api.get = (async (url: string) => {
      assert.equal(url, '/api/assistant/offers')
      calls += 1
      return {
        data: {
          ...assistantOffersFixture,
          data: {
            ...assistantOffersFixture.data,
            developer_access_granted: false,
            read_only: false,
            checkout_available: true,
          },
        },
      }
    }) as typeof api.get

    const rendered = await renderTool(false)

    assert.equal(calls, 1)
    assert.match(rendered.container.textContent ?? '', /Pro/)
    assert.match(rendered.container.textContent ?? '', /save 20%/)
    assert.match(
      rendered.container.textContent ?? '',
      /Estimated discounted base amount\$80 \(Platform\)/
    )
    assert.ok(rendered.container.querySelector('#assistant-expected-credit'))
    assert.ok(rendered.container.querySelector('#assistant-topup-credit'))
    const checkoutLink =
      rendered.container.querySelector<HTMLAnchorElement>('a[href="/wallet"]')
    assert.ok(checkoutLink)
    assert.match(checkoutLink.className, /w-full/)
    assert.match(checkoutLink.className, /sm:w-auto/)
    assert.doesNotMatch(rendered.container.textContent ?? '', /Read-only/)
    assert.doesNotMatch(
      rendered.container.textContent ?? '',
      /Unlock L1 access/
    )

    await unmount(rendered)
  })

  test('offers the L1 request only for an L0 read-only response', async () => {
    let accessRequests = 0
    api.get = (async (url: string) => {
      assert.equal(url, '/api/assistant/offers')
      return {
        data: {
          ...assistantOffersFixture,
          data: {
            ...assistantOffersFixture.data,
            developer_access_granted: false,
            read_only: true,
            checkout_available: false,
          },
        },
      }
    }) as typeof api.get

    const rendered = await renderTool(false, () => {
      accessRequests += 1
    })
    assert.match(rendered.container.textContent ?? '', /Pro/)
    assert.match(rendered.container.textContent ?? '', /Read-only plan advice/)
    assert.equal(rendered.container.querySelector('a[href="/wallet"]'), null)

    await act(async () => {
      const button = [...rendered.container.querySelectorAll('button')].find(
        (candidate) => candidate.textContent?.includes('Unlock L1 access')
      )
      assert.ok(button)
      button.click()
    })
    assert.equal(accessRequests, 1)

    await unmount(rendered)
  })

  test('recovers live offers and explains the updated recommendation', async () => {
    let calls = 0
    api.get = (async (url: string) => {
      assert.equal(url, '/api/assistant/offers')
      calls += 1
      if (calls === 1) throw new Error('offers offline')
      return { data: assistantOffersFixture }
    }) as typeof api.get

    const rendered = await renderTool(true)
    assert.match(
      rendered.container.textContent ?? '',
      /Unable to load live subscription plans/
    )

    await act(async () => {
      findRetryButton('Unable to load live subscription plans').click()
      await flushQueries()
    })
    await act(flushQueries)

    assert.match(rendered.container.textContent ?? '', /Pro/)
    assert.match(rendered.container.textContent ?? '', /Closest fit/)
    assert.match(
      rendered.container.textContent ?? '',
      /smallest available capacity that covers your \$20 \(Platform\) monthly estimate/
    )
    assert.match(rendered.container.textContent ?? '', /save 20%/)
    assert.equal(calls, 2)

    const expectedInput = rendered.container.querySelector<HTMLInputElement>(
      '#assistant-expected-credit'
    )
    assert.ok(expectedInput)
    await act(async () => {
      const setValue = Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        'value'
      )?.set
      assert.ok(setValue)
      setValue.call(expectedInput, '40')
      expectedInput.dispatchEvent(new Event('input', { bubbles: true }))
      await flushQueries()
    })
    assert.match(
      rendered.container.textContent ?? '',
      /No plan fully covers your \$40 \(Platform\) monthly estimate/
    )

    await unmount(rendered)
  })

  test('keeps L1 read-only offers visible without suggesting another unlock', async () => {
    api.get = (async (url: string) => {
      assert.equal(url, '/api/assistant/offers')
      return {
        data: {
          ...assistantOffersFixture,
          data: {
            ...assistantOffersFixture.data,
            read_only: true,
            checkout_available: false,
            payment_hidden: true,
            topup_discounts: {},
          },
        },
      }
    }) as typeof api.get

    const rendered = await renderTool(true)
    assert.match(rendered.container.textContent ?? '', /Closest fit/)
    assert.match(rendered.container.textContent ?? '', /Read-only plan advice/)
    assert.match(
      rendered.container.textContent ?? '',
      /Payment is unavailable for this account\./
    )
    assert.equal(rendered.container.querySelector('a[href="/wallet"]'), null)
    assert.doesNotMatch(
      rendered.container.textContent ?? '',
      /Unlock L1 access/
    )
    assert.doesNotMatch(rendered.container.textContent ?? '', /save 20%/)

    await unmount(rendered)
  })
})
