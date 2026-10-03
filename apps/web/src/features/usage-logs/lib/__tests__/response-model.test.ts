/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { describe, test } from 'node:test'

import { usageLogSchema } from '../../data/schema'
import { formatModelName } from '../format'
import {
  getResponseModelObservation,
  isResponseModelMismatch,
} from '../response-model'

type CompatibilityCase = {
  name: string
  requested: string
  selected: string
  returned: string
  mismatch: boolean
}
const contract: CompatibilityCase[] = JSON.parse(
  readFileSync(
    new URL(
      '../../../../../../api-go/relay/common/testdata/response_model_compatibility.json',
      import.meta.url
    ),
    'utf8'
  )
)

function modelInfo(other: unknown) {
  return formatModelName(
    usageLogSchema.parse({
      id: 1,
      user_id: 1,
      created_at: 1,
      type: 2,
      content: '',
      model_name: 'requested',
      other: JSON.stringify(other),
    })
  )
}

describe('response model compatibility shared with Go and Rust', () => {
  for (const scenario of contract) {
    test(scenario.name, () => {
      assert.equal(
        isResponseModelMismatch({
          requested_model: scenario.requested,
          upstream_model: scenario.selected,
          returned_model: scenario.returned,
        }),
        scenario.mismatch
      )
    })
  }
})

test('missing observations and historical logs keep the existing model display', () => {
  assert.equal(isResponseModelMismatch(undefined), false)
  assert.deepEqual(modelInfo({}), {
    name: 'requested',
    isMapped: false,
    actualModel: undefined,
    responseModel: undefined,
  })
  assert.deepEqual(
    modelInfo({ is_model_mapped: true, upstream_model_name: 'selected' }),
    {
      name: 'requested',
      isMapped: true,
      actualModel: 'selected',
      responseModel: undefined,
    }
  )
})

test('mapping and returned names remain separate and stored flags are ignored', () => {
  const response = {
    requested_model: 'requested',
    upstream_model: 'selected',
    returned_model: 'different',
  }
  const info = modelInfo({
    is_model_mapped: true,
    upstream_model_name: 'selected',
    response_model: { ...response, mismatch: false },
  })
  assert.equal(info.name, 'requested')
  assert.equal(info.actualModel, 'selected')
  assert.deepEqual(info.responseModel, response)
  assert.equal(isResponseModelMismatch(info.responseModel), true)

  const matching = modelInfo({
    response_model: {
      ...response,
      returned_model: 'SELECTED',
      mismatch: true,
    },
  })
  assert.equal(matching.actualModel, 'selected')
  assert.equal(isResponseModelMismatch(matching.responseModel), false)
})

test('malformed historical metadata cannot crash or create a warning', () => {
  for (const value of [
    null,
    true,
    [],
    'model',
    {},
    { requested_model: 'requested', upstream_model: '', returned_model: 42 },
    { requested_model: [], upstream_model: '', returned_model: 'different' },
    { requested_model: 'requested', upstream_model: '', returned_model: ' ' },
  ]) {
    assert.equal(getResponseModelObservation(value), undefined)
    const info = modelInfo({
      is_model_mapped: true,
      upstream_model_name: 'selected',
      response_model: value,
    })
    assert.equal(info.actualModel, 'selected')
    assert.equal(isResponseModelMismatch(info.responseModel), false)
  }
})
