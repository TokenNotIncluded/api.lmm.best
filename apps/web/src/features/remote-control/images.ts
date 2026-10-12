/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
export type RemoteImage = {
  type: 'image'
  mimeType: 'image/jpeg'
  data: string
}
export const REMOTE_IMAGE_MAX_BYTES = 24 * 1024

export function validRemoteImages(images: unknown): images is RemoteImage[] {
  if (!Array.isArray(images) || images.length !== 1) return false
  const image = images[0]
  if (
    !image ||
    image.type !== 'image' ||
    image.mimeType !== 'image/jpeg' ||
    typeof image.data !== 'string' ||
    image.data.length > 32 * 1024 ||
    !/^[A-Za-z0-9+/]+={0,2}$/.test(image.data)
  ) {
    return false
  }
  try {
    const bytes = atob(image.data)
    return (
      bytes.length >= 4 &&
      bytes.length <= REMOTE_IMAGE_MAX_BYTES &&
      btoa(bytes) === image.data &&
      bytes.charCodeAt(0) === 255 &&
      bytes.charCodeAt(1) === 216 &&
      bytes.charCodeAt(2) === 255 &&
      bytes.charCodeAt(bytes.length - 2) === 255 &&
      bytes.charCodeAt(bytes.length - 1) === 217
    )
  } catch {
    return false
  }
}

/** Local-only conversion strips metadata; original files are never uploaded. */
export async function prepareRemoteImage(file: File): Promise<RemoteImage> {
  if (
    !['image/jpeg', 'image/png', 'image/webp'].includes(file.type) ||
    !file.size ||
    file.size > 10 * 1024 * 1024
  ) {
    throw new Error('Choose a PNG, JPEG or WebP image up to 10 MB.')
  }
  const url = URL.createObjectURL(file)
  try {
    const image = new Image()
    image.src = url
    await image.decode()
    if (
      !image.naturalWidth ||
      !image.naturalHeight ||
      image.naturalWidth * image.naturalHeight > 32_000_000
    ) {
      throw new Error('Unable to prepare this image. Choose a smaller image.')
    }
    const canvas = document.createElement('canvas')
    const context = canvas.getContext('2d')
    if (!context) {
      throw new Error('Unable to prepare this image. Choose a smaller image.')
    }
    for (const edge of [1536, 1024, 768, 512]) {
      const ratio = Math.min(
        1,
        edge / Math.max(image.naturalWidth, image.naturalHeight)
      )
      canvas.width = Math.max(1, Math.round(image.naturalWidth * ratio))
      canvas.height = Math.max(1, Math.round(image.naturalHeight * ratio))
      context.fillStyle = '#ffffff'
      context.fillRect(0, 0, canvas.width, canvas.height)
      context.drawImage(image, 0, 0, canvas.width, canvas.height)
      for (const quality of [0.82, 0.65, 0.45]) {
        const result: RemoteImage = {
          type: 'image',
          mimeType: 'image/jpeg',
          data: canvas.toDataURL('image/jpeg', quality).split(',')[1] ?? '',
        }
        if (validRemoteImages([result])) return result
      }
    }
    throw new Error('Unable to prepare this image. Choose a smaller image.')
  } catch {
    throw new Error('Unable to prepare this image. Choose a smaller image.')
  } finally {
    URL.revokeObjectURL(url)
  }
}
