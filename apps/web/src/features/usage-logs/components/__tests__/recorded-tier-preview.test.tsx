/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import {
  flexRender,
  getCoreRowModel,
  useReactTable,
} from '@tanstack/react-table'
import { Window } from 'happy-dom'

import en from '@/i18n/locales/en.json'
import zh from '@/i18n/locales/zh.json'

import { usageLogSchema, type UsageLog } from '../../data/schema'

const domWindow = new Window({ url: 'http://localhost/' })
domWindow.document.write('<!doctype html><html><body></body></html>')
for (const key of [
  'window',
  'document',
  'navigator',
  'localStorage',
  'HTMLElement',
  'HTMLButtonElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
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
Object.defineProperty(globalThis, 'IS_REACT_ACT_ENVIRONMENT', {
  configurable: true,
  value: true,
})

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { useCommonLogsColumns } = await import('../columns/common-logs-columns')
const { DEFAULT_CURRENCY_CONFIG, useSystemConfigStore } =
  await import('@/stores/system-config-store')
const originalConfig = useSystemConfigStore.getState().config
const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  fallbackLng: 'en',
  interpolation: { escapeValue: false },
  resources: { en, zh },
})

let rendered:
  | { root: ReturnType<typeof createRoot>; container: HTMLDivElement }
  | undefined

afterEach(async () => {
  if (rendered) {
    await act(async () => rendered?.root.unmount())
    rendered.container.remove()
    rendered = undefined
  }
  useSystemConfigStore.setState({ config: originalConfig })
  document.body.replaceChildren()
})
after(() => domWindow.close())

function usageLog(other: unknown, overrides: Partial<UsageLog> = {}): UsageLog {
  return usageLogSchema.parse({
    id: 1,
    user_id: 1,
    created_at: 1,
    type: 2,
    content: '',
    model_name: 'recorded-tier-test',
    quota: 3_500_000,
    other: JSON.stringify(other),
    ...overrides,
  })
}

function PreviewHarness({ log }: { log: UsageLog }) {
  const columns = useCommonLogsColumns(false).filter(
    (column) =>
      'accessorKey' in column &&
      ['content', 'quota'].includes(String(column.accessorKey))
  )
  const table = useReactTable({
    data: [log],
    columns,
    getCoreRowModel: getCoreRowModel(),
  })
  return table
    .getRowModel()
    .rows[0].getVisibleCells()
    .map((cell) => (
      <section key={cell.id} data-column={cell.column.id}>
        {flexRender(cell.column.columnDef.cell, cell.getContext())}
      </section>
    ))
}

async function mount(log: UsageLog, language = 'en') {
  await act(async () => i18n.changeLanguage(language))
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  rendered = { root, container }
  await act(async () =>
    root.render(
      <I18nextProvider i18n={i18n}>
        <PreviewHarness log={log} />
      </I18nextProvider>
    )
  )
  const preview = container.querySelector('[data-column=content] button')
  assert.ok(preview, 'Expected the real details-column preview button')
  return { preview, container }
}

const encode = (expression: string) =>
  Buffer.from(expression).toString('base64')
const nestedExpression = `len <= 272000
  ? (c > 5
    ? tier("standard", p * 2 + c * 10 + cr * 0.1 + cc * 2.5)
    : tier("free_small_output", fixed(0)))
  : (c > 5
    ? tier("long_context", p * 5 + c * 22.5 + cr * 0.5)
    : tier("free_small_output", fixed(0)))`

const failures = [
  { name: 'missing expression', expr: undefined },
  { name: 'invalid base64', expr: '%' },
  { name: 'invalid decoded expression', expr: encode('tier(') },
  {
    name: 'recorded tier absent from expression',
    expr: encode('tier("different", p * 99 + c * 99)'),
  },
  {
    name: 'unsupported task expression without pricing schema',
    expr: encode('tier("recorded", u("clips") * 0.25)'),
  },
]

