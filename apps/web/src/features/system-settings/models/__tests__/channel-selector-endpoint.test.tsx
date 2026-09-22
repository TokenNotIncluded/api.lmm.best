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
const domGlobals = [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLInputElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'MutationObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
] as const

for (const key of domGlobals) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act, useState } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { ChannelSelectorDialog } = await import('../channel-selector-dialog')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const CUSTOM_PLACEHOLDER = '/your/endpoint'

const channels = [
  { id: 1, name: 'Upstream', base_url: 'https://example.test', status: 1 },
]

function DialogFixture(props: { initialEndpoints?: Record<number, string> }) {
  const [endpoints, setEndpoints] = useState(props.initialEndpoints ?? {})

  return (
    <ChannelSelectorDialog
      open
      onOpenChange={() => {}}
      channels={channels}
      selectedChannelIds={[]}
      onSelectedChannelIdsChange={() => {}}
      channelEndpoints={endpoints}
      onChannelEndpointsChange={setEndpoints}
      onConfirm={() => {}}
    />
  )
}

/**
 * The select trigger of the single `Upstream` row. Scoping by row keeps the
 * assertion about *that* channel's endpoint rather than the dialog's first
 * rendered combobox.
 */
function endpointSelect() {
  const row = [...document.querySelectorAll('[data-slot="table-row"]')].find(
    (candidate) => candidate.textContent?.includes('Upstream')
  )
  assert.ok(row, 'expected the upstream channel row to be rendered')
  const trigger = row.querySelector('[data-slot="select-trigger"]')
  assert.ok(trigger, 'expected the endpoint select to be rendered')
  return trigger as HTMLElement
}

function selectOption(label: string) {
  const option = [...document.querySelectorAll('[role="option"]')].find(
    (candidate) => candidate.textContent === label
  )
  assert.ok(option, `expected a ${label} option in the open listbox`)
  return option as HTMLElement
}

function customInput() {
  return document.querySelector<HTMLInputElement>(
    `input[placeholder="${CUSTOM_PLACEHOLDER}"]`
  )
}

function requiredCustomInput() {
  const input = customInput()
  assert.ok(input, 'expected the custom endpoint input to be rendered')
  return input
}

function typeIntoInput(input: HTMLInputElement, value: string) {
  const valueSetter = Object.getOwnPropertyDescriptor(
    domWindow.HTMLInputElement.prototype,
    'value'
  )?.set
  assert.ok(valueSetter)
  valueSetter.call(input, value)
  input.dispatchEvent(
    new domWindow.Event('input', { bubbles: true }) as unknown as Event
  )
}

async function renderDialog(endpoints: Record<number, string>) {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  await act(async () => {
    root.render(
      <I18nextProvider i18n={i18n}>
        <DialogFixture initialEndpoints={endpoints} />
      </I18nextProvider>
    )
  })

  return async () => {
    await act(async () => root.unmount())
    container.remove()
  }
}

async function openEndpointSelect() {
  await act(async () => {
    endpointSelect().click()
  })
}

describe('channel price source endpoints', () => {
  after(() => {
    domWindow.close()
  })

  test('keeps custom selected with an empty editable input after switching from the default', async () => {
    const unmount = await renderDialog({})

    assert.equal(endpointSelect().textContent, 'pricing')
    assert.equal(customInput(), null)

    await openEndpointSelect()
    await act(async () => {
      selectOption('custom').click()
    })

    // The regression: `|| DEFAULT_ENDPOINT` coerced the empty string written by
    // the custom branch back to `/api/pricing`, so the select snapped back to
    // `pricing` and the editor never appeared.
    assert.equal(endpointSelect().textContent, 'custom')
    assert.equal(requiredCustomInput().value, '')

    await unmount()
  })

  test('keeps the custom input mounted after editing and clearing a saved endpoint', async () => {
    const unmount = await renderDialog({ 1: '/custom/pricing' })

    assert.equal(endpointSelect().textContent, 'custom')
    assert.equal(requiredCustomInput().value, '/custom/pricing')

    await act(async () => {
      typeIntoInput(requiredCustomInput(), '/custom/ratios')
    })
    assert.equal(requiredCustomInput().value, '/custom/ratios')

    await act(async () => {
      typeIntoInput(requiredCustomInput(), '')
    })

    // Clearing must not fall back to the default endpoint while the user is
    // still typing a replacement.
    assert.equal(endpointSelect().textContent, 'custom')
    assert.equal(requiredCustomInput().value, '')

    await unmount()
  })

  for (const [endpoint, label] of [
    ['/api/pricing', 'pricing'],
    ['/api/ratio_config', 'ratio_config'],
    ['openrouter', 'OpenRouter'],
  ] as const) {
    test(`preserves the ${label} preset and allows switching back from custom`, async () => {
      const unmount = await renderDialog({ 1: endpoint })

      assert.equal(endpointSelect().textContent, label)
      assert.equal(customInput(), null)

      await openEndpointSelect()
      await act(async () => {
        selectOption('custom').click()
      })
      assert.equal(requiredCustomInput().value, '')

      await openEndpointSelect()
      await act(async () => {
        selectOption(label).click()
      })

      assert.equal(endpointSelect().textContent, label)
      assert.equal(customInput(), null)

      await unmount()
    })
  }
})
