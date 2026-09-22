/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  createL0ChatSession,
  type CloudSender,
  type CloudReply,
} from './l0-chat-session'
import { insertedTokens, visualTokens } from './l0-text-flow'

const wait = (ms = 30) => new Promise((resolve) => setTimeout(resolve, ms))
function fixture() {
  const calls: Array<{
    input: Parameters<CloudSender>[0]
    handlers: Parameters<CloudSender>[1]
    signal: AbortSignal
    resolve: (reply: CloudReply) => void
    reject: (error: Error) => void
  }> = []
  const session = createL0ChatSession(
    (input, handlers, signal) =>
      new Promise((resolve, reject) => {
        calls.push({ input, handlers, signal, resolve, reject })
      })
  )
  return { calls, session }
}

test('Chinese, combined emoji and accents preserve complete graphemes and offsets', () => {
  const text = '字👩🏽‍💻é🇨🇳\n[]'
  const tokens = visualTokens(text)
  assert.deepEqual(
    tokens.map((token) => token.text),
    ['字', '👩🏽‍💻', 'é', '🇨🇳', '\n', '[', ']']
  )
  assert.equal(
    tokens
      .map((token) => text.slice(token.index, token.index + token.text.length))
      .join(''),
    text
  )
})

test('composition commit, middle edits, paste and delete produce only inserted graphemes', () => {
  assert.deepEqual(
    insertedTokens('你', '你好').map((t) => t.text),
    ['好']
  )
  assert.deepEqual(
    insertedTokens('你好吗', '你还好吗').map((t) => t.text),
    ['还']
  )
  assert.deepEqual(insertedTokens('你好', '你'), [])
  assert.deepEqual(insertedTokens('你好', '你好'), [])
  assert.equal(
    insertedTokens('', '中文👩🏽‍💻')
      .map((t) => t.text)
      .join(''),
    '中文👩🏽‍💻'
  )
})

test('a preset begins transport immediately and repeated clicks do not duplicate requests', () => {
  const { session, calls } = fixture()
  assert.equal(session.send('帮我选模型'), true)
  assert.equal(calls.length, 1)
  assert.equal(calls[0].input.message, '帮我选模型')
  assert.equal(session.send('Second click'), false)
  assert.equal(calls.length, 1)
  session.stop()
})

test('no response is invented before deltas arrive; exact text survives chunk boundaries', async () => {
  const { session, calls } = fixture()
  session.send('Hello')
  assert.equal(session.snapshot.answer, '')
  assert.equal(session.snapshot.phase, 'waiting')
  for (const delta of ['你', '好', '👩', '🏽‍', '💻', '\n', 'code']) {
    calls[0].handlers.onDelta(delta)
  }
  await wait()
  assert.equal(session.snapshot.answer, '你好👩🏽‍💻\ncode')
  calls[0].resolve({ content: '你好👩🏽‍💻\ncode' })
  await wait()
  assert.equal(session.snapshot.phase, 'done')
})

test('retry reset invalidates stale visual output and the final response is authoritative', async () => {
  const { session, calls } = fixture()
  session.send('Question')
  const revision = session.snapshot.revision
  calls[0].handlers.onDelta('old')
  calls[0].handlers.onReset()
  assert.equal(session.snapshot.answer, '')
  assert.equal(session.snapshot.revision, revision + 1)
  calls[0].handlers.onDelta('fresh')
  calls[0].resolve({ content: 'fresh final' })
  await wait()
  assert.equal(session.snapshot.answer, 'fresh final')
})

test('stop cancels transport, preserves partial text and ignores late callbacks', async () => {
  const { session, calls } = fixture()
  session.send('Question')
  calls[0].handlers.onDelta('partial')
  session.stop()
  assert.equal(calls[0].signal.aborted, true)
  calls[0].handlers.onDelta('late')
  calls[0].resolve({ content: 'wrong final' })
  await wait()
  assert.equal(session.snapshot.answer, 'partial')
  assert.equal(session.snapshot.phase, 'stopped')
})

test('subscriber cleanup aborts and suppresses old-session output', async () => {
  const { session, calls } = fixture()
  const changes: string[] = []
  const unsubscribe = session.subscribe((state) => changes.push(state.answer))
  session.send('Question')
  unsubscribe()
  const count = changes.length
  calls[0].handlers.onDelta('private late output')
  calls[0].resolve({ content: 'private late output' })
  await wait()
  assert.equal(calls[0].signal.aborted, true)
  assert.equal(changes.length, count)
})

