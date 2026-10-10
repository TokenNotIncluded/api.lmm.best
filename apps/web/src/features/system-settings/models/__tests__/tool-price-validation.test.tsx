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
import { after, describe, test } from 'node:test'

import { Window } from 'happy-dom'

const domWindow = new Window()
const domGlobals = [
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
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { ToolPriceSettings } = await import('../tool-price-settings')
const { buildToolPriceOverrides } = await import('../tool-price-overrides')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        'Price (USD/1K calls)': 'Price (USD/1K calls)',
        'Please enter a valid number': 'Please enter a valid number',
        'Tool identifier': 'Tool identifier',
      },
    },
  },
})

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

function changeInputValue(input: HTMLInputElement, value: string) {
  const valueSetter = Object.getOwnPropertyDescriptor(
    domWindow.HTMLInputElement.prototype,
    'value'
  )?.set
  assert.ok(valueSetter)
  valueSetter.call(input, value)
  input.dispatchEvent(
    new domWindow.Event('input', { bubbles: true }) as unknown as Event
  )
}

describe('tool price validation', () => {
  after(() => {
    domWindow.close()
  })

  test('fallback USD defaults are not saved as operator overrides', () => {
    const defaults = { web_search: 10 / 7.2, file_search: 2.5 / 7.2 }
    assert.deepEqual(buildToolPriceOverrides(defaults, '{}', defaults), {})
    assert.deepEqual(
      buildToolPriceOverrides({ ...defaults, web_search: 0 }, '{}', defaults),
      { web_search: 0 }
    )
    assert.deepEqual(
      buildToolPriceOverrides(
        { ...defaults, web_search: 0 },
        '{"web_search":0}',
        defaults
      ),
      { web_search: 0 }
    )
  })

  test('blocks an empty price without converting it to an explicit zero', async () => {
    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)
    const queryClient = new QueryClient({
      defaultOptions: { mutations: { retry: false } },
    })

    await act(async () => {
      root.render(
        <QueryClientProvider client={queryClient}>
          <I18nextProvider i18n={i18n}>
            <ToolPriceSettings
              defaultValue='{"web_search":10}'
              pricingConfig={{
                schema_version: 2,
                currency: 'USD',
                storage_basis: 'legacy_pricing_unit',
                revision: 'a'.repeat(64),
                credits_per_usd: 3_600_000,
                legacy_pricing_units_per_usd: 7.2,
                model_ratio_usd_per_million: 1_000_000 / 3_600_000,
                values:
                  {} as import('../model-pricing-api').ModelPricingConfig['values'],
              }}
            />
          </I18nextProvider>
        </QueryClientProvider>
      )
    })

    const priceInput = container.querySelector<HTMLInputElement>(
      'input[aria-label="Price (USD/1K calls): web_search"]'
    )
    assert.ok(priceInput)

    await act(async () => {
      changeInputValue(priceInput, '')
    })

    assert.equal(priceInput.getAttribute('aria-invalid'), 'true')
    assert.equal(
      priceInput.closest('[data-slot="field"]')?.querySelector('[role="alert"]')
        ?.textContent,
      'Please enter a valid number'
    )
    const saveButton = [...container.querySelectorAll('button')].find(
      (button) => button.textContent === 'Save tool prices'
    )
    assert.ok(saveButton)
    assert.equal(saveButton.disabled, true)

    await act(async () => {
      changeInputValue(priceInput, '0')
    })

    assert.equal(priceInput.getAttribute('aria-invalid'), 'false')
    assert.equal(saveButton.disabled, false)

    await act(async () => root.unmount())
    container.remove()
    queryClient.clear()
  })
})
