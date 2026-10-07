/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { normalizeStoreImageSource, safeStoreMediaUrl } from './product-media'

/** Existing ordered gallery: logo, header, then additional images. Empty role
 * slots keep optional roles independent without dropping or reordering media. */
export function storeProductMediaDraft(
  logo: string,
  header: string,
  additional: string
) {
  const gallery = additional
    .split(/\r?\n/)
    .map((value) => value.trim())
    .filter(Boolean)
  if (gallery.length > 30) return undefined
  const result: string[] = []
  for (const source of [logo.trim(), header.trim(), ...gallery]) {
    const image = source ? normalizeStoreImageSource(source) : ''
    if (image === undefined) return undefined
    result.push(image)
  }
  while (result.at(-1) === '') result.pop()
  return result
}

export function storeProductHeaderImage(images: readonly string[]) {
  return safeStoreMediaUrl(images[1]) ?? safeStoreMediaUrl(images[0])
}

export function storeProductGalleryImages(images: readonly string[]) {
  const headerIndex = safeStoreMediaUrl(images[1])
    ? 1
    : safeStoreMediaUrl(images[0])
      ? 0
      : -1
  return images.flatMap((value, index) => {
    const image = safeStoreMediaUrl(value)
    return image && index !== headerIndex ? [image] : []
  })
}
