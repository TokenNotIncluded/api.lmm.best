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
