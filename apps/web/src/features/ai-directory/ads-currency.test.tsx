/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
// @ts-expect-error Bun's test module is available only in the test runtime.
import { mock as moduleMock } from 'bun:test'
import assert from 'node:assert/strict'
import { after, afterEach, beforeEach, test } from 'node:test'

import { Window } from 'happy-dom'
import type { ReactNode } from 'react'
import type { Root } from 'react-dom/client'

import type { DirectoryAd, DirectoryAdInput } from './ads-api'

const domWindow = new Window({
  url: 'https://console.example.test/ai-directory',
})
for (const key of [
  'window',
  'document',
  'navigator',
  'localStorage',
  'HTMLElement',
  'HTMLInputElement',
  'HTMLButtonElement',
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
  'matchMedia',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
moduleMock.module('@tanstack/react-router', () => ({
  Link: ({
    children,
    to,
    onClick,
  }: {
    children?: ReactNode
    to: string
    onClick?: () => void
  }) => (
    <a href={to} onClick={onClick}>
      {children}
    </a>
  ),
}))

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider, notifyManager } =
  await import('@tanstack/react-query')
const { I18nextProvider } = await import('react-i18next')
const { default: i18n } = await import('@/i18n/config')
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const { DEFAULT_CURRENCY_CONFIG, useSystemConfigStore } =
  await import('@/stores/system-config-store')
const { useWalletCurrencyPreferenceStore } =
  await import('@/stores/wallet-currency-preference-store')
const { SponsoredDirectorySection } = await import('./sponsored-section')
const { AIDirectoryAdsSection } =
  await import('../system-settings/site/ai-directory-ads-section')
notifyManager.setScheduler(queueMicrotask)

const historicalAd: DirectoryAd = {
  id: 1,
  name: 'Historical placement',
  url: 'https://historical.example.test',
  summary: 'A historical ad',
  description: '',
  bid_cents: 100,
  charged_quota: 500_000,
  charged_amount_usd: '0.142857142857142857',
  status: 'active',
  paid_at: 1,
  expires_at: Math.floor(Date.now() / 1000) + 86_400,
  hidden_at: 0,
  refunded_at: 0,
}
const original = {
  fetch: globalThis.fetch,
  get: api.get,
  post: api.post,
  put: api.put,
  config: useSystemConfigStore.getState().config,
  auth: useAuthStore.getState().auth,
  preference: useWalletCurrencyPreferenceStore.getState().preference,
  language: i18n.language,
}
let root: Root
let container: HTMLDivElement
let cache: InstanceType<typeof QueryClient>
let oldQuote = false
const bids: DirectoryAdInput[] = []

async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 20))
}
async function click(label: string, scope: ParentNode = document) {
  const button = [...scope.querySelectorAll<HTMLButtonElement>('button')].find(
    (candidate) => candidate.textContent === label
  )
  assert.ok(button, `Missing ${label}`)
  await act(async () => {
    button.click()
    await flush()
  })
}
async function select(label: string, scope: ParentNode = container) {
  const trigger = scope.querySelector<HTMLButtonElement>(
    '[data-slot="select-trigger"]'
  )
  assert.ok(trigger)
  await act(async () => {
    trigger.click()
    await flush()
  })
  const option = [
    ...document.querySelectorAll<HTMLElement>('[role="option"]'),
  ].find((candidate) => candidate.textContent === label)
  assert.ok(option, `Missing ${label} option`)
  await act(async () => {
    option.click()
    await flush()
  })
}
async function edit(id: string, value: string) {
  const input = document.getElementById(id)
  assert.ok(input instanceof HTMLInputElement)
  const setter = Object.getOwnPropertyDescriptor(
    HTMLInputElement.prototype,
    'value'
  )?.set
  assert.ok(setter)
  await act(async () => {
    setter.call(input, value)
    input.dispatchEvent(new Event('input', { bubbles: true }))
    await flush()
  })
}
async function render(moderation = false) {
  await act(async () => {
    root.render(
      <QueryClientProvider client={cache}>
        <I18nextProvider i18n={i18n}>
          {moderation ? (
            <AIDirectoryAdsSection />
          ) : (
            <SponsoredDirectorySection />
          )}
        </I18nextProvider>
      </QueryClientProvider>
    )
    await flush()
  })
}

