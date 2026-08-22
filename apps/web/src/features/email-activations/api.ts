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
import { isAxiosError } from 'axios'

import { api } from '@/lib/api'

import type {
  HeroSmsActivation,
  HeroSmsActivationDetail,
  HeroSmsActivationsPage,
  HeroSmsApiErrorShape,
  HeroSmsCreateActivationsInput,
  HeroSmsCreateActivationsResult,
  HeroSmsEnvelope,
  HeroSmsListActivationsParams,
  HeroSmsListProductsParams,
  HeroSmsParsedError,
  HeroSmsProduct,
  HeroSmsProductsPage,
  HeroSmsReorderInput,
} from './types'

const PRODUCTS_PATH = '/api/hero-sms/email/products'
const ACTIVATIONS_PATH = '/api/hero-sms/email/activations'

async function unwrap<T>(request: Promise<{ data: HeroSmsEnvelope<T> }>) {
  const response = await request
  if (!response.data.success) {
    const error = new Error(response.data.message || 'HeroSMS request failed')
    Object.assign(error, { code: response.data.code })
    throw error
  }
  return response.data.data
}

function normalizeTimestamp(value: unknown) {
  if (value == null || value === '') return ''
  const numeric = Number(value)
  if (Number.isFinite(numeric) && /^\d+(?:\.\d+)?$/.test(String(value))) {
    const milliseconds = numeric < 1_000_000_000_000 ? numeric * 1000 : numeric
    const date = new Date(milliseconds)
    return Number.isNaN(date.getTime()) ? '' : date.toISOString()
  }
  return String(value)
}

function normalizeActivation(raw: unknown): HeroSmsActivation {
  const activation = raw as Record<string, unknown>
  return {
    id: String(activation.id ?? ''),
    order_id: String(activation.order_id ?? ''),
    domain_id: String(activation.domain_id ?? ''),
    email: String(activation.email ?? ''),
    code:
      activation.code == null || activation.code === ''
        ? null
        : String(activation.code),
    message:
      activation.message == null || activation.message === ''
        ? null
        : String(activation.message),
    site:
      activation.site == null || activation.site === ''
        ? null
        : String(activation.site),
    domain:
      activation.domain == null || activation.domain === ''
        ? null
        : String(activation.domain),
    status: String(activation.status ?? 'unknown'),
    charge_quota: Number(activation.charge_quota ?? 0),
    cost_usd: Number(activation.cost_usd ?? 0),
    currency: String(activation.currency ?? ''),
    currency_code: Number(activation.currency_code ?? 0),
    cancel_reason: String(activation.cancel_reason ?? ''),
    created_at: normalizeTimestamp(activation.created_at),
    updated_at: normalizeTimestamp(activation.updated_at),
  }
}

function normalizeActivationsPage(raw: unknown): HeroSmsActivationsPage {
  const value = raw as Record<string, unknown>
  let itemsSource: unknown[] = []
  if (Array.isArray(value.items)) {
    itemsSource = value.items
  } else if (Array.isArray(value.activations)) {
    itemsSource = value.activations
  }

  return {
    items: itemsSource.map((item) => normalizeActivation(item)),
    page: Number(value.page ?? 1),
    size: Number(value.size ?? itemsSource.length ?? 10),
    total: Number(value.total ?? itemsSource.length),
  }
}

function normalizeActivationDetail(raw: unknown): HeroSmsActivationDetail {
  const value = raw as Record<string, unknown>
  const activation = 'activation' in value ? value.activation : value
  return {
    activation: normalizeActivation(activation),
  }
}

function normalizeCreateResult(raw: unknown): HeroSmsCreateActivationsResult {
  const value = raw as Record<string, unknown>
  const activations = Array.isArray(value.activations)
    ? value.activations.map((item) => normalizeActivation(item))
    : []

  return {
    order: (value.order as Record<string, unknown> | null | undefined) ?? null,
    activations,
  }
}

export function createHeroSmsIdempotencyKey() {
  if (typeof globalThis.crypto?.randomUUID === 'function') {
    return globalThis.crypto.randomUUID()
  }
  return `hero-sms-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`
}

