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
import { after, test } from 'node:test'

import type { Table } from '@tanstack/react-table'
import { Window } from 'happy-dom'

const dom = new Window({ url: 'http://localhost/' })
for (const key of [
  'window', 'document', 'navigator', 'HTMLElement', 'HTMLInputElement',
  'HTMLButtonElement', 'Node', 'Element', 'Event', 'MouseEvent',
  'MutationObserver', 'ResizeObserver', 'requestAnimationFrame',
  'cancelAnimationFrame',
] as const) {
  Object.defineProperty(globalThis, key, { configurable: true, value: dom[key] })
}
Object.defineProperty(globalThis, 'getComputedStyle', {
  configurable: true, value: dom.getComputedStyle.bind(dom),
})
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean })
  .IS_REACT_ACT_ENVIRONMENT = true
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider } = await import('react-i18next')
const { getCoreRowModel, useReactTable } = await import('@tanstack/react-table')
const { DataTableToolbar } = await import('./toolbar')
const { getFilterValues, removeFilterValue } = await import('./filter-summary')
const { Tabs, TabsList, TabsTrigger, TabsContent } = await import('@/components/ui/tabs')
const { LogsFilterToolbar } = await import('@/features/usage-logs/components/logs-filter-toolbar')
const i18n = createInstance()
await i18n.init({ lng: 'en', resources: { en: { translation: {} } }, keySeparator: false })
after(() => dom.happyDOM.abort())

type Row = { name: string; status: string }
const data: Row[] = [{ name: 'Example', status: 'enabled' }]
const columns = [{ accessorKey: 'name' }, { accessorKey: 'status' }]
const filters = [{ columnId: 'status', title: 'Status', options: [
  { label: 'Enabled', value: 'enabled' }, { label: 'Disabled', value: 'disabled' },
] }]

async function mount(mode: 'table' | 'logs' | 'tabs' = 'table') {
  let table!: Table<Row>
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  function Harness() {
    table = useReactTable({ data, columns, getCoreRowModel: getCoreRowModel() })
    if (mode === 'logs') return <LogsFilterToolbar table={table}
      primaryFilters={<input aria-label='Primary draft' defaultValue='primary' />}
      advancedFilters={<input aria-label='Log draft' defaultValue='unsaved' />}
      hasActiveFilters={false} onReset={() => undefined} onSearch={() => undefined} />
    if (mode === 'tabs') return <Tabs orientation='vertical' defaultValue='first'>
      <TabsList aria-label='Sections'>
        <TabsTrigger value='first'>First</TabsTrigger>
        <TabsTrigger value='second'>Second</TabsTrigger>
      </TabsList>
      <TabsContent value='first' keepMounted><input defaultValue='draft' /></TabsContent>
      <TabsContent value='second' keepMounted>Second panel</TabsContent>
    </Tabs>
    return <DataTableToolbar table={table} filters={filters} searchKey='name'
      hideViewOptions additionalSearch={<input aria-label='Custom draft' defaultValue='unsaved' />} />
  }
  await act(async () => root.render(<I18nextProvider i18n={i18n}><Harness /></I18nextProvider>))
  if (mode === 'table') {
    await act(async () => table.setColumnFilters([
      { id: 'name', value: 'Example' },
      { id: 'status', value: ['enabled', 'disabled'] },
    ]))
  }
  return { host, getTable: () => table, cleanup: async () => {
    await act(async () => root.unmount())
    host.remove()
  } }
}

function button(host: HTMLElement, text: string) {
  const found = [...host.querySelectorAll<HTMLButtonElement>('button')]
    .find((entry) => entry.textContent?.trim() === text)
  assert.ok(found, `Button missing: ${text}`)
  return found
}

test('normalizes scalar/array filters and safely ignores malformed values', () => {
  assert.deepEqual(getFilterValues('enabled'), ['enabled'])
  assert.deepEqual(getFilterValues([false, 0, 'enabled', null, {}, Number.NaN]), [false, 0, 'enabled'])
  assert.deepEqual(getFilterValues({ invalid: true }), [])
  assert.equal(removeFilterValue('enabled', 'enabled'), undefined)
  assert.deepEqual(removeFilterValue(['enabled', 'disabled'], 'enabled'), ['disabled'])
})

