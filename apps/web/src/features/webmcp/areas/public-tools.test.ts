/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { publicToolsTools } from './public-tools'

const tools = publicToolsTools({
  router: { navigate: async () => undefined, subscribe: () => () => undefined },
})
const options = { signal: new AbortController().signal }

function tool(name: string) {
  const found = tools.find((entry) => entry.name === name)
  assert.ok(found)
  return found
}

test('red packet claims require confirmation and never return a redemption secret', async () => {
  const auth = useAuthStore.getState().auth
  const post = api.post
  const secret = 'private-redemption-code-123456'
  let calls = 0
  api.post = (async () => {
    calls += 1
    return {
      data: {
        success: true,
        data: {
          code: secret,
          name: 'Credit',
          item_type: 'redemption',
          quota: 100,
        },
      },
    }
  }) as typeof api.post
  useAuthStore.setState({
    auth: { ...auth, user: { id: 1, username: 'test' } as never },
  })
  try {
    await assert.rejects(
      tool('lmm_red_packet_open').execute({ slug: 'gift' }, options),
      /confirm: true/
    )
    await assert.rejects(
      tool('lmm_red_packet_open').execute(
        { slug: 'gift', confirm: false },
        options
      ),
      /confirm: true/
    )
    assert.equal(calls, 0)
    const result = await tool('lmm_red_packet_open').execute(
      { slug: 'gift', confirm: true },
      options
    )
    assert.equal(calls, 1)
    assert.doesNotMatch(JSON.stringify(result), new RegExp(secret))
    assert.deepEqual((result as { reward: unknown }).reward, {
      type: 'redemption',
      name: 'Credit',
      code_masked: '••••',
      quota: 100,
      discount_percent: null,
    })
    assert.equal(
      tool('lmm_red_packet_open').annotations?.untrustedContentHint,
      true
    )
  } finally {
    api.post = post
    useAuthStore.setState({ auth })
  }
})

test('red packet reads respect scheduled and expired windows', async () => {
  const get = api.get
  const now = Math.floor(Date.now() / 1000)
  let packet = {
    slug: 'gift',
    enabled: true,
    remaining_items: 2,
    start_at: now + 3600,
    end_at: 0,
  }
  api.get = (async () => ({
    data: { success: true, data: packet },
  })) as typeof api.get
  try {
    const read = async () =>
      (await tool('lmm_red_packet_read').execute(
        { slug: 'gift' },
        options
      )) as { open: boolean; starts_at: string | null; ends_at: string | null }
    assert.equal((await read()).open, false)
    packet = { ...packet, start_at: 0, end_at: now - 1 }
    assert.equal((await read()).open, false)
    packet = { ...packet, end_at: 0 }
    assert.deepEqual(
      await read().then(({ open, starts_at, ends_at }) => ({
        open,
        starts_at,
        ends_at,
      })),
      { open: true, starts_at: null, ends_at: null }
    )
  } finally {
    api.get = get
  }
})
