/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import type { StoreProductInput } from './types'

export const EXTORE_DEFAULT_BASE_URL = 'https://extore.lmm.best'
export const EXTORE_IMPORT_MAX_BYTES = 2 * 1024 * 1024

export interface ExtoreImportVariant {
  id: string
  name: string
  description: string
  referencePrice: string | null
  currency: string
  enabled: boolean
}

export interface ExtoreImportProduct {
  key: string
  id: string | null
  shopId: string | null
  revision: string | null
  name: string
  description: string
  images: string[]
  contact: string
  redemptionUrl: string
  variants: ExtoreImportVariant[]
  source: Record<string, unknown>
  unmappedFields: string[]
}

export interface ExtoreImportDocument {
  issuer: string
  products: ExtoreImportProduct[]
}

function invalid(): never {
  throw new Error('Invalid or unsupported Extore product data.')
}

function record(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) invalid()
  return value as Record<string, unknown>
}

function optionalText(value: unknown, max = 128 * 1024): string {
  if (value === undefined || value === null) return ''
  if (typeof value !== 'string' || value.length > max) invalid()
  return value
}

function identifier(value: unknown): string | null {
  if (value === undefined || value === null) return null
  if (typeof value !== 'string' || !value || value.length > 256 || Array.from(value).some((char) => char.charCodeAt(0) <= 32 || char.charCodeAt(0) === 127)) invalid()
  return value
}

export function extoreText(value: unknown, language: string): string {
  if (value === undefined || value === null) return ''
  if (typeof value === 'string') return optionalText(value)
  const translations = record(value)
  if (Object.values(translations).some((item) => typeof item !== 'string')) invalid()
  const keys = [...new Set([language, language.split('-')[0], 'zh-CN', 'en', ...Object.keys(translations).sort()])]
  for (const key of keys) {
    const text = optionalText(translations[key])
    if (text) return text
  }
  return ''
}

export function normalizeExtoreBaseUrl(value: string): string {
  const raw = value.trim() || EXTORE_DEFAULT_BASE_URL
  let url: URL
  try {
    url = new URL(raw.includes('://') ? raw : `https://${raw}`)
  } catch {
    throw new Error('Use an HTTPS Extore base URL without a path or credentials.')
  }
  if (url.protocol !== 'https:' || url.username || url.password || url.pathname !== '/' || raw.includes('?') || raw.includes('#') || !url.hostname.includes('.') || url.hostname.endsWith('.local') || url.hostname.endsWith('.localhost')) {
    throw new Error('Use an HTTPS Extore base URL without a path or credentials.')
  }
  return url.origin
}

function mediaUrl(value: unknown): string {
  if (typeof value !== 'string' || value.length > 4096) return ''
  try {
    const url = new URL(value)
    return (url.protocol === 'https:' || url.protocol === 'http:') && !url.username && !url.password ? url.href : ''
  } catch {
    return ''
  }
}

