/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { Window } from 'happy-dom'

import { mountSettingsScrubber, scrubIndex } from './settings-scrubber'

test('scrubbing is bounded and advances once per 32 pixels', () => {
  assert.equal(scrubIndex(5, 32, 10), 6)
  assert.equal(scrubIndex(5, -32, 10), 4)
  assert.equal(scrubIndex(5, 1000, 10), 9)
  assert.equal(scrubIndex(5, -1000, 10), 0)
})

for (const ending of ['release', 'cancel', 'blur', 'cleanup']) {
  test(`long press previews without navigation until ${ending}`, async () => {
    const view = new Window()
    const old = Object.getOwnPropertyDescriptor(globalThis, 'window')
    Object.defineProperty(globalThis, 'window', {
      configurable: true,
      value: view,
    })
    const button = view.document.createElement('button')
    view.document.body.append(button)
    const selected: number[] = [],
      previews: (number | null)[] = []
    let opened = 0
    const cleanup = mountSettingsScrubber(
      button as unknown as HTMLElement,
      2,
      20,
      (index) => previews.push(index),
      (index) => selected.push(index),
      () => {
        opened++
      }
    )
    const pointer = (type: string, y: number) =>
      button.dispatchEvent(
        new view.PointerEvent(type, {
          pointerId: 1,
          isPrimary: true,
          button: 0,
          clientY: y,
        })
      )
    try {
      pointer('pointerdown', 100)
      await new Promise((resolve) => setTimeout(resolve, 310))
      pointer('pointermove', 196)
      assert.equal(previews.at(-1), 5)
      assert.equal(selected.length, 0)
      if (ending === 'release') pointer('pointerup', 196)
      if (ending === 'cancel') pointer('pointercancel', 196)
      if (ending === 'blur') view.dispatchEvent(new view.Event('blur'))
      if (ending === 'cleanup') cleanup()
      assert.deepEqual(selected, ending === 'release' ? [5] : [])
      assert.equal(opened, 0)
      assert.equal(previews.at(-1), null)
    } finally {
      cleanup()
      view.close()
      if (old) Object.defineProperty(globalThis, 'window', old)
      else Reflect.deleteProperty(globalThis, 'window')
    }
  })
}
