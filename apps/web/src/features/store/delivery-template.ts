/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { STORE_DELIVERY_TEMPLATE_COPY as copy } from './delivery-template-copy'
import { MAX_IMPORT_BYTES, MAX_IMPORT_ITEMS, safeStoreUrl } from './utils'

export type StoreDeliveryTemplate =
  | 'card-key'
  | 'text'
  | 'custom-text'
  | 'redemption-code'
  | 'license-key'
  | 'download-link'
  | 'account-details'
export type StructuredDeliveryTemplate =
  | 'redemption-code'
  | 'license-key'
  | 'download-link'
  | 'account-details'
type DeliveryField = {
  name: string
  label: string
  required?: boolean
  url?: boolean
  password?: boolean
}
export const DELIVERY_TEMPLATES: Record<
  StoreDeliveryTemplate,
  { label: string; help: string }
> = {
  'card-key': { label: 'Activation keys', help: copy.cardHelp },
  text: { label: 'Text items', help: copy.textHelp },
  'custom-text': { label: 'Custom text', help: copy.customHelp },
  'redemption-code': { label: copy.redemption, help: copy.redemptionHelp },
  'license-key': { label: copy.license, help: copy.licenseHelp },
  'download-link': { label: copy.download, help: copy.downloadHelp },
  'account-details': { label: copy.account, help: copy.accountHelp },
}
export const DELIVERY_FIELDS: Record<
  StructuredDeliveryTemplate,
  DeliveryField[]
> = {
  'redemption-code': [
    { name: 'code', label: copy.redeemCode, required: true },
    { name: 'redeem_url', label: copy.redeemUrl, url: true },
    { name: 'instructions', label: copy.instructions },
  ],
  'license-key': [
    { name: 'license_key', label: copy.licenseKey, required: true },
    { name: 'product', label: 'Product' },
    { name: 'instructions', label: copy.instructions },
  ],
  'download-link': [
    { name: 'url', label: copy.downloadUrl, required: true, url: true },
    { name: 'access_code', label: copy.accessCode },
    { name: 'instructions', label: copy.instructions },
  ],
  'account-details': [
    { name: 'username', label: 'Username', required: true },
    { name: 'password', label: 'Password', required: true, password: true },
    { name: 'url', label: copy.loginUrl, url: true },
    { name: 'instructions', label: copy.instructions },
  ],
}
export const DELIVERY_FIELD_BYTE_LIMITS: Record<string, number> = {
  code: 4096,
  license_key: 4096,
  password: 4096,
  access_code: 4096,
  username: 1000,
  product: 1000,
  instructions: 16384,
  url: 2048,
  redeem_url: 2048,
}
export function structuredTemplate(
  template: string
): template is StructuredDeliveryTemplate {
  return Object.hasOwn(DELIVERY_FIELDS, template)
}
export function validDeliveryUrl(value: string) {
  return (
    !/\p{Cc}/u.test(value) &&
    value === value.trim() &&
    /^https?:\/\/[^/?#]+/i.test(value) &&
    Boolean(safeStoreUrl(value))
  )
}
export function validDeliveryFields(
  template: StructuredDeliveryTemplate,
  fields: Record<string, unknown>
) {
  const definitions = DELIVERY_FIELDS[template]
  return (
    Object.keys(fields).every((key) =>
      definitions.some((field) => field.name === key)
    ) &&
    definitions.every((field) => {
      const value = fields[field.name]
      if (value === undefined) return !field.required
      if (typeof value !== 'string') return false
      if (field.required && !value.trim()) return false
      if (
        new TextEncoder().encode(value).length >
        DELIVERY_FIELD_BYTE_LIMITS[field.name]
      ) {
        return false
      }
      return !field.url || !value || validDeliveryUrl(value)
    })
  )
}
export function encodeDeliveryItem(
  template: StructuredDeliveryTemplate,
  fields: Record<string, string>
) {
  if (!validDeliveryFields(template, fields)) return undefined
  return JSON.stringify({
    lmm_store_delivery: 1,
    template,
    fields: Object.fromEntries(
      Object.entries(fields).filter(([, value]) => value !== '')
    ),
  })
}
export function parseDeliveryItem(
  raw: string,
  frozenTemplate: string | undefined
):
  | {
      template: StructuredDeliveryTemplate
      fields: Record<string, string>
    }
  | undefined {
  if (!frozenTemplate || !structuredTemplate(frozenTemplate)) return undefined
  try {
    const payload = JSON.parse(raw)
    if (
      !payload ||
      typeof payload !== 'object' ||
      Array.isArray(payload) ||
      Object.keys(payload).some(
        (key) => !['lmm_store_delivery', 'template', 'fields'].includes(key)
      ) ||
      payload.lmm_store_delivery !== 1 ||
      payload.template !== frozenTemplate ||
      !payload.fields ||
      typeof payload.fields !== 'object' ||
      Array.isArray(payload.fields) ||
      !validDeliveryFields(frozenTemplate, payload.fields)
    ) {
      return undefined
    }
    return { template: frozenTemplate, fields: payload.fields }
  } catch {
    return undefined
  }
}
export function deliveryItemText(
  raw: string,
  template: string | undefined,
  translate: (key: string) => string
) {
  const parsed = parseDeliveryItem(raw, template)
  if (!parsed) return raw
  return DELIVERY_FIELDS[parsed.template]
    .filter((field) => parsed.fields[field.name])
    .map((field) => `${translate(field.label)}: ${parsed.fields[field.name]}`)
    .join('\n')
}

export function validateComposedItems(items: string[]) {
  if (
    items.length > MAX_IMPORT_ITEMS ||
    items.some((item) => new TextEncoder().encode(item).length > 32768) ||
    new TextEncoder().encode(JSON.stringify({ items })).length >
      MAX_IMPORT_BYTES
  ) {
    throw new Error('Inventory import is too large')
  }
}
