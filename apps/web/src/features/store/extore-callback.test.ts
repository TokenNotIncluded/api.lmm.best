/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { extoreCallbackURL, clearExtoreCallbackURL } from './extore-callback'

test('the early callback survives repeated initialization and retains duplicate parameters for server validation', () => {
  const previous = Object.getOwnPropertyDescriptor(globalThis, 'window')
  const raw =
    'https://shop.example/store/manage?code=one&code=two&state=fixture&iss=fixture'
  const location = new URL('https://shop.example/store/manage')
  const bridge = {
    __lmmExtoreCallback: raw,
    __lmmExtoreCallbackPage: true,
    location,
    history: {
      state: null,
      replaceState: (_state: unknown, _title: string, value: string) => {
        location.href = new URL(value, location).href
      },
    },
  }
  Object.defineProperty(globalThis, 'window', {
    configurable: true,
    value: bridge,
  })
  try {
    assert.equal(extoreCallbackURL(), raw)
    assert.equal(extoreCallbackURL(), raw)
    clearExtoreCallbackURL()
    assert.equal(extoreCallbackURL(), null)
    assert.equal(bridge.__lmmExtoreCallback, undefined)
    assert.equal(bridge.__lmmExtoreCallbackPage, true)
    assert.equal(location.href, 'https://shop.example/store/manage')
  } finally {
    if (previous) Object.defineProperty(globalThis, 'window', previous)
    else Reflect.deleteProperty(globalThis, 'window')
  }
})
