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
    creditsPerUsd: 2500000,
    cnyPerUsd: 7.2,
    legacyPricingUnitsPerUsd: 5,
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
    policy_version: 'anthropic-aligned-v1',
    reference_effective_date: '2025-09-15',
    reference_url: 'https://www.anthropic.com/legal/aup',
    alignment: 'Local public adaptation',
    enforcement: {
      enabled: true,
      on_prompt: true,
      action: 'block',
    },
    protected_groups: ['default'],
    risk_categories: [
      {
        id: 'privacy_identity',
        name: 'Privacy and identity rights',
        layer: 'universal_standard',
        severity: 'high',
        description: 'Unauthorized use of private data.',
        source: 'anthropic_usage_policy',
      },
    ],
    rules: [
      {
        id: 'privacy-rule',
        name: 'Protect private data',
        category: 'privacy_identity',
        layer: 'universal_standard',
        severity: 'high',
        source: 'local_custom',
        version: '1',
        description: 'Do not expose another person’s sensitive data.',
      },
    ],
    violation_fees: [
      {
        code: 'violation_fee.grok.csam',
        provider: 'Grok / xAI upstream',
        trigger: 'The upstream provider returns a violation marker.',
        enabled: true,
        amount_usd: 0.25,
        charge_unit: 'per request',
        retryable: false,
        description: 'An additional fee may be charged.',
        charging_notes: 'The fee is applied only when enabled.',
        local_guardrail_fee: false,
      },
    ],
  },
}

const statsResponse = {
  success: true,
  data: {
    start_timestamp: 1_780_000_000,
    end_timestamp: 1_780_086_400,
    total_matches: 17,
    blocked_matches: 11,
    audited_matches: 6,
    affected_requests: 14,
    affected_users: 3,
    by_category: [{ key: 'privacy_identity', count: 9 }],
    by_rule: [{ key: 'privacy-rule', count: 9 }],
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
  test('distinguishes live Moderation totals from historical rule statistics', async () => {
    api.get = (async (url: string) => {
      if (url === '/api/security/policy') return { data: policyResponse }
      if (url === '/api/security/stats') {
        return {
          data: {
            ...statsResponse,
            data: {
              ...statsResponse.data,
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
          },
        }
      }
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
      assert.match(content, /Historical rule matching statistics/)
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

  test('renders policy categories, rule summaries, real stats, and fee rules', async () => {
    const requestedUrls: string[] = []
    api.get = (async (url: string) => {
      requestedUrls.push(url)
      if (url === '/api/security/policy') return { data: policyResponse }
      if (url === '/api/security/stats') return { data: statsResponse }
      throw new Error(`Unexpected GET ${url}`)
    }) as typeof api.get

    const rendered = await renderSecurityContent()
    try {
      await waitForText(rendered.container, 'Protect private data')
      const content = rendered.container.textContent ?? ''

      assert.match(content, /Privacy and identity rights/)
      assert.match(content, /Policy metadata/)
      assert.match(content, /default/)
      assert.match(content, /Historical safety rules and fees/)
      assert.doesNotMatch(content, /Block matching requests/)
      assert.match(content, /retired literal rules/)
      assert.match(content, /OpenAI Moderation/)
      assert.match(content, /17/)
      assert.match(content, /11/)
      assert.match(content, /violation_fee\.grok\.csam/)
      assert.match(content, /0\.05 USD/)
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
        'The public security policy is not available yet.'
      )
      const content = rendered.container.textContent ?? ''

      assert.match(content, /No live risk metrics are available yet\./)
      assert.match(
        content,
        /No categories, rules, or fee amounts are fabricated/
      )
      assert.match(content, /\/api\/security\/policy/)
      assert.match(content, /\/api\/security\/stats/)
      assert.doesNotMatch(content, /0\.05 USD/)
    } finally {
      await act(async () => rendered.root.unmount())
    }
  })
})

test('an explicit USD provider fee bypasses the legacy pricing-unit bridge', async () => {
  api.get = (async (url: string) => {
    if (url === '/api/security/policy') {
      return {
        data: {
          ...policyResponse,
          data: {
            ...policyResponse.data,
            violation_fees: policyResponse.data.violation_fees.map((fee) => ({
              ...fee,
              amount_currency: 'USD',
            })),
          },
        },
      }
    }
    if (url === '/api/security/stats') return { data: statsResponse }
    throw new Error(`Unexpected GET ${url}`)
  }) as typeof api.get
  const rendered = await renderSecurityContent()
  try {
    await waitForText(rendered.container, 'violation_fee.grok.csam')
    assert.match(rendered.container.textContent ?? '', /0\.25 USD/)
    assert.doesNotMatch(rendered.container.textContent ?? '', /0\.05 USD/)
  } finally {
    await act(async () => rendered.root.unmount())
  }
})
