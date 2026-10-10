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
*/
import assert from 'node:assert/strict'
import { after, afterEach, describe, test } from 'node:test'

import { Window } from 'happy-dom'

const domWindow = new Window({ url: 'https://console.example.test/security' })
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
  'HTMLButtonElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'MutationObserver',
  'ResizeObserver',
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
const { useSystemConfigStore, DEFAULT_CURRENCY_CONFIG } =
  await import('@/stores/system-config-store')
const { useWalletCurrencyPreferenceStore } =
  await import('@/stores/wallet-currency-preference-store')
useSystemConfigStore.getState().setConfig({
  currency: {
    ...DEFAULT_CURRENCY_CONFIG,
    currencyUnit: 'credit',
    creditsPerUsd: 500000,
    cnyPerUsd: 7.2,
    legacyPricingUnitsPerUsd: 1,
  },
})
useWalletCurrencyPreferenceStore.getState().setPreference('USD')
const { api } = await import('@/lib/api')
const { SecurityContent } = await import('./index')

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

const policyResponse = {
  success: true,
  data: {
    policy_version: 'moderation-v1',
    reference_effective_date: '2026-10-07',
    reference_url: 'https://platform.openai.com/docs/guides/moderation',
    alignment: 'OpenAI Moderation',
    moderation: {
      enabled: true,
      assistant_enabled: true,
      engine: 'openai_moderation',
      async: true,
      group_policies: {
        default: {
          mode: 'strict',
          category_fines_usd: { harassment: 0.25 },
          amount_currency: 'USD',
        },
      },
      supported_inputs: ['text'],
      notice_only: false,
    },
  },
}

const statsResponse = {
  success: true,
  data: {
    moderation: {
      pending: 2,
      running: 1,
      completed: 34,
      failed: 3,
      cancelled: 4,
      flagged: 7,
      fined: 2,
      charged_quota: 100000,
    },
  },
}

async function flushQueries() {
  await new Promise((resolve) => setTimeout(resolve, 10))
}

async function waitForText(container: HTMLElement, text: string) {
  for (let attempt = 0; attempt < 60; attempt += 1) {
    if (container.textContent?.includes(text)) return
    await act(flushQueries)
  }
  throw new Error(`Could not find ${text}: ${container.textContent}`)
}

async function renderSecurityContent() {
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
          <SecurityContent />
        </I18nextProvider>
      </QueryClientProvider>
    )
    await flushQueries()
  })

  return { container, root }
}

afterEach(() => {
  api.get = originalGet
  document.body.replaceChildren()
})

after(() => domWindow.close())

describe('SecurityContent', () => {
  test('shows current Moderation totals without historical rule statistics', async () => {
    api.get = (async (url: string) => {
      if (url === '/api/security/policy') return { data: policyResponse }
      if (url === '/api/security/stats') return { data: statsResponse }
      throw new Error(`Unexpected GET ${url}`)
    }) as typeof api.get
    const rendered = await renderSecurityContent()
    try {
      await waitForText(rendered.container, 'All-time Moderation statistics')
      const content = rendered.container.textContent ?? ''
      assert.match(content, /Completed Moderation reviews/)
      assert.match(content, /Flagged Moderation reviews/)
      assert.match(content, /Reviews with wallet deductions/)
      assert.match(content, /34/)
      assert.doesNotMatch(content, /Historical rule matching statistics/)
      assert.match(
        content,
        /Flagged reviews include user input and assistant output/
      )
      assert.doesNotMatch(
        content,
        /Moderation statistics are not available yet/
      )
    } finally {
      await act(async () => rendered.root.unmount())
    }
  })

  test('renders current policy, fees and metadata without retired sections or endpoints', async () => {
    const requestedUrls: string[] = []
    api.get = (async (url: string) => {
      requestedUrls.push(url)
      if (url === '/api/security/policy') return { data: policyResponse }
      if (url === '/api/security/stats') return { data: statsResponse }
      throw new Error(`Unexpected GET ${url}`)
    }) as typeof api.get

    const rendered = await renderSecurityContent()
    try {
      await waitForText(rendered.container, 'Moderation group policies')
      const content = rendered.container.textContent ?? ''

      assert.match(content, /Policy metadata/)
      assert.match(content, /default/)
      assert.match(content, /Current Moderation settings/)
      assert.match(content, /harassment/)
      assert.match(content, /0\.25 USD/)
      assert.doesNotMatch(
        content,
        /Historical|Legacy detection|retired literal|violation_fee\.grok/
      )
      assert.equal(
        rendered.container.querySelector('#security-policy-title'),
        null
      )
      assert.equal(
        rendered.container.querySelector('#security-enforcement-title'),
        null
      )
      assert.deepEqual(
        requestedUrls.filter((url) => url.startsWith('/api/security/')).sort(),
        ['/api/security/policy', '/api/security/stats']
      )
      assert.equal(requestedUrls.includes('/api/security/overview'), false)
    } finally {
      await act(async () => rendered.root.unmount())
    }
  })

  test('shows explicit empty states when policy and stats have no data', async () => {
    api.get = (async (url: string) => {
      if (url === '/api/security/policy' || url === '/api/security/stats') {
        return { data: { success: true } }
      }
      throw new Error(`Unexpected GET ${url}`)
    }) as typeof api.get

    const rendered = await renderSecurityContent()
    try {
      await waitForText(
        rendered.container,
        'Moderation settings are not published yet.'
      )
      const content = rendered.container.textContent ?? ''

      assert.match(content, /No live risk metrics are available yet\./)
      assert.match(content, /Active groups and fees cannot be confirmed/)
      assert.match(content, /\/api\/security\/stats/)
      assert.doesNotMatch(content, /0\.25 USD/)
    } finally {
      await act(async () => rendered.root.unmount())
    }
  })
})
