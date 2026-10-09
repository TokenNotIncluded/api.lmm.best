/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { STORE_FIXED_CONTENT_COPY as fixedCopy } from './fixed-content-copy'
import { STORE_PUBLISHING_COPY as copy } from './publishing-copy'
import type { StoreVariantInput } from './types'

export interface StoreNewVariantDraft extends StoreVariantInput {
  key: string
}
export const STORE_MAX_VARIANTS = 200

export function storeNewVariantsError(
  variants: StoreVariantInput[],
  minimum: number | undefined
): string | undefined {
  if (variants.length < 1 || variants.length > STORE_MAX_VARIANTS) {
    return copy.variantsLimit
  }
  const names = new Set<string>()
  for (const variant of variants) {
    const name = variant.name.trim()
    if (
      !name ||
      name.includes('\0') ||
      new TextEncoder().encode(name).length > 200 ||
      names.has(name.toLowerCase()) ||
      !Number.isSafeInteger(variant.price_quota) ||
      variant.price_quota <= 0 ||
      minimum === undefined ||
      variant.price_quota < minimum
    ) {
      return copy.invalidVariants
    }
    names.add(name.toLowerCase())
    if (
      variant.template === 'fixed-content' &&
      (!variant.fixed_content?.trim() ||
        variant.fixed_content.includes('\0') ||
        new TextEncoder().encode(variant.fixed_content).length > 128 * 1024)
    ) {
      return fixedCopy.invalid
    }
  }
  if (!variants.some((variant) => variant.enabled)) {
    return copy.invalidVariants
  }
  return undefined
}

export function storeNewVariantInput(
  variant: StoreVariantInput
): StoreVariantInput {
  return {
    name: variant.name.trim(),
    price_quota: variant.price_quota,
    template: variant.template,
    enabled: variant.enabled,
    ...(variant.template === 'fixed-content'
      ? { fixed_content: variant.fixed_content }
      : {}),
  }
}
