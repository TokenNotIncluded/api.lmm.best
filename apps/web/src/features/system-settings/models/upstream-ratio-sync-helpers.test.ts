/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import type { DifferencesMap } from '../types'
import { USD_PRICING_KEYS, type ModelPricingConfig } from './model-pricing-api'
import {
  applyResolutionSelections,
  buildUpstreamPricingUpdates,
  getEffectiveResolutionSelections,
  getSyncFieldDisplayValue,
  isBulkSelectableUpstreamField,
  getUpstreamDisplayName,
  type ResolutionSelection,
} from './upstream-ratio-sync-helpers'

function fixture(): ModelPricingConfig {
  return {
    schema_version: 2,
    currency: 'USD',
    storage_basis: 'legacy_pricing_unit',
    revision: 'a'.repeat(64),
    credits_per_usd: 3_500_000,
    legacy_pricing_units_per_usd: 7,
    model_ratio_usd_per_million: 1 / 3.5,
    values: {
      ...Object.fromEntries(USD_PRICING_KEYS.map((key) => [key, '{}'])),
      ModelPrice: '{"untouched":1.25,"model":2}',
      ModelRatio: '{"model":7}',
      'billing_setting.billing_mode': '{"model":"tiered_expr"}',
      'billing_setting.billing_expr':
        '{"model":"v1:p * 2.5","untouched":"v1:p * 1.25"}',
    } as ModelPricingConfig['values'],
  }
}
function fields(updates: Record<string, string>) {
  return Object.fromEntries(
    Object.entries(updates).map(([key, raw]) => [key, JSON.parse(raw)])
  )
}

test('canonical USD prices and expressions are never divided by the legacy factor again', () => {
  const config = fixture()
  assert.equal(
    getSyncFieldDisplayValue('model_price', 1.25, config),
    'USD 1.25'
  )
  assert.equal(getSyncFieldDisplayValue('model_ratio', 7, config), 'USD 2 /1M')
  assert.equal(
    getSyncFieldDisplayValue('billing_expr', 'v1:p * 2.5', config),
    'v1:p * 2.5'
  )
})

test('bulk token sync clears old fixed and expression billing, retains untouched true USD literally', () => {
  const config = fixture()
  const before = JSON.stringify(config)
  const updates = fields(
    buildUpstreamPricingUpdates(config, {
      model: {
        model_ratio: 3.5,
        completion_ratio: 0,
        cache_ratio: 0.1,
        create_cache_ratio: 1.25,
        image_ratio: 0.5,
        audio_ratio: 2,
        audio_completion_ratio: 3,
      },
      other: { model_price: 0 },
    })
  )
  assert.deepEqual(updates.ModelPrice, { untouched: 1.25, other: 0 })
  for (const [key, expected] of Object.entries({
    ModelRatio: 3.5,
    CompletionRatio: 0,
    CacheRatio: 0.1,
    CreateCacheRatio: 1.25,
    ImageRatio: 0.5,
    AudioRatio: 2,
    AudioCompletionRatio: 3,
  })) {
    assert.deepEqual(updates[key], { model: expected })
  }
  assert.deepEqual(updates['billing_setting.billing_mode'], {
    model: 'ratio',
    other: 'ratio',
  })
  assert.deepEqual(updates['billing_setting.billing_expr'], {
    untouched: 'v1:p * 1.25',
  })
  assert.equal(updates.ModelPriceLock, undefined)
  assert.equal(updates['tool_price_setting.prices'], undefined)
  assert.equal(JSON.stringify(config), before)
})

test('zero fixed price switches away from tiered mode and clears all seven ratios', () => {
  const config = fixture()
  const keys = [
    'CompletionRatio',
    'CacheRatio',
    'CreateCacheRatio',
    'ImageRatio',
    'AudioRatio',
    'AudioCompletionRatio',
  ] as const
  for (const key of keys) config.values[key] = '{"model":2,"untouched":3}'
  const updates = fields(
    buildUpstreamPricingUpdates(config, { model: { model_price: 0 } })
  )
  assert.deepEqual(updates.ModelPrice, { untouched: 1.25, model: 0 })
  assert.deepEqual(updates.ModelRatio, {})
  for (const key of keys) assert.deepEqual(updates[key], { untouched: 3 })
  assert.deepEqual(updates['billing_setting.billing_mode'], { model: 'ratio' })
  assert.deepEqual(updates['billing_setting.billing_expr'], {
    untouched: 'v1:p * 1.25',
  })
})

