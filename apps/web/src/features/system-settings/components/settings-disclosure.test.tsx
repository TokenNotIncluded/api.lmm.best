/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { Window } from 'happy-dom'

const dom = new Window()
for (const key of [
  'window',
  'document',
  'HTMLElement',
  'HTMLDetailsElement',
  'MutationObserver',
  'Event',
  'Node',
  'Element',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: dom[key],
  })
}
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { SettingsDisclosure } = await import('./settings-disclosure')

test('closed sections retain form fields and expand all ancestors of an invalid control', async () => {
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  await act(async () => {
    root.render(
      <SettingsDisclosure title='Advanced'>
        <SettingsDisclosure title='Translations'>
          <input aria-label='Retained field' defaultValue='Unsubmitted edit' />
        </SettingsDisclosure>
      </SettingsDisclosure>
    )
  })
  const input = host.querySelector('input')!
  assert.equal(input.value, 'Unsubmitted edit')
  const sections = [...host.querySelectorAll('details')]
  assert.equal(sections.length, 2)
  assert.ok(sections.every((section) => !section.open))
  await act(async () => {
    input.setAttribute('aria-invalid', 'true')
    await new Promise((resolve) => setTimeout(resolve, 20))
  })
  assert.ok(sections.every((section) => section.open))
  assert.equal(input.value, 'Unsubmitted edit')
  await act(async () => root.unmount())
  host.remove()
})
