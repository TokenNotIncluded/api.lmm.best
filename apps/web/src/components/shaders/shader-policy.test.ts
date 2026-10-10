/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  canAnimateShader,
  FORGE_SHADER_LIMITS,
  requestShaderSlot,
  shaderChapterVariant,
  shaderCapability,
  shaderRenderSize,
} from './shader-policy'

test('capability preflight refuses missing, denied and failed adapters before SDK import', async () => {
  assert.equal(await shaderCapability(undefined), 'unsupported')
  assert.equal(
    await shaderCapability({ requestAdapter: async () => null }),
    'no-adapter'
  )
  assert.equal(
    await shaderCapability({
      requestAdapter: async () => {
        throw new Error('denied')
      },
    }),
    'request-failed'
  )
  assert.equal(
    await shaderCapability({ requestAdapter: async () => ({}) }),
    'available'
  )
})

test('render dimensions stay inside the first-allocation pixel budget at any viewport', () => {
  for (const [width, height] of [
    [32, 32],
    [390, 200],
    [1440, 900],
    [7680, 4320],
    [100000, 100000],
    [1, 99999],
  ]) {
    const size = shaderRenderSize(width, height)
    assert.ok(size)
    assert.ok(size.width * 2 <= FORGE_SHADER_LIMITS.width)
    assert.ok(size.height * 2 <= FORGE_SHADER_LIMITS.height)
    assert.ok(
      size.width * size.height * 4 <=
        FORGE_SHADER_LIMITS.width * FORGE_SHADER_LIMITS.height
    )
    assert.ok(size.scale >= 1)
  }
  for (const dimension of [0, -1, Number.NaN, Infinity]) {
    assert.equal(shaderRenderSize(dimension, 100), null)
  }
})

test('route consumers share one lease, cancel waiters, and release once', () => {
  const events: string[] = []
  const first = requestShaderSlot(() => events.push('first'))
  const cancelled = requestShaderSlot(() => events.push('cancelled'))
  const second = requestShaderSlot(() => events.push('second'))
  const third = requestShaderSlot(() => events.push('third'))
  assert.deepEqual(events, ['first'])
  cancelled()
  first()
  first()
  assert.deepEqual(events, ['first', 'second'])
  second()
  assert.deepEqual(events, ['first', 'second', 'third'])
  third()
  const next = requestShaderSlot(() => events.push('next'))
  assert.equal(events.at(-1), 'next')
  next()
})

test('each motion, visibility, intent and user pause constraint blocks animation', () => {
  const enabled = {
    active: true,
    visible: true,
    documentVisible: true,
    reducedMotion: false,
    saveData: false,
    homePaused: false,
    intent: true,
  }
  assert.equal(canAnimateShader(enabled), true)
  for (const key of [
    'active',
    'visible',
    'documentVisible',
    'intent',
  ] as const) {
    assert.equal(canAnimateShader({ ...enabled, [key]: false }), false)
  }
  for (const key of ['reducedMotion', 'saveData', 'homePaused'] as const) {
    assert.equal(canAnimateShader({ ...enabled, [key]: true }), false)
  }
  assert.deepEqual(['0', '1', '2', '3', '4'].map(shaderChapterVariant), [
    'home',
    'store',
    'tools',
    'ecosystem',
    'future',
  ])
  assert.equal(shaderChapterVariant(undefined), 'home')
  assert.equal(shaderChapterVariant('999'), 'home')
})
