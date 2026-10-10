/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { MarketAPIError, type CallResponse } from './api'
import {
  callCanBeEdited,
  callConfirmation,
  canStartAnotherCall,
} from './call-utils'

const pending: CallResponse = {
  call: {
    id: 'one',
    service_id: 'service',
    tool_id: 'tool',
    version_id: 'version',
    client_id: 'web-market',
    execution_status: 'awaiting_confirmation',
    settlement_status: 'held',
    price_quota: 0,
    created_at: 1,
    resolve_by: 120,
  },
  result: {
    requestState: 'opaque-state',
    inputRequests: {
      confirmation: { mode: 'form', message: 'Confirm a transfer' },
    },
  },
  result_expired: false,
}

test('only definitive pre-execution errors reopen call parameters', () => {
  assert.equal(
    callCanBeEdited(new MarketAPIError('TOOL_MARKET_ARGUMENTS')),
    true
  )
  assert.equal(callCanBeEdited(new MarketAPIError('TOOL_MARKET_BUSY')), true)
  assert.equal(
    callCanBeEdited(new MarketAPIError('TOOL_MARKET_UNAVAILABLE')),
    false
  )
  assert.equal(callCanBeEdited(new Error('network timeout')), false)
})

test('unknown and confirmation calls cannot be restarted as another operation', () => {
  assert.deepEqual(callConfirmation(pending), {
    requestState: 'opaque-state',
    message: 'Confirm a transfer',
  })
  assert.equal(canStartAnotherCall(pending), false)
  assert.equal(
    canStartAnotherCall({
      ...pending,
      call: {
        ...pending.call,
        execution_status: 'unknown',
        settlement_status: 'released',
      },
    }),
    false
  )
  assert.equal(
    canStartAnotherCall({
      ...pending,
      call: {
        ...pending.call,
        execution_status: 'succeeded',
        settlement_status: 'held',
      },
    }),
    false
  )
  assert.equal(
    canStartAnotherCall({
      ...pending,
      call: {
        ...pending.call,
        execution_status: 'succeeded',
        settlement_status: 'settled',
      },
    }),
    true
  )
  assert.equal(
    callConfirmation({
      ...pending,
      result: {
        requestState: '',
        inputRequests: { confirmation: { mode: 'form', message: 'unsafe' } },
      },
    }),
    null
  )
})
