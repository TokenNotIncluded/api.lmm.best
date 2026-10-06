/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { createInitialLaneState } from './model-pricing-core'
import { getPriceDetail, getPriceSummary } from './model-pricing-snapshots'
import {
  ratioToUsdPerMillion,
  usdPerMillionToRatio,
} from './model-pricing-units'

const t = (key: string) => key

test('fee ratios use credits per real USD, independent of legacy units and FX', () => {
  const creditsPerUsd = 3_600_000
  assert.equal(ratioToUsdPerMillion(7.2, creditsPerUsd), 2)
  assert.equal(usdPerMillionToRatio(2, creditsPerUsd), 7.2)
  const state = createInitialLaneState(
    {
      name: 'model',
      ratio: '7.2',
      completionRatio: '4',
      audioRatio: '3',
      audioCompletionRatio: '2',
    },
    creditsPerUsd
  )
  assert.equal(state.promptPrice, '2')
  assert.equal(state.prices.completion, '8')
  assert.equal(state.prices.audioInput, '6')
  assert.equal(state.prices.audioOutput, '12')
  assert.equal(
    getPriceSummary(
      { name: 'model', ratio: '7.2', creditsPerUsd, hasConflict: false },
      t
    ),
    'Input USD 2'
  )
})

test('zero USD prices and dependent zero ratios remain explicit', () => {
  assert.equal(usdPerMillionToRatio(0, 3_600_000), 0)
  assert.equal(
    getPriceSummary(
      {
        name: 'free',
        price: '0',
        billingMode: 'per-request',
        hasConflict: false,
      },
      t
    ),
    'USD 0 / request'
  )
  assert.equal(
    getPriceDetail(
      {
        name: 'free',
        ratio: '0',
        completionRatio: '0',
        creditsPerUsd: 3_600_000,
        hasConflict: false,
      },
      t
    ),
    'Output USD 0'
  )
})

test('unknown credit conversion is rejected instead of inventing a USD price', () => {
  for (const invalid of [0, -1, Number.NaN, Number.POSITIVE_INFINITY]) {
    assert.throws(() => ratioToUsdPerMillion(1, invalid))
    assert.throws(() => usdPerMillionToRatio(1, invalid))
  }
})
