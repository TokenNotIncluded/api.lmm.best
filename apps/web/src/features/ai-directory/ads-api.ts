/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { api } from '@/lib/api'

import { getPublicDirectory } from './public-api'

export type DirectoryAd = {
  id: number
  name: string
  url: string
  summary: string
  description: string
  bid_cents: number
  charged_quota: number
  charged_amount_usd: string | null
  status: 'active' | 'hidden'
  paid_at: number
  expires_at: number
  hidden_at: number
  refunded_at: number
}

export type DirectoryAdQuote = {
  pricing_schema_version: 2
  bid_cents: number
  quota: number
  currency: 'USD'
  duration_days: number
  min_bid_cents: number
  max_bid_cents: number
}

export function isUsdDirectoryAdQuote(
  value: unknown
): value is DirectoryAdQuote {
  if (!value || typeof value !== 'object') return false
  const quote = value as Partial<DirectoryAdQuote>
  return (
    quote.pricing_schema_version === 2 &&
    quote.currency === 'USD' &&
    Number.isSafeInteger(quote.quota) &&
    Number(quote.quota) > 0 &&
    Number.isSafeInteger(quote.bid_cents) &&
    Number(quote.bid_cents) >= 100
  )
}

export type DirectoryAdInput = {
  name: string
  url: string
  summary: string
  description: string
  bid_cents: number
  expected_quota: number
  request_id: string
}

type ApiEnvelope<T> = {
  success: boolean
  code?: string
  message?: string
  data: T
}

async function unwrap<T>(
  request: Promise<{ data: ApiEnvelope<T> }>
): Promise<T> {
  const response = await request
  if (!response.data.success) {
    const error = new Error(
      response.data.message || 'Unable to load advertisements'
    )
    Object.assign(error, { code: response.data.code })
    throw error
  }
  return response.data.data
}

export function listDirectoryAds(offset = 0) {
  return unwrap<{
    items: DirectoryAd[]
    has_more: boolean
    next_offset: number
  }>(getPublicDirectory(`/api/ai-directory/ads?offset=${offset}`))
}

export async function quoteDirectoryAd(bidCents: number) {
  const quote = await unwrap<DirectoryAdQuote>(
    api.get(`/api/ai-directory/ads/quote?bid_cents=${bidCents}`, {
      skipErrorHandler: true,
      skipBusinessError: true,
    })
  )
  if (!isUsdDirectoryAdQuote(quote)) {
    throw new Error('Unable to get a price quote. Try again.')
  }
  return quote
}

export function createDirectoryAd(input: DirectoryAdInput) {
  return unwrap<{ ad: DirectoryAd; created: boolean; charged_quota: number }>(
    api.post('/api/ai-directory/ads', input, {
      skipErrorHandler: true,
      skipBusinessError: true,
    })
  )
}

export function listMyDirectoryAds() {
  return unwrap<{ items: DirectoryAd[] }>(
    api.get('/api/ai-directory/ads/mine', {
      skipErrorHandler: true,
      skipBusinessError: true,
    })
  )
}

export function hideDirectoryAd(id: number) {
  return unwrap<{ ad: DirectoryAd; refunded: boolean; refunded_quota: number }>(
    api.post(`/api/ai-directory/ads/${id}/hide`, null, {
      skipErrorHandler: true,
      skipBusinessError: true,
    })
  )
}

export function directoryAdErrorCode(error: unknown): string | null {
  if (!error || typeof error !== 'object') return null
  const direct = (error as { code?: unknown }).code
  if (typeof direct === 'string') return direct
  const response = (error as { response?: { data?: { code?: unknown } } })
    .response
  return typeof response?.data?.code === 'string' ? response.data.code : null
}
