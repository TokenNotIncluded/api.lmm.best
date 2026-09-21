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
import type { ReactNode } from 'react'
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
const { Calendar, CalendarDayButton } = await import('./calendar')

after(() => domWindow.close())

async function withCalendarRoot(
  run: (
    container: HTMLDivElement,
    render: (element: ReactNode) => Promise<void>
  ) => Promise<void>
) {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  try {
    await run(container, async (element) => {
      await act(async () => root.render(element))
    })
  } finally {
    await act(async () => root.unmount())
    container.remove()
  }
}

const localeCases: { code?: string; intl?: string }[] = [
  { code: 'zhCN', intl: 'zh-CN' },
  { code: 'zhTW', intl: 'zh-TW' },
  { code: 'zh-CN', intl: 'zh-CN' },
  { code: 'zh-TW', intl: 'zh-TW' },
  { code: 'en', intl: 'en' },
  { code: 'en-GB', intl: 'en-GB' },
  { code: 'fr', intl: 'fr' },
  { code: 'ru', intl: 'ru' },
  { code: 'ja', intl: 'ja' },
  { code: 'vi', intl: 'vi' },
  { code: 'invalid_locale', intl: undefined },
  { code: undefined, intl: undefined },
]
const date = new Date(2026, 8, 21)
const startMonth = new Date(2026, 0, 1)
const endMonth = new Date(2026, 11, 1)

describe('Calendar locale formatting', () => {
  for (const { code, intl } of localeCases) {
    test(`renders a valid date marker for ${code ?? 'default'}`, async () => {
      await withCalendarRoot(async (container, render) => {
        await render(
          <CalendarDayButton
            day={new CalendarDay(date, date)}
            modifiers={{}}
            locale={code === undefined ? undefined : { code }}
          />
        )
        assert.equal(
          container.querySelector('[data-day]')?.getAttribute('data-day'),
          date.toLocaleDateString(intl)
        )
      })
    })

    test(`renders and changes the month for ${code ?? 'default'}`, async () => {
      await withCalendarRoot(async (container, render) => {
        let changedMonth: Date | undefined
        await render(
          <Calendar
            mode='single'
            defaultMonth={date}
            today={date}
            startMonth={startMonth}
            endMonth={endMonth}
            captionLayout='dropdown-months'
            locale={code === undefined ? undefined : { code }}
            onMonthChange={(month) => {
              changedMonth = month
            }}
          />
        )
        const select = container.querySelector('select')
        assert.ok(select)
        assert.equal(select.options.length, 12)
        for (let month = 0; month < 12; month++) {
          assert.equal(
            select.options[month].textContent,
            new Date(2026, month, 1).toLocaleString(intl, { month: 'short' })
          )
        }
        assert.equal(select.value, '8')
        await act(async () => {
          select.value = '9'
          select.dispatchEvent(new Event('change', { bubbles: true }))
        })
        assert.ok(changedMonth)
        assert.equal(changedMonth.getFullYear(), 2026)
        assert.equal(changedMonth.getMonth(), 9)
        assert.equal(container.querySelector('select')?.value, '9')
        const markers = Array.from(container.querySelectorAll('[data-day]'))
        assert.ok(
          markers.some(
            (day) =>
              day.getAttribute('data-day') ===
              new Date(2026, 9, 21).toLocaleDateString(intl)
          )
        )
      })
    })
  }

  test('keeps caller-provided month formatting', async () => {
    await withCalendarRoot(async (container, render) => {
      await render(
        <Calendar
          mode='single'
          defaultMonth={date}
          today={date}
          startMonth={startMonth}
          endMonth={endMonth}
          captionLayout='dropdown-months'
          locale={{ code: 'zhCN' }}
          formatters={{
            formatMonthDropdown: (month) => `month-${month.getMonth() + 1}`,
          }}
        />
      )
      assert.equal(
        container.querySelector('select option[value="8"]')?.textContent,
        'month-9'
      )
    })
  })

  test('updates date and month labels when the locale changes', async () => {
    await withCalendarRoot(async (container, render) => {
      for (const { code, intl } of localeCases.slice(0, 7)) {
        await render(
          <Calendar
            mode='single'
            selected={date}
            defaultMonth={date}
            today={date}
            startMonth={startMonth}
            endMonth={endMonth}
            captionLayout='dropdown-months'
            locale={{ code }}
          />
        )
        assert.equal(
          container
            .querySelector('[data-selected-single="true"]')
            ?.getAttribute('data-day'),
          date.toLocaleDateString(intl)
        )
        assert.equal(
          container.querySelector('select option[value="8"]')?.textContent,
          date.toLocaleString(intl, { month: 'short' })
        )
      }
    })
  })
})
