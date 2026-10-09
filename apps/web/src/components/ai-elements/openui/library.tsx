/* Copyright (C) 2026 LIghtJUNction; licensed under AGPL-3.0-or-later. */
import { createLibrary, defineComponent } from '@openuidev/react-lang'
import { z } from 'zod/v4'

import { OPENUI_PAGES, resolveOpenUIPage } from './policy'
import { OpenUIDataTable } from './table'

const shortText = z.string().max(160)
const metricSchema = z.object({
  label: shortText,
  value: shortText,
  detail: z.string().max(320),
})
const chartSchema = z.object({
  title: shortText,
  items: z
    .array(
      z.object({
        label: shortText,
        value: z.number().finite().nonnegative().max(1e15),
      })
    )
    .min(1)
    .max(24),
  unit: z.string().max(40),
})
const tableSchema = z.object({
  title: shortText,
  columns: z.array(shortText).min(1).max(8),
  rows: z
    .array(z.array(z.union([z.string().max(320), z.number().finite()])).max(8))
    .max(32),
})
const Metric = defineComponent({
  name: 'Metric',
  description:
    'One verified metric. Use an exact value and unit from a live tool, with its time window or source in detail.',
  props: metricSchema,
  component: ({ props }) => {
    const result = metricSchema.safeParse(props)
    if (!result.success) return null
    return (
      <dl className='bg-muted/40 min-w-0 rounded-xl p-4'>
        <dt className='text-muted-foreground text-sm'>{result.data.label}</dt>
        <dd className='text-foreground mt-2 text-2xl font-semibold break-words tabular-nums'>
          {result.data.value}
        </dd>
        <dd className='text-muted-foreground mt-2 text-xs leading-5'>
          {result.data.detail}
        </dd>
      </dl>
    )
  },
})
const BarChart = defineComponent({
  name: 'BarChart',
  description:
    'Compare up to 24 nonnegative values from one live tool, with a shared unit. Do not invent missing data or mix units.',
  props: chartSchema,
  component: ({ props }) => {
    const result = chartSchema.safeParse(props)
    if (!result.success) return null
    const { title, items, unit } = result.data
    const maximum = Math.max(...items.map((item) => item.value)) || 1
    return (
      <figure className='min-w-0 space-y-4'>
        <figcaption className='text-foreground text-sm font-medium'>
          {title}
        </figcaption>
        <ul className='space-y-3'>
          {items.map((item, index) => (
            <li key={index} className='space-y-1.5'>
              <div className='flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 text-xs'>
                <span className='min-w-0 break-words'>{item.label}</span>
                <span className='text-muted-foreground shrink-0 tabular-nums'>
                  {item.value} {unit}
                </span>
              </div>
              <div
                className='bg-muted h-2 overflow-hidden rounded-full'
                aria-hidden='true'
              >
                <div
                  className='bg-primary h-full rounded-full'
                  style={{ width: `${(item.value / maximum) * 100}%` }}
                />
              </div>
            </li>
          ))}
        </ul>
      </figure>
    )
  },
})
const DataTable = defineComponent({
  name: 'DataTable',
  description:
    'A locally sortable read-only table. Each row must have the same number of cells as columns. Keep all labels in the user language.',
  props: tableSchema,
  component: ({ props }) => {
    const result = tableSchema.safeParse(props)
    if (!result.success) return null
    return <OpenUIDataTable {...result.data} />
  },
})
const ConsoleLink = defineComponent({
  name: 'ConsoleLink',
  description:
    'An explicit user-clicked link to an existing console page. It does not create keys, pay, install tools, or authorize changes.',
  props: z.object({ page: z.enum(OPENUI_PAGES), label: shortText }),
  component: ({ props }) => {
    const href = resolveOpenUIPage(props.page)
    if (!href || typeof props.label !== 'string') return null
    return (
      <a
        href={href}
        className='text-primary focus-visible:ring-ring inline-flex min-h-11 items-center gap-2 rounded-sm text-sm font-medium underline-offset-4 hover:underline focus-visible:ring-2 focus-visible:outline-none'
      >
        {props.label}
        <span aria-hidden='true'>↗</span>
      </a>
    )
  },
})
const Stack = defineComponent({
  name: 'Stack',
  description:
    'The root layout. Use a short focused view, with at most 12 components.',
  props: z.object({
    children: z
      .array(
        z.union([Metric.ref, BarChart.ref, DataTable.ref, ConsoleLink.ref])
      )
      .max(12),
  }),
  component: ({ props, renderNode }) => (
    <div className='grid min-w-0 gap-5'>
      {renderNode(
        Array.isArray(props.children) ? props.children.slice(0, 12) : []
      )}
    </div>
  ),
})
export const assistantOpenUILibrary = createLibrary({
  root: 'Stack',
  components: [Stack, Metric, BarChart, DataTable, ConsoleLink],
})
export function assistantOpenUIPrompt(): string {
  return `${assistantOpenUILibrary.prompt({
    preamble:
      'Optional LMM Generative UI. Answer ordinary questions in Markdown. When a compact interactive view helps explain verified tool results, first provide a useful Markdown explanation, then ONE fenced code block whose language is openui. Apply the following OpenUI syntax instructions ONLY inside that block. Keep the explanation useful if the UI cannot render.',
    toolCalls: false,
    bindings: false,
    additionalRules: [
      'Use only the registered components. Begin the program with root = Stack(...). Limit the block to 16000 characters and 128 lines.',
      'Use live tool results for all account values, prices, model IDs and usage. Show the source/time window and units. Never infer wallet balance from usage or subscription quota. Missing values are unknown, not zero.',
      'Never include credentials, passwords, private keys, cookies, account identifiers or invitation codes. The UI is display-only and is not a permission grant.',
      'Do not emit Query, Mutation, forms, scripts, HTML, external URLs or custom actions. Continue to use existing server tools and confirmation cards for all account writes and paid actions.',
      'ConsoleLink can only open an existing console page after a user click. Never claim that opening it completed a purchase, created a key, installed a tool or changed settings.',
      'Every table row must match its column count. Charts use one nonnegative numeric unit. Use the user language for labels. Do not format technical code examples as openui.',
    ],
  })}\n`
}
