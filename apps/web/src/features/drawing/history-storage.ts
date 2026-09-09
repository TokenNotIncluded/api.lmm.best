/*
Copyright (C) 2026 LIghtJUNction
*/

// Per account, oldest first eviction. Browser site-data eviction can still remove
// this cache; it is not a server backup. No prompts or bytes enter localStorage.
export const DRAWING_HISTORY_LIMIT = 50
export const DRAWING_HISTORY_BYTES = 100 * 1024 * 1024
export const DRAWING_IMAGE_BYTES = 32 * 1024 * 1024

export type DrawingMetadata = {
  prompt: string
  model: string
  group: string
  createdAt: number
}

export type StoredDrawing = DrawingMetadata & {
  userId: number
  id: string
  revisedPrompt?: string
  blob: Blob
}

export type DrawingHistoryData = { epoch: number; images: StoredDrawing[] }

export interface DrawingHistoryStore {
  load(userId: number): Promise<DrawingHistoryData>
  save(userId: number, epoch: number, images: StoredDrawing[]): Promise<boolean>
  clear(userId: number): Promise<number>
}

export function retainDrawings<
  T extends { id: string; createdAt: number; blob?: Blob },
>(images: T[]): T[] {
  const ordered = [
    ...new Map(images.map((image) => [image.id, image])).values(),
  ].sort((a, b) => b.createdAt - a.createdAt || b.id.localeCompare(a.id))
  let bytes = 0
  return ordered.filter((image, index) => {
    bytes += image.blob?.size ?? 0
    return index < DRAWING_HISTORY_LIMIT && bytes <= DRAWING_HISTORY_BYTES
  })
}

export function createDrawingHistoryStore(
  factory: () => IDBFactory = () => globalThis.indexedDB,
  name = 'lmm-drawing-history'
): DrawingHistoryStore {
  const open = () =>
    new Promise<IDBDatabase>((resolve, reject) => {
      let request: IDBOpenDBRequest
      try {
        request = factory().open(name, 1)
      } catch (error) {
        reject(error)
        return
      }
      let blocked = false
      request.onupgradeneeded = () => {
        const db = request.result
        const images = db.createObjectStore('images', {
          keyPath: ['userId', 'id'],
        })
        images.createIndex('user', 'userId')
        db.createObjectStore('accounts', { keyPath: 'userId' })
      }
      request.onerror = () => reject(request.error)
      request.onblocked = () => {
        blocked = true
        reject(new Error('Drawing history database is blocked'))
      }
      request.onsuccess = () => {
        if (blocked) {
          request.result.close()
          return
        }
        request.result.onversionchange = () => request.result.close()
        resolve(request.result)
      }
    })

  // All reads/writes for one operation share a transaction. An account epoch is
  // a tombstone: a clear in another tab also rejects older in-flight saves.
  async function transaction<T>(
    mode: IDBTransactionMode,
    run: (tx: IDBTransaction, result: (value: T) => void) => void
  ): Promise<T> {
    const db = await open()
    return new Promise<T>((resolve, reject) => {
      let value: T
      let tx: IDBTransaction
      try {
        tx = db.transaction(['images', 'accounts'], mode)
        tx.oncomplete = () => {
          db.close()
          resolve(value)
        }
        tx.onabort = () => {
          db.close()
          reject(tx.error ?? new Error('Drawing history transaction aborted'))
        }
        tx.onerror = () => {
          /* onabort handles failures, including quota errors. */
        }
        run(tx, (result) => {
          value = result
        })
      } catch (error) {
        db.close()
        reject(error)
      }
    })
  }

  return {
    load: (userId) =>
      transaction('readonly', (tx, result) => {
        const account = tx.objectStore('accounts').get(userId)
        const images = tx.objectStore('images').index('user').getAll(userId)
        images.onsuccess = () =>
          result({
            epoch: account.result?.epoch ?? 0,
            images: retainDrawings(images.result),
          })
      }),
    save: (userId, epoch, additions) =>
      transaction('readwrite', (tx, result) => {
        const account = tx.objectStore('accounts').get(userId)
        const store = tx.objectStore('images')
        const images = store.index('user').getAll(userId)
        images.onsuccess = () => {
          if ((account.result?.epoch ?? 0) !== epoch) {
            result(false)
            return
          }
          const valid = additions.filter(
            (image) =>
              image.userId === userId &&
              image.blob.size > 0 &&
              image.blob.size <= DRAWING_IMAGE_BYTES
          )
          const retained = retainDrawings([...images.result, ...valid])
          const ids = new Set(retained.map((image) => image.id))
          for (const image of images.result as StoredDrawing[]) {
            if (!ids.has(image.id)) store.delete([userId, image.id])
          }
          for (const image of valid) {
            if (ids.has(image.id)) store.put(image)
          }
          result(true)
        }
      }),
    clear: (userId) =>
      transaction('readwrite', (tx, result) => {
        const accounts = tx.objectStore('accounts')
        const account = accounts.get(userId)
        const store = tx.objectStore('images')
        const keys = store.index('user').getAllKeys(userId)
        keys.onsuccess = () => {
          const epoch = (account.result?.epoch ?? 0) + 1
          for (const key of keys.result) store.delete(key)
          accounts.put({ userId, epoch })
          result(epoch)
        }
      }),
  }
}
