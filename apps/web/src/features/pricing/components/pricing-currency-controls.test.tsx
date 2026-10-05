/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'
import { after, afterEach, beforeEach, test } from 'node:test'

import { Window } from 'happy-dom'

import type { PricingModel, TokenUnit } from '../types'

const domWindow = new Window({ url: 'https://console.example.test/pricing' })
domWindow.document.write(
  '<!doctype html><html><head></head><body></body></html>'
)
Object.defineProperty(domWindow.document, 'compatMode', {
  configurable: true,
  value: 'CSS1Compat',
})
const globals = new Map<string, PropertyDescriptor | undefined>()
for (const key of [
  'window',
  'document',
  'navigator',
  'history',
  'location',
  'localStorage',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLInputElement',
  'SVGElement',
  'customElements',
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
  globals.set(key, Object.getOwnPropertyDescriptor(globalThis, key))
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}
globals.set(
  'matchMedia',
  Object.getOwnPropertyDescriptor(globalThis, 'matchMedia')
)
Object.defineProperty(globalThis, 'matchMedia', {
  configurable: true,
  value: (media: string) => ({
    matches: false,
    media,
    onchange: null,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
    dispatchEvent() {
      return false
    },
  }),
})

const { act, useState } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { I18nextProvider } = await import('react-i18next')
const { default: i18n } = await import('@/i18n/config')
const { TooltipProvider } = await import('@/components/ui/tooltip')
const { useWalletCurrency } = await import('@/hooks/use-wallet-currency')
const { useAuthStore } = await import('@/stores/auth-store')
const { useSystemConfigStore } = await import('@/stores/system-config-store')
const { useWalletCurrencyPreferenceStore } =
  await import('@/stores/wallet-currency-preference-store')
const { formatGroupPrice, formatPrice } = await import('../lib/price')
const { PricingToolbar } = await import('./pricing-toolbar')
const { DynamicPricingBreakdown } = await import('./dynamic-pricing-breakdown')

const originalConfig = useSystemConfigStore.getState().config
const originalAuth = useAuthStore.getState().auth
const originalPreference =
  useWalletCurrencyPreferenceStore.getState().preference
const originalLanguage = i18n.language
const reactGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
const originalActEnvironment = reactGlobals.IS_REACT_ACT_ENVIRONMENT
reactGlobals.IS_REACT_ACT_ENVIRONMENT = true

const model: PricingModel = {
  id: 1,
  model_name: 'quoted-model',
  quota_type: 0,
  pricing_schema_version: 2,
  pricing_currency: 'USD',
  input_price: 2.5,
  output_price: 15,
  // Deliberately different raw ratios catch accidental legacy recalculation.
  model_ratio: 9999,
  completion_ratio: 888,
  enable_groups: ['default'],
  group_ratio: { default: 10 },
}

function WalletObserver() {
  const wallet = useWalletCurrency()
  return (
    <output aria-label='Wallet quote'>
      {wallet.formatUSD(25, { abbreviate: false })}
    </output>
  )
}

function PricingHarness() {
  const wallet = useWalletCurrency()
  const [tokenUnit, setTokenUnit] = useState<TokenUnit>('M')
  const ignore = () => {}
  return (
    <>
      <PricingToolbar
        filteredCount={1}
        totalCount={1}
        sortBy='name'
        onSortChange={ignore}
        tokenUnit={tokenUnit}
        onTokenUnitChange={setTokenUnit}
        displayCurrency={wallet.currency}
        onDisplayCurrencyChange={(value) => {
          void wallet.setPreference(value)
        }}
        viewMode='card'
        onViewModeChange={ignore}
        quotaTypeFilter='all'
        endpointTypeFilter='all'
        vendorFilter='all'
        groupFilter='default'
        tagFilter='all'
        onQuotaTypeChange={ignore}
        onEndpointTypeChange={ignore}
        onVendorChange={ignore}
        onGroupChange={ignore}
        onTagChange={ignore}
        vendors={[]}
        groups={['default']}
        groupRatios={{ default: 10 }}
        tags={[]}
        models={[model]}
        hasActiveFilters={false}
        activeFilterCount={0}
        onClearFilters={ignore}
      />
      <output aria-label='Selected quote'>
        {formatPrice(model, 'input', tokenUnit, wallet.currency, 'default')}
      </output>
      <output aria-label='Group quote'>
        {formatGroupPrice(
          model,
          'default',
          'input',
          tokenUnit,
          wallet.currency,
          { default: 10 }
        )}
      </output>
      <output aria-label='Token unit'>{tokenUnit}</output>
      <WalletObserver />
    </>
  )
}

const mounted: Array<{
  container: HTMLDivElement
  root: ReturnType<typeof createRoot>
  queryClient: InstanceType<typeof QueryClient>
}> = []

async function render(children: React.ReactNode) {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  mounted.push({ container, root, queryClient })
  await act(async () => {
    root.render(
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>
          <TooltipProvider>{children}</TooltipProvider>
        </I18nextProvider>
      </QueryClientProvider>
    )
  })
  return container
}

function output(container: HTMLElement, label: string) {
  const element = container.querySelector(`output[aria-label="${label}"]`)
  assert.ok(element, `missing ${label}`)
  return element.textContent
}

function displayedPrices(container: HTMLElement) {
  return Array.from(container.querySelectorAll('td'))
    .map((cell) => cell.textContent)
    .filter((text) => text?.match(/ (?:USD|CNY|Credits)$/))
}

function button(container: HTMLElement, label: string) {
  const found = Array.from(container.querySelectorAll('button')).find(
    (element) => element.textContent === label
  )
  assert.ok(found, `missing button ${label}`)
  return found
}

async function click(container: HTMLElement, label: string) {
  await act(async () => {
    button(container, label).click()
  })
}

beforeEach(async () => {
  useAuthStore.getState().auth.reset()
  useWalletCurrencyPreferenceStore.getState().setPreference('')
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...originalConfig.currency,
      currencyUnit: 'credit',
      creditsPerUsd: 500_000,
      creditsPerUsdExact: '500000',
      cnyPerUsd: 7,
      cnyPerUsdExact: '7',
      legacyPricingUnitsPerUsd: 14,
    },
  })
  await i18n.changeLanguage('en')
})

