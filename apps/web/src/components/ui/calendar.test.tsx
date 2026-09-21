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
import { after, describe, test } from 'node:test'

import { Window } from 'happy-dom'
import { CalendarDay } from 'react-day-picker'

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
Object.defineProperty(globalThis, 'IS_REACT_ACT_ENVIRONMENT', {
  configurable: true,
  value: true,
})
const { CalendarDayButton } = await import('./calendar')

after(() => domWindow.close())

describe('Calendar locale formatting', () => {
  for (const locale of ['zhCN', 'zhTW'] as const) {
    test(`renders a valid date marker for ${locale}`, async () => {
      const container = document.createElement('div')
      document.body.append(container)
      const root = createRoot(container)
      const date = new Date(2026, 8, 21)
      try {
        await act(async () => {
          root.render(
            <CalendarDayButton
              day={new CalendarDay(date, date)}
              modifiers={{}}
              locale={{ code: locale }}
            />
          )
        })
        assert.equal(
          container.querySelector('[data-day]')?.getAttribute('data-day'),
          date.toLocaleDateString(locale === 'zhCN' ? 'zh-CN' : 'zh-TW')
        )
      } finally {
        await act(async () => root.unmount())
        container.remove()
      }
    })
  }
})
