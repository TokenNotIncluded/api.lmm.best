/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
/*
Copyright (C) 2026 LIghtJUNction
*/
import { getGravatarUrl } from './avatar'

const CACHE_NAME = 'lmm:avatars:v1'
const EXPIRES_HEADER = 'x-lmm-avatar-expires'
const IMAGE_TTL = 24 * 60 * 60 * 1000
const MISSING_TTL = 60 * 60 * 1000
const MAX_ENTRIES = 32
const MAX_IMAGE_BYTES = 512 * 1024
type AvatarImage = Blob | string | null
const pending = new Map<string, Promise<AvatarImage>>()

async function loadImage(url: string): Promise<AvatarImage> {
  let cache: Cache | undefined
  try {
    cache = await globalThis.caches?.open(CACHE_NAME)
    const cached = await cache?.match(url)
    if (cached && Number(cached.headers.get(EXPIRES_HEADER)) > Date.now()) {
      if (cached.status === 404) return null
      if (cached.ok) {
        const blob = await cached.blob()
        if (
          blob.type.startsWith('image/') &&
          blob.size > 0 &&
          blob.size <= MAX_IMAGE_BYTES
        ) {
          return blob
        }
      }
    }
  } catch {
    // Private browsing and storage policies must not prevent avatar loading.
  }

  // Preserve ordinary <img> loading when persistent storage is unsupported.
  if (!cache) return url

  let response: Response
  let blob: Blob | null = null
  try {
    response = await fetch(url, {
      credentials: 'omit',
      referrerPolicy: 'no-referrer',
      cache: 'no-cache',
      signal: AbortSignal.timeout(8000),
    })
    if (response.status !== 404) {
      if (!response.ok) return null
      blob = await response.blob()
      if (!blob.type.startsWith('image/') || blob.size === 0) return null
      if (blob.size > MAX_IMAGE_BYTES) return blob
    }
  } catch {
    // A direct image may still load when CORS blocks reading its response.
    return url
  }

  try {
    await cache.put(
      url,
      new Response(blob, {
        status: blob ? 200 : 404,
        headers: {
          [EXPIRES_HEADER]: String(
            Date.now() + (blob ? IMAGE_TTL : MISSING_TTL)
          ),
        },
      })
    )
    const keys = await cache.keys()
    await Promise.all(
      keys
        .slice(0, Math.max(0, keys.length - MAX_ENTRIES))
        .map((key) => cache.delete(key))
    )
  } catch {
    // A full cache must not discard the image we have already downloaded.
  }
  return blob
}

/** Cache image bytes across reloads and share simultaneous requests by URL. */
export async function getGravatarImage(
  email: string | null | undefined,
  size = 192
): Promise<AvatarImage> {
  const url = await getGravatarUrl(email, size)
  if (!url) return null
  let request = pending.get(url)
  if (!request) {
    request = loadImage(url).finally(() => pending.delete(url))
    pending.set(url, request)
  }
  return request
}