test('expression choice binds its selected expression and mode, preserves unrelated maps', () => {
  const updates = fields(
    buildUpstreamPricingUpdates(fixture(), {
      new: { billing_expr: 'v1:p * 0.5 + c * 2' },
    })
  )
  assert.deepEqual(updates['billing_setting.billing_expr'], {
    model: 'v1:p * 2.5',
    untouched: 'v1:p * 1.25',
    new: 'v1:p * 0.5 + c * 2',
  })
  assert.deepEqual(updates['billing_setting.billing_mode'], {
    model: 'tiered_expr',
    new: 'tiered_expr',
  })
  assert.equal(updates.ModelPrice, undefined)
  assert.equal(updates.ModelRatio, undefined)
})

test('mode-only, contradictory, unsupported and invalid numeric selections fail closed', () => {
  const invalid: Array<Record<string, number | string>> = [
    { billing_mode: 'tiered_expr' },
    { billing_mode: 'ratio' },
    { model_price: 1, model_ratio: 2 },
    { billing_expr: 'v1:p', billing_mode: 'ratio' },
    { billing_expr: 'v1:p', model_price: 1 },
    { model_price: Number.POSITIVE_INFINITY },
    { model_price: -1 },
    { model_price: '' },
    { model_price: 'same' },
    { unknown: 1 },
  ]
  for (const selection of invalid) {
    assert.throws(() =>
      buildUpstreamPricingUpdates(fixture(), { model: selection })
    )
  }
  const broken = fixture()
  broken.values.ModelPrice = '[]'
  assert.throws(() =>
    buildUpstreamPricingUpdates(broken, { model: { model_price: 1 } })
  )
})

test('switching choices clears stale expressions, source bulk ratio-mode does not erase a fixed price', () => {
  const diffs: DifferencesMap = {
    model: {
      model_ratio: { current: 7, upstreams: { source: 3.5 }, confidence: {} },
      model_price: { current: 2, upstreams: { source: 0 }, confidence: {} },
      billing_mode: {
        current: 'tiered_expr',
        upstreams: { source: 'ratio' },
        confidence: {},
      },
    },
  }
  const ratio: ResolutionSelection = {
    model: 'model',
    ratioType: 'model_ratio',
    value: 3.5,
    sourceName: 'source',
  }
  assert.deepEqual(
    applyResolutionSelections(
      { model: { billing_mode: 'tiered_expr', billing_expr: 'v1:p' } },
      diffs,
      [ratio]
    ),
    { model: { model_ratio: 3.5 } }
  )
  const bulk: ResolutionSelection[] = [
    ratio,
    {
      model: 'model',
      ratioType: 'model_price',
      value: 0,
      sourceName: 'source',
    },
    {
      model: 'model',
      ratioType: 'billing_mode',
      value: 'ratio',
      sourceName: 'source',
    },
  ]
  const selected = applyResolutionSelections(
    {},
    diffs,
    getEffectiveResolutionSelections(diffs, bulk)
  )
  assert.deepEqual(selected, {
    model: { model_price: 0, billing_mode: 'ratio' },
  })
  assert.deepEqual(
    fields(buildUpstreamPricingUpdates(fixture(), selected)).ModelPrice,
    { untouched: 1.25, model: 0 }
  )
})

test('column-wide selections exclude all fields of an uncertain model without excluding explicit zero', () => {
  const fields = {
    model_ratio: {
      current: 1,
      upstreams: { source: 7 },
      confidence: { source: false },
    },
    completion_ratio: {
      current: 1,
      upstreams: { source: 0 },
      confidence: { source: true },
    },
  }
  assert.equal(
    isBulkSelectableUpstreamField(fields, 'model_ratio', 'source'),
    false
  )
  assert.equal(
    isBulkSelectableUpstreamField(fields, 'completion_ratio', 'source'),
    false
  )
  fields.model_ratio.confidence.source = true
  assert.equal(
    isBulkSelectableUpstreamField(fields, 'completion_ratio', 'source'),
    true
  )
})

