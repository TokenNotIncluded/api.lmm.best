/*
Copyright (C) 2026 LIghtJUNction
*/
import type {
  SubscriptionPlan,
  WaffoPancakePlanProduct,
  WaffoPancakeProductType,
  WaffoPancakePurchaseOption,
} from '../types'

export type PancakeDraftProduct = WaffoPancakePlanProduct & {
  /** Form-only signature. Never sent as a price or payment proof. */
  terms: string
}

export type PancakePlanTerms = Pick<
  SubscriptionPlan,
  'title' | 'price_amount' | 'currency' | 'duration_unit' | 'duration_value'
> & Pick<Partial<SubscriptionPlan>, 'custom_seconds'>

export function pancakeProductTerms(
  plan: PancakePlanTerms,
  productType: WaffoPancakeProductType
): string {
  // The server is the price authority; this only prevents accidental reuse
  // of a draft product after the form's price or recurring period changes.
  return JSON.stringify([
    Number(plan.price_amount),
    plan.currency.trim().toUpperCase(),
    productType,
    ...(productType === 'subscription'
      ? [plan.duration_unit, plan.duration_value, plan.custom_seconds || 0]
      : []),
  ])
}

export function pancakeProductsForPlan(
  plan?: SubscriptionPlan
): PancakeDraftProduct[] {
  const saved = plan?.waffo_pancake_products ??
    (plan?.waffo_pancake_product_id
      ? [{
          product_type: plan.waffo_pancake_product_type || 'subscription',
          product_id: plan.waffo_pancake_product_id,
          enabled: true,
        }]
      : [])
  return (['one_time', 'subscription'] as const).map((productType) => {
    const product = saved.find((item) => item.product_type === productType)
    return {
      product_type: productType,
      product_id: product?.product_id || '',
      enabled: product?.enabled || false,
      terms: plan ? pancakeProductTerms(plan, productType) : '',
    }
  })
}

export type PancakeProductCreateInput = {
  name: string
  amount: string
  currency: string
  duration_unit: string
  duration_value: number
  product_type: WaffoPancakeProductType
}

type PancakeProductCreateResult = {
  message?: string
  data?: { product_id: string; product_type: WaffoPancakeProductType }
}

export class PancakeProductCreationUncertain extends Error {
  readonly productType: WaffoPancakeProductType

  constructor(productType: WaffoPancakeProductType) {
    super('Check the Pancake catalog before creating this product again.')
    this.name = 'PancakeProductCreationUncertain'
    this.productType = productType
  }
}

// Reuse the existing authenticated product endpoint. No private key is put in
// the browser. Persist each success in the open form before starting the next.
export async function ensurePancakePlanProducts(
  draft: PancakeDraftProduct[],
  plan: PancakePlanTerms,
  create: (input: PancakeProductCreateInput) => Promise<PancakeProductCreateResult>,
  onProgress: (products: PancakeDraftProduct[]) => void,
  blockedTypes: ReadonlySet<WaffoPancakeProductType> = new Set()
): Promise<PancakeDraftProduct[]> {
  const next = draft.map((product) => {
    const terms = pancakeProductTerms(plan, product.product_type)
    return {
      ...product,
      product_id: product.terms === terms ? product.product_id : '',
      terms,
    }
  })
  onProgress(next.map((product) => ({ ...product })))
  for (const product of next) {
    if (!product.enabled || product.product_id) continue
    if (blockedTypes.has(product.product_type)) {
      throw new PancakeProductCreationUncertain(product.product_type)
    }
    try {
      const response = await create({
        name: plan.title.trim(),
        amount: Number(plan.price_amount).toFixed(2),
        currency: plan.currency,
        duration_unit: plan.duration_unit,
        duration_value: plan.duration_value,
        product_type: product.product_type,
      })
      if (
        response.message !== 'success' ||
        response.data?.product_type !== product.product_type ||
        !response.data.product_id?.trim()
      ) {
        throw new Error('Invalid Pancake product response')
      }
      product.product_id = response.data.product_id.trim()
      onProgress(next.map((item) => ({ ...item })))
    } catch {
      // A timeout may follow a successful provider write. Do not auto-retry.
      throw new PancakeProductCreationUncertain(product.product_type)
    }
  }
  return next
}

export function pancakeProductsPayload(
  products: PancakeDraftProduct[]
): WaffoPancakePlanProduct[] {
  return products.map(({ product_type, product_id, enabled }) => ({
    product_type, product_id, enabled,
  }))
}

// An empty options list is authoritative. Never fall back to a stale quote.
// With two options, even if only one currency is usable, no automatic switch
// from one-time purchase to auto-renewal is permitted.
export function selectPancakePurchaseOption(
  options: WaffoPancakePurchaseOption[] | undefined,
  selectedType: WaffoPancakeProductType | ''
): WaffoPancakePurchaseOption | undefined {
  if (!options) return undefined
  if (selectedType) {
    return options.find((option) => option.product_type === selectedType)
  }
  return options.length === 1 ? options[0] : undefined
}
