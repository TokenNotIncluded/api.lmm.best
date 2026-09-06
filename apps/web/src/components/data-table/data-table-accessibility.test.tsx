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

import type { Column, Table } from '@tanstack/react-table'
import { Window } from 'happy-dom'

const domWindow = new Window({ url: 'https://console.example.test/' })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLInputElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'KeyboardEvent',
  'MouseEvent',
  'PointerEvent',
  'FocusEvent',
  'MutationObserver',
  'ResizeObserver',
  'IntersectionObserver',
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
const { DataTablePagination } = await import('./core/pagination')
const { DataTableBulkActions } = await import('./toolbar/bulk-actions')
const { DataTableFacetedFilter } = await import('./toolbar/faceted-filter')

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        Pagination: 'Pagination',
        'Rows per page': 'Rows per page',
        'More pages': 'More pages',
        'Total:': 'Total:',
        'Go to first page': 'Go to first page',
        'Go to previous page': 'Go to previous page',
        'Go to next page': 'Go to next page',
        'Go to last page': 'Go to last page',
        'Go to page {{page}}': 'Go to page {{page}}',
        Filter: 'Filter',
        'No results found.': 'No results found.',
        'Clear filters': 'Clear filters',
        selected: 'selected',
        'Clear selection': 'Clear selection',
        'Clear selection (Escape)': 'Clear selection (Escape)',
      },
    },
  },
})

afterEach(() => {
  document.body.replaceChildren()
})

after(() => domWindow.close())

function createPaginationTable(): Table<unknown> {
  let pageSize = 20

  return {
    getState: () => ({ pagination: { pageIndex: 2, pageSize } }),
    getPageCount: () => 10,
    getRowCount: () => 100,
    getCanPreviousPage: () => true,
    getCanNextPage: () => true,
    setPageIndex: () => undefined,
    setPageSize: (nextPageSize: number) => {
      pageSize = nextPageSize
    },
    previousPage: () => undefined,
    nextPage: () => undefined,
  } as unknown as Table<unknown>
}

async function renderWithI18n(node: React.ReactNode) {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  await act(async () => {
    root.render(<I18nextProvider i18n={i18n}>{node}</I18nextProvider>)
  })

  return { container, root }
}

describe('data-table pagination accessibility', () => {
  test('exposes navigation, current page, omitted pages, and page-size labels', async () => {
    const rendered = await renderWithI18n(
      <DataTablePagination table={createPaginationTable()} />
    )

    const navigation = rendered.container.querySelector(
      '[role="navigation"][aria-label="Pagination"]'
    )
    assert.ok(navigation)
    assert.equal(
      navigation.classList.contains('overflow-x-auto'),
      true,
      'narrow pagination controls should remain horizontally reachable'
    )

    const currentPage = navigation.querySelector('button[aria-current="page"]')
    assert.ok(currentPage)
    assert.match(currentPage.textContent?.trim() ?? '', /3$/)

    const morePages = [...navigation.querySelectorAll('span.sr-only')].find(
      (element) => element.textContent === 'More pages'
    )
    assert.ok(morePages)

    const pageSizeTrigger = navigation.querySelector(
      '[data-slot="select-trigger"]'
    )
    assert.ok(pageSizeTrigger)
    assert.equal(pageSizeTrigger.getAttribute('aria-label'), 'Rows per page')
    assert.equal(
      pageSizeTrigger.classList.contains('@lg/pagination:h-8'),
      true,
      'wide pagination containers should keep the compact page-size control'
    )
    assert.equal(
      pageSizeTrigger.classList.contains('h-11'),
      true,
      'narrow pagination containers should expose a 44px page-size target'
    )

    const currentPageButton = navigation.querySelector(
      'button[aria-current="page"]'
    )
    assert.ok(currentPageButton)
    assert.equal(
      currentPageButton.classList.contains('h-11'),
      true,
      'narrow pagination containers should expose 44px page targets'
    )

    await act(async () => rendered.root.unmount())
  })
})

describe('data-table faceted filter accessibility', () => {
  test('keeps the trigger named when no title is supplied and preserves option counts', async () => {
    const filterColumn = {
      getFacetedUniqueValues: () =>
        new Map([
          ['active', 2],
          ['empty', 0],
        ]),
      getFilterValue: () => undefined,
      setFilterValue: () => undefined,
    } as unknown as Column<unknown, string>

    function FilterIcon({ className }: { className?: string }) {
      return <span data-filter-icon className={className} />
    }

    const rendered = await renderWithI18n(
      <DataTableFacetedFilter
        column={filterColumn}
        options={[
          { label: 'Active', value: 'active', icon: FilterIcon },
          { label: 'Empty', value: 'empty' },
        ]}
      />
    )

    const trigger = rendered.container.querySelector<HTMLButtonElement>(
      'button[aria-label="Filter"]'
    )
    assert.ok(trigger)

    await act(async () => trigger.click())

    const popup = document.querySelector('[data-slot="popover-content"]')
    assert.ok(popup)
    assert.ok(popup.querySelector('[data-filter-icon]'))
    assert.equal(
      popup
        .querySelector('[data-slot="command-input"]')
        ?.getAttribute('aria-label'),
      'Filter'
    )

    const activeOption = [
      ...popup.querySelectorAll('[data-slot="command-item"]'),
    ].find((item) => item.textContent?.includes('Active'))
    assert.ok(activeOption)
    assert.match(activeOption.textContent ?? '', /2/)

    const emptyOption = [
      ...popup.querySelectorAll('[data-slot="command-item"]'),
    ].find((item) => item.textContent?.includes('Empty'))
    assert.ok(emptyOption)
    assert.doesNotMatch(emptyOption.textContent ?? '', /0/)

    await act(async () => rendered.root.unmount())
  })
})

describe('data-table bulk action keyboard behavior', () => {
  test('skips disabled actions and keeps the description reference unique', async () => {
    const table = {
      getFilteredSelectedRowModel: () => ({ rows: [{}] }),
      resetRowSelection: () => undefined,
    } as unknown as Table<unknown>

    const rendered = await renderWithI18n(
      <DataTableBulkActions table={table} entityName='item'>
        <button type='button' disabled>
          Disabled action
        </button>
        <button type='button' data-action='enabled'>
          Enabled action
        </button>
      </DataTableBulkActions>
    )

    const toolbar = rendered.container.querySelector('[role="toolbar"]')
    assert.ok(toolbar)
    assert.equal(
      toolbar.firstElementChild?.classList.contains('max-w-[calc(100vw-1rem)]'),
      true
    )

    const descriptionId = toolbar.getAttribute('aria-describedby')
    assert.ok(descriptionId)
    const description = [
      ...document.querySelectorAll<HTMLElement>('[id]'),
    ].find((element) => element.id === descriptionId)
    assert.equal(description?.id, descriptionId)

    const enabledButtons = toolbar.querySelectorAll<HTMLButtonElement>(
      'button:not(:disabled)'
    )
    assert.equal(enabledButtons.length, 2)
    await act(async () => enabledButtons[0]?.focus())

    await act(async () => {
      enabledButtons[0]?.dispatchEvent(
        new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true })
      )
    })

    assert.equal(document.activeElement?.getAttribute('data-action'), 'enabled')

    await act(async () => rendered.root.unmount())
  })
})
