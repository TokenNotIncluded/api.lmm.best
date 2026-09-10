/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { IDBFactory } from 'fake-indexeddb'

import {
  createDrawingHistoryStore,
  DRAWING_HISTORY_BYTES,
  DRAWING_HISTORY_LIMIT,
  retainDrawings,
  type StoredDrawing,
} from './history-storage'

function image(userId: number, id = 'image', createdAt = 1): StoredDrawing {
  return {
    userId,
    id,
    createdAt,
    prompt: 'private prompt',
    model: 'image-2',
    group: 'image-2',
    blob: new Blob(['generated pixels'], { type: 'image/png' }),
  }
}

test('stores actual Blob bytes and metadata across reopen, isolated by account', async () => {
  const factory = new IDBFactory()
  const store = createDrawingHistoryStore(() => factory)
  await store.save(1, 0, [image(1)])
  await store.save(2, 0, [image(2)])
  const reopened = createDrawingHistoryStore(() => factory)
  const first = await reopened.load(1)
  assert.equal(first.images.length, 1)
  assert.equal(first.images[0].userId, 1)
  assert.equal(first.images[0].prompt, 'private prompt')
  assert.equal(first.images[0].model, 'image-2')
  assert.equal(first.images[0].group, 'image-2')
  assert.equal(await first.images[0].blob.text(), 'generated pixels')
  assert.equal(first.images[0].blob.type, 'image/png')
  assert.equal((await reopened.load(3)).images.length, 0)
  await store.clear(1)
  assert.equal((await reopened.load(1)).images.length, 0)
  assert.equal((await reopened.load(2)).images.length, 1)
})

test('enforces bounded newest-first retention and rejects cross-account additions', async () => {
  assert.equal(DRAWING_HISTORY_LIMIT, 50)
  assert.equal(DRAWING_HISTORY_BYTES, 100 * 1024 * 1024)
  const store = createDrawingHistoryStore(() => newFactory)
  const newFactory = new IDBFactory()
  await store.save(1, 0, [
    ...Array.from({ length: DRAWING_HISTORY_LIMIT + 5 }, (_, index) =>
      image(1, `${index}`, index)
    ),
    image(2, 'foreign'),
  ])
  const data = await store.load(1)
  assert.equal(data.images.length, DRAWING_HISTORY_LIMIT)
  assert.equal(data.images[0].createdAt, DRAWING_HISTORY_LIMIT + 4)
  assert.equal(data.images.at(-1)?.createdAt, 5)
  assert.equal((await store.load(2)).images.length, 0)
  const budgeted = retainDrawings(
    [1, 2, 3].map((createdAt) => ({
      id: String(createdAt),
      createdAt,
      blob: { size: DRAWING_HISTORY_BYTES / 2 } as Blob,
    }))
  )
  assert.deepEqual(
    budgeted.map((entry) => entry.createdAt),
    [3, 2]
  )
})

test('clear is an atomic epoch barrier against late and cross-tab saves', async () => {
  const factory = new IDBFactory()
  const first = createDrawingHistoryStore(() => factory)
  const second = createDrawingHistoryStore(() => factory)
  const epoch = (await first.load(1)).epoch
  await Promise.all([
    first.save(1, epoch, [image(1, 'a')]),
    second.save(1, epoch, [image(1, 'b')]),
  ])
  assert.equal((await first.load(1)).images.length, 2)
  const nextEpoch = await second.clear(1)
  assert.equal(await first.save(1, epoch, [image(1, 'late')]), false)
  assert.equal((await first.load(1)).images.length, 0)
  assert.equal(await first.save(1, nextEpoch, [image(1, 'new')]), true)
  assert.deepEqual(
    (await first.load(1)).images.map((entry) => entry.id),
    ['new']
  )
})

test('unavailable IndexedDB rejects explicitly, without pretending to save', async () => {
  const store = createDrawingHistoryStore(() => {
    throw new Error('private browsing')
  })
  await assert.rejects(store.load(1), /private browsing/)
  await assert.rejects(store.save(1, 0, [image(1)]), /private browsing/)
  await assert.rejects(store.clear(1), /private browsing/)
})