beforeEach(async () => {
  oldQuote = false
  bids.length = 0
  await i18n.changeLanguage('en')
  useAuthStore.setState({
    auth: { ...original.auth, user: null, accessToken: null },
  })
  useWalletCurrencyPreferenceStore.getState().setPreference('USD')
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...DEFAULT_CURRENCY_CONFIG,
      currencyUnit: 'credit',
      creditsPerUsd: 3_500_000,
      creditsPerUsdExact: '3500000',
      cnyPerUsd: 7,
      cnyPerUsdExact: '7',
      legacyPricingUnitsPerUsd: 7,
      quotaPerUnit: 500_000,
    },
  })
  globalThis.fetch = async () =>
    Response.json({
      success: true,
      data: { items: [historicalAd], has_more: false, next_offset: 1 },
    })
  api.get = (async (url: string) => {
    if (url.startsWith('/api/ai-directory/ads/quote')) {
      return {
        data: {
          success: true,
          data: {
            ...(oldQuote ? {} : { pricing_schema_version: 2 }),
            currency: 'USD',
            bid_cents: 100,
            quota: 3_500_000,
            duration_days: 30,
            min_bid_cents: 100,
            max_bid_cents: 1_000_000,
          },
        },
      }
    }
    if (url === '/api/ai-directory/ads/mine') {
      return { data: { success: true, data: { items: [historicalAd] } } }
    }
    if (url === '/api/user/self') {
      return {
        data: { success: true, data: useAuthStore.getState().auth.user },
      }
    }
    throw new Error(`Unexpected request: ${url}`)
  }) as typeof api.get
  api.post = (async (url: string, input: DirectoryAdInput) => {
    assert.equal(url, '/api/ai-directory/ads')
    bids.push(input)
    return {
      data: {
        success: true,
        data: { ad: historicalAd, created: true, charged_quota: 3_500_000 },
      },
    }
  }) as typeof api.post
  api.put = (async () => ({ data: { success: true } })) as typeof api.put
  cache = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: Infinity },
      mutations: { retry: false },
    },
  })
  container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)
})
afterEach(async () => {
  await act(async () => root.unmount())
  cache.clear()
  container.remove()
  globalThis.fetch = original.fetch
  api.get = original.get
  api.post = original.post
  api.put = original.put
  useAuthStore.setState({ auth: original.auth })
  useSystemConfigStore.setState({ config: original.config })
  useWalletCurrencyPreferenceStore.getState().setPreference(original.preference)
})
after(async () => {
  await i18n.changeLanguage(original.language)
  domWindow.close()
})

test('historical public and moderation prices follow charged quota, language and shared display preference', async () => {
  await render()
  assert.match(container.textContent ?? '', /Paid: 0\.142857 USD/)
  assert.doesNotMatch(container.textContent ?? '', /1\.00 USD equivalent/)
  await select('CNY')
  assert.match(container.textContent ?? '', /Paid: 1 CNY/)
  assert.equal(useWalletCurrencyPreferenceStore.getState().preference, 'CNY')
  await select('Credits')
  assert.match(container.textContent ?? '', /Paid: 500,000 Credits/)
  await select('Follow language')
  await act(async () => i18n.changeLanguage('zhCN'))
  assert.match(container.textContent ?? '', /1 CNY/)
  await act(async () => i18n.changeLanguage('en'))
  await render(true)
  assert.match(container.textContent ?? '', /Paid: 0\.142857 USD/)
  await select('CNY')
  await click('Hide and refund', container)
  assert.match(
    document.querySelector('[role="alertdialog"]')?.textContent ?? '',
    /1 CNY will be refunded/
  )
  assert.equal(bids.length, 0)
})

test('unknown K blocks public credit and USD display while unknown FX blocks CNY display', async () => {
  await render()
  await act(async () =>
    useSystemConfigStore.getState().setConfig({
      currency: {
        ...useSystemConfigStore.getState().config.currency,
        creditsPerUsd: 0,
        creditsPerUsdExact: '',
      },
    })
  )
  assert.match(container.textContent ?? '', /Paid: -/)
  await select('Credits')
  assert.match(container.textContent ?? '', /Paid: -/)
  assert.doesNotMatch(container.textContent ?? '', /Paid: 500,000 Credits/)
  await select('CNY')
  await act(async () =>
    useSystemConfigStore.getState().setConfig({
      currency: {
        ...useSystemConfigStore.getState().config.currency,
        creditsPerUsd: 3_500_000,
        creditsPerUsdExact: '3500000',
        cnyPerUsd: 0,
        cnyPerUsdExact: '',
      },
    })
  )
  assert.match(container.textContent ?? '', /Paid: -/)
})

