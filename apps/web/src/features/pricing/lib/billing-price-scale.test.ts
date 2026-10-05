/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import {
  evaluateBillingExpression,
  parseTiersFromExpr,
  unwrapBillingPriceScale,
} from './billing-expr'

const expression =
  'len <= 200000 ? tier("short/*", p * 28 + c * 112) : tier("long", p * 56 + c * 224)'
describe('whole-expression USD conversion', () => {
  test('unwraps only the monetary wrapper and preserves conditions', () => {
    const converted = `v1:(${expression}) / (14)`
    assert.deepEqual(unwrapBillingPriceScale(converted), {
      expression: `v1:${expression}`,
      scale: 1 / 14,
    })
    const tiers = parseTiersFromExpr(converted)
    assert.equal(tiers[0].inputPrice, 2)
    assert.equal(tiers[0].outputPrice, 8)
    assert.deepEqual(tiers[0].conditions, [
      { var: 'len', op: '<=', value: 200000 },
    ])
    assert.equal(tiers[1].inputPrice, 4)
    assert.equal(
      evaluateBillingExpression(converted, { p: 200001, c: 1000, len: 200001 })
        .value,
      816004
    )
  })
  test('keeps nested read/write wrappers reversible without rewriting rates', () => {
    const wrapped = `(((${expression}) / (14)) * (14))`
    assert.deepEqual(unwrapBillingPriceScale(wrapped), { expression, scale: 1 })
    assert.deepEqual(
      parseTiersFromExpr(wrapped),
      parseTiersFromExpr(expression)
    )
    assert.equal(
      unwrapBillingPriceScale(`2 * ((${expression}) / (14))`).scale,
      2 / 14
    )
  })
  test('does not treat per-token arithmetic or tier thresholds as a currency wrapper', () => {
    const conditional =
      'len < 1000 ? tier("a", p * 2 + c * 8) : tier("b", p * 4 + c * 16)'
    assert.deepEqual(unwrapBillingPriceScale(conditional), {
      expression: conditional,
      scale: 1,
    })
    const variableDivisor = '(tier("a", p * 2 + c * 8)) / len'
    assert.equal(unwrapBillingPriceScale(variableDivisor).scale, 1)
  })
  test('rejects zero, negative and overflow conversion factors', () => {
    for (const divisor of ['0', '-14']) {
      assert.throws(
        () => unwrapBillingPriceScale(`(${expression}) / (${divisor})`),
        /positive finite factor/
      )
      assert.deepEqual(parseTiersFromExpr(`(${expression}) / (${divisor})`), [])
    }
    assert.deepEqual(
      parseTiersFromExpr(`((${expression}) * (1e308)) * (1e308)`),
      []
    )
  })
})
