/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { CHANNEL_STATUS } from '../../constants'
import type { Channel } from '../../types'
import { summarizeChannelHealth } from '../channel-health'
import type { TagRow } from '../channel-utils'

function channel(overrides: Partial<Channel> = {}): Channel {
  return {
    id: 1,
    name: 'channel',
    status: CHANNEL_STATUS.ENABLED,
    response_time: 0,
    balance: 0,
    ...overrides,
  } as Channel
}

describe('channel fleet health summary', () => {
  test('counts every status bucket and leaves unknown out of the named ones', () => {
    const summary = summarizeChannelHealth([
      channel({ id: 1, status: CHANNEL_STATUS.ENABLED }),
      channel({ id: 2, status: CHANNEL_STATUS.MANUAL_DISABLED }),
      channel({ id: 3, status: CHANNEL_STATUS.AUTO_DISABLED }),
      channel({ id: 4, status: 99 }),
    ])

    assert.equal(summary.sampled, 4)
    assert.equal(summary.enabled, 1)
    assert.equal(summary.manualDisabled, 1)
    assert.equal(summary.autoDisabled, 1)
    assert.equal(summary.unknown, 1)
    assert.equal(summary.healthPercent, 25)
  })

  test('flags never-tested enabled channels but not disabled ones', () => {
    const summary = summarizeChannelHealth([
      channel({ id: 1, status: CHANNEL_STATUS.ENABLED, response_time: 0 }),
      channel({
        id: 2,
        status: CHANNEL_STATUS.MANUAL_DISABLED,
        response_time: 0,
      }),
    ])

    assert.equal(summary.neverTested, 1)
  })

  test('averages only tested channels and flags the very slow ones', () => {
    const summary = summarizeChannelHealth([
      channel({ id: 1, response_time: 200 }),
      channel({ id: 2, response_time: 400 }),
      // Never tested: must not drag the average down.
      channel({ id: 3, response_time: 0 }),
      channel({ id: 4, response_time: 9000 }),
    ])

    assert.equal(summary.averageResponseTimeMs, 3200)
    assert.equal(summary.slowOverThreshold, 1)
  })

  test('counts zero and negative balances', () => {
    const summary = summarizeChannelHealth([
      channel({ id: 1, balance: 0 }),
      channel({ id: 2, balance: -3 }),
      channel({ id: 3, balance: 12.5 }),
    ])

    assert.equal(summary.zeroBalance, 2)
  })

  test('skips tag aggregate rows so their children are not double counted', () => {
    const aggregate: TagRow = {
      ...channel({ id: 0, name: 'tag:prod' }),
      children: [channel({ id: 1 }), channel({ id: 2 })],
    }

    const summary = summarizeChannelHealth([aggregate])

    assert.equal(summary.sampled, 0)
    assert.equal(summary.healthPercent, null)
    assert.equal(summary.averageResponseTimeMs, null)
  })

  test('an empty fleet yields a neutral, division-safe summary', () => {
    const summary = summarizeChannelHealth([])

    assert.equal(summary.sampled, 0)
    assert.equal(summary.healthPercent, null)
    assert.equal(summary.averageResponseTimeMs, null)
  })
})
