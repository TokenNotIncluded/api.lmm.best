/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import {
  flexRender,
  getCoreRowModel,
  useReactTable,
} from '@tanstack/react-table'
import { Window } from 'happy-dom'
import type { ReactNode } from 'react'

import { usageLogSchema, type UsageLog } from '../../data/schema'
import { formatModelName } from '../../lib/format'

const domWindow = new Window()
domWindow.document.write('<!doctype html><html><body></body></html>')
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
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
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { useCommonLogsColumns } = await import('../columns/common-logs-columns')
const { UsageLogsMobileList } = await import('../usage-logs-mobile-card')
const { DetailsDialog } = await import('../dialogs/details-dialog')
const { ModelBadge, ResponseModelDetails } = await import('../model-badge')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  fallbackLng: 'en',
  interpolation: { escapeValue: false },
  resources: { en: { translation: {} } },
})
;(
  globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
).IS_REACT_ACT_ENVIRONMENT = true

function usageLog(other: unknown): UsageLog {
  return usageLogSchema.parse({
    id: 1,
    user_id: 1,
    created_at: 1,
    type: 2,
    content: '',
    model_name: 'requested',
    other: JSON.stringify(other),
  })
}

function ListHarness({ log }: { log: UsageLog }) {
  const columns = useCommonLogsColumns(false).filter(
    (column) => 'accessorKey' in column && column.accessorKey === 'model_name'
  )
  const table = useReactTable({
    data: [log],
    columns,
    getCoreRowModel: getCoreRowModel(),
  })
  const modelCell = table.getRowModel().rows[0].getVisibleCells()[0]
  return (
    <>
      <section data-view='desktop'>
        {flexRender(modelCell.column.columnDef.cell, modelCell.getContext())}
      </section>
      <section data-view='mobile'>
        <UsageLogsMobileList table={table} logCategory='common' />
      </section>
    </>
  )
}

async function mount(children: ReactNode) {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(<I18nextProvider i18n={i18n}>{children}</I18nextProvider>)
  })
  return {
    container,
    async unmount() {
      await act(async () => root.unmount())
      container.remove()
    },
  }
}

after(() => domWindow.close())

