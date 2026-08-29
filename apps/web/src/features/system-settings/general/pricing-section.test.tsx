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

For commercial licensing, please contact support@quantumnous.com
*/
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'
import type { ComponentProps } from 'react'

const domWindow = new Window({
  url: 'https://console.example.test/admin/system-settings/billing',
})
domWindow.document.write(
  '<!doctype html><html><head></head><body></body></html>'
)
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
const { SettingsPageProvider } =
  await import('../components/settings-page-context')
const { PricingSection } = await import('./pricing-section')

type PricingDefaults = ComponentProps<typeof PricingSection>['defaultValues']

type ExchangeRatePayload = {
  success: true
  message: string
  data: {
    base_currency: 'USD'
    quote_currency: string
    rate: number
    fetched_at: string
    provider: string
  }
}

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

after(() => domWindow.close())

function pricingDefaults(
  displayType: PricingDefaults['general_setting']['quota_display_type'],
  overrides: Partial<PricingDefaults> = {}
): PricingDefaults {
  return {
    QuotaPerUnit: 500_000,
    USDExchangeRate: 7,
    TopUpPlatformUnitsPerCNY: 1,
    DisplayInCurrencyEnabled: true,
    DisplayTokenStatEnabled: true,
    general_setting: {
      quota_display_type: displayType,
      custom_currency_symbol: '¤',
      custom_currency_code: '',
      custom_currency_exchange_rate: 1,
    },
    ...overrides,
  }
}

function exchangeRatePayload(currency: string, rate: number) {
  return {
    data: {
      success: true,
      message: '',
      data: {
        base_currency: 'USD',
        quote_currency: currency,
        rate,
        fetched_at: '2026-08-28T00:00:00Z',
        provider: 'test-provider',
      },
    } satisfies ExchangeRatePayload,
  }
}

async function renderPricing(defaultValues: PricingDefaults) {
  const container = document.createElement('div')
  const actionsContainer = document.createElement('div')
  const titleStatusContainer = document.createElement('span')
  container.append(actionsContainer, titleStatusContainer)
  document.body.append(container)

  const root = createRoot(container)
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const rootRoute = createRootRoute({
    component: () => (
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>
          <SettingsPageProvider
            actionsContainer={actionsContainer}
            titleStatusContainer={titleStatusContainer}
          >
            <PricingSection defaultValues={defaultValues} />
          </SettingsPageProvider>
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

  await act(async () => {
    root.render(<RouterProvider router={router} />)
    await new Promise((resolve) => setTimeout(resolve, 20))
  })

  return {
    container,
    queryClient,
    root,
    titleStatusContainer,
    async cleanup() {
      await act(async () => root.unmount())
      queryClient.clear()
      container.remove()
    },
  }
}

async function clickSync(container: HTMLElement, index = 0) {
  const button = container.querySelectorAll<HTMLButtonElement>(
    'button[aria-label="Sync USD exchange rate"]'
  )[index]
  assert.ok(button)
  await act(async () => {
    button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  })
  return button
}

async function flushAsyncWork() {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 20))
  })
}

async function setNumberInput(input: HTMLInputElement, value: number) {
  const setValue = Object.getOwnPropertyDescriptor(
    HTMLInputElement.prototype,
    'value'
  )?.set
  assert.ok(setValue)

  await act(async () => {
    setValue.call(input, String(value))
    input.dispatchEvent(new Event('input', { bubbles: true }))
    input.dispatchEvent(new Event('change', { bubbles: true }))
    await new Promise((resolve) => setTimeout(resolve, 20))
  })
}

test('loads CNY 6.8, exposes loading state, and marks the form dirty', async () => {
  const originalGet = api.get
  let resolveRequest:
    | ((value: ReturnType<typeof exchangeRatePayload>) => void)
    | undefined
  const pendingResponse = new Promise<ReturnType<typeof exchangeRatePayload>>(
    (resolve) => {
      resolveRequest = resolve
    }
  )
  api.get = ((url: string, config?: { skipErrorHandler?: boolean }) => {
    assert.equal(url, '/api/option/exchange-rate?currency=CNY')
    assert.equal(config?.skipErrorHandler, true)
    return pendingResponse
  }) as typeof api.get

  const rendered = await renderPricing(pricingDefaults('CNY'))
  try {
    const button = await clickSync(rendered.container)
    assert.equal(button.disabled, true)
    assert.equal(button.getAttribute('aria-busy'), 'true')
    assert.match(button.textContent ?? '', /Syncing/)

    await act(async () => {
      assert.ok(resolveRequest)
      resolveRequest(exchangeRatePayload('CNY', 6.8))
      await new Promise((resolve) => setTimeout(resolve, 20))
    })

    const input = rendered.container.querySelector<HTMLInputElement>(
      'input[name="USDExchangeRate"]'
    )
    assert.ok(input)
    assert.equal(input.value, '6.8')
    assert.match(
      rendered.titleStatusContainer.textContent ?? '',
      /Unsaved changes/
    )
  } finally {
    api.get = originalGet
    await rendered.cleanup()
  }
})

