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
import type { Root } from 'react-dom/client'

import type { NavigateFn } from './use-table-url-state'

const domWindow = new Window({ url: 'https://console.example.test/users' })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'Node',
  'Element',
  'Event',
  'MutationObserver',
  'localStorage',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act, useEffect } = await import('react')
const { createRoot } = await import('react-dom/client')
const { useTableUrlState } = await import('./use-table-url-state')

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

type HookValue = ReturnType<typeof useTableUrlState>
let currentHook: HookValue | null = null
const navigations: Parameters<NavigateFn>[0][] = []
const navigate: NavigateFn = (options) => {
  navigations.push(options)
}

function Harness({ search }: { search: Record<string, unknown> }) {
  const hook = useTableUrlState({
    search,
    navigate,
    pagination: { defaultPage: 1, defaultPageSize: 20 },
    globalFilter: { key: 'query' },
  })
  useEffect(() => {
    currentHook = hook
  }, [hook])
  return null
}

async function render(root: Root, search: Record<string, unknown>) {
  await act(async () => {
    root.render(<Harness search={search} />)
  })
}

after(() => domWindow.close())

test('global filter follows URL history without clobbering a pending local value', async () => {
  const container = document.createElement('div')
  const root = createRoot(container)

  try {
    await render(root, {})
    assert.equal(currentHook?.globalFilter, '')

    await act(async () => {
      currentHook?.onGlobalFilterChange?.('  pending  ')
    })
    assert.equal(currentHook?.globalFilter, 'pending')
    assert.equal(navigations.length, 1)

    // An equivalent URL object must not reset local state before navigation
    // publishes the pending value.
    await render(root, {})
    assert.equal(currentHook?.globalFilter, 'pending')

    await render(root, { query: 'from-history' })
    assert.equal(currentHook?.globalFilter, 'from-history')

    await render(root, {})
    assert.equal(currentHook?.globalFilter, '')
  } finally {
    await act(async () => root.unmount())
    currentHook = null
    navigations.length = 0
  }
})
