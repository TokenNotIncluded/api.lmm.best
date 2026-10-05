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
domWindow.document.write(
  '<!doctype html><html><head></head><body></body></html>'
)
Object.defineProperty(domWindow.document, 'compatMode', {
  configurable: true,
  value: 'CSS1Compat',
})
for (const key of [
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
const { toast } = await import('sonner')
const { useAuthStore } = await import('@/stores/auth-store')
const { DEFAULT_CURRENCY_CONFIG, useSystemConfigStore } =
  await import('@/stores/system-config-store')
const { useWalletCurrencyPreferenceStore } =
  await import('@/stores/wallet-currency-preference-store')
const { CheckinCalendarCard } = await import('./checkin-calendar-card')

const originalGet = api.get
const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'zhCN',
  resources: {
    zhCN: {
      translation: {
        'Checked in': '已签到',
        'Next month': '下个月',
        'Previous month': '上个月',
      },
    },
  },
})

async function waitForCondition(
  condition: () => boolean,
  failureMessage: string
): Promise<void> {
  const deadline = Date.now() + 1500
  while (!condition()) {
    if (Date.now() >= deadline) {
      throw new Error(`${failureMessage}: ${document.body.textContent}`)
    }
    await new Promise((resolve) => setTimeout(resolve, 5))
  }
}

afterEach(() => {
  api.get = originalGet
  document.body.replaceChildren()
})

after(() => domWindow.close())

describe('check-in calendar accessibility', () => {
  test('renders localized, non-actionable dates and bounded month navigation', async () => {
    const now = new Date()
    const recordDate = `${now.getFullYear()}-${String(
      now.getMonth() + 1
    ).padStart(2, '0')}-01`
    api.get = (async () => ({
      data: {
        success: true,
        data: {
          enabled: true,
          stats: {
            checked_in_today: false,
            total_checkins: 1,
            total_quota: 100,
            checkin_count: 1,
            records: [{ checkin_date: recordDate, quota_awarded: 100 }],
          },
        },
      },
    })) as typeof api.get

    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)

    await act(async () => {
      root.render(
        <QueryClientProvider client={queryClient}>
          <I18nextProvider i18n={i18n}>
            <CheckinCalendarCard
              checkinEnabled
              turnstileEnabled={false}
              turnstileSiteKey=''
            />
          </I18nextProvider>
        </QueryClientProvider>
      )
    })
    await act(async () => {
      await waitForCondition(
        () => container.querySelector('[role="grid"]') !== null,
        'calendar grid did not render'
      )
    })

    const grid = container.querySelector('[role="grid"]')
    assert.ok(grid)
    assert.equal(grid.querySelectorAll('[role="gridcell"] button').length, 0)
    assert.deepEqual(
      [...grid.querySelectorAll('[role="columnheader"]')]
        .slice(0, 2)
        .map((header) => header.textContent),
      ['周日', '周一']
    )

    const checkedCell = grid.querySelector('[aria-label*="已签到"]')
    assert.ok(checkedCell)
    assert.equal(checkedCell.tagName, 'DIV')
    assert.equal(checkedCell.getAttribute('tabindex'), '0')

    const previousButton = container.querySelector<HTMLButtonElement>(
      'button[aria-label="上个月"]'
    )
    const nextButton = container.querySelector<HTMLButtonElement>(
      'button[aria-label="下个月"]'
    )
    assert.ok(previousButton)
    assert.ok(nextButton)
    assert.match(previousButton.className, /\bsize-11\b/)
    assert.equal(nextButton.disabled, true)

    await act(async () => {
      previousButton.click()
      await waitForCondition(
        () =>
          container.querySelector<HTMLButtonElement>(
            'button[aria-label="下个月"]'
          )?.disabled === false,
        'next-month navigation did not become available'
      )
    })

    await act(async () => root.unmount())
    queryClient.clear()
  })
})

test('check-in success uses the newly selected currency without remounting', async () => {
  const previousAuth = useAuthStore.getState().auth
  const previousCurrency = useSystemConfigStore.getState().config.currency
  const previousPreference =
    useWalletCurrencyPreferenceStore.getState().preference
  const previousPost = api.post
  const previousSuccess = toast.success
  let successMessage = ''
  toast.success = ((message: unknown) => {
    successMessage = String(message)
    return 1
  }) as typeof toast.success
  useAuthStore.getState().auth.setUser(null)
  useWalletCurrencyPreferenceStore.getState().setPreference('USD')
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...DEFAULT_CURRENCY_CONFIG,
      currencyUnit: 'credit',
      creditsPerUsd: 500_000,
      creditsPerUsdExact: '500000',
      cnyPerUsd: 7,
      cnyPerUsdExact: '7',
    },
  })
  api.get = (async () => ({
    data: {
      success: true,
      data: {
        enabled: true,
        stats: {
          checked_in_today: false,
          total_checkins: 0,
          total_quota: 0,
          checkin_count: 0,
          records: [],
        },
      },
    },
  })) as typeof api.get
  api.post = (async () => ({
    data: { success: true, data: { quota_awarded: 3_500_000 } },
  })) as typeof api.post
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () =>
      root.render(
        <QueryClientProvider client={client}>
          <I18nextProvider i18n={i18n}>
            <CheckinCalendarCard
              checkinEnabled
              turnstileEnabled={false}
              turnstileSiteKey=''
            />
          </I18nextProvider>
        </QueryClientProvider>
      )
    )
    await act(async () =>
      waitForCondition(
        () => host.querySelector('[role="grid"]') !== null,
        'calendar not loaded'
      )
    )
    await act(async () =>
      useWalletCurrencyPreferenceStore.getState().setPreference('CNY')
    )
    const button = [...host.querySelectorAll<HTMLButtonElement>('button')].find(
      (element) => element.textContent?.trim() === 'Check in now'
    )
    assert.ok(button, 'check-in button not found')
    await act(async () => {
      button.click()
      await waitForCondition(
        () => Boolean(successMessage),
        'check-in success not shown'
      )
    })
    assert.match(successMessage, /49 CNY/)
    assert.doesNotMatch(successMessage, /1 USD|Platform|\$/)
  } finally {
    await act(async () => root.unmount())
    client.clear()
    host.remove()
    api.post = previousPost
    toast.success = previousSuccess
    useAuthStore.setState({ auth: previousAuth })
    useSystemConfigStore.getState().setConfig({ currency: previousCurrency })
    useWalletCurrencyPreferenceStore
      .getState()
      .setPreference(previousPreference)
  }
})
