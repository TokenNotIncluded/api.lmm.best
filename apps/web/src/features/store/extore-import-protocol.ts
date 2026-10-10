/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { z } from 'zod'

import type { StoreProductInput } from './types'

export const EXTORE_DEFAULT_ORIGIN = 'https://extore.lmm.best'
export const EXTORE_INVALID_CATALOG =
  'This server did not return a valid Extore commerce import v1 response.'

const id = z
  .string()
  .min(1)
  .max(100)
  .refine((value) =>
    [...value].every(
      (char) => char.charCodeAt(0) >= 32 && char.charCodeAt(0) !== 127
    )
  )
const text = (limit: number) =>
  z.union([
    z.string().max(limit),
    z.record(z.string().max(40), z.string().max(limit)),
  ])
const variantSchema = z
  .object({
    id: z
      .string()
      .regex(/^[a-z0-9][a-z0-9_-]{0,39}$/)
      .optional(),
    name: z.string().max(120).optional(),
    description: z.string().max(10000).optional(),
    price: z
      .string()
      .regex(/^[0-9]+(?:\.[0-9]{1,6})?$/)
      .max(100)
      .nullable()
      .optional(),
    currency: z
      .string()
      .regex(/^[A-Z]{3,5}$/)
      .optional(),
    enabled: z.boolean().optional(),
    attributes: z.record(
      z.string().max(100),
      z.union([
        z.string().max(10000),
        z.number().finite(),
        z.boolean(),
        z.null(),
      ])
    ),
  })
  .passthrough()
const listingSchema = z
  .object({
    schema: z.literal('extore.product-listing.v1'),
    id,
    shop_id: id,
    revision: z.string().regex(/^[a-f0-9]{64}$/),
    redemption_url: z.string().url().max(2048),
    semantics: z.object({
      price: z.literal('reference'),
      inventory: z.literal('not_exported'),
      payment: z.literal('external_sales_platform'),
      redemption: z.literal('extore'),
    }),
    product: z
      .object({
        name: text(120).optional(),
        description: text(20000).optional(),
        public: z.boolean().optional(),
        logo: z.string().max(2048).optional(),
        image: z.string().max(2048).optional(),
        support_email: z.string().max(320).optional(),
      })
      .passthrough(),
    variants: z.array(variantSchema).max(100),
  })
  .passthrough()
const catalogSchema = z.object({
  schema: z.literal('extore.commerce-catalog.v1'),
  issuer: z.string().url(),
  shop: z.object({ id, name: z.string() }),
  grant_id: z.string().min(1),
  products: z.array(listingSchema).max(100),
})
export type ExtoreListing = z.infer<typeof listingSchema>
export type ExtoreVariant = z.infer<typeof variantSchema>
export type ExtoreCatalog = z.infer<typeof catalogSchema>
export type ExtoreAuthorization = {
  authorization_url: string
  base_url: string
  redirect_uri: string
}
export type ExtoreDraft = {
  fields: Partial<StoreProductInput>
  source: {
    issuer: string
    shopId: string
    productId: string
    variantId: string
    revision: string
  }
  referencePrice: string
  referenceCurrency: string
}

// Only non-secret UI preferences use browser storage. Credentials stay server-side.
export function extoreOrigin(value: string): string {
  const input = value.trim() || EXTORE_DEFAULT_ORIGIN
  const url = new URL(input.includes('://') ? input : `https://${input}`)
  if (
    url.protocol !== 'https:' ||
    url.username ||
    url.password ||
    url.port ||
    url.pathname !== '/' ||
    url.search ||
    input.includes('?') ||
    input.includes('#') ||
    !url.hostname.includes('.') ||
    /^\d+(?:\.\d+)*$/.test(url.hostname) ||
    !/^[a-z0-9.-]+$/.test(url.hostname) ||
    url.hostname.endsWith('.') ||
    /\.(localhost|local|internal|lan|home|onion)$/.test(url.hostname)
  ) {
    throw new Error('Enter a public HTTPS Extore base URL without a path.')
  }
  return url.origin
}

export function parseExtoreCatalog(value: unknown): ExtoreCatalog {
  const result = catalogSchema.safeParse(value)
  if (!result.success) throw new Error(EXTORE_INVALID_CATALOG)
  const catalog = result.data
  try {
    if (extoreOrigin(catalog.issuer) !== catalog.issuer) throw new Error()
    const seen = new Set<string>()
    for (const listing of catalog.products) {
      const redemption = new URL(listing.redemption_url)
      if (
        listing.shop_id !== catalog.shop.id ||
        seen.has(listing.id) ||
        redemption.origin !== catalog.issuer ||
        redemption.username ||
        redemption.password ||
        redemption.hash
      ) {
        throw new Error()
      }
      seen.add(listing.id)
      const ids = listing.variants.flatMap((variant) =>
        variant.id ? [variant.id] : []
      )
      if (new Set(ids).size !== ids.length) throw new Error()
    }
  } catch {
    throw new Error(EXTORE_INVALID_CATALOG)
  }
  return catalog
}

export function extoreText(
  value: string | Record<string, string> | undefined,
  language: string
): string {
  if (typeof value === 'string') return value
  if (!value) return ''
  return (
    value[language] ??
    value[language.startsWith('zh') ? 'zh-CN' : language.split('-')[0]] ??
    value.en ??
    Object.values(value)[0] ??
    ''
  )
}

export function extoreImage(value: string | undefined): string {
  if (!value) return ''
  try {
    const url = new URL(value)
    return url.protocol === 'https:' && !url.username && !url.password
      ? url.href
      : ''
  } catch {
    return ''
  }
}

export function extoreProductDraft(
  catalog: ExtoreCatalog,
  listing: ExtoreListing,
  variant: ExtoreVariant,
  language: string
): ExtoreDraft {
  if (
    !variant.id ||
    variant.enabled !== true ||
    !catalog.products.includes(listing) ||
    !listing.variants.includes(variant)
  ) {
    throw new Error('Choose an enabled Extore variant.')
  }
  const name = extoreText(listing.product.name, language).trim()
  const variantName = extoreText(variant.name, language).trim() || variant.id
  // A missing title stays blank for the seller to supply in the existing editor.
  const title =
    name && listing.variants.length > 1 ? `${name} · ${variantName}` : name
  if (new TextEncoder().encode(title).length > 200) {
    throw new Error(
      'The imported title is too long. Shorten it in Extore first.'
    )
  }
  return {
    fields: {
      title,
      description: [
        extoreText(listing.product.description, language),
        extoreText(variant.description, language),
      ]
        .filter(Boolean)
        .join('\n\n'),
      image_urls: [
        extoreImage(listing.product.logo),
        extoreImage(listing.product.image),
      ],
      contact: listing.product.support_email ?? '',
      links: [
        { title: 'Extore', url: listing.redemption_url, description: '' },
      ],
      template: 'card-key',
      // Public exposure is a separate seller decision, including for private listings.
      visibility: listing.product.public === true ? 'public' : 'private',
    },
    source: {
      issuer: catalog.issuer,
      shopId: catalog.shop.id,
      productId: listing.id,
      variantId: variant.id,
      revision: listing.revision,
    },
    referencePrice: variant.price ?? '',
    referenceCurrency: variant.currency ?? '',
  }
}
