/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import type { SupportAssistantContext } from './support-api'
import {
  parseSupportSearch,
  storeSupportAssistantPrompt,
  storeSupportHref,
  supportMessageAttempt,
} from './support-helpers'

test('support search rejects invalid IDs and unrecognized roles', () => {
  assert.deepEqual(
    parseSupportSearch({
      role: 'admin',
      tab: 'delete',
      order: 'a?token=secret',
      conversation: '../another',
      product: 'x'.repeat(37),
    }),
    {
      role: 'buyer',
      tab: 'messages',
      order: undefined,
      conversation: undefined,
      product: undefined,
    }
  )
  assert.equal(
    parseSupportSearch({
      order: 'valid-order-12',
      role: 'seller',
      tab: 'customers',
    }).order,
    'valid-order-12'
  )
  assert.equal(
    storeSupportHref({ order: 'order-1', role: 'seller' }),
    '/store/support?order=order-1&role=seller'
  )
})

test('lost-response retries retain their key but changed messages do not', () => {
  const first = supportMessageAttempt({ text: ' hello ' }, () => 'first-key')
  assert.deepEqual(first, { body: 'hello', key: 'first-key' })
  assert.equal(
    supportMessageAttempt({ text: 'hello', attempt: first }, () => 'unused'),
    first
  )
  assert.deepEqual(
    supportMessageAttempt(
      { text: 'changed', attempt: first },
      () => 'next-key'
    ),
    { body: 'changed', key: 'next-key' }
  )
  assert.throws(() => supportMessageAttempt({ text: ' ' }))
  assert.throws(() => supportMessageAttempt({ text: '字'.repeat(4001) }))
})

test('assistant preview uses a fresh allowlist, bounded context and key redaction', () => {
  const secret = `sk-proj-${'a'.repeat(100)}`
  const context = {
    role: 'seller',
    conversation_id: 'conversation-1',
    subject: 'A product',
    note: 'PRIVATE-NOTE',
    pickup_url: 'PRIVATE-LINK',
    order: {
      id: 'order-1',
      status: 'paid',
      quantity: 1,
      ciphertext: 'PRIVATE-CIPHERTEXT',
    },
    messages: Array.from({ length: 12 }, (_, index) => ({
      role: 'buyer',
      text: index === 11 ? secret : `message-${index} ${'x'.repeat(600)}`,
      internal_note: 'PRIVATE-MESSAGE-NOTE',
    })),
  } as unknown as SupportAssistantContext
  const prompt = storeSupportAssistantPrompt(context, 'reply', 'zh')
  for (const hidden of [
    'PRIVATE-NOTE',
    'PRIVATE-LINK',
    'PRIVATE-CIPHERTEXT',
    'PRIVATE-MESSAGE-NOTE',
    secret,
    'message-0 ',
    'message-1 ',
    'x'.repeat(501),
  ]) {
    assert.equal(prompt.includes(hidden), false, hidden)
  }
  assert.ok(prompt.includes('供我检查的草稿'))
  assert.ok(prompt.includes('不要发送消息'))
  assert.ok(prompt.includes('paid'))
})

test('assistant prompts distinguish buyers from sellers and task intent', () => {
  const context: SupportAssistantContext = {
    role: 'buyer',
    conversation_id: 'c',
    subject: 'Product',
    messages: [],
  }
  assert.ok(
    storeSupportAssistantPrompt(context, 'summary', 'en').includes(
      'I am the buyer'
    )
  )
  assert.ok(
    storeSupportAssistantPrompt(context, 'summary', 'en').includes(
      'agreed facts'
    )
  )
  assert.ok(
    storeSupportAssistantPrompt(
      { ...context, role: 'seller' },
      'help',
      'zh'
    ).includes('可核实的处理步骤')
  )
})
