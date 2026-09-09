/*
Copyright (C) 2026 LIghtJUNction
*/

import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import {
  isModelPriceLocked,
  parseModelPriceLocks,
  withModelPriceLock,
} from './model-price-lock'

describe('model price locks', () => {
  test('starts unlocked for absent, malformed and non-object configuration', () => {
    for (const value of ['', '{', 'null', '[]', '[true]', 'true', '42']) {
      const locks = parseModelPriceLocks(value)
      assert.deepEqual(locks, {}, value)
      assert.equal(isModelPriceLocked(locks, 'gpt-4o'), false)
    }
  })

  test('only explicit true values enable a lock', () => {
    const locks = parseModelPriceLocks(
      '{"locked":true,"unlocked":false,"text":"true","number":1,"empty":null}'
    )
    assert.deepEqual(locks, { locked: true })
    assert.equal(isModelPriceLocked(locks, 'locked'), true)
    assert.equal(isModelPriceLocked(locks, 'unlocked'), false)
    assert.equal(isModelPriceLocked(locks, 'other'), false)
  })

  test('shares locks between billing aliases without affecting other model families', () => {
    const cases = [
      ['gpt-4-gizmo-a', 'gpt-4-gizmo-b', 'gpt-4o-gizmo-a'],
      ['gpt-4o-gizmo-a', 'gpt-4o-gizmo-*', 'gpt-4-gizmo-a'],
      [
        'gemini-2.5-flash-thinking-128',
        'gemini-2.5-flash-thinking-2048',
        'gemini-2.5-flash',
      ],
      [
        'gemini-2.5-flash-lite-thinking-128',
        'gemini-2.5-flash-lite-thinking-*',
        'gemini-2.5-flash-thinking-128',
      ],
      [
        'gemini-2.5-pro-thinking-128',
        'gemini-2.5-pro-thinking-2048',
        'gemini-2.5-pro',
      ],
    ]
    for (const [locked, alias, unrelated] of cases) {
      assert.equal(isModelPriceLocked({ [locked]: true }, alias), true, alias)
      assert.equal(
        isModelPriceLocked({ [locked]: true }, unrelated),
        false,
        unrelated
      )
    }
  })

  test('unlocking an alias clears every matching lock and preserves other locks', () => {
    const original = {
      'gpt-4-gizmo-*': true,
      'gpt-4-gizmo-a': true,
      'gpt-4-gizmo-b': true,
      'gpt-4o': true,
    }
    const unlocked = withModelPriceLock(original, 'gpt-4-gizmo-c', false)
    assert.deepEqual(unlocked, { 'gpt-4o': true })
    assert.equal(isModelPriceLocked(unlocked, 'gpt-4-gizmo-a'), false)
    assert.equal(Object.keys(original).length, 4)
    assert.deepEqual(withModelPriceLock(unlocked, 'gpt-4-gizmo-c', true), {
      'gpt-4o': true,
      'gpt-4-gizmo-c': true,
    })
  })
})
