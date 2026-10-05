/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import type { PricingModel } from '../types'
import {
  getAvailableGroups,
  getDisplayGroupRatio,
  getDisplayPriceGroup,
} from './model-helpers'

function model(enableGroups: string[]): PricingModel {
  return {
    id: 1,
    model_name: 'shared-model',
    quota_type: 0,
    model_ratio: 1,
    completion_ratio: 1,
    enable_groups: enableGroups,
  }
}

describe('pricing model group helpers', () => {
  test('availability badges follow the filter or source order, never the cheapest ratio', () => {
    const pricedModel = {
      ...model(['official', 'default', 'cheap']),
      group_ratio: { official: 5, default: 10, cheap: 0.1 },
    }
    assert.equal(getDisplayPriceGroup(pricedModel), 'official')
    assert.equal(getDisplayPriceGroup(pricedModel, 'default'), 'default')
    assert.equal(getDisplayGroupRatio(pricedModel, 'default'), 1)
  })

  test('group multipliers cannot make the public base price free', () => {
    const pricedModel = {
      ...model(['paid', 'invalid', 'free']),
      group_ratio: { paid: 2, invalid: -1, free: 0 },
    }
    assert.equal(getDisplayPriceGroup(pricedModel), 'paid')
    assert.equal(getDisplayGroupRatio(pricedModel), 1)
  })
  test('expands all-groups models to every usable group', () => {
    const usableGroups = {
      default: { desc: 'Default', ratio: 1 },
      vip: { desc: 'VIP', ratio: 0.8 },
      auto: { desc: 'Automatic', ratio: 1 },
      '': { desc: 'Empty', ratio: 1 },
    }

    assert.deepEqual(getAvailableGroups(model(['all']), usableGroups), [
      'default',
      'vip',
    ])
  })

  test('selected groups never multiply base prices for all-groups models', () => {
    const sharedModel = {
      ...model(['all']),
      group_ratio: { default: 1, vip: 0.6 },
    }

    assert.equal(getDisplayGroupRatio(sharedModel, 'vip'), 1)
  })

  test('unfiltered models keep the base price', () => {
    const sharedModel = {
      ...model(['all']),
      group_ratio: { default: 1, vip: 0.6, staff: 0.75 },
    }

    assert.equal(getDisplayGroupRatio(sharedModel), 1)
  })

  test('hidden groups cannot affect the price or availability badge', () => {
    const sharedModel = {
      ...model(['all']),
      group_ratio: { default: 1, vip: 0.8, auto: 0.1, '': 0.2 },
    }

    assert.equal(getDisplayGroupRatio(sharedModel), 1)
    assert.equal(getDisplayPriceGroup(sharedModel), 'default')
  })
})
