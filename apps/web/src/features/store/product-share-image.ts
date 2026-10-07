/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { safeStoreMediaUrl, STORE_SVG_DATA_PREFIX } from './product-media'
import {
  isPublicStoreShareProduct,
  type PublicStoreShareProduct,
} from './product-x-share'

const IMAGE_MAX_BYTES = 10 * 1024 * 1024
const IMAGE_MAX_DIMENSION = 4096
const IMAGE_MAX_PIXELS = 4096 * 4096
const imageTypes = new Map([
  ['image/png', 'png'],
  ['image/jpeg', 'jpg'],
  ['image/webp', 'webp'],
  ['image/gif', 'gif'],
])

export function storeProductShareImageUrl(product: PublicStoreShareProduct) {
  if (
    !isPublicStoreShareProduct(product) ||
    !/^[A-Za-z0-9-]{1,64}$/.test(product.id)
  ) {
    return undefined
  }
  const source = safeStoreMediaUrl(product.image_urls?.[0])
  if (source?.startsWith(STORE_SVG_DATA_PREFIX)) return source
  if (!source) return undefined
  const url = new URL(source)
  return url.protocol === 'https:' && !url.username && !url.password
    ? url.href
    : undefined
}

async function svgImageToPNG(source: string, signal?: AbortSignal) {
  const image = new Image()
  await new Promise<void>((resolve, reject) => {
    const cleanup = () => signal?.removeEventListener('abort', abort)
    const abort = () => {
      cleanup()
      image.onload = null
      image.onerror = null
      image.src = ''
      reject(new DOMException('Aborted', 'AbortError'))
    }
    image.onload = () => {
      cleanup()
      resolve()
    }
    image.onerror = () => {
      cleanup()
      reject(new Error('Image download unavailable'))
    }
    if (signal?.aborted) return abort()
    signal?.addEventListener('abort', abort, { once: true })
    image.src = source
  })
  const width = image.naturalWidth
  const height = image.naturalHeight
  if (
    !width ||
    !height ||
    width > IMAGE_MAX_DIMENSION ||
    height > IMAGE_MAX_DIMENSION ||
    width * height > IMAGE_MAX_PIXELS
  ) {
    throw new Error('Image download unavailable')
  }
  const canvas = document.createElement('canvas')
  canvas.width = width
  canvas.height = height
  const context = canvas.getContext('2d')
  if (!context) throw new Error('Image download unavailable')
  context.drawImage(image, 0, 0, width, height)
  return new Promise<Blob>((resolve, reject) => {
    canvas.toBlob((blob) => {
      if (blob && blob.size <= IMAGE_MAX_BYTES && !signal?.aborted) {
        resolve(blob)
      } else reject(new Error('Image download unavailable'))
    }, 'image/png')
  })
}

async function boundedImageBody(response: Response) {
  if (
    !response.ok ||
    Number(response.headers.get('content-length')) > IMAGE_MAX_BYTES ||
    !response.body
  ) {
    await response.body?.cancel()
    throw new Error('Image download unavailable')
  }
  const reader = response.body.getReader()
  const parts: Uint8Array<ArrayBuffer>[] = []
  let size = 0
  try {
    for (;;) {
      const { value, done } = await reader.read()
      if (done) break
      size += value.byteLength
      if (size > IMAGE_MAX_BYTES) {
        await reader.cancel()
        throw new Error('Image download unavailable')
      }
      // Own the buffer rather than retaining a possibly shared stream buffer.
      parts.push(new Uint8Array(value))
    }
  } finally {
    reader.releaseLock()
  }
  if (!size) throw new Error('Image download unavailable')
  return parts
}

export async function prepareStoreProductShareImage(
  product: PublicStoreShareProduct,
  signal?: AbortSignal,
  fetchImage: typeof fetch = fetch
) {
  const source = storeProductShareImageUrl(product)
  if (!source) throw new Error('Image download unavailable')
  if (source.startsWith(STORE_SVG_DATA_PREFIX)) {
    const blob = await svgImageToPNG(source, signal)
    return { blob, filename: `product-${product.id}.png` }
  }
  const response = await fetchImage(source, {
    credentials: 'omit',
    referrerPolicy: 'no-referrer',
    signal,
  })
  const type =
    response.headers.get('content-type')?.split(';')[0].trim().toLowerCase() ??
    ''
  const extension = imageTypes.get(type)
  if (!extension) {
    await response.body?.cancel()
    throw new Error('Image download unavailable')
  }
  const parts = await boundedImageBody(response)
  return {
    blob: new Blob(parts, { type }),
    filename: `product-${product.id}.${extension}`,
  }
}

export function downloadStoreProductShareImage(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = filename
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
  // Download consumers may resolve the URL after the click handler has ended.
  window.setTimeout(() => URL.revokeObjectURL(url), 1000)
}