test('public credit denomination updates a paid receipt without changing its USD amount or ledger charge', async () => {
  await render()
  await act(async () =>
    useSystemConfigStore.getState().setConfig({
      currency: {
        ...useSystemConfigStore.getState().config.currency,
        creditUnitSchemaVersion: 2,
        ledgerQuotaPerUsd: 3_500_000,
        ledgerQuotaPerUsdExact: '3500000',
        publicCreditsPerUsd: 100_000,
        publicCreditsPerUsdExact: '100000',
        quotaUnit: 'LEDGER_QUOTA',
        publicCreditUnit: 'CREDIT',
      },
    })
  )
  assert.match(container.textContent ?? '', /Paid: 0\.142857 USD/)
  await select('Credits')
  assert.match(container.textContent ?? '', /Paid: 14,285\.71 Credits/)
  assert.doesNotMatch(container.textContent ?? '', /Paid: 500,000 Credits/)
  await act(async () =>
    useSystemConfigStore.getState().setConfig({
      currency: {
        ...useSystemConfigStore.getState().config.currency,
        publicCreditsPerUsd: 200_000,
        publicCreditsPerUsdExact: '200000',
        cnyPerUsd: 0,
        cnyPerUsdExact: '',
      },
    })
  )
  assert.match(container.textContent ?? '', /Paid: 28,571\.43 Credits/)
  await select('USD')
  assert.match(container.textContent ?? '', /Paid: 0\.142857 USD/)
  assert.equal(historicalAd.charged_quota, 500_000)
  assert.equal(bids.length, 0)
})

test('a one-dollar bid submits 100 USD cents and the server charge while its display uses CNY or Credits', async () => {
  useAuthStore.setState({
    auth: {
      ...original.auth,
      user: {
        id: 7,
        username: 'advertiser',
        role: 1,
        developer_access_granted: true,
        setting: { wallet_display_currency: 'CNY' },
      },
      accessToken: 'test-only-token',
    },
  })
  await render()
  await click('Promote your website', container)
  await edit('sponsor-name', 'New placement')
  await edit('sponsor-url', 'https://new.example.test')
  const dialog = document.querySelector('[role="dialog"]')
  assert.ok(dialog)
  assert.match(dialog.textContent ?? '', /Bid \(USD\)/)
  assert.match(dialog.textContent ?? '', /Exact charge: 7 CNY for 30 days/)
  assert.match(dialog.textContent ?? '', /1 CNY paid/)
  const form = dialog.querySelector('form')
  assert.ok(form)
  // Happy DOM does not dispatch the browser's form submit from this Base UI
  // button click. Exercise the same submit handler explicitly.
  await act(async () => {
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    await flush()
  })
  assert.match(dialog.textContent ?? '', /Bid1 USD/)
  assert.match(dialog.textContent ?? '', /Wallet charge7 CNY/)
  await select('Credits', dialog)
  assert.match(dialog.textContent ?? '', /Wallet charge3,500,000 Credits/)
  assert.match(dialog.textContent ?? '', /Bid1 USD/)
  await click('Pay and publish', dialog)
  assert.equal(bids.length, 1)
  assert.equal(bids[0]?.bid_cents, 100)
  assert.equal(bids[0]?.expected_quota, 3_500_000)
})

test('a legacy backend quote cannot open confirmation or submit a payment', async () => {
  oldQuote = true
  useAuthStore.setState({
    auth: {
      ...original.auth,
      user: {
        id: 7,
        username: 'advertiser',
        role: 1,
        developer_access_granted: true,
      },
      accessToken: 'test-only-token',
    },
  })
  await render()
  await click('Promote your website', container)
  await edit('sponsor-name', 'New placement')
  await edit('sponsor-url', 'https://new.example.test')
  const dialog = document.querySelector('[role="dialog"]')
  assert.ok(dialog)
  assert.match(dialog.textContent ?? '', /Unable to get a price quote/)
  assert.doesNotMatch(dialog.textContent ?? '', /Exact charge:/)
  const review = [...dialog.querySelectorAll<HTMLButtonElement>('button')].find(
    (button) => button.textContent === 'Review payment'
  )
  assert.ok(review?.disabled)
  assert.doesNotMatch(dialog.textContent ?? '', /Confirm advertisement payment/)
  assert.equal(bids.length, 0)
})
