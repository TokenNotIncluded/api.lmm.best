/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, afterEach, beforeEach, test } from 'node:test'

import { Window } from 'happy-dom'

import type { PriceDisplayCurrency, PricingModel, TokenUnit } from '../types'

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
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const { useSystemConfigStore } = await import('@/stores/system-config-store')
const { filterByGroup } = await import('../lib/filters')
const { ModelCard } = await import('./model-card')
const { ModelDetailsContent } = await import('./model-details')
const { PricingTable } = await import('./pricing-table')
const { ModelCompareDialog } = await import('./model-compare-dialog')

const originalConfig = useSystemConfigStore.getState().config
const originalAuth = useAuthStore.getState().auth
const originalLanguage = i18n.language
const originalGet = api.get
const originalPost = api.post
const reactGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
const originalActEnvironment = reactGlobals.IS_REACT_ACT_ENVIRONMENT
reactGlobals.IS_REACT_ACT_ENVIRONMENT = true

const groups = { cheap: 0.13, premium: 2 }
const usableGroup = {
  cheap: { desc: 'Cheap access', ratio: 0.13 },
  premium: { desc: 'Premium access', ratio: 2 },
}
const model: PricingModel = {
  id: 1,
  model_name: 'base-quote-model',
  quota_type: 0,
  pricing_schema_version: 2,
  pricing_currency: 'USD',
  input_price: 4,
  output_price: 20,
  cache_read_price: 0.4,
  cache_write_price: 5,
  image_price: 2,
  audio_input_price: 0.8,
  audio_output_price: 1.6,
  // These are billing metadata, deliberately unrelated to the USD quote.
  model_ratio: 9999,
  completion_ratio: 888,
  enable_groups: ['cheap', 'premium'],
  group_ratio: groups,
  runtime_state: {
    status: 'available',
    source: 'routing_configuration',
    observed_at: 1_791_158_400,
  },
}

function PricingFaces(props: {
  currency: PriceDisplayCurrency
  tokenUnit: TokenUnit
  quotedModel?: PricingModel
}) {
  const [selectedGroup, setSelectedGroup] = useState('all')
  const quotedModel = props.quotedModel ?? model
  const models = filterByGroup(
    [
      quotedModel,
      { ...quotedModel, id: 2, model_name: 'second-base-quote-model' },
      {
        ...quotedModel,
        id: 3,
        model_name: 'cheap-only-model',
        enable_groups: ['cheap'],
      },
    ],
    selectedGroup
  )
  const displayProps = {
    displayCurrency: props.currency,
    tokenUnit: props.tokenUnit,
    selectedGroup,
  }
  return (
    <>
      <nav aria-label='Availability group filter'>
        {['all', 'cheap', 'premium'].map((group) => (
          <button
            key={group}
            type='button'
            onClick={() => setSelectedGroup(group)}
          >
            {group}
          </button>
        ))}
      </nav>
      <output aria-label='Available model count'>{models.length}</output>
      <section aria-label='Catalog card'>
        <ModelCard model={quotedModel} onClick={() => {}} {...displayProps} />
      </section>
      <section aria-label='Catalog table'>
        <PricingTable models={models} {...displayProps} />
      </section>
      <section aria-label='Public model details'>
        <ModelDetailsContent
          model={quotedModel}
          groupRatio={groups}
          usableGroup={usableGroup}
          endpointMap={{}}
          autoGroups={[]}
          {...displayProps}
        />
      </section>
      <ModelCompareDialog
        open
        onOpenChange={() => {}}
        models={models.slice(0, 2)}
        onRemove={() => {}}
        onViewDetails={() => {}}
        {...displayProps}
      />
    </>
  )
}

const mounted: Array<{
  container: HTMLDivElement
  root: ReturnType<typeof createRoot>
  queryClient: InstanceType<typeof QueryClient>
}> = []

async function renderFaces(
  currency: PriceDisplayCurrency,
  tokenUnit: TokenUnit = 'M',
  quotedModel?: PricingModel
) {
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
          <TooltipProvider>
            <PricingFaces
              currency={currency}
              tokenUnit={tokenUnit}
              quotedModel={quotedModel}
            />
          </TooltipProvider>
        </I18nextProvider>
      </QueryClientProvider>
    )
  })
  return container
}

function section(container: HTMLElement, label: string) {
  const found = container.querySelector<HTMLElement>(
    `section[aria-label="${label}"]`
  )
  assert.ok(found, `missing ${label}`)
  return found
}

function compareDialog() {
  const found = document.querySelector<HTMLElement>('[role="dialog"]')
  assert.ok(found, 'missing actual compare dialog')
  return found
}

function assertQuote(container: HTMLElement, quote: string) {
  const escaped = quote.replaceAll(/[.*+?^${}()|[\]\\]/g, '\\$&')
  assert.match(
    container.textContent ?? '',
    new RegExp(`(?:^|[^\\d.])${escaped}(?![\\d.])`)
  )
}

async function selectGroup(container: HTMLElement, group: string) {
  const button = [
    ...container.querySelectorAll<HTMLButtonElement>('nav button'),
  ].find((item) => item.textContent === group)
  assert.ok(button)
  await act(async () => button.click())
}

