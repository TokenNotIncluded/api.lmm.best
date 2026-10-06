/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  parseTrustLevelBenefits,
  serializeTrustLevelBenefits,
  type TrustLevelBenefitsConfig,
} from './trust-level-benefits-schema'

export function testLevelConfig(): TrustLevelBenefitsConfig {
  return {
    version: 1,
    paid_activation_enabled: true,
    decay_period_days: 0,
    tiers: [0, 1, 2, 3, 4].map((level) => ({
      level,
      min_paid_credits: level * 100,
      discount_ratio: 1 - level / 100,
      benefits:
        level === 0
          ? ['standard_access']
          : ['developer_access', 'usage_discount'],
    })),
    role_tiers: [
      {
        level: 5,
        role: 10,
        discount_ratio: 0.9,
        benefits: ['administrator_access', 'usage_discount'],
      },
      {
        level: 6,
        role: 100,
        discount_ratio: 0.85,
        benefits: ['superadministrator_access', 'usage_discount'],
      },
    ],
  }
}

test('keeps the server thresholds, independent discounts and an explicitly disabled decay period', () => {
  const config = testLevelConfig()
  config.tiers[4].min_paid_credits = Number.MAX_SAFE_INTEGER
  assert.deepEqual(
    parseTrustLevelBenefits(serializeTrustLevelBenefits(config)),
    config
  )
  assert.equal(parseTrustLevelBenefits(''), null)
  assert.equal(parseTrustLevelBenefits('{'), null)
})

test('rejects ascending-threshold violations, unsafe credit values and purchasable administrator roles', () => {
  for (const change of [
    (config: TrustLevelBenefitsConfig) => {
      config.tiers[2].min_paid_credits = config.tiers[1].min_paid_credits
    },
    (config: TrustLevelBenefitsConfig) => {
      config.tiers[1].min_paid_credits = 0
    },
    (config: TrustLevelBenefitsConfig) => {
      config.tiers[4].min_paid_credits = Number.MAX_SAFE_INTEGER + 1
    },
    (config: TrustLevelBenefitsConfig) => {
      config.tiers[4].level = 5
    },
    (config: TrustLevelBenefitsConfig) => {
      config.tiers[1].discount_ratio = 0
    },
    (config: TrustLevelBenefitsConfig) => {
      config.tiers[2].benefits = ['usage_discount', 'usage_discount']
    },
  ]) {
    const config = testLevelConfig()
    change(config)
    assert.equal(parseTrustLevelBenefits(JSON.stringify(config)), null)
    assert.throws(() => serializeTrustLevelBenefits(config))
  }
  const injected = testLevelConfig()
  assert.ok(injected.role_tiers)
  assert.equal(
    parseTrustLevelBenefits(
      JSON.stringify({
        ...injected,
        tiers: [
          ...injected.tiers,
          {
            level: 5,
            min_paid_credits: 500,
            discount_ratio: 1,
            benefits: ['administrator_access'],
          },
        ],
      })
    ),
    null
  )
  assert.equal(
    parseTrustLevelBenefits(
      JSON.stringify({
        ...injected,
        role_tiers: [
          { ...injected.role_tiers[0], min_paid_credits: 500 },
          injected.role_tiers[1],
        ],
      })
    ),
    null
  )
  assert.equal(
    parseTrustLevelBenefits(
      JSON.stringify({
        ...injected,
        role_tiers: [
          { ...injected.role_tiers[0], role: 100 },
          injected.role_tiers[1],
        ],
      })
    ),
    null
  )
  assert.equal(
    parseTrustLevelBenefits(
      JSON.stringify({ ...injected, decay_period_days: '' })
    ),
    null
  )
  assert.equal(
    parseTrustLevelBenefits(
      JSON.stringify({ ...injected, decay_period_days: null })
    ),
    null
  )
  assert.equal(
    parseTrustLevelBenefits(
      JSON.stringify({
        ...injected,
        tiers: [
          { ...injected.tiers[0], min_paid_credits: null },
          ...injected.tiers.slice(1),
        ],
      })
    ),
    null
  )
})
