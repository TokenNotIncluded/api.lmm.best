/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import {
  ENDPOINT_TYPES,
  FILTER_ALL,
  QUOTA_TYPES,
  SORT_OPTIONS,
} from '../constants'
import type { PricingModel } from '../types'
import { filterAndSortModels } from './filters'

function model(
  id: number,
  modelName: string,
  vendorName: string,
  enableGroups: string[]
): PricingModel {
  return {
    id,
    model_name: modelName,
    vendor_name: vendorName,
    quota_type: 0,
    model_ratio: 1,
    completion_ratio: 1,
    enable_groups: enableGroups,
  }
}

const noOtherFilters = {
  search: '',
  quotaType: QUOTA_TYPES.ALL,
  endpointType: ENDPOINT_TYPES.ALL,
  tag: FILTER_ALL,
  sortBy: SORT_OPTIONS.NAME,
}

describe('pricing model filters', () => {
  test('combines provider and group filters', () => {
    const models = [
      model(1, 'alpha-standard', 'Alpha', ['standard']),
      model(2, 'alpha-premium', 'Alpha', ['premium']),
      model(3, 'beta-premium', 'Beta', ['premium']),
    ]

    const result = filterAndSortModels(models, {
      ...noOtherFilters,
      vendor: 'Alpha',
      group: 'premium',
    })

    assert.deepEqual(
      result.map((item) => item.model_name),
      ['alpha-premium']
    )
  })

  test('keeps all-groups models when a specific group is selected', () => {
    const models = [
      model(1, 'alpha-premium', 'Alpha', ['premium']),
      model(2, 'alpha-shared', 'Alpha', ['all']),
      model(3, 'beta-shared', 'Beta', ['all']),
    ]

    const result = filterAndSortModels(models, {
      ...noOtherFilters,
      vendor: 'Alpha',
      group: 'premium',
    })

    assert.deepEqual(
      result.map((item) => item.model_name),
      ['alpha-premium', 'alpha-shared']
    )
  })
})