export function parseHeroSmsError(error: unknown): HeroSmsParsedError {
  if (isAxiosError(error)) {
    const data = error.response?.data as HeroSmsApiErrorShape | undefined
    return {
      status: error.response?.status,
      code: data?.code,
      message: data?.message || error.message || 'HeroSMS request failed',
    }
  }

  if (error instanceof Error) {
    return { message: error.message }
  }

  return { message: 'HeroSMS request failed' }
}

export async function listHeroSmsProducts(
  params: HeroSmsListProductsParams = {}
) {
  const result = await unwrap<HeroSmsProductsPage>(
    api.get(PRODUCTS_PATH, {
      params: {
        page: params.page ?? 1,
        size: params.size ?? 100,
        site: params.site || undefined,
      },
      skipBusinessError: true,
      skipErrorHandler: true,
    })
  )

  return {
    ...result,
    items: (result.items ?? []).map((item) => ({
      ...item,
      id: item.id,
      domain: String(item.domain ?? ''),
      site: String(item.site ?? ''),
      cost_usd: Number(item.cost_usd ?? 0),
      customer_price_usd: Number(item.customer_price_usd ?? 0),
      charge_quota: Number(item.charge_quota ?? 0),
      count: Number(item.count ?? 0),
      available:
        typeof item.available === 'boolean'
          ? item.available
          : Number(item.available ?? item.count ?? 0) > 0,
    })) as HeroSmsProduct[],
  }
}

export async function createHeroSmsActivations(
  input: HeroSmsCreateActivationsInput
) {
  const result = await unwrap<unknown>(
    api.post(
      ACTIVATIONS_PATH,
      {
        domain_id: input.domain_id,
        quantity: input.quantity,
      },
      {
        headers: { 'Idempotency-Key': input.idempotencyKey },
        skipBusinessError: true,
        skipErrorHandler: true,
      }
    )
  )

  return normalizeCreateResult(result)
}

export async function listHeroSmsActivations(
  params: HeroSmsListActivationsParams
) {
  const result = await unwrap<unknown>(
    api.get(ACTIVATIONS_PATH, {
      params: {
        page: params.page,
        size: params.size,
        status: params.status || undefined,
      },
      skipBusinessError: true,
      skipErrorHandler: true,
    })
  )

  return normalizeActivationsPage(result)
}

export async function getCurrentHeroSmsActivation(): Promise<HeroSmsActivation | null> {
  const result = await unwrap<unknown | null>(
    api.get(`${ACTIVATIONS_PATH}/current`, {
      skipBusinessError: true,
      skipErrorHandler: true,
    })
  )
  return result == null ? null : normalizeActivation(result)
}

export async function getHeroSmsActivationDetail(
  activationId: number | string
) {
  const result = await unwrap<unknown>(
    api.get(`${ACTIVATIONS_PATH}/${activationId}`, {
      skipBusinessError: true,
      skipErrorHandler: true,
    })
  )

  return normalizeActivationDetail(result)
}

export async function refreshHeroSmsActivation(activationId: number | string) {
  const result = await unwrap<unknown>(
    api.post(`${ACTIVATIONS_PATH}/${activationId}/refresh`, undefined, {
      skipBusinessError: true,
      skipErrorHandler: true,
    })
  )

  return normalizeActivationDetail(result)
}

export async function cancelHeroSmsActivation(activationId: number | string) {
  const result = await unwrap<unknown>(
    api.post(`${ACTIVATIONS_PATH}/${activationId}/cancel`, undefined, {
      skipBusinessError: true,
      skipErrorHandler: true,
    })
  )

  return normalizeActivationDetail(result)
}

export async function reorderHeroSmsActivation(input: HeroSmsReorderInput) {
  const result = await unwrap<unknown>(
    api.post(
      `${ACTIVATIONS_PATH}/${input.activationId}/reorder`,
      { domain_id: input.domain_id },
      {
        headers: { 'Idempotency-Key': input.idempotencyKey },
        skipBusinessError: true,
        skipErrorHandler: true,
      }
    )
  )

  return normalizeCreateResult(result)
}
