/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { storeProductShareUrl } from './product-share'

const textEntities = new Map([
  ['&amp;', '&'],
  ['&lt;', '<'],
  ['&gt;', '>'],
  ['&quot;', '"'],
  ['&apos;', "'"],
  ['&nbsp;', ' '],
])

export interface PublicStoreShareProduct {
  id: string
  title: string
  description: string
  status: string
  visibility?: string
  test_mode?: boolean
  image_urls?: string[]
}

export function isPublicStoreShareProduct(product: PublicStoreShareProduct) {
  // Empty visibility is the established legacy representation of public items.
  return (
    product.status === 'published' &&
    product.test_mode !== true &&
    (!product.visibility || product.visibility === 'public')
  )
}

function plainShareText(value: string) {
  return value
    .slice(0, 16000)
    .replaceAll(/<(script|style)\b[^>]*>[\s\S]*?<\/\1\s*>/gi, ' ')
    .replaceAll(/<!--[^]*?-->|<[^>]*>/g, ' ')
    .replaceAll(/!\[[^\]]*\]\([^)]*\)/g, ' ')
    .replaceAll(/\[([^\]]+)\]\([^)]*\)/g, '$1')
    .replaceAll(
      /(?:https?:\/\/|www\.|data:|javascript:|mailto:)[^\s<>]+/gi,
      ' '
    )
    .replaceAll(/\/?store\/claim\/[^\s<>]+/gi, ' ')
    .replaceAll(/(?:[\p{L}\p{N}-]+\.)+[\p{L}]{2,63}(?:[/?#][^\s<>]*)?/giu, ' ')
    .replaceAll(
      /&(?:amp|lt|gt|quot|apos|nbsp);/g,
      (entity) => textEntities.get(entity) ?? ' '
    )
    .replaceAll(/[#*_`~]|\p{Cc}|[\u202a-\u202e\u2066-\u2069]/gu, ' ')
    .replaceAll(/\s+/g, ' ')
    .trim()
    .normalize('NFC')
}

function excerpt(value: string, limit: number) {
  const characters = Array.from(value)
  return characters.length <= limit
    ? value
    : `${characters
        .slice(0, limit - 1)
        .join('')
        .trimEnd()}…`
}

export function storeProductXShare(
  product: PublicStoreShareProduct,
  origin: string
) {
  if (!isPublicStoreShareProduct(product)) return undefined
  const url = storeProductShareUrl(product.id, origin)
  const title = excerpt(plainShareText(product.title), 70)
  const description = excerpt(plainShareText(product.description), 49)
  // At most 120 code points: conservatively <=240 weighted characters,
  // leaving room for the canonical URL (23) and its separator on X.
  const text = [title, description].filter(Boolean).join('\n')
  const intent = new URL('https://x.com/intent/tweet')
  intent.searchParams.set('text', text)
  intent.searchParams.set('url', url)
  return { title, description, text, url, intentUrl: intent.href }
}
