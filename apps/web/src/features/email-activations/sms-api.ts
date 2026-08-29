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
import { api } from '@/lib/api'

import { createHeroSmsIdempotencyKey } from './api.js'

interface HeroSmsEnvelope<T> {
  success: boolean
  code?: string
  message?: string
  data: T
}

export interface HeroSmsSmsCountry {
  id: number
  name: string
  english_name: string
  chinese_name: string
  popularity: number
}

export interface HeroSmsSmsService {
  code: string
  name: string
  popularity: number
}

export interface HeroSmsSmsPriceTier {
  id: string
  inventory: number
  customer_price_usd: string
  charge_quota: number
}

export interface HeroSmsSmsOffer {
  id: string
  country_id: number
  service: string
  operator: string
  inventory: number
  customer_price_usd: string
  charge_quota: number
  bid?: boolean
  tiers?: HeroSmsSmsPriceTier[]
}

// typos:ignore DISMATCH -- HeroSMS's official complaint enum uses this spelling.
export type HeroSmsSmsComplaintReason =
  | 'NUMBER_BLOCKED'
  | 'NUMBER_ALREADY_IN_USE'
  | 'SMS_CODE_DISMATCH'
  | 'SMS_NOT_RECEIVED'
  | 'CODE_SENT_TO_APP'
  | 'INCOMING_CALL_NUMBER'
  | 'INCOMING_CALL_VOICE'

export interface HeroSmsSmsOrder {
  id: string
  country_id: number
  service: string
  operator: string
  status: string
  customer_price_usd: string
  charge_quota: number
  refunded_quota: number
  provider_id: string | null
  can_cancel?: boolean
  can_complain?: boolean
  complaint_type?: HeroSmsSmsComplaintReason | ''
  complaint_status?: string
  complaint_submitted_at?: number
  phone_number: string
  code: string
  message: string
  last_error_code: string
  last_error_message: string
  created_at: number
  updated_at: number
  expires_at?: number
}

export interface HeroSmsSmsOrderPage {
  items: HeroSmsSmsOrder[]
  page: number
  size: number
  total: number
}

async function unwrap<T>(request: Promise<{ data: HeroSmsEnvelope<T> }>) {
  const response = await request
  if (!response.data.success) {
    const error = new Error(response.data.message || 'HeroSMS request failed')
    Object.assign(error, { code: response.data.code })
    throw error
  }
  return response.data.data
}

const requestOptions = {
  skipBusinessError: true,
  skipErrorHandler: true,
} as const

export function listHeroSmsSmsCountries(service?: string) {
  return unwrap<HeroSmsSmsCountry[]>(
    api.get('/api/hero-sms/sms/countries', {
      ...requestOptions,
      params: service ? { service } : undefined,
    })
  )
}

export function listHeroSmsSmsServices() {
  return unwrap<HeroSmsSmsService[]>(
    api.get('/api/hero-sms/sms/services', requestOptions)
  )
}

export function listHeroSmsSmsOperators(country: number) {
  return unwrap<string[]>(
    api.get('/api/hero-sms/sms/operators', {
      ...requestOptions,
      params: { country },
    })
  )
}

export function getHeroSmsSmsOffer(input: {
  country: number
  service: string
  operator?: string
  maxPriceUSD?: string
}) {
  return unwrap<HeroSmsSmsOffer>(
    api.get('/api/hero-sms/sms/offer', {
      ...requestOptions,
      params: {
        country: input.country,
        service: input.service,
        operator: input.operator,
        max_price_usd: input.maxPriceUSD,
      },
    })
  )
}

export function createHeroSmsSmsOrder(
  offerId: string,
  idempotencyKey = createHeroSmsIdempotencyKey()
) {
  return unwrap<{ order: HeroSmsSmsOrder; quota: number }>(
    api.post(
      '/api/hero-sms/sms/orders',
      { offer_id: offerId },
      {
        ...requestOptions,
        headers: { 'Idempotency-Key': idempotencyKey },
      }
    )
  )
}

export function refreshHeroSmsSmsOrder(orderId: string) {
  return unwrap<{ order: HeroSmsSmsOrder }>(
    api.get(
      `/api/hero-sms/sms/orders/${encodeURIComponent(orderId)}`,
      requestOptions
    )
  )
}

export function submitHeroSmsSmsComplaint(
  orderId: string,
  reason: HeroSmsSmsComplaintReason
) {
  return unwrap<{ order: HeroSmsSmsOrder }>(
    api.post(
      `/api/hero-sms/sms/orders/${encodeURIComponent(orderId)}/complaints`,
      { reason },
      requestOptions
    )
  )
}

export function cancelHeroSmsSmsOrder(orderId: string) {
  return unwrap<{ order: HeroSmsSmsOrder; quota: number }>(
    api.post(
      `/api/hero-sms/sms/orders/${encodeURIComponent(orderId)}/cancel`,
      {},
      requestOptions
    )
  )
}

export async function listCurrentHeroSmsSmsOrders() {
  const data = await unwrap<{ items: HeroSmsSmsOrder[] }>(
    api.get('/api/hero-sms/sms/orders/current-list', requestOptions)
  )
  return data.items
}

export function listHeroSmsSmsOrders(page = 1, size = 20) {
  return unwrap<HeroSmsSmsOrderPage>(
    api.get('/api/hero-sms/sms/orders', {
      ...requestOptions,
      params: { page, size, summary: true },
    })
  )
}

export function hideHeroSmsSmsOrderFromHistory(orderId: string) {
  return unwrap<{ hidden: boolean }>(
    api.delete(
      `/api/hero-sms/sms/history/${encodeURIComponent(orderId)}`,
      requestOptions
    )
  )
}

export function clearHeroSmsSmsOrderHistory() {
  return unwrap<{ hidden_count: number }>(
    api.delete('/api/hero-sms/sms/history', requestOptions)
  )
}