test('desktop, mobile and details show the same dynamic warning and three names', async () => {
  const log = usageLog({
    is_model_mapped: true,
    upstream_model_name: 'selected',
    response_model: {
      requested_model: 'requested',
      upstream_model: 'selected',
      returned_model: 'returned',
      mismatch: false,
    },
  })
  const rendered = await mount(<ListHarness log={log} />)
  try {
    for (const view of ['desktop', 'mobile']) {
      const scope = rendered.container.querySelector(`[data-view=${view}]`)
      assert.ok(scope)
      const warning = scope.querySelector('[data-response-model-warning]')
      assert.ok(warning)
      assert.equal(
        warning.getAttribute('aria-label'),
        'Response model mismatch'
      )
      assert.equal(warning.textContent, 'Response model: returned')
      assert.ok(scope.textContent?.includes('requested'))
    }

    const trigger = rendered.container.querySelector('button')
    assert.ok(trigger)
    await act(async () => {
      trigger.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const popover = document.querySelector('[data-slot=popover-content]')
    assert.ok(popover)
    for (const name of ['requested', 'selected', 'returned']) {
      assert.ok(popover.textContent?.includes(name))
    }
    assert.ok(popover.querySelector('[data-response-model-warning]'))
    const copied: string[] = []
    const originalWriteText = navigator.clipboard.writeText
    navigator.clipboard.writeText = async (value) => {
      copied.push(value)
    }
    try {
      for (const label of [
        'Request Model',
        'Upstream Model',
        'Response Model',
      ]) {
        const copyButton: HTMLButtonElement | null = popover.querySelector(
          `button[aria-label="Copy to clipboard (${label})"]`
        )
        assert.ok(copyButton)
        await act(async () => {
          copyButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
        })
      }
      assert.deepEqual(copied, ['requested', 'selected', 'returned'])
    } finally {
      navigator.clipboard.writeText = originalWriteText
    }
  } finally {
    await rendered.unmount()
  }

  const details = await mount(
    <DetailsDialog log={log} isAdmin={false} open onOpenChange={() => {}} />
  )
  try {
    const dialog = document.querySelector('[role=dialog]')
    assert.ok(dialog)
    assert.equal(
      dialog.querySelectorAll('[data-response-model-warning]').length,
      1
    )
    for (const name of ['requested', 'selected', 'returned']) {
      assert.ok(dialog.textContent?.includes(name))
    }
  } finally {
    await details.unmount()
  }
})

test('compatible snapshots and aliases show their raw returned name without a warning', async () => {
  for (const returned of [
    'requested',
    'REQUESTED',
    'vendor/requested',
    'requested-2026-10-03',
    'requested-latest',
    'requested-preview-10-2026',
  ]) {
    const response = {
      requested_model: 'requested',
      upstream_model: 'requested',
      returned_model: returned,
      mismatch: true,
    }
    const info = formatModelName(usageLog({ response_model: response }))
    assert.ok(info.responseModel)
    const rendered = await mount(
      <>
        <ModelBadge modelName={info.name} responseModel={info.responseModel} />
        <ResponseModelDetails observation={info.responseModel} />
      </>
    )
    try {
      assert.equal(
        rendered.container.querySelector('[data-response-model-warning]'),
        null
      )
      assert.ok(rendered.container.textContent?.includes(returned))
    } finally {
      await rendered.unmount()
    }
  }
})

test('historical rows preserve mapping details and do not invent a returned model', async () => {
  for (const other of [
    {},
    { is_model_mapped: true, upstream_model_name: 'selected' },
    {
      is_model_mapped: true,
      upstream_model_name: 'selected',
      response_model: { returned_model: 42, mismatch: true },
    },
  ]) {
    const rendered = await mount(<ListHarness log={usageLog(other)} />)
    try {
      assert.equal(
        rendered.container.querySelector('[data-response-model-warning]'),
        null
      )
      const trigger = rendered.container.querySelector('button')
      if ('is_model_mapped' in other) {
        assert.ok(trigger)
        await act(async () => {
          trigger.dispatchEvent(new MouseEvent('click', { bubbles: true }))
        })
        const popover = document.querySelector('[data-slot=popover-content]')
        assert.ok(popover)
        assert.ok(popover.textContent?.includes('selected'))
        assert.ok(popover.textContent?.includes('Actual Model:'))
        assert.ok(!popover.textContent?.includes('Response Model'))
      } else {
        assert.equal(trigger, null)
      }
    } finally {
      await rendered.unmount()
    }
  }
})

test('an absent selected name remains visibly absent and cannot copy the request name', async () => {
  const rendered = await mount(
    <ResponseModelDetails
      observation={{
        requested_model: 'requested',
        upstream_model: '',
        returned_model: 'different',
      }}
    />
  )
  try {
    const label = Array.from(rendered.container.querySelectorAll('span')).find(
      (element) => element.textContent === 'Upstream Model'
    )
    assert.ok(label)
    assert.equal(label.nextElementSibling?.textContent, '—')
    assert.equal(label.parentElement?.querySelector('button'), null)
    assert.equal(rendered.container.querySelectorAll('button').length, 2)
    assert.ok(rendered.container.querySelector('[data-response-model-warning]'))
  } finally {
    await rendered.unmount()
  }
})

test('long provider model names remain complete in the warning and details', async () => {
  const returned = `provider/${'long-model-name-'.repeat(20)}different`
  const response = {
    requested_model: 'requested',
    upstream_model: 'selected',
    returned_model: returned,
  }
  const rendered = await mount(<ResponseModelDetails observation={response} />)
  try {
    assert.equal(
      rendered.container.querySelector('[data-response-model-warning]')
        ?.textContent,
      `Response model: ${returned}`
    )
    assert.ok(rendered.container.textContent?.includes(returned))
    assert.equal(rendered.container.querySelector('.truncate'), null)
  } finally {
    await rendered.unmount()
  }
})