beforeEach(async () => {
  useAuthStore.getState().auth.reset()
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...originalConfig.currency,
      currencyUnit: 'credit',
      creditsPerUsd: 500_000,
      creditsPerUsdExact: '500000',
      cnyPerUsd: 7,
      cnyPerUsdExact: '7',
      legacyPricingUnitsPerUsd: 1,
    },
  })
  api.get = (async (url: string) => {
    assert.equal(url, '/api/perf-metrics', 'unexpected API read in render test')
    return { data: { success: true, data: { groups: [] } } }
  }) as typeof api.get
  api.post = (async () => {
    throw new Error('Unexpected API write in public pricing render test')
  }) as typeof api.post
  await i18n.changeLanguage('en')
})

afterEach(async () => {
  for (const item of mounted.splice(0)) {
    await act(async () => item.root.unmount())
    item.queryClient.clear()
    item.container.remove()
  }
  api.get = originalGet
  api.post = originalPost
  useSystemConfigStore.getState().setConfig(originalConfig)
  useAuthStore.setState({ auth: originalAuth })
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

for (const {
  currency,
  input,
  output,
  cache,
  estimate,
  perRequest,
  perMonth,
} of [
  {
    currency: 'USD',
    input: '4 USD',
    output: '20 USD',
    cache: '0.4 USD',
    estimate: '0.08 USD',
    perRequest: '0.018 USD',
    perMonth: '54 USD',
  },
  {
    currency: 'CNY',
    input: '28 CNY',
    output: '140 CNY',
    cache: '2.8 CNY',
    estimate: '0.56 CNY',
    perRequest: '0.126 CNY',
    perMonth: '378 CNY',
  },
  {
    currency: 'CREDIT',
    input: '2,000,000 Credits',
    output: '10,000,000 Credits',
    cache: '200,000 Credits',
    estimate: '40,000 Credits',
    perRequest: '9,000 Credits',
    perMonth: '27,000,000 Credits',
  },
] as const) {
  test(`public ${currency} quotes stay at base rates while groups only filter availability`, async () => {
    const container = await renderFaces(currency)
    for (const group of ['all', 'cheap', 'premium']) {
      await selectGroup(container, group)
      assert.equal(
        container.querySelector('output')?.textContent,
        group === 'premium' ? '2' : '3'
      )
      for (const face of [
        section(container, 'Catalog card'),
        section(container, 'Catalog table'),
        section(container, 'Public model details'),
        compareDialog(),
      ]) {
        assertQuote(face, input)
        assertQuote(face, output)
        assertQuote(face, cache)
      }
      const details = section(container, 'Public model details')
      const estimateSection = [...details.querySelectorAll('h3')]
        .find((heading) => heading.textContent === 'Request cost estimate')
        ?.closest('section')
      assert.ok(estimateSection)
      assert.equal(estimateSection.querySelector('select'), null)
      assert.equal(
        estimateSection.querySelector('[aria-live]')?.textContent,
        estimate
      )
      assert.doesNotMatch(
        details.textContent ?? '',
        /\b(?:0\.13|2)x\b|selected group multiplier/
      )
      assertQuote(compareDialog(), perRequest)
      assertQuote(compareDialog(), perMonth)
    }
  })
}

test('public tier prices retain cache TTL, audio and per-minute units at base rates', async () => {
  const tieredModel: PricingModel = {
    ...model,
    billing_mode: 'tiered_expr',
    billing_expr:
      'len < 200000 ? tier("short", p*4+c*20+cr*0.4+cc*5+cc1h*8+ai*0.8+ao*1.6+audio_s*100) : tier("long", p*8+c*40+cr*0.8+cc*10+cc1h*16+ai*1.6+ao*3.2+audio_s*200)',
  }
  const container = await renderFaces('CNY', 'K', tieredModel)
  for (const group of ['cheap', 'premium']) {
    await selectGroup(container, group)
    for (const face of [
      section(container, 'Catalog card'),
      section(container, 'Catalog table'),
    ]) {
      assertQuote(face, '0.028 CNY')
      assertQuote(face, '0.14 CNY')
      assertQuote(face, '0.042 CNY')
      assert.match(face.textContent ?? '', /minute/)
    }
    assertQuote(section(container, 'Catalog table'), '0.0028 CNY')
    const details = section(container, 'Public model details')
    for (const quote of [
      '0.028 CNY',
      '0.056 CNY',
      '0.14 CNY',
      '0.28 CNY',
      '0.035 CNY',
      '0.056 CNY',
      '0.112 CNY',
      '0.0056 CNY',
      '0.0112 CNY',
      '0.042 CNY',
      '0.084 CNY',
    ]) {
      assertQuote(details, quote)
    }
    assert.match(details.textContent ?? '', /short/)
    assert.match(details.textContent ?? '', /long/)
    assert.match(details.textContent ?? '', /1K/)
    assert.match(details.textContent ?? '', /minute/)
    assert.match(details.textContent ?? '', /1h|1 hour/i)
  }
})
