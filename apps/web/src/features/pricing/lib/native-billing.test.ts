/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import type { PricingModel } from '../types'
import {
  BILLING_VARS,
  coefficientToDisplayPrice,
  displayPriceToCoefficient,
  evaluateTextRequestExpression,
  parseTiersFromExpr,
} from './billing-expr'
import {
  getDynamicPriceEntries,
  getDynamicPricingSummary,
} from './dynamic-price'
import {
  evalExprLocally,
  generateExprFromVisualConfig,
  normalizeVisualTier,
  tryParseVisualConfig,
  type ExtraTokenValues,
} from './tier-expr'

const emptyExtras: ExtraTokenValues = {
  cacheReadTokens: 0,
  cacheCreateTokens: 0,
  cacheCreate1hTokens: 0,
  imageTokens: 0,
  imageOutputTokens: 0,
  audioInputTokens: 0,
  audioOutputTokens: 0,
}

describe('native cache and duration billing', () => {
  test('preserves independent modality rates across visual editing and expression parsing', () => {
    const config = {
      tiers: [
        normalizeVisualTier({
          label: 'native',
          input_unit_cost: 1,
          output_unit_cost: 2,
          cache_read_unit_cost: 0.2,
          cache_create_unit_cost: 0.4,
          cache_text_read_unit_cost: 0.03,
          cache_image_read_unit_cost: 0.04,
          cache_audio_read_unit_cost: 0.05,
          audio_duration_unit_cost: 100,
        }),
      ],
    }
    const expr = generateExprFromVisualConfig(config)
    assert.deepEqual(tryParseVisualConfig(expr), config)
    const [parsed] = parseTiersFromExpr(expr)
    assert.equal(parsed.cacheReadPrice, 0.2)
    assert.equal(parsed.cacheTextReadPrice, 0.03)
    assert.equal(parsed.cacheImageReadPrice, 0.04)
    assert.equal(parsed.cacheAudioReadPrice, 0.05)
    assert.equal(parsed.audioDurationPrice, 100)

    const legacy = 'tier("old", p * 1 + c * 2 + cr * 0.2 + cc * 0.4)'
    assert.equal(
      generateExprFromVisualConfig(tryParseVisualConfig(legacy)),
      legacy
    )
  })

  test('converts minute prices without applying the token display divisor', () => {
    const duration = BILLING_VARS.find((variable) => variable.key === 'audio_s')
    assert.ok(duration)
    assert.equal(displayPriceToCoefficient(duration, 0.006), 100)
    assert.equal(coefficientToDisplayPrice(duration, 100), 0.006)
    const tier = parseTiersFromExpr(
      'tier("audio", p * 0 + c * 0 + audio_s * 100)'
    )[0]
    for (const tokenUnit of ['M', 'K'] as const) {
      const [entry] = getDynamicPriceEntries(tier, {
        tokenUnit,
        groupRatioMultiplier: 2,
      })
      assert.equal(entry.unit, 'minute')
      assert.equal(entry.formatted.includes('0.012'), true)
    }
    const model = {
      model_name: 'audio-judge',
      billing_mode: 'tiered_expr',
      billing_expr: 'tier("audio", p * 0 + c * 0 + audio_s * 100)',
    } as PricingModel
    assert.equal(
      getDynamicPricingSummary(model, { tokenUnit: 'M' })?.primaryEntries[0]
        .key,
      'audio_s'
    )
  })

  test('estimates modality cache and fractional seconds using their actual units', () => {
    const result = evalExprLocally(
      'tier("native", p * 1 + cr_text * 0.03 + cr_img * 0.04 + cr_audio * 0.05 + audio_s * 100)',
      1000,
      0,
      {
        ...emptyExtras,
        cacheTextReadTokens: 100,
        cacheImageReadTokens: 50,
        cacheAudioReadTokens: 20,
        audioDurationSeconds: 60.5,
      }
    )
    assert.equal(result.error, null)
    assert.equal(result.cost, 7056)
    assert.equal(result.cost / 1_000_000, 0.007056)
  })

  test('refuses classified-cache estimates when nonzero aggregate lacks a valid split', () => {
    const expression = 'tier("native", p * 1 + cr_text * 0.1)'
    for (const details of [
      {},
      {
        cacheTextReadTokens: 20,
        cacheImageReadTokens: 0,
        cacheAudioReadTokens: 0,
      },
    ]) {
      assert.equal(
        evalExprLocally(expression, 1000, 0, {
          ...emptyExtras,
          cacheReadTokens: 100,
          ...details,
        }).error,
        'Cache classification is unavailable'
      )
    }
    assert.equal(evalExprLocally(expression, 1000, 0, emptyExtras).error, null)
  })

  test('keeps text-only estimates accurate and refuses missing duration', () => {
    assert.equal(
      evaluateTextRequestExpression('p * 1 + cr_text * 0.1', 1000, 0, 100),
      910
    )
    assert.throws(
      () => evaluateTextRequestExpression('audio_s * 100', 1000, 0, 0),
      /Audio duration is required/
    )
  })
})
