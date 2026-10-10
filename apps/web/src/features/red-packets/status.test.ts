/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { redPacketStatus } from './status'
import type { RedPacketPublic } from './types'

const now = 1_800_000_000
const packet: RedPacketPublic = {
  slug: 'test',
  title: 'test',
  description: '',
  cover_image: '',
  draw_mode: 'random',
  per_user_limit: 1,
  start_at: 0,
  end_at: 0,
  enabled: true,
  total_items: 22,
  remaining_items: 17,
  claim_count: 5,
}

test('packet status identifies live, scheduled, paused, expired and exhausted packets', () => {
  const cases: Array<[Partial<RedPacketPublic>, string]> = [
    [{}, 'Live'],
    [{ start_at: now + 10 }, 'Scheduled'],
    [{ start_at: now }, 'Live'],
    [{ end_at: now + 1 }, 'Live'],
    [{ end_at: now }, 'Ended'],
    [{ end_at: now - 1 }, 'Ended'],
    [{ enabled: false }, 'Paused'],
    [{ total_items: 7, remaining_items: 0, claim_count: 7 }, 'Exhausted'],
  ]
  for (const [patch, status] of cases) {
    assert.equal(redPacketStatus({ ...packet, ...patch }, now), status)
  }
})
