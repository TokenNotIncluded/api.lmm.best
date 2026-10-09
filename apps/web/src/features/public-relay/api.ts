/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/

import { api } from '@/lib/api'

import type {
  PublicRelay,
  PublicRelayConfig,
  PublicRelayReport,
  PublicRelayReview,
  PublicRelayRoutingItem,
} from './types'

type Envelope<T> = { success: boolean; message?: string; data: T }

async function unwrap<T>(request: Promise<{ data: Envelope<T> }>) {
  const response = await request
  if (!response.data.success) {
    throw new Error(response.data.message || 'Request failed')
  }
  return response.data.data
}

export function getPublicRelayConfig() {
  return unwrap<PublicRelayConfig>(api.get('/api/public-relays/config'))
}

export function listPublicRelays() {
  return unwrap<{ items: PublicRelay[]; group: string }>(
    api.get('/api/public-relays?limit=100')
  )
}

export function listMyPublicRelays() {
  return unwrap<{ items: PublicRelay[]; group: string }>(
    api.get('/api/public-relays/mine?limit=100')
  )
}

export function submitPublicRelay(
  input: Pick<PublicRelay, 'name' | 'base_url' | 'models' | 'description'> & {
    channel_config: unknown
  }
) {
  return unwrap<PublicRelay>(api.post('/api/public-relays', input))
}

export function reportPublicRelay(id: number, reason: string) {
  return unwrap<PublicRelayReport>(
    api.post(`/api/public-relays/${id}/report`, { reason })
  )
}

export function listPublicRelayReviews(id: number) {
  return unwrap<{ items: PublicRelayReview[] }>(
    api.get(`/api/public-relays/${id}/reviews`)
  )
}

export function ratePublicRelay(id: number, rating: number, comment: string) {
  return unwrap<null>(
    api.post(`/api/public-relays/${id}/review`, { rating, comment })
  )
}

export function tipPublicRelay(id: number, amountUSD: number, message: string) {
  return unwrap<{ amount_usd: number }>(
    api.post(`/api/public-relays/${id}/tip`, {
      amount_usd: amountUSD,
      message,
    })
  )
}

export function getPublicRelayRouting() {
  return unwrap<{ items: PublicRelayRoutingItem[]; group: string }>(
    api.get('/api/public-relays/routing')
  )
}

export function updatePublicRelayRouting(
  disabledIds: number[],
  orderIds: number[]
) {
  return unwrap<null>(
    api.put('/api/public-relays/routing', {
      disabled_ids: disabledIds,
      order_ids: orderIds,
    })
  )
}

export function withdrawPublicRelayTips(id: number, group: string) {
  return unwrap<{ quota: number; group: string }>(
    api.post(`/api/public-relays/${id}/withdraw`, { group })
  )
}

export function listAdminPublicRelays(status = '') {
  return unwrap<{ items: PublicRelay[]; group: string }>(
    api.get('/api/public-relays/admin', {
      params: status ? { status } : undefined,
    })
  )
}

export function reviewPublicRelay(id: number, approve: boolean, note: string) {
  return unwrap<PublicRelay>(
    api.post(`/api/public-relays/admin/${id}/review`, { approve, note })
  )
}

export function listAdminPublicRelayReports() {
  return unwrap<{ items: PublicRelayReport[] }>(
    api.get('/api/public-relays/admin/reports', { params: { status: 'open' } })
  )
}

export function reviewPublicRelayReport(
  id: number,
  close: boolean,
  note: string
) {
  return unwrap<null>(
    api.post(`/api/public-relays/admin/reports/${id}/review`, { close, note })
  )
}
