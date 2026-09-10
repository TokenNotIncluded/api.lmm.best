/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { IDBFactory } from 'fake-indexeddb'

import { DrawingHistory } from './drawing-history'
import {
  createDrawingHistoryStore,
  type DrawingHistoryStore,
  type StoredDrawing,
} from './history-storage'

const metadata = {
  prompt: 'A private painting',
  model: 'image-2',
  group: 'image-2',
  createdAt: 1,
}
const generated = [
  { b64_json: btoa('generated pixels'), revised_prompt: 'A refined painting' },
]

async function loaded(history: DrawingHistory) {
  for (
    let attempt = 0;
    attempt < 100 && history.getSnapshot().loading;
    attempt++
  ) {
    await new Promise((resolve) => setTimeout(resolve, 1))
  }
  assert.equal(history.getSnapshot().loading, false)
}

function memoryStore() {
  const factory = new IDBFactory()
  return createDrawingHistoryStore(() => factory)
}

test('history rehydrates image bytes after refresh, appends instead of replacing and revokes object URLs', async () => {
  const store = memoryStore()
  const revoked: string[] = []
  const revoke = URL.revokeObjectURL
  URL.revokeObjectURL = (url) => {
    revoked.push(url)
    revoke(url)
  }
  const first = new DrawingHistory(1, store)
  const refreshed = new DrawingHistory(1, store)
  try {
    first.start()
    await loaded(first)
    await first.remember(generated, metadata, first.capture())
    const firstURL = first.getSnapshot().images[0].src
    assert.ok(firstURL.startsWith('blob:'))
    assert.equal(first.getSnapshot().images[0].saved, true)
    await first.remember(
      generated,
      { ...metadata, createdAt: 2, prompt: 'Second painting' },
      first.capture()
    )
    assert.equal(first.getSnapshot().images.length, 2)
    first.dispose()
    assert.ok(revoked.includes(firstURL))
    refreshed.start()
    await loaded(refreshed)
    assert.equal(refreshed.getSnapshot().images.length, 2)
    assert.equal(refreshed.getSnapshot().images[0].prompt, 'Second painting')
    assert.equal(
      await refreshed.getSnapshot().images[0].blob?.text(),
      'generated pixels'
    )
    assert.notEqual(refreshed.getSnapshot().images[1].src, firstURL)
    const urls = refreshed.getSnapshot().images.map((image) => image.src)
    await refreshed.clear()
    assert.equal((await store.load(1)).images.length, 0)
    assert.ok(urls.every((url) => revoked.includes(url)))
  } finally {
    first.dispose()
    refreshed.dispose()
    URL.revokeObjectURL = revoke
  }
})

test('storage quota failure keeps successful images downloadable and does not retry generation or save', async () => {
  let saves = 0
  const store: DrawingHistoryStore = {
    load: async () => ({ epoch: 0, images: [] }),
    save: async () => {
      saves++
      throw new DOMException('Full', 'QuotaExceededError')
    },
    clear: async () => {
      throw new Error('Blocked')
    },
  }
  const history = new DrawingHistory(1, store)
  history.start()
  await loaded(history)
  await history.remember(generated, metadata, history.capture())
  assert.equal(history.getSnapshot().images.length, 1)
  assert.equal(
    await history.getSnapshot().images[0].blob?.text(),
    'generated pixels'
  )
  assert.equal(history.getSnapshot().images[0].saved, false)
  assert.equal(history.getSnapshot().warning, 'save')
  assert.equal(saves, 1)
  await history.clear()
  assert.equal(history.getSnapshot().warning, 'clear')
  assert.equal(history.getSnapshot().clearing, false)
  history.dispose()
})

