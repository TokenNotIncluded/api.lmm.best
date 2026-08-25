/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
/*
Copyright (C) 2026 LIghtJUNction
*/
import type { HeroSmsSmsOffer, HeroSmsSmsOrder } from './sms-api.js'
import { clampHeroSmsQuantity } from './sms-selection.js'

export type HeroSmsBatchFailureCode =
  | 'PRICE_CHANGED'
  | 'OUT_OF_STOCK'
  | 'REQUEST_FAILED'

export interface HeroSmsBatchPurchaseResult {
  requested: number
  orders: HeroSmsSmsOrder[]
  failure?: {
    code: HeroSmsBatchFailureCode
    item: number
    error?: unknown
    ambiguous?: boolean
    offerId?: string
    idempotencyKey?: string
  }
}

interface HeroSmsBatchPurchaseDependencies {
  initialOffer: HeroSmsSmsOffer
  quantity: number
  idempotencyKey: string
  getFreshOffer: () => Promise<HeroSmsSmsOffer>
  createOrder: (
    offerId: string,
    idempotencyKey: string
  ) => Promise<{ order: HeroSmsSmsOrder; quota: number }>
  isAmbiguousNetworkError: (error: unknown) => boolean
  onProgress?: (completed: number, total: number) => void
}

function isSamePurchaseQuote(
  initialOffer: HeroSmsSmsOffer,
  currentOffer: HeroSmsSmsOffer
) {
  return (
    currentOffer.country_id === initialOffer.country_id &&
    currentOffer.service === initialOffer.service &&
    currentOffer.operator === initialOffer.operator &&
    currentOffer.charge_quota === initialOffer.charge_quota
  )
}

const ambiguousPurchaseMarker = Symbol('ambiguous-hero-sms-purchase')

type AmbiguousHeroSmsPurchaseError = Error & {
  [ambiguousPurchaseMarker]: true
  cause: unknown
}

function ambiguousHeroSmsPurchaseError(
  cause: unknown
): AmbiguousHeroSmsPurchaseError {
  return Object.assign(new Error('HeroSMS purchase outcome is uncertain'), {
    [ambiguousPurchaseMarker]: true as const,
    cause,
  })
}

function isAmbiguousHeroSmsPurchaseError(
  error: unknown
): error is AmbiguousHeroSmsPurchaseError {
  return error instanceof Error && ambiguousPurchaseMarker in error
}

async function createOrderWithOneSafeRetry(
  dependencies: HeroSmsBatchPurchaseDependencies,
  offerId: string,
  idempotencyKey: string
) {
  try {
    return await dependencies.createOrder(offerId, idempotencyKey)
  } catch (error) {
    if (!dependencies.isAmbiguousNetworkError(error)) throw error
    try {
      return await dependencies.createOrder(offerId, idempotencyKey)
    } catch (retryError) {
      // The first request may still have succeeded. A later HTTP error cannot
      // make that unknown outcome definitive; preserve the exact offer/key so
      // the UI can replay only this item through the serialized backend path.
      throw ambiguousHeroSmsPurchaseError(retryError)
    }
  }
}

export async function purchaseHeroSmsBatch(
  dependencies: HeroSmsBatchPurchaseDependencies
): Promise<HeroSmsBatchPurchaseResult> {
  const requested = clampHeroSmsQuantity(
    dependencies.quantity,
    dependencies.initialOffer.inventory
  )
  const orders: HeroSmsSmsOrder[] = []

  for (let index = 0; index < requested; index += 1) {
    let offer: HeroSmsSmsOffer
    try {
      offer =
        index === 0
          ? dependencies.initialOffer
          : await dependencies.getFreshOffer()
    } catch (error) {
      return {
        requested,
        orders,
        failure: { code: 'REQUEST_FAILED', item: index + 1, error },
      }
    }

    if (!isSamePurchaseQuote(dependencies.initialOffer, offer)) {
      return {
        requested,
        orders,
        failure: { code: 'PRICE_CHANGED', item: index + 1 },
      }
    }
    if (offer.inventory < 1) {
      return {
        requested,
        orders,
        failure: { code: 'OUT_OF_STOCK', item: index + 1 },
      }
    }

    const itemIdempotencyKey = `${dependencies.idempotencyKey}-${index + 1}`
    try {
      const result = await createOrderWithOneSafeRetry(
        dependencies,
        offer.id,
        itemIdempotencyKey
      )
      orders.push(result.order)
      dependencies.onProgress?.(orders.length, requested)
    } catch (error) {
      return {
        requested,
        orders,
        failure: {
          code: 'REQUEST_FAILED',
          item: index + 1,
          error: isAmbiguousHeroSmsPurchaseError(error) ? error.cause : error,
          ambiguous:
            isAmbiguousHeroSmsPurchaseError(error) ||
            dependencies.isAmbiguousNetworkError(error),
          offerId: offer.id,
          idempotencyKey: itemIdempotencyKey,
        },
      }
    }
  }

  return { requested, orders }
}