for (const language of ['en', 'zh']) {
  for (const isTask of [false, true]) {
    for (const { name, expr } of failures) {
      test(`${language}: ${name} preserves recorded tier, task=${isTask}`, async () => {
        const { preview } = await mount(
          usageLog({
            is_task: isTask,
            billing_mode: 'tiered_expr',
            matched_tier: 'recorded',
            expr_b64: expr,
          }),
          language
        )
        assert.equal(
          preview.textContent,
          `${language === 'zh' ? '阶梯计费' : 'Tiered pricing'} · recorded`
        )
        assert.doesNotMatch(
          preview.textContent ?? '',
          /99|USD|\/M|无匹配|No matching/
        )
      })
    }

    for (const matchedTier of [undefined, '']) {
      test(`${language}: missing/empty tier uses neutral copy, task=${isTask}, tier=${matchedTier}`, async () => {
        const { preview } = await mount(
          usageLog({
            is_task: isTask,
            billing_mode: 'tiered_expr',
            matched_tier: matchedTier,
            expr_b64: encode('tier("base", p * 2 + c * 10)'),
          }),
          language
        )
        assert.equal(
          preview.textContent,
          language === 'zh'
            ? '阶梯计费 · 特殊计费表达式'
            : 'Tiered pricing · Special billing expression'
        )
      })
    }
  }
}

for (const matchedTier of ['standard', 'long_context', 'free_small_output']) {
  test(`nested expression retains ${matchedTier} without guessing unavailable prices`, async () => {
    const { preview } = await mount(
      usageLog({
        billing_mode: 'tiered_expr',
        matched_tier: matchedTier,
        expr_b64: encode(nestedExpression),
      })
    )
    // The local parser supports nested token tiers (unlike the upstream
    // fixture), but cannot summarize fixed(0). Neither path may invent USD.
    assert.equal(
      preview.textContent,
      matchedTier === 'free_small_output'
        ? `Tiered pricing · ${matchedTier}`
        : `${matchedTier} · - / -/M`
    )
    assert.doesNotMatch(preview.textContent ?? '', /USD|\$|No matching/)
  })
}

test('a supported nested expression still displays only the recorded tier prices', async () => {
  const { preview } = await mount(
    usageLog({
      billing_mode: 'tiered_expr',
      matched_tier: 'long_context',
      expr_b64: encode(
        'len <= 272000 ? (c > 5 ? tier("standard", p * 2 + c * 10) : tier("free", p * 0)) : tier("long_context", p * 5 + c * 22.5)'
      ),
      pricing_schema_version: 2,
      billing_expr_currency_basis: 'USD',
    })
  )
  assert.equal(preview.textContent, 'long_context · 5 USD / 22.5 USD/M')
})

test('parsed historical tier prices without a frozen money basis remain unavailable', async () => {
  useSystemConfigStore.setState({
    config: {
      ...originalConfig,
      currency: {
        ...DEFAULT_CURRENCY_CONFIG,
        creditsPerUsd: 1,
        creditsPerUsdExact: '1',
      },
    },
  })
  const { preview, container } = await mount(
    usageLog({
      billing_mode: 'tiered_expr',
      matched_tier: 'standard',
      expr_b64: encode('tier("standard", p * 2 + c * 10)'),
    })
  )
  assert.equal(preview.textContent, 'standard · - / -/M')
  assert.equal(
    container.querySelector('[data-column=quota]')?.textContent,
    '3,500,000 Raw credits'
  )
})

for (const moneyAvailable of [false, true]) {
  test(`recorded tier fallback does not change explicit historical money availability=${moneyAvailable}`, async () => {
    const { preview, container } = await mount(
      usageLog(
        {
          billing_mode: 'tiered_expr',
          matched_tier: 'recorded',
          pricing_credits_per_usd: 3_500_000,
        },
        moneyAvailable
          ? {
              quota_money_available: true,
              quota_money_basis: 'captured_credits_per_usd',
              quota_usd: '1',
            }
          : {
              quota_money_available: false,
              quota_money_basis: 'missing_credit_basis',
            }
      )
    )
    assert.equal(preview.textContent, 'Tiered pricing · recorded')
    assert.equal(
      container.querySelector('[data-column=quota]')?.textContent,
      moneyAvailable ? '1 USD' : '3,500,000 Raw credits'
    )
  })
}
