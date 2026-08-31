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
import { getAvailableGroups, getDisplayGroupRatio } from './model-helpers'

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

  test('uses the selected group ratio for an all-groups model', () => {
    const sharedModel = {
      ...model(['all']),
      group_ratio: { default: 1, vip: 0.6 },
    }

    assert.equal(getDisplayGroupRatio(sharedModel, 'vip'), 0.6)
  })

  test('uses the best disclosed ratio for an unfiltered all-groups model', () => {
    const sharedModel = {
      ...model(['all']),
      group_ratio: { default: 1, vip: 0.6, staff: 0.75 },
    }

    assert.equal(getDisplayGroupRatio(sharedModel), 0.6)
  })

  test('excludes hidden groups from the best disclosed ratio', () => {
    const sharedModel = {
      ...model(['all']),
      group_ratio: { default: 1, vip: 0.8, auto: 0.1, '': 0.2 },
    }

    assert.equal(getDisplayGroupRatio(sharedModel), 0.8)
  })
})
