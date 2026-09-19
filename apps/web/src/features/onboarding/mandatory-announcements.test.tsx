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

const dom = new Window({ url: 'http://localhost/' })
dom.document.write('<!doctype html><html><body></body></html>')
Object.defineProperty(dom.document, 'compatMode', {
  configurable: true,
  value: 'CSS1Compat',
})
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'Element',
  'Node',
  'Event',
  'MutationObserver',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: key === 'window' ? dom : dom[key],
  })
}
Object.defineProperty(globalThis, 'ResizeObserver', {
  configurable: true,
  value: class {
    observe() {}
    disconnect() {}
  },
})
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { initReactI18next } = await import('react-i18next')
await createInstance()
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const { AnnouncementReader } = await import('./mandatory-announcements')
after(() => dom.close())

test('reading requires the bottom and failed confirmation can be retried', async () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  let confirmations = 0
  await act(async () => {
    root.render(
      <AnnouncementReader
        item={{
          id: 1,
          content: 'Read this announcement',
          publishDate: '2025-01-01T00:00:00Z',
          revision: 'r1',
          read_at: 0,
        }}
        completed={0}
        total={2}
        onContinue={async () => {
          confirmations++
          if (confirmations === 1) throw new Error('offline')
        }}
      />
    )
  })
  const viewport = container.querySelector('[role="region"]') as HTMLDivElement
  Object.defineProperty(viewport, 'clientHeight', {
    configurable: true,
    value: 200,
  })
  Object.defineProperty(viewport, 'scrollHeight', {
    configurable: true,
    value: 1000,
  })
  const button = container.querySelector('button')
  assert.ok(button)
  await act(async () =>
    viewport.dispatchEvent(new Event('scroll', { bubbles: true }))
  )
  assert.equal(button.disabled, true)
  assert.equal(confirmations, 0)
  await act(async () => {
    viewport.scrollTop = 800
    viewport.dispatchEvent(new Event('scroll', { bubbles: true }))
  })
  assert.equal(button.disabled, false)
  await act(async () => button.click())
  assert.equal(confirmations, 1)
  assert.ok(container.querySelector('[role="alert"]'))
  assert.equal(button.disabled, false)
  await act(async () => button.click())
  assert.equal(confirmations, 2)
  await act(async () => root.unmount())
  container.remove()
})
