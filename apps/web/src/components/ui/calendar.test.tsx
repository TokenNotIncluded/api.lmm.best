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
import { describe, test } from 'node:test'

import { Window } from 'happy-dom'

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
  'CustomEvent',
  'MutationObserver',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { Calendar } = await import('./calendar')

describe('Calendar locale formatting', () => {
  for (const locale of ['zhCN', 'zhTW'] as const) {
    test(`renders a valid date marker for ${locale}`, async () => {
      const container = document.createElement('div')
      document.body.append(container)
      const root = createRoot(container)
      await act(async () =>
        root.render(<Calendar locale={{ code: locale } as never} />)
      )
      const marker = container
        .querySelector('[data-day]')
        ?.getAttribute('data-day')
      assert.ok(marker)
      assert.notEqual(marker, '')
      root.unmount()
      container.remove()
    })
  }
})