test('URL-only CORS failure stays visibly unsaved; successful URL download stores bytes', async () => {
  let downloads = 0
  const store = memoryStore()
  const failed = new DrawingHistory(1, store, async () => {
    downloads++
    throw new TypeError('CORS')
  })
  failed.start()
  await loaded(failed)
  await failed.remember(
    [{ url: 'https://image.example.test/expires.png' }],
    metadata,
    failed.capture()
  )
  assert.equal(
    failed.getSnapshot().images[0].src,
    'https://image.example.test/expires.png'
  )
  assert.equal(failed.getSnapshot().images[0].saved, false)
  assert.equal(failed.getSnapshot().warning, 'save')
  assert.equal((await store.load(1)).images.length, 0)
  assert.equal(downloads, 1)
  failed.dispose()
  const success = new DrawingHistory(
    1,
    store,
    async () => new Blob(['downloaded pixels'], { type: 'image/png' })
  )
  success.start()
  await loaded(success)
  await success.remember(
    [{ url: 'https://image.example.test/expires.png' }],
    metadata,
    success.capture()
  )
  assert.equal(success.getSnapshot().images[0].saved, true)
  assert.equal(
    await (await store.load(1)).images[0].blob.text(),
    'downloaded pixels'
  )
  success.dispose()
})

test('clear rejects late POST results and late URL fetches, even when fetch ignores abort', async () => {
  const store = memoryStore()
  let finish: (value: Blob) => void = () => {
    throw new Error('Fetch not started')
  }
  const history = new DrawingHistory(
    1,
    store,
    () =>
      new Promise((resolve) => {
        finish = resolve
      })
  )
  history.start()
  await loaded(history)
  const ticket = history.capture()
  const pending = history.remember(
    [{ url: 'https://image.example.test/expires.png' }],
    metadata,
    ticket
  )
  await history.clear()
  finish(new Blob(['late pixels'], { type: 'image/png' }))
  await pending
  await history.remember(generated, metadata, ticket)
  assert.equal(history.getSnapshot().images.length, 0)
  assert.equal((await store.load(1)).images.length, 0)
  history.dispose()
})

test('logout/account switch rejects late results and never loads another user history', async () => {
  const store = memoryStore()
  const first = new DrawingHistory(1, store)
  first.start()
  await loaded(first)
  await first.remember(generated, metadata, first.capture())
  const ticket = first.capture()
  first.dispose()
  await first.remember(generated, metadata, ticket)
  const second = new DrawingHistory(2, store)
  second.start()
  await loaded(second)
  assert.equal(second.getSnapshot().images.length, 0)
  assert.equal((await store.load(1)).images.length, 1)
  second.dispose()
})

test('clear during initial load cannot resurrect cached images; failed load allows memory fallback', async () => {
  const image: StoredDrawing = {
    ...metadata,
    id: 'old',
    userId: 1,
    blob: new Blob(['old pixels'], { type: 'image/png' }),
  }
  let finish: (value: {
    epoch: number
    images: StoredDrawing[]
  }) => void = () => {
    throw new Error('Load not started')
  }
  const history = new DrawingHistory(1, {
    load: () =>
      new Promise((resolve) => {
        finish = resolve
      }),
    save: async () => true,
    clear: async () => 1,
  })
  history.start()
  await history.clear()
  finish({ epoch: 0, images: [image] })
  await new Promise((resolve) => setTimeout(resolve, 1))
  assert.equal(history.getSnapshot().images.length, 0)
  history.dispose()
  const fallback = new DrawingHistory(1, {
    load: async () => {
      throw new Error('Storage unavailable')
    },
    save: async () => {
      throw new Error('Must not try saving with an unknown epoch')
    },
    clear: async () => 1,
  })
  fallback.start()
  await loaded(fallback)
  assert.equal(fallback.getSnapshot().warning, 'load')
  await fallback.remember(generated, metadata, fallback.capture())
  assert.equal(fallback.getSnapshot().images.length, 1)
  assert.equal(fallback.getSnapshot().images[0].saved, false)
  assert.equal(fallback.getSnapshot().warning, 'save')
  fallback.dispose()
})