afterEach(async () => {
  for (const item of mounted.splice(0)) {
    await act(async () => item.root.unmount())
    item.queryClient.clear()
    item.container.remove()
  }
  useSystemConfigStore.getState().setConfig(originalConfig)
  useAuthStore.setState({ auth: originalAuth })
  useWalletCurrencyPreferenceStore.getState().setPreference(originalPreference)
})

after(async () => {
  await i18n.changeLanguage(originalLanguage)
  reactGlobals.IS_REACT_ACT_ENVIRONMENT = originalActEnvironment
  domWindow.close()
  for (const [key, descriptor] of globals) {
    if (descriptor) Object.defineProperty(globalThis, key, descriptor)
    else Reflect.deleteProperty(globalThis, key)
  }
})

test('automatic anonymous currency follows interface language without saving a fiat preference', async () => {
  const container = await render(<PricingHarness />)
  assert.equal(button(container, 'USD').getAttribute('aria-pressed'), 'true')
  assert.equal(output(container, 'Selected quote'), '25 USD')
  assert.equal(output(container, 'Wallet quote'), '25 USD')
  for (const language of ['zhCN', 'zhTW']) {
    await act(async () => {
      await i18n.changeLanguage(language)
    })
    assert.equal(button(container, 'CNY').getAttribute('aria-pressed'), 'true')
    assert.equal(output(container, 'Selected quote'), '175 CNY')
    assert.equal(output(container, 'Wallet quote'), '175 CNY')
    assert.equal(useWalletCurrencyPreferenceStore.getState().preference, '')
  }
  await act(async () => {
    await i18n.changeLanguage('en')
  })
  assert.equal(output(container, 'Selected quote'), '25 USD')
  assert.equal(useWalletCurrencyPreferenceStore.getState().preference, '')
})

