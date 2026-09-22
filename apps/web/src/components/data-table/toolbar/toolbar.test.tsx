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

import { Window } from 'happy-dom'

const domWindow = new Window({ url: 'http://localhost/' })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLInputElement',
  'HTMLButtonElement',
  'HTMLDetailsElement',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
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
Object.defineProperty(globalThis, 'getComputedStyle', {
  configurable: true,
  value: domWindow.getComputedStyle.bind(domWindow),
})
;(
  globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
).IS_REACT_ACT_ENVIRONMENT = true
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider } = await import('react-i18next')
const i18n = createInstance()
await i18n.init({
  lng: 'en',
  resources: { en: { translation: {} } },
  keySeparator: false,
})
after(() => domWindow.happyDOM.abort())

const { getCoreRowModel, useReactTable } = await import('@tanstack/react-table')
const { DataTableToolbar } = await import('./toolbar')
const data = [{ name: 'Example', status: 'enabled' }]
const columns = [{ accessorKey: 'name' }, { accessorKey: 'status' }]

function Harness({
  active = false,
  onReset,
}: {
  active?: boolean
  onReset?: () => void
}) {
  const table = useReactTable({
    data,
    columns,
    getCoreRowModel: getCoreRowModel(),
  })
  return (
    <DataTableToolbar
      table={table}
      hideViewOptions
      hasExpandedActiveFilters={active}
      onReset={onReset}
      expandable={
        <input aria-label='Advanced value' defaultValue='unfinished draft' />
      }
      filters={[
        {
          columnId: 'status',
          title: 'Status',
          options: [{ label: 'Enabled', value: 'enabled' }],
        },
      ]}
    />
  )
}

test('secondary filters are discoverable and preserve input state when collapsed', async () => {
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () =>
      root.render(
        <I18nextProvider i18n={i18n}>
          <Harness />
        </I18nextProvider>
      )
    )
    const toggle = host.querySelector<HTMLButtonElement>(
      'button[aria-controls]'
    )!
    const panel = host.querySelector<HTMLElement>('[role=region]')!
    const input = host.querySelector<HTMLInputElement>(
      '[aria-label="Advanced value"]'
    )!
    assert.equal(toggle.getAttribute('aria-expanded'), 'false')
    assert.equal(toggle.getAttribute('aria-controls'), panel.id)
    assert.equal(panel.hidden, true)
    await act(async () => toggle.click())
    assert.equal(panel.hidden, false)
    input.value = 'preserve this draft'
    await act(async () => toggle.click())
    assert.equal(panel.hidden, true)
    assert.equal(host.querySelector('[aria-label="Advanced value"]'), input)
    await act(async () => toggle.click())
    assert.equal(input.value, 'preserve this draft')
  } finally {
    await act(async () => root.unmount())
    host.remove()
  }
})

test('hidden active custom filters keep their reset action reachable', async () => {
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  let resets = 0
  try {
    await act(async () =>
      root.render(
        <I18nextProvider i18n={i18n}>
          <Harness
            active
            onReset={() => {
              resets += 1
            }}
          />
        </I18nextProvider>
      )
    )
    const reset = Array.from(
      host.querySelectorAll<HTMLButtonElement>('button')
    ).find((button) => button.textContent?.includes('Reset'))
    assert.ok(reset)
    assert.equal(reset.disabled, false)
    await act(async () => reset.click())
    assert.equal(resets, 1)
  } finally {
    await act(async () => root.unmount())
    host.remove()
  }
})
