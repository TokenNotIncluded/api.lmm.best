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
import { after, beforeEach, describe, test } from 'node:test'

import { Window } from 'happy-dom'
import type React from 'react'

const domWindow = new Window()
const domGlobals = [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'MutationObserver',
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

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        Subscription: 'Subscription',
        'Deducted by subscription': 'Deducted by subscription',
        'Includes tool-call surcharge': 'Includes tool-call surcharge',
      },
    },
  },
})

const { LogCostDisplay } = await import('../log-cost-display')
const { useAuthStore } = await import('@/stores/auth-store')
const { DEFAULT_CURRENCY_CONFIG, useSystemConfigStore } =
  await import('@/stores/system-config-store')
const { useWalletCurrencyPreferenceStore } =
  await import('@/stores/wallet-currency-preference-store')
const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

type RenderedCost = {
  container: HTMLDivElement
  root: ReturnType<typeof createRoot>
}

async function renderCost(
  props: React.ComponentProps<typeof LogCostDisplay>
): Promise<RenderedCost> {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  await act(async () => {
    root.render(
      <I18nextProvider i18n={i18n}>
        <LogCostDisplay {...props} />
      </I18nextProvider>
    )
  })

  return { container, root }
}

async function unmountCost(rendered: RenderedCost) {
  await act(async () => rendered.root.unmount())
  rendered.container.remove()
}

function normalizedText(value: string | null): string {
  return (value ?? '').replaceAll(/\s/g, '')
}

describe('log cost display', () => {
  beforeEach(async () => {
    useAuthStore.getState().auth.setUser(null)
    useWalletCurrencyPreferenceStore.getState().setPreference('')
    useSystemConfigStore.getState().setConfig({
      currency: {
        ...DEFAULT_CURRENCY_CONFIG,
        currencyUnit: 'credit',
        creditsPerUsd: 3_500_000,
        creditsPerUsdExact: '3500000',
        cnyPerUsd: 7,
        cnyPerUsdExact: '7',
      },
    })
    await i18n.changeLanguage('en')
  })
  after(() => {
    domWindow.close()
  })

  test('keeps the regular cost visible and adds an accessible surcharge marker', async () => {
    const rendered = await renderCost({
      quota: 12500,
      other: {
        tool_surcharges: [{ name: 'lookup_customer', count: 1, price: 5 }],
      },
    })

    assert.equal(
      normalizedText(rendered.container.textContent).includes(
        normalizedText('0.00357143 USD')
      ),
      true
    )
    const marker = rendered.container.querySelector(
      '[data-tool-surcharge-indicator="true"]'
    )
    assert.ok(marker)
    assert.equal(
      marker.getAttribute('aria-label'),
      'Includes tool-call surcharge'
    )
    assert.equal(marker.getAttribute('tabindex'), '0')

    await unmountCost(rendered)
  })

  test('shows final subscription consumption inline and preserves the surcharge marker', async () => {
    const rendered = await renderCost({
      quota: 5000,
      other: {
        billing_source: 'subscription',
        subscription_consumed: 2500,
        web_search: true,
        web_search_call_count: 1,
        web_search_price: 10,
      },
    })

    const subscriptionBadge = rendered.container.querySelector(
      '[data-slot="tooltip-trigger"], [data-slot="status-badge"]'
    )
    assert.ok(subscriptionBadge)
    assert.equal(
      normalizedText(subscriptionBadge.textContent),
      normalizedText('Subscription (0.00071429 USD)')
    )
    assert.ok(
      rendered.container.querySelector('[data-tool-surcharge-indicator="true"]')
    )

    await unmountCost(rendered)
  })

  test('falls back to the logged quota for legacy subscription records', async () => {
    const rendered = await renderCost({
      quota: 5000,
      other: { billing_source: 'subscription' },
    })

    const subscriptionBadge = rendered.container.querySelector(
      '[data-slot="tooltip-trigger"], [data-slot="status-badge"]'
    )
    assert.ok(subscriptionBadge)
    assert.equal(
      normalizedText(subscriptionBadge.textContent),
      normalizedText('Subscription (0.00142857 USD)')
    )

    await unmountCost(rendered)
  })
  test('keeps mounted costs in USD across language, wallet preference and FX changes', async () => {
    await i18n.changeLanguage('zhTW')
    const rendered = await renderCost({ quota: 3_500_000, other: null })
    try {
      assert.match(rendered.container.textContent ?? '', /1 USD/)
      await act(async () => i18n.changeLanguage('en'))
      assert.match(rendered.container.textContent ?? '', /1 USD/)
      await act(async () =>
        useWalletCurrencyPreferenceStore.getState().setPreference('CREDIT')
      )
      assert.match(rendered.container.textContent ?? '', /1 USD/)
      await act(async () => i18n.changeLanguage('zhCN'))
      assert.match(rendered.container.textContent ?? '', /1 USD/)
      await act(async () =>
        useWalletCurrencyPreferenceStore.getState().setPreference('CNY')
      )
      await act(async () =>
        useSystemConfigStore.getState().setConfig({
          currency: {
            ...useSystemConfigStore.getState().config.currency,
            cnyPerUsd: 8,
            cnyPerUsdExact: '8',
          },
        })
      )
      assert.match(rendered.container.textContent ?? '', /1 USD/)
      assert.doesNotMatch(
        rendered.container.textContent ?? '',
        /Platform|Credits|CNY|\$/
      )
    } finally {
      await unmountCost(rendered)
    }
  })
})
