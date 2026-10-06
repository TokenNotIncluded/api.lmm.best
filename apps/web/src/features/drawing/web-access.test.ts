/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { getDrawingWebDenial, resolveDrawingWebAccess } from './web-access'

const gate = (credits: number | null, allowed = true) => ({
  minimum_balance_credit: 5_000_000,
  minimum_balance_usd: 10,
  balance_credit: credits,
  balance_usd: credits === null ? null : credits / 500_000,
  allowed,
})

test('raw Credit floor remains exact at fixed 500000 credits per USD', () => {
  for (const credits of [0, 3_500_000, 4_999_999, -1, null, Number.NaN]) {
    assert.equal(
      resolveDrawingWebAccess(gate(credits), undefined, 0).allowed,
      false
    )
  }
  const access = resolveDrawingWebAccess(gate(5_000_000), undefined, 0)
  assert.equal(access.allowed, true)
  assert.equal(access.minimum_balance_credit, 5_000_000)
  assert.equal(access.minimum_balance_usd, 10)
  assert.equal(
    resolveDrawingWebAccess(gate(5_000_000, false), undefined, 0).allowed,
    false
  )
  assert.equal(
    resolveDrawingWebAccess(gate(null, false), 99_999_999, 1).balance_usd,
    null
  )
})

test('legacy or malformed gate metadata fails closed without inventing a USD threshold', () => {
  const access = resolveDrawingWebAccess(
    { minimum_balance_usd: 10, balance_usd: 100, allowed: true },
    undefined,
    0
  )
  assert.equal(access.allowed, false)
  assert.equal(access.minimum_balance_usd, null)
  assert.equal(access.balance_usd, null)
  for (const minimum of [Number.NaN, -1, 1.5, Number.MAX_SAFE_INTEGER + 1]) {
    assert.equal(
      resolveDrawingWebAccess(
        { ...gate(5_000_000), minimum_balance_credit: minimum },
        undefined,
        0
      ).allowed,
      false
    )
  }
})

test('fixed K can display USD but wallet data cannot authorize generation', () => {
  const access = resolveDrawingWebAccess(undefined, 3_500_000, 500_000)
  assert.equal(access.balance_usd, 7)
  assert.equal(access.minimum_balance_usd, null)
  assert.equal(access.allowed, false)
  assert.equal(
    resolveDrawingWebAccess(undefined, 5_000_000, 0).balance_usd,
    null
  )
  assert.equal(
    resolveDrawingWebAccess(undefined, Number.NaN, 500_000).balance_usd,
    null
  )
})

test('backend denial preserves the actual Credit gate and never authorizes drawing', () => {
  const response = {
    data: {
      error: { code: 'WEB_DRAWING_MINIMUM_BALANCE' },
      drawing_web_access: gate(3_500_000),
    },
  }
  const denied = getDrawingWebDenial({ response })
  assert.equal(denied?.balance_usd, 7)
  assert.equal(denied?.balance_credit, 3_500_000)
  assert.equal(denied?.minimum_balance_credit, 5_000_000)
  assert.equal(denied?.allowed, false)
  assert.equal(
    getDrawingWebDenial({
      response: { data: { error: { code: 'WEB_DRAWING_MINIMUM_BALANCE' } } },
    })?.balance_usd,
    null
  )
  assert.equal(
    getDrawingWebDenial({
      response: { data: { error: { code: 'QUOTA_EXCEEDED' } } },
    }),
    undefined
  )
})
