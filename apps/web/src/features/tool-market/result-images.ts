/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import { schemaObject } from './schema-form-utils'

// The drawing relay accepts up to 32 MiB, including encoded image data.
const maxImageBase64Bytes = 32 * 1024 * 1024

export type ResultImage = { data: string; mimeType: string }

export function resultImage(
  data: unknown,
  declaredType?: unknown
): ResultImage | null {
  if (
    typeof data !== 'string' ||
    !data.length ||
    data.length > maxImageBase64Bytes ||
    !/^[A-Za-z0-9+/=\r\n]+$/.test(data)
  ) {
    return null
  }
  const normalized = data.replaceAll(/[\r\n]/g, '')
  let prefix: string
  try {
    prefix = atob(normalized.slice(0, 24))
  } catch {
    return null
  }
  const mimeType = prefix.startsWith('\x89PNG\r\n\x1a\n')
    ? 'image/png'
    : prefix.startsWith('\xff\xd8\xff')
      ? 'image/jpeg'
      : prefix.startsWith('GIF87a') || prefix.startsWith('GIF89a')
        ? 'image/gif'
        : prefix.startsWith('RIFF') && prefix.slice(8, 12) === 'WEBP'
          ? 'image/webp'
          : null
  if (!mimeType || (declaredType !== undefined && declaredType !== mimeType)) {
    return null
  }
  return { data: normalized, mimeType }
}

// Keep provider metadata, but never expand image base64 into the JSON view.
export function drawingResultImages(value: unknown, imageLabel: string) {
  if (
    !schemaObject(value) ||
    !schemaObject(value.data) ||
    (!Array.isArray(value.data.data) && !schemaObject(value.data.data))
  ) {
    return { images: [] as ResultImage[], metadata: value }
  }
  const images: ResultImage[] = []
  const entries = Array.isArray(value.data.data)
    ? value.data.data
    : [value.data.data]
  const data = entries.map((entry: unknown) => {
    if (!schemaObject(entry) || typeof entry.b64_json !== 'string') return entry
    const image = resultImage(entry.b64_json)
    if (image) images.push(image)
    return { ...entry, b64_json: imageLabel }
  })
  return {
    images,
    metadata: {
      ...value,
      data: {
        ...value.data,
        data: Array.isArray(value.data.data) ? data : data[0],
      },
    },
  }
}
