/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { afterEach, test } from 'node:test'

import { api } from '@/lib/api'

import {
  AssistantRequestError,
  claimAssistantNewUserGift,
  getAssistantNewUserGift,
} from './api'
import { filterAssistantPreConversationPresets } from './assistant-prompt-presets'

const originalGet = api.get
const originalPost = api.post
afterEach(() => {
  api.get = originalGet
  api.post = originalPost
})
const receipt = {
  already_claimed: false,
  gift: {
    amount_cents: 20,
    quota: 100000,
    status: 'claimed',
    reason: 'Stored offer',
    created_at: 1,
    claimed_at: 2,
  },
}

test('a disabled gift capability removes both server and fallback prompts', () => {
  for (const presets of [
    undefined,
    [{ id: 'new_user_gift', prompt: 'Ask for a gift', label: 'Gift' }],
  ]) {
    assert.ok(
      !filterAssistantPreConversationPresets(
        presets,
        { role: 1 },
        { new_user_gift: false }
      ).some((preset) => preset.id === 'new_user_gift')
    )
  }
  assert.ok(
    filterAssistantPreConversationPresets(undefined, { role: 1 }).some(
      (preset) => preset.id === 'new_user_gift'
    )
  )
  assert.ok(
    filterAssistantPreConversationPresets(
      undefined,
      { role: 1 },
      { new_user_gift: true }
    ).some((preset) => preset.id === 'new_user_gift')
  )
})
test('gift reads preserve explicit unavailable codes and cancellation signals', async () => {
  const signal = new AbortController().signal
  api.get = (async (_url: string, options: { signal?: AbortSignal }) => {
    assert.equal(options.signal, signal)
    throw Object.assign(new Error('HTTP 503'), {
      isAxiosError: true,
      response: {
        status: 503,
        data: { code: 'ASSISTANT_GIFT_UNAVAILABLE', message: 'Not installed' },
      },
    })
  }) as typeof api.get
  await assert.rejects(
    getAssistantNewUserGift(signal),
    (error) =>
      error instanceof AssistantRequestError &&
      error.code === 'ASSISTANT_GIFT_UNAVAILABLE'
  )
})
test('claim success requires a completed stored receipt, not merely HTTP success', async () => {
  for (const data of [
    null,
    {},
    { ...receipt, already_claimed: undefined },
    { ...receipt, gift: { ...receipt.gift, status: 'offered' } },
    { ...receipt, gift: { ...receipt.gift, claimed_at: 0 } },
    { ...receipt, gift: { ...receipt.gift, credit_amount: 0 } },
    { ...receipt, gift: { ...receipt.gift, quota: 0.5 } },
  ]) {
    api.post = (async () => ({
      data: { success: true, data },
    })) as typeof api.post
    await assert.rejects(
      claimAssistantNewUserGift(),
      (error) =>
        error instanceof AssistantRequestError &&
        error.code === 'ASSISTANT_GIFT_UNCONFIRMED'
    )
  }
  for (const already_claimed of [false, true]) {
    api.post = (async () => ({
      data: { success: true, data: { ...receipt, already_claimed } },
    })) as typeof api.post
    assert.equal(
      (await claimAssistantNewUserGift()).already_claimed,
      already_claimed
    )
  }
})
test('a lost claim response is uncertain and is never automatically repeated', async () => {
  let calls = 0
  api.post = (async () => {
    calls++
    throw Object.assign(new Error('Network error'), { isAxiosError: true })
  }) as typeof api.post
  await assert.rejects(
    claimAssistantNewUserGift(),
    (error) =>
      error instanceof AssistantRequestError &&
      error.code === 'ASSISTANT_GIFT_UNCONFIRMED'
  )
  assert.equal(calls, 1)
})
