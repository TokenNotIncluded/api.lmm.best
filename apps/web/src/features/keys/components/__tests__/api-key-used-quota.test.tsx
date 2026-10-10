/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { after, afterEach, beforeEach, test } from 'node:test'

import { Window } from 'happy-dom'

const domWindow = new Window()
for (const key of [
  'window',
  'document',
  'HTMLElement',
  'Node',
  'Element',
  'navigator',
  'getComputedStyle',
  'MutationObserver',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { ApiKeyUsedQuota, UnlimitedQuotaBadge } =
  await import('../api-keys-cells')
const { default: i18n } = await import('@/i18n/config')
const { useAuthStore } = await import('@/stores/auth-store')
const { DEFAULT_CURRENCY_CONFIG, useSystemConfigStore } =
  await import('@/stores/system-config-store')
const { useWalletCurrencyPreferenceStore } =
  await import('@/stores/wallet-currency-preference-store')

const originalCurrency = useSystemConfigStore.getState().config.currency
const originalUser = useAuthStore.getState().auth.user
const originalPreference =
  useWalletCurrencyPreferenceStore.getState().preference
const originalLanguage = i18n.language
let root: ReturnType<typeof createRoot> | undefined

const projectedUsage = (normalized: number) => ({
  used_quota: 3_359_744,
  normalized_used_quota: normalized,
  usage_projection_available: true,
})

beforeEach(async () => {
  useAuthStore.getState().auth.setUser(null)
  useWalletCurrencyPreferenceStore.getState().setPreference('')
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...DEFAULT_CURRENCY_CONFIG,
      currencyUnit: 'credit',
      creditsPerUsd: 500_000,
      creditsPerUsdExact: '500000',
      cnyPerUsd: 7,
      cnyPerUsdExact: '7',
      legacyPricingUnitsPerUsd: 1,
      quotaPerUnit: 500_000,
    },
  })
  await i18n.changeLanguage('en')
})

afterEach(async () => {
  await act(async () => root?.unmount())
  root = undefined
})

after(async () => {
  useSystemConfigStore.getState().setConfig({ currency: originalCurrency })
  useAuthStore.getState().auth.setUser(originalUser)
  useWalletCurrencyPreferenceStore.getState().setPreference(originalPreference)
  await i18n.changeLanguage(originalLanguage)
  domWindow.close()
})

for (const [currency, amounts] of [
  ['USD', ['0 USD', '1 USD', '0.000002 USD']],
  ['CNY', ['0 CNY', '7 CNY', '0.000014 CNY']],
  ['CREDIT', ['0 Credits', '500,000 Credits', '1 Credits']],
] as const) {
  test(`renders zero, 500,000 credits and one credit literally in ${currency}`, async () => {
    const container = document.createElement('div')
    root = createRoot(container)
    useWalletCurrencyPreferenceStore.getState().setPreference(currency)

    for (const [index, used] of [0, 500_000, 1].entries()) {
      await act(async () =>
        root?.render(<ApiKeyUsedQuota apiKey={projectedUsage(used)} />)
      )
      const usage = container.querySelector('[data-api-key-used-quota]')
      assert.ok(usage)
      assert.equal(usage.textContent, amounts[index])
    }
  })
}

test('mounted usage cells react to currency changes without a parent render', async () => {
  const container = document.createElement('div')
  root = createRoot(container)
  await act(async () =>
    root?.render(
      <>
        <ApiKeyUsedQuota apiKey={projectedUsage(0)} />
        <ApiKeyUsedQuota apiKey={projectedUsage(500_000)} />
        <ApiKeyUsedQuota apiKey={projectedUsage(1)} />
      </>
    )
  )
  for (const [currency, expected] of [
    ['CNY', ['0 CNY', '7 CNY', '0.000014 CNY']],
    ['CREDIT', ['0 Credits', '500,000 Credits', '1 Credits']],
    ['USD', ['0 USD', '1 USD', '0.000002 USD']],
  ] as const) {
    await act(async () => {
      useWalletCurrencyPreferenceStore.getState().setPreference(currency)
    })
    assert.deepEqual(
      [...container.querySelectorAll('[data-api-key-used-quota]')].map(
        (usage) => usage.textContent
      ),
      expected
    )
  }
})

test('mounted usage follows language defaults and retains a manual USD preference', async () => {
  const container = document.createElement('div')
  root = createRoot(container)
  await act(async () =>
    root?.render(<ApiKeyUsedQuota apiKey={projectedUsage(500_000)} />)
  )
  assert.equal(container.textContent, '1 USD')
  await act(async () => {
    await i18n.changeLanguage('zhCN')
  })
  assert.equal(container.textContent, '7 CNY')
  await act(async () => {
    useWalletCurrencyPreferenceStore.getState().setPreference('USD')
  })
  assert.equal(container.textContent, '1 USD')
  await act(async () => {
    await i18n.changeLanguage('en')
  })
  assert.equal(container.textContent, '1 USD')
})

test('legacy quota-per-unit alone does not imply a fiat denomination', async () => {
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...DEFAULT_CURRENCY_CONFIG,
      quotaPerUnit: 500_000,
    },
  })
  const container = document.createElement('div')
  root = createRoot(container)
  await act(async () =>
    root?.render(<ApiKeyUsedQuota apiKey={projectedUsage(500_000)} />)
  )
  assert.equal(container.textContent, '-')
})

for (const projection of [
  {},
  { usage_projection_available: false, normalized_used_quota: 500_000 },
  { usage_projection_available: true },
  { usage_projection_available: true, normalized_used_quota: null },
  { usage_projection_available: true, normalized_used_quota: -1 },
  { usage_projection_available: true, normalized_used_quota: 0.5 },
  { usage_projection_available: true, normalized_used_quota: Number.NaN },
  {
    usage_projection_available: true,
    normalized_used_quota: Number.MAX_SAFE_INTEGER + 1,
  },
]) {
  test(`unconfirmed or invalid projection stays raw CREDIT: ${JSON.stringify(projection)}`, async () => {
    const container = document.createElement('div')
    root = createRoot(container)
    for (const currency of ['USD', 'CNY', 'CREDIT'] as const) {
      await act(async () => {
        useWalletCurrencyPreferenceStore.getState().setPreference(currency)
        root?.render(
          <ApiKeyUsedQuota apiKey={{ used_quota: 3_359_744, ...projection }} />
        )
      })
      assert.equal(container.textContent, '3,359,744 Credits')
    }
  })
}

test('unlimited quota tooltip labels use the same projected or raw usage', async () => {
  const container = document.createElement('div')
  root = createRoot(container)
  await act(async () =>
    root?.render(<UnlimitedQuotaBadge apiKey={projectedUsage(500_000)} />)
  )
  assert.equal(
    container.querySelector('button')?.getAttribute('aria-label'),
    'Unlimited; Used: 1 USD'
  )
  await act(async () =>
    root?.render(<UnlimitedQuotaBadge apiKey={{ used_quota: 3_359_744 }} />)
  )
  assert.equal(
    container.querySelector('button')?.getAttribute('aria-label'),
    'Unlimited; Used: 3,359,744 Credits'
  )
})
