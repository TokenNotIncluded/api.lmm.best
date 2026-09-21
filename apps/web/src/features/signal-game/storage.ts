/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import type { GameRecord } from './api'

const DATABASE = 'lmm-signal-game-v2'
async function database(): Promise<IDBDatabase> {
  if (typeof indexedDB === 'undefined') {
    throw new Error('Local records unavailable')
  }
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(DATABASE, 1)
    const timer = setTimeout(
      () => reject(new Error('Local records unavailable')),
      5000
    )
    request.onupgradeneeded = () => {
      request.result.createObjectStore('records', { keyPath: 'id' })
    }
    request.onsuccess = () => {
      clearTimeout(timer)
      resolve(request.result)
    }
    request.onerror = () => {
      clearTimeout(timer)
      reject(request.error)
    }
    request.onblocked = () => {
      clearTimeout(timer)
      reject(new Error('Local records unavailable'))
    }
  })
}
export async function readLocalRecords(): Promise<GameRecord[]> {
  const db = await database()
  try {
    return await new Promise((resolve, reject) => {
      const tx = db.transaction('records', 'readonly')
      const request = tx.objectStore('records').getAll()
      request.onsuccess = () =>
        resolve(
          (request.result as GameRecord[])
            .filter(
              (r) =>
                r &&
                typeof r.id === 'string' &&
                Array.isArray(r.actions) &&
                r.actions.length <= 65536
            )
            .sort((a, b) => b.created_at - a.created_at)
        )
      request.onerror = () => reject(request.error)
    })
  } finally {
    db.close()
  }
}
export async function putLocalRecord(record: GameRecord): Promise<void> {
  const db = await database()
  try {
    await new Promise<void>((resolve, reject) => {
      const tx = db.transaction('records', 'readwrite')
      tx.objectStore('records').put(record)
      tx.oncomplete = () => resolve()
      tx.onerror = () => reject(tx.error)
      tx.onabort = () => reject(tx.error)
    })
  } finally {
    db.close()
  }
}
