/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { getDrawingWebDenial, resolveDrawingWebAccess } from './web-access'

test('web access permits exact USD 10 and rejects below it or an unavailable balance', () => {
  for (const balance of [0, 9, 9.999999, -1, null, Number.NaN]) {
    assert.equal(
      resolveDrawingWebAccess(
        { minimum_balance_usd: 10, balance_usd: balance, allowed: true },
        99999999,
        1
      ).allowed,
      false
    )
  }
  assert.equal(
    resolveDrawingWebAccess(
      { minimum_balance_usd: 10, balance_usd: 10, allowed: true },
      undefined,
      1
    ).allowed,
    true
  )
  assert.equal(
    resolveDrawingWebAccess(
      { minimum_balance_usd: 10, balance_usd: 100, allowed: false },
      undefined,
      1
    ).allowed,
    false
  )
  assert.equal(
    resolveDrawingWebAccess(
      { minimum_balance_usd: 10, balance_usd: null, allowed: false },
      99999999,
      1
    ).balance_usd,
    null
  )
})

test('wallet quota can display USD but never authorizes generation without the server payload', () => {
  assert.equal(
    resolveDrawingWebAccess(undefined, 5000000, 500000).allowed,
    false
  )
  assert.equal(
    resolveDrawingWebAccess(undefined, 4999999, 500000).allowed,
    false
  )
  assert.equal(resolveDrawingWebAccess(undefined, 100, 20).balance_usd, 5)
  assert.equal(
    resolveDrawingWebAccess(undefined, undefined, 500000).balance_usd,
    null
  )
  assert.equal(
    resolveDrawingWebAccess(undefined, Number.NaN, 500000).balance_usd,
    null
  )
  assert.equal(resolveDrawingWebAccess(undefined, 5000000, 0).balance_usd, null)
})

test('backend denial is detected from the OpenAI envelope and preserves unknown balances', () => {
  const response = {
    data: {
      error: { code: 'WEB_DRAWING_MINIMUM_BALANCE' },
      drawing_web_access: {
        minimum_balance_usd: 10,
        balance_usd: 7,
        allowed: false,
      },
    },
  }
  assert.equal(getDrawingWebDenial({ response })?.balance_usd, 7)
  assert.equal(
    getDrawingWebDenial({
      response: {
        data: {
          error: { code: 'WEB_DRAWING_MINIMUM_BALANCE' },
          drawing_web_access: {
            minimum_balance_usd: 10,
            balance_usd: 100,
            allowed: true,
          },
        },
      },
    })?.allowed,
    false
  )
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