function parseProduct(raw: unknown, issuer: string, language: string, index: number, shopId?: string): ExtoreImportProduct {
  const source = record(raw)
  if (source.schema !== 'extore.product-listing.v1') invalid()
  const product = record(source.product)
  const id = identifier(source.id)
  const sourceShop = identifier(source.shop_id)
  if (shopId && (sourceShop !== shopId || !id)) invalid()
  const revision = identifier(source.revision)
  if (shopId && !revision) invalid()
  if (revision && !/^[a-f0-9]{64}$/u.test(revision)) invalid()
  if (source.semantics !== undefined) {
    const semantics = record(source.semantics)
    if (semantics.price !== 'reference' || semantics.inventory !== 'not_exported' || semantics.payment !== 'external_sales_platform' || semantics.redemption !== 'extore') invalid()
  } else if (shopId) {
    invalid()
  }
  const name = extoreText(product.name, language).trim()
  if (!name || !Array.isArray(source.variants) || source.variants.length === 0 || source.variants.length > 100) invalid()
  const ids = new Set<string>()
  const variants = source.variants.map((rawVariant): ExtoreImportVariant => {
    const variant = record(rawVariant)
    const variantId = identifier(variant.id)
    if (!variantId || ids.has(variantId) || typeof variant.enabled !== 'boolean') invalid()
    ids.add(variantId)
    if (variant.price !== undefined && variant.price !== null && (typeof variant.price !== 'string' || variant.price.length > 100 || !/^[0-9]+(\.[0-9]{1,6})?$/u.test(variant.price) || variant.price.split('.')[0].replace(/^0+/u, '').length > 12)) invalid()
    return {
      id: variantId,
      name: extoreText(variant.name, language) || variantId,
      description: extoreText(variant.description, language),
      referencePrice: typeof variant.price === 'string' ? variant.price : null,
      currency: optionalText(variant.currency, 16),
      enabled: variant.enabled,
    }
  })
  const logo = mediaUrl(product.logo)
  const header = mediaUrl(product.image)
  const redemptionUrl = optionalText(source.redemption_url, 4096) || `${issuer}/`
  let redemption: URL
  try { redemption = new URL(redemptionUrl) } catch { invalid() }
  if (redemption.origin !== issuer || redemption.username || redemption.password || redemption.hash) invalid()
  const mapped = new Set(['name', 'description', 'logo', 'image', 'support_email'])
  return {
    key: `${sourceShop ?? ''}:${id ?? index}`,
    id,
    shopId: sourceShop,
    revision,
    name,
    description: extoreText(product.description, language),
    images: logo || header ? [logo, header] : [],
    contact: optionalText(product.support_email, 4096),
    redemptionUrl: redemption.href,
    variants,
    source,
    unmappedFields: [
      ...Object.keys(product).filter((key) => !mapped.has(key)).map((key) => `product.${key}`),
      ...source.variants.flatMap((item, variantIndex) => Object.keys(record(item)).filter((key) => !['id', 'name', 'description', 'price', 'currency', 'enabled'].includes(key)).map((key) => `variants[${variantIndex}].${key}`)),
    ],
  }
}

export function parseExtoreImport(text: string, baseUrl: string, language: string): ExtoreImportDocument {
  if (new TextEncoder().encode(text).length > EXTORE_IMPORT_MAX_BYTES) {
    throw new Error('Extore data must not exceed 2 MiB.')
  }
  const trimmed = text.trim()
  const json = /^```(?:json)?\s*([\s\S]*?)```$/u.exec(trimmed)?.[1] ?? trimmed
  let decoded: unknown
  try { decoded = JSON.parse(json) } catch { invalid() }
  const document = record(decoded)
  const issuer = normalizeExtoreBaseUrl(baseUrl)
  if (document.schema === 'extore.product-listing.v1') {
    return { issuer, products: [parseProduct(document, issuer, language, 0)] }
  }
  if (document.schema !== 'extore.commerce-catalog.v1' || document.issuer !== issuer || !Array.isArray(document.products) || document.products.length > 100) invalid()
  const shop = record(document.shop)
  const shopId = identifier(shop.id)
  if (!shopId) invalid()
  const products = document.products.map((product, index) => parseProduct(product, issuer, language, index, shopId))
  if (new Set(products.map((product) => product.key)).size !== products.length) invalid()
  return { issuer, products }
}

export function extoreDraft(product: ExtoreImportProduct, variantId: string, title: string, quota: number): StoreProductInput {
  const variant = product.variants.find((item) => item.id === variantId && item.enabled)
  if (!variant || !title.trim() || new TextEncoder().encode(title.trim()).length > 200 || !Number.isSafeInteger(quota) || quota <= 0) invalid()
  const description = [product.description, variant.description].filter(Boolean).join('\n\n')
  if (new TextEncoder().encode(description).length > 128 * 1024) invalid()
  const source = [product.shopId, product.id, variant.id, product.revision].filter(Boolean).join(' / ')
  return {
    title: title.trim(),
    description,
    image_urls: product.images,
    contact: product.contact,
    links: [{ title: 'Extore', url: product.redemptionUrl, description: source }],
    price_quota: quota,
    template: 'card-key',
    delivery_strategy: 'sequential',
    payment_methods: [],
    pickup_login_required: true,
    pickup_code_required: false,
    email_pickup_link: false,
  }
}