test('reads a second live FX rate and saves B independently in USD display mode', async () => {
  const originalGet = api.get
  const originalPut = api.put
  const updates: Array<{ key: string; value: string }> = []
  api.get = (async (url: string) => {
    assert.equal(url, '/api/option/exchange-rate?currency=CNY')
    return exchangeRatePayload('CNY', 7.2)
  }) as typeof api.get
  api.put = (async (url: string, request: { key: string; value: string }) => {
    assert.equal(url, '/api/option/')
    updates.push(request)
    return { data: { success: true, message: '' } }
  }) as typeof api.put

  const rendered = await renderPricing(
    pricingDefaults('USD', {
      USDExchangeRate: 1.25,
      TopUpPlatformUnitsPerCNY: 1.1,
    })
  )
  try {
    await clickSync(rendered.container)
    await flushAsyncWork()

    const exchangeRateInput =
      rendered.container.querySelector<HTMLInputElement>(
        'input[name="USDExchangeRate"]'
      )
    const rechargeRatioInput =
      rendered.container.querySelector<HTMLInputElement>(
        'input[name="TopUpPlatformUnitsPerCNY"]'
      )
    const form = rendered.container.querySelector('form')
    assert.ok(exchangeRateInput)
    assert.ok(rechargeRatioInput)
    assert.ok(form)
    assert.equal(exchangeRateInput.value, '7.2')
    assert.equal(rechargeRatioInput.value, '1.1')

    await setNumberInput(rechargeRatioInput, 1.35)
    assert.equal(exchangeRateInput.value, '7.2')
    assert.equal(rechargeRatioInput.value, '1.35')

    await act(async () => {
      form.dispatchEvent(
        new Event('submit', { bubbles: true, cancelable: true })
      )
      await new Promise((resolve) => setTimeout(resolve, 40))
    })

    assert.deepEqual(updates, [
      { key: 'USDExchangeRate', value: '7.2' },
      { key: 'TopUpPlatformUnitsPerCNY', value: '1.35' },
    ])
  } finally {
    api.get = originalGet
    api.put = originalPut
    await rendered.cleanup()
  }
})

test('uses the explicit CUSTOM ISO code instead of guessing from its symbol', async () => {
  const originalGet = api.get
  api.get = (async (url: string) => {
    assert.equal(url, '/api/option/exchange-rate?currency=JPY')
    return exchangeRatePayload('JPY', 149.5)
  }) as typeof api.get

  const rendered = await renderPricing(
    pricingDefaults('CUSTOM', {
      general_setting: {
        quota_display_type: 'CUSTOM',
        custom_currency_symbol: '$',
        custom_currency_code: 'JPY',
        custom_currency_exchange_rate: 120,
      },
    })
  )
  try {
    await clickSync(rendered.container, 1)
    await flushAsyncWork()

    const input = rendered.container.querySelector<HTMLInputElement>(
      'input[name="general_setting.custom_currency_exchange_rate"]'
    )
    assert.ok(input)
    assert.equal(input.value, '149.5')
  } finally {
    api.get = originalGet
    await rendered.cleanup()
  }
})

test('does not overwrite the existing rate when sync fails', async () => {
  const originalGet = api.get
  api.get = (async () => {
    throw new Error('exchange provider unavailable')
  }) as typeof api.get

  const rendered = await renderPricing(
    pricingDefaults('CNY', { USDExchangeRate: 7.2 })
  )
  try {
    await clickSync(rendered.container)
    await flushAsyncWork()

    const input = rendered.container.querySelector<HTMLInputElement>(
      'input[name="USDExchangeRate"]'
    )
    assert.ok(input)
    assert.equal(input.value, '7.2')
    assert.equal(rendered.titleStatusContainer.textContent, '')
  } finally {
    api.get = originalGet
    await rendered.cleanup()
  }
})
