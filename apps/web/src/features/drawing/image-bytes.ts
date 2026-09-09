/*
Copyright (C) 2026 LIghtJUNction
*/
import { DRAWING_IMAGE_BYTES } from './history-storage'

export type GeneratedDrawing = {
  url?: string
  b64_json?: string
  revised_prompt?: string
}

const rasterTypes = new Set([
  'image/png',
  'image/jpeg',
  'image/webp',
  'image/gif',
])

export function safeDrawingURL(value?: string): string | undefined {
  if (!value) return undefined
  try {
    const url = new URL(value)
    if (url.protocol !== 'https:' || url.username || url.password) {
      return undefined
    }
    return url.href
  } catch {
    return undefined
  }
}

export function drawingSource(image: GeneratedDrawing): string | undefined {
  if (image.b64_json?.trim()) {
    return `data:image/png;base64,${image.b64_json.trim()}`
  }
  return safeDrawingURL(image.url)
}

export function drawingBase64Blob(value: string): Blob {
  if (value.length > Math.ceil(DRAWING_IMAGE_BYTES / 3) * 4) {
    throw new Error('Drawing image exceeds browser cache limit')
  }
  const binary = atob(value.trim())
  if (!binary.length) throw new Error('Empty drawing image')
  const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0))
  let type = 'image/png'
  if (bytes[0] === 0xff && bytes[1] === 0xd8) type = 'image/jpeg'
  else if (binary.startsWith('GIF8')) type = 'image/gif'
  else if (binary.startsWith('RIFF') && binary.slice(8, 12) === 'WEBP') {
    type = 'image/webp'
  }
  return new Blob([bytes], { type })
}

// This fetch never uses the authenticated API client. Signed URL fetches omit
// cookies, bearer headers and referrers; CORS/redirect failures remain explicit.
export async function fetchDrawingBlob(
  source: string,
  signal: AbortSignal,
  fetcher: typeof fetch = fetch
): Promise<Blob> {
  const url = safeDrawingURL(source)
  if (!url) throw new Error('Unsafe drawing image URL')
  const controller = new AbortController()
  const abort = () => controller.abort()
  signal.addEventListener('abort', abort, { once: true })
  if (signal.aborted) abort()
  const timeout = setTimeout(abort, 15_000)
  let reader: ReadableStreamDefaultReader<Uint8Array> | undefined
  try {
    const response = await fetcher(url, {
      signal: controller.signal,
      mode: 'cors',
      credentials: 'omit',
      referrerPolicy: 'no-referrer',
      redirect: 'error',
    })
    const type = response.headers
      .get('content-type')
      ?.split(';')[0]
      .trim()
      .toLowerCase()
    if (!response.ok || !type || !rasterTypes.has(type) || !response.body) {
      throw new Error('Drawing URL could not be cached as an image')
    }
    if (Number(response.headers.get('content-length')) > DRAWING_IMAGE_BYTES) {
      throw new Error('Drawing image exceeds browser cache limit')
    }
    reader = response.body.getReader()
    const chunks: BlobPart[] = []
    let bytes = 0
    while (true) {
      const chunk = await reader.read()
      if (chunk.done) break
      bytes += chunk.value.byteLength
      if (bytes > DRAWING_IMAGE_BYTES) {
        throw new Error('Drawing image exceeds browser cache limit')
      }
      chunks.push(new Uint8Array(chunk.value))
    }
    if (!bytes) throw new Error('Empty drawing image')
    return new Blob(chunks, { type })
  } finally {
    controller.abort()
    await reader?.cancel().catch(() => undefined)
    clearTimeout(timeout)
    signal.removeEventListener('abort', abort)
  }
}