test('reference preset labels preserve wire identities without claiming official provenance', () => {
  assert.equal(getUpstreamDisplayName('官方倍率预设(-100)'), 'basellm')
  assert.equal(
    getUpstreamDisplayName('models.dev 价格预设(-101)'),
    'models.dev'
  )
  assert.equal(getUpstreamDisplayName('Custom(15)'), 'Custom(15)')
})

test('accessory ratios alone cannot switch fixed or tiered billing, or invent a missing base rate', () => {
  const config = fixture()
  assert.throws(
    () => buildUpstreamPricingUpdates(config, { model: { cache_ratio: 0.1 } }),
    /complete/
  )
  config.values['billing_setting.billing_mode'] = '{}'
  assert.throws(
    () =>
      buildUpstreamPricingUpdates(config, { model: { completion_ratio: 2 } }),
    /complete/
  )
  assert.throws(
    () =>
      buildUpstreamPricingUpdates(config, { unknown: { image_ratio: 0.5 } }),
    /complete/
  )
  const updates = fields(
    buildUpstreamPricingUpdates(config, {
      model: { model_ratio: 3.5, cache_ratio: 0.1 },
    })
  )
  assert.deepEqual(updates.ModelRatio, { model: 3.5 })
  assert.deepEqual(updates.ModelPrice, { untouched: 1.25 })
})

test('a model with an existing ratio base can adjust an accessory without changing its base or unrelated prices', () => {
  const config = fixture()
  config.values.ModelPrice = '{"untouched":1.25}'
  config.values['billing_setting.billing_mode'] = '{"model":"ratio"}'
  config.values['billing_setting.billing_expr'] = '{"untouched":"v1:p * 1.25"}'
  const updates = fields(
    buildUpstreamPricingUpdates(config, { model: { cache_ratio: 0 } })
  )
  assert.deepEqual(updates.CacheRatio, { model: 0 })
  assert.equal(updates.ModelRatio, undefined)
  assert.equal(updates.ModelPrice, undefined)
  assert.equal(updates['billing_setting.billing_expr'], undefined)
})

test('positive sub-picodollar prices remain visible while exact zero stays free', () => {
  const config = fixture()
  assert.equal(
    getSyncFieldDisplayValue('model_price', 1e-13, config),
    'USD 1e-13'
  )
  const ratioLabel = getSyncFieldDisplayValue('model_ratio', 4e-13, {
    ...config,
    credits_per_usd: 4_000_000,
  })
  assert.equal(ratioLabel, 'USD 1e-13 /1M')
  assert.notEqual(ratioLabel, 'USD 0 /1M')
  assert.equal(getSyncFieldDisplayValue('model_price', 0, config), 'USD 0')
  assert.equal(getSyncFieldDisplayValue('model_ratio', 0, config), 'USD 0 /1M')
  assert.deepEqual(
    fields(
      buildUpstreamPricingUpdates(config, { tiny: { model_price: 1e-13 } })
    ).ModelPrice,
    { untouched: 1.25, model: 2, tiny: 1e-13 }
  )
})

test('accessory edits reject null or boolean base rates and preserve an explicit numeric zero base', () => {
  const config = fixture()
  config.values.ModelPrice = '{}'
  config.values['billing_setting.billing_mode'] = '{"model":"ratio"}'
  for (const invalid of ['null', 'false', '"0"']) {
    config.values.ModelRatio = `{"model":${invalid}}`
    assert.throws(
      () =>
        buildUpstreamPricingUpdates(config, { model: { cache_ratio: 0.1 } }),
      /complete/
    )
  }
  config.values.ModelRatio = '{"model":0}'
  const updates = fields(
    buildUpstreamPricingUpdates(config, { model: { cache_ratio: 0.1 } })
  )
  assert.deepEqual(updates.CacheRatio, { model: 0.1 })
  assert.equal(updates.ModelRatio, undefined)
})
