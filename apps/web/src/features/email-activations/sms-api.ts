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
}

export interface HeroSmsSmsService {
  code: string
  name: string
}

export interface HeroSmsSmsOffer {
  id: string
  country_id: number
  service: string
  operator: string
  inventory: number
  provider_price_cny: string
  customer_price_usd: string
  charge_quota: number
  price_multiplier: string
}

export interface HeroSmsSmsOrder {
  id: string
  country_id: number
  service: string
  operator: string
  status: string
  provider_price_cny: string
  customer_price_usd: string
  charge_quota: number
  refunded_quota: number
  provider_id: string | null
  phone_number: string
  code: string
  message: string
  last_error_code: string
  last_error_message: string
  created_at: number
  updated_at: number
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

export function listHeroSmsSmsCountries() {
  return unwrap<HeroSmsSmsCountry[]>(
    api.get('/api/hero-sms/sms/countries', requestOptions)
  )
}

export function listHeroSmsSmsServices() {
  return unwrap<HeroSmsSmsService[]>(
    api.get('/api/hero-sms/sms/services', requestOptions)
  )
}

export function getHeroSmsSmsOffer(input: {
  country: number
  service: string
  operator?: string
}) {
  return unwrap<HeroSmsSmsOffer>(
    api.get('/api/hero-sms/sms/offer', {
      ...requestOptions,
      params: input,
    })
  )
}

export function createHeroSmsSmsOrder(offerId: string) {
  return unwrap<{ order: HeroSmsSmsOrder; quota: number }>(
    api.post(
      '/api/hero-sms/sms/orders',
      { offer_id: offerId },
      {
        ...requestOptions,
        headers: { 'Idempotency-Key': createHeroSmsIdempotencyKey() },
      }
    )
  )
}

export function getCurrentHeroSmsSmsOrder() {
  return unwrap<{ order: HeroSmsSmsOrder | null }>(
    api.get('/api/hero-sms/sms/orders/current', requestOptions)
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

export function cancelHeroSmsSmsOrder(orderId: string) {
  return unwrap<{ order: HeroSmsSmsOrder; quota: number }>(
    api.post(
      `/api/hero-sms/sms/orders/${encodeURIComponent(orderId)}/cancel`,
      {},
      requestOptions
    )
  )
}

export function listHeroSmsSmsOrders(page = 1, size = 20) {
  return unwrap<HeroSmsSmsOrderPage>(
    api.get('/api/hero-sms/sms/orders', {
      ...requestOptions,
      params: { page, size },
    })
  )
}
