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
// @ts-expect-error Bun's test module is only available in the test runtime.
import { mock } from 'bun:test'
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

let hash = ''
mock.module('@tanstack/react-router', () => ({
  useLocation: ({
    select,
  }: {
    select: (location: { hash: string }) => unknown
  }) => select({ hash }),
}))
const { ConsoleDisclosure } = await import('./console-disclosure')

test('wallet sections open at deep links and retain drafts across toggles', async () => {
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  const render = () =>
    root.render(
      <ConsoleDisclosure id='referral-program' title='Referral'>
        <input defaultValue='draft' />
      </ConsoleDisclosure>
    )
  try {
    await act(async () => render())
    const details = host.querySelector('details')!
    const input = host.querySelector('input')!
    assert.equal(details.open, false)
    hash = 'referral-program'
    await act(async () => render())
    assert.equal(details.open, true)
    input.value = 'unsaved value'
    await act(async () => {
      details.open = false
      details.dispatchEvent(new Event('toggle'))
    })
    assert.equal(details.open, false)
    assert.equal(host.querySelector('input'), input)
    assert.equal(input.value, 'unsaved value')
    hash = 'subscription-plans'
    await act(async () => render())
    hash = '#referral-program'
    await act(async () => render())
    assert.equal(details.open, true)
  } finally {
    await act(async () => root.unmount())
    host.remove()
  }
})
