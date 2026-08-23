/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

const domWindow = new Window({
  url: 'https://console.example.test/admin/system-settings/models',
})
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
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { DynamicPricingSection } = await import('./dynamic-pricing-section')

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

test('allows channel costs to be configured before dynamic pricing is enabled', async () => {
  const originalGet = api.get
  api.get = (async (url: string) => {
    assert.equal(url, '/api/dynamic_pricing/status')
    return {
      data: {
        success: true,
        message: '',
        data: {
          enabled: false,
          preview_factor: 1,
          setting: {
            enabled: false,
            min_factor: 1,
            require_channel_cost: true,
            tick_interval_seconds: 30,
            window_minutes: 5,
            target_tpm: 1,
            target_rpm: 1,
            target_cost_rate: 1,
            base_price_usd_per_million: 1,
            alpha_load: 1,
            alpha_up: 1,
            alpha_down: 1,
            cost_floor_factor: 1,
            max_factor: 2,
            load_deadzone: 0,
            heat_gamma: 1,
            max_step_up: 0.1,
            max_step_down: 0.1,
            failover_probability: 0,
            channel_costs: {},
            per_model: {},
          },
          models: {},
          safety: {
            ready: false,
            status: 'not_ready',
            reason:
              'one or more active channels do not have a conservative upstream cost',
            active_channel_count: 1,
            configured_channel_count: 0,
            channels: [
              {
                id: 42,
                name: 'Primary channel',
                cost: 0,
                cost_floor: 1,
                configured: false,
              },
            ],
            missing_channels: [{ id: 42, name: 'Primary channel' }],
            require_channel_cost: true,
          },
        },
      },
    }
  }) as typeof api.get

  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })

  try {
    await act(async () => {
      root.render(
        <QueryClientProvider client={queryClient}>
          <I18nextProvider i18n={i18n}>
            <DynamicPricingSection
              defaultValues={{
                GroupRatio: '{}',
                'dynamic_pricing_setting.enabled': false,
                'dynamic_pricing_setting.min_factor': 1,
                'dynamic_pricing_setting.base_price_usd_per_million': 1,
                'dynamic_pricing_setting.cost_floor_factor': 1,
                'dynamic_pricing_setting.max_factor': 2,
                'dynamic_pricing_setting.channel_costs': '{}',
              }}
            />
          </I18nextProvider>
        </QueryClientProvider>
      )
    })
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 50))
    })

    const input = container.querySelector<HTMLInputElement>(
      'input[aria-label="Cost for Primary channel"]'
    )
    assert.ok(
      input,
      JSON.stringify({
        text: container.textContent,
        inputs: [...container.querySelectorAll('input')].map((element) => ({
          ariaLabel: element.getAttribute('aria-label'),
          disabled: element.disabled,
        })),
      })
    )
    assert.equal(input.disabled, false)
    assert.match(container.textContent ?? '', /Primary channel/)
  } finally {
    api.get = originalGet
    await act(async () => root.unmount())
    queryClient.clear()
    container.remove()
  }
})