test('active conditions stay visible while the filter panel is collapsed', async () => {
  const view = await mount()
  try {
    assert.equal(view.host.querySelector<HTMLElement>('[role=region]')?.hidden, true)
    const chips = view.host.querySelectorAll('[aria-label="Filters active"] button')
    assert.equal(chips.length, 2)
    assert.ok(chips[0].textContent?.includes('Enabled'))
    assert.ok(view.host.querySelector('input[aria-label="Filter..."]'))
  } finally { await view.cleanup() }
})

test('external updates and Reset refresh facet labels with stable column identity', async () => {
  const view = await mount()
  try {
    const column = view.getTable().getColumn('status')!
    await act(async () => column.setFilterValue(['disabled']))
    assert.equal(view.getTable().getColumn('status'), column)
    const trigger = view.host.querySelector('[aria-label="Status"]')!
    assert.ok(trigger.textContent?.includes('Disabled'))
    assert.equal(trigger.textContent?.includes('Enabled'), false)
    await act(async () => button(view.host, 'Reset').click())
    assert.equal(view.host.querySelector('[aria-label="Filters active"]'), null)
    assert.equal(trigger.textContent?.trim(), 'Status')
  } finally { await view.cleanup() }
})

test('clearing one chip preserves other conditions and restores keyboard focus', async () => {
  const view = await mount()
  try {
    const clear = view.host.querySelector<HTMLButtonElement>('[aria-label="Clear filters: Status: Enabled"]')!
    await act(async () => clear.click())
    assert.deepEqual(view.getTable().getColumn('status')?.getFilterValue(), ['disabled'])
    assert.equal(view.getTable().getColumn('name')?.getFilterValue(), 'Example')
    assert.equal(view.host.querySelector<HTMLInputElement>('[aria-label="Custom draft"]')?.value, 'unsaved')
    assert.equal(document.activeElement, view.host.querySelector('button[aria-controls]'))
  } finally { await view.cleanup() }
})

test('log advanced fields remain mounted when closed and reopened', async () => {
  const view = await mount('logs')
  try {
    const input = view.host.querySelector<HTMLInputElement>('[aria-label="Log draft"]')!
    assert.ok(input)
    await act(async () => button(view.host, 'Expand').click())
    input.value = 'keep this unsubmitted value'
    await act(async () => button(view.host, 'Collapse').click())
    assert.equal(view.host.querySelector('[aria-label="Log draft"]'), input)
    assert.equal(input.parentElement?.hidden, true)
    await act(async () => button(view.host, 'Expand').click())
    assert.equal(input.value, 'keep this unsubmitted value')
  } finally { await view.cleanup() }
})

test('vertical tabs expose the correct orientation and keep inactive drafts', async () => {
  const view = await mount('tabs')
  try {
    assert.equal(view.host.querySelector('[role=tablist]')?.getAttribute('aria-orientation'), 'vertical')
    const input = view.host.querySelector('input')!
    input.value = 'retained value'
    await act(async () => button(view.host, 'Second').click())
    assert.equal(input.closest('[role=tabpanel]')?.hasAttribute('hidden'), true)
    await act(async () => button(view.host, 'First').click())
    assert.equal(view.host.querySelector('input'), input)
    assert.equal(input.value, 'retained value')
  } finally { await view.cleanup() }
})

test('low-balance SMS notice renders in every supported interface language', async () => {
  const { renderToStaticMarkup } = await import('react-dom/server')
  const { INTERFACE_LANGUAGE_OPTIONS } = await import('@/i18n/languages')
  const { SmsBalanceNotice } = await import('@/features/email-activations/sms-balance-notice')
  try {
    for (const code of [...INTERFACE_LANGUAGE_OPTIONS.map((item) => item.code), 'invalid_locale']) {
      await i18n.changeLanguage(code)
      const html = renderToStaticMarkup(<I18nextProvider i18n={i18n}>
        <SmsBalanceNotice status='below-minimum' balanceUSD={1}
          isLoading={false} isRefreshing={false} onRefresh={() => undefined} />
      </I18nextProvider>)
      assert.ok(html.includes('role="status"'), code)
      assert.ok(html.includes('Existing orders can still'), code)
    }
  } finally { await i18n.changeLanguage('en') }
})