test('explicit CREDIT persists across language changes while token unit remains independent', async () => {
  const container = await render(<PricingHarness />)
  await click(container, 'CREDIT')
  assert.equal(button(container, 'CREDIT').getAttribute('aria-pressed'), 'true')
  assert.equal(output(container, 'Selected quote'), '12,500,000 Credits')
  assert.equal(output(container, 'Group quote'), '12,500,000 Credits')
  assert.equal(output(container, 'Wallet quote'), '12,500,000 Credits')
  assert.equal(useWalletCurrencyPreferenceStore.getState().preference, 'CREDIT')
  await act(async () => {
    await i18n.changeLanguage('zhCN')
  })
  await act(async () => {
    await i18n.changeLanguage('en')
  })
  assert.equal(button(container, 'CREDIT').getAttribute('aria-pressed'), 'true')
  assert.equal(output(container, 'Selected quote'), '12,500,000 Credits')
  await click(container, '/1K')
  assert.equal(output(container, 'Selected quote'), '12,500 Credits')
  assert.equal(output(container, 'Group quote'), '12,500 Credits')
  assert.equal(output(container, 'Wallet quote'), '12,500,000 Credits')
  assert.equal(output(container, 'Token unit'), 'K')
  assert.equal(useWalletCurrencyPreferenceStore.getState().preference, 'CREDIT')
  await click(container, 'CNY')
  assert.equal(output(container, 'Selected quote'), '0.175 CNY')
  assert.equal(output(container, 'Token unit'), 'K')
  await click(container, 'USD')
  assert.equal(output(container, 'Selected quote'), '0.025 USD')
  assert.equal(output(container, 'Group quote'), '0.025 USD')
  await click(container, '/1M')
  assert.equal(output(container, 'Selected quote'), '25 USD')
  assert.equal(useWalletCurrencyPreferenceStore.getState().preference, 'USD')
})

test('canonical USD expression wrappers are converted once and preserve usage units', async () => {
  const usd = await render(
    <DynamicPricingBreakdown
      billingExpr='tier("a", p*28+c*112)/14'
      expressionCurrencyBasis='USD'
      expressionUsdMultiplier={99}
      displayCurrency='USD'
    />
  )
  assert.deepEqual(displayedPrices(usd), ['2 USD', '8 USD'])
  const perK = await render(
    <DynamicPricingBreakdown
      billingExpr='tier("a", p*28)/14'
      expressionCurrencyBasis='USD'
      displayCurrency='USD'
      tokenUnit='K'
    />
  )
  assert.deepEqual(displayedPrices(perK), ['0.002 USD'])
  assert.match(perK.textContent ?? '', /1K tokens/)
  const credit = await render(
    <DynamicPricingBreakdown
      billingExpr='tier("a", p*28)/14'
      expressionCurrencyBasis='USD'
      displayCurrency='CREDIT'
    />
  )
  assert.deepEqual(displayedPrices(credit), ['1,000,000 Credits'])
})

test('legacy expression prices require a positive frozen USD multiplier', async () => {
  // A historical log's frozen factor must win over today's legacy bridge.
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...useSystemConfigStore.getState().config.currency,
      legacyPricingUnitsPerUsd: 140,
    },
  })
  const known = await render(
    <DynamicPricingBreakdown
      billingExpr='tier("a", p*28)'
      expressionCurrencyBasis='legacy_pricing_unit'
      expressionUsdMultiplier={1 / 14}
      displayCurrency='USD'
    />
  )
  assert.deepEqual(displayedPrices(known), ['2 USD'])
  for (const multiplier of [
    undefined,
    0,
    -1,
    Number.NaN,
    Number.POSITIVE_INFINITY,
  ]) {
    const unknown = await render(
      <DynamicPricingBreakdown
        billingExpr='tier("a", p*28)'
        expressionCurrencyBasis='legacy_pricing_unit'
        expressionUsdMultiplier={multiplier}
        displayCurrency='USD'
      />
    )
    assert.match(unknown.textContent ?? '', /Price conversion unavailable/)
    assert.doesNotMatch(unknown.textContent ?? '', /\d USD/)
    assert.ok(
      Array.from(unknown.querySelectorAll('td')).some(
        (cell) => cell.textContent === '-'
      )
    )
  }
  const absentBasis = await render(
    <DynamicPricingBreakdown
      billingExpr='tier("a", p*28)'
      expressionUsdMultiplier={1 / 14}
      displayCurrency='USD'
    />
  )
  assert.match(absentBasis.textContent ?? '', /Price conversion unavailable/)
  assert.doesNotMatch(absentBasis.textContent ?? '', /\d USD/)
})