test('strict-mode resubscription can send again without duplicate listeners', async () => {
  const { session, calls } = fixture()
  session.subscribe(() => {})()
  const unsubscribe = session.subscribe(() => {})
  assert.equal(session.send('Question'), true)
  assert.equal(calls.length, 1)
  calls[0].resolve({ content: 'Answer' })
  await wait()
  assert.equal(session.snapshot.phase, 'done')
  unsubscribe()
})

test('rapid stream updates are batched, not held for an animation queue', async () => {
  const { session, calls } = fixture()
  let updates = 0
  const unsubscribe = session.subscribe(() => updates++)
  session.send('Question')
  for (let i = 0; i < 500; i++) calls[0].handlers.onDelta('x')
  await wait()
  assert.equal(session.snapshot.answer.length, 500)
  assert.ok(updates < 10)
  unsubscribe()
})

test('server errors keep the partial answer without inventing successful completion', async () => {
  const { session, calls } = fixture()
  session.send('Question')
  calls[0].handlers.onDelta('partial')
  calls[0].reject(new Error('Disconnected'))
  await wait()
  assert.equal(session.snapshot.phase, 'error')
  assert.equal(session.snapshot.answer, 'partial')
})

test('conversation ID and bounded history continue a dialogue; clear starts a new one', async () => {
  const { session, calls } = fixture()
  session.send('First')
  calls[0].resolve({ content: 'Answer', conversationId: 42 })
  await wait()
  session.send('Second')
  assert.equal(calls[1].input.conversationId, 42)
  assert.deepEqual(calls[1].input.history, [
    { role: 'user', content: 'First' },
    { role: 'assistant', content: 'Answer' },
  ])
  session.clear()
  session.send('New')
  assert.equal(calls[2].input.conversationId, undefined)
  assert.deepEqual(calls[2].input.history, [])
  session.stop()
})

test('restricted replies cannot persist visible content or carry history forward', async () => {
  const { session, calls } = fixture()
  session.send('Question')
  calls[0].resolve({
    content: 'restricted',
    conversationId: 42,
    restricted: true,
  })
  await wait()
  assert.equal(session.snapshot.answer, '')
  assert.equal(session.snapshot.phase, 'error')
  session.send('Next')
  assert.equal(calls[1].input.conversationId, undefined)
  session.stop()
})

test('previous turns remain visible and immutable while the next response streams', async () => {
  const { session, calls } = fixture()
  session.send('First')
  calls[0].resolve({ content: 'First answer', conversationId: 42 })
  await wait()
  session.send('Second')
  const turns = session.snapshot.turns
  assert.equal(turns.length, 1)
  assert.equal(turns[0].question, 'First')
  assert.equal(turns[0].answer, 'First answer')
  calls[1].handlers.onDelta('Second answer')
  assert.equal(session.snapshot.answer, 'Second answer')
  assert.equal(session.snapshot.turns, turns)
  session.clear()
  assert.deepEqual(session.snapshot.turns, [])
})

test('stopped partial answers stay readable but are not sent as completed context', () => {
  const { session, calls } = fixture()
  session.send('First')
  calls[0].handlers.onDelta('Partial')
  session.stop()
  session.send('Second')
  assert.equal(session.snapshot.turns[0].phase, 'stopped')
  assert.equal(session.snapshot.turns[0].answer, 'Partial')
  assert.deepEqual(calls[1].input.history, [])
  session.stop()
})

test('manual retry reuses the same idempotency key without duplicating the visible turn', async () => {
  const { session, calls } = fixture()
  session.send('Configured question', 'custom-preset')
  assert.equal(calls[0].input.presetId, 'custom-preset')
  calls[0].reject(new Error('offline'))
  await wait()
  assert.equal(session.retry(), true)
  assert.equal(calls[1].input.turnId, calls[0].input.turnId)
  assert.equal(calls[1].input.replay, true)
  assert.equal(session.snapshot.turns.length, 0)
  assert.equal(session.retry(), false)
  session.stop()
})

test('restricted responses clear earlier visible history as well as request history', async () => {
  const { session, calls } = fixture()
  session.send('First')
  calls[0].resolve({ content: 'Private earlier reply', conversationId: 42 })
  await wait()
  session.send('Second')
  calls[1].resolve({ content: 'Restricted', restricted: true })
  await wait()
  assert.deepEqual(session.snapshot.turns, [])
  assert.equal(session.snapshot.question, '')
  assert.equal(session.retry(), false)
})

test('the in-memory transcript remains bounded over many turns', async () => {
  const { session, calls } = fixture()
  for (let n = 0; n < 32; n++) {
    session.send(`Question ${n}`)
    calls[n].resolve({ content: `Answer ${n}` })
    await Promise.resolve()
  }
  assert.equal(session.snapshot.turns.length, 24)
  assert.ok(calls.at(-1)!.input.history.length <= 12)
})
