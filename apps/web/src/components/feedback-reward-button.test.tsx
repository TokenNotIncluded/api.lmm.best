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

const domWindow = new Window()
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'KeyboardEvent',
  'PointerEvent',
  'CustomEvent',
  'MutationObserver',
  'ResizeObserver',
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
const { FeedbackRewardButton } = await import('./feedback-reward-button')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        'Report & earn': 'Report & earn',
        'Feedback rewards': 'Feedback rewards',
        'Valid reports earn at least 5 USD after review. Submission does not guarantee a reward.':
          'Valid reports earn at least 5 USD after review. Submission does not guarantee a reward.',
        'Frontend improvement': 'Frontend improvement',
        'Improve interface, accessibility, or mobile usability.':
          'Improve interface, accessibility, or mobile usability.',
        'Feature request': 'Feature request',
        'Suggest a useful capability or workflow.':
          'Suggest a useful capability or workflow.',
        'Bug report': 'Bug report',
        'Report a reproducible problem and its impact.':
          'Report a reproducible problem and its impact.',
      },
    },
  },
})

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

describe('FeedbackRewardButton', () => {
  after(() => domWindow.close())

  test('keeps the trigger and issue choices usable inside mobile safe areas', async () => {
    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)

    await act(async () =>
      root.render(
        <I18nextProvider i18n={i18n}>
          <FeedbackRewardButton />
        </I18nextProvider>
      )
    )

    const wrapper = container.firstElementChild
    assert.ok(wrapper)
    assert.equal(
      wrapper.classList.contains(
        'right-[calc(1rem+env(safe-area-inset-right))]'
      ),
      true
    )
    assert.equal(
      wrapper.classList.contains(
        'bottom-[calc(1rem+env(safe-area-inset-bottom))]'
      ),
      true
    )

    const trigger = container.querySelector<HTMLButtonElement>(
      'button[aria-label="Report & earn"]'
    )
    assert.ok(trigger)
    assert.equal(trigger.classList.contains('h-11'), true)
    assert.equal(trigger.classList.contains('min-w-11'), true)
    assert.match(trigger.textContent ?? '', /5\+ USD/)

    await act(async () => trigger.click())
    const popup = document.querySelector<HTMLElement>(
      '[data-slot="popover-content"]'
    )
    assert.ok(popup)
    assert.equal(popup.classList.contains('overflow-y-auto'), true)
    assert.equal(popup.classList.contains('overscroll-contain'), true)
    assert.equal(
      popup.classList.contains(
        'max-h-[calc(100dvh_-_6.5rem_-_env(safe-area-inset-top)_-_env(safe-area-inset-bottom))]'
      ),
      true
    )
    assert.equal(
      popup.classList.contains(
        'w-[calc(100vw_-_2rem_-_env(safe-area-inset-left)_-_env(safe-area-inset-right))]'
      ),
      true
    )

    const links = [...popup.querySelectorAll<HTMLAnchorElement>('a')]
    assert.equal(links.length, 3)
    assert.deepEqual(
      links.map((link) => new URL(link.href).searchParams.get('template')),
      [
        'frontend_improvement_en.yml',
        'feature_request_en.yml',
        'bug_report_en.yml',
      ]
    )
    for (const link of links) {
      assert.equal(link.target, '_blank')
      assert.equal(link.rel, 'noopener noreferrer')
    }

    await act(async () => root.unmount())
    container.remove()
  })
})
