/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { canDeleteRedPacket, redPacketStatus } from './status'
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

test('only paused, expired, or exhausted cards offer deletion', () => {
  const cases: Array<[Partial<RedPacketPublic>, string, boolean]> = [
    [{}, 'Live', false],
    [{ start_at: now + 10 }, 'Scheduled', false],
    [{ start_at: now }, 'Live', false],
    [{ end_at: now + 1 }, 'Live', false],
    [{ end_at: now }, 'Ended', true],
    [{ end_at: now - 1 }, 'Ended', true],
    [{ enabled: false }, 'Paused', true],
    [{ total_items: 7, remaining_items: 0, claim_count: 7 }, 'Exhausted', true],
  ]
  for (const [patch, status, removable] of cases) {
    assert.equal(redPacketStatus({ ...packet, ...patch }, now), status)
    assert.equal(canDeleteRedPacket({ ...packet, ...patch }, now), removable)
  }
})
