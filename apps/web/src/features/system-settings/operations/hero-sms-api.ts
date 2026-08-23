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

export interface HeroSmsSettingsResponse {
  enabled: boolean
  email_enabled: boolean
  sms_enabled: boolean
  api_key_configured: boolean
  pending_work: boolean
  currency: 'USD'
  currency_code: 840
  price_multiplier: number
}

export interface HeroSmsSettingsFormValues {
  enabled: boolean
  emailEnabled: boolean
  smsEnabled: boolean
  apiKey: string
  priceMultiplier: number
}

export interface HeroSmsSettingsUpdateRequest {
  enabled: boolean
  email_enabled: boolean
  sms_enabled: boolean
  price_multiplier: string
  api_key?: string
}

interface HeroSmsEnvelope<T> {
  success: boolean
  code?: string
  message?: string
  data: T
}

async function unwrap<T>(request: Promise<{ data: HeroSmsEnvelope<T> }>) {
  const response = await request
  if (!response.data.success) {
    const error = new Error(
      response.data.message || 'HeroSMS settings request failed'
    )
    Object.assign(error, { code: response.data.code })
    throw error
  }
  return response.data.data
}

export function toHeroSmsSettingsFormValues(
  data: HeroSmsSettingsResponse
): HeroSmsSettingsFormValues {
  return {
    enabled: Boolean(data.enabled),
    emailEnabled: data.email_enabled !== false,
    smsEnabled: data.sms_enabled === true,
    apiKey: '',
    priceMultiplier: Number(data.price_multiplier || 1),
  }
}

export function serializeHeroSmsSettingsUpdate(
  values: HeroSmsSettingsFormValues
): HeroSmsSettingsUpdateRequest {
  const trimmedApiKey = values.apiKey.trim()
  return {
    enabled: values.enabled,
    email_enabled: values.emailEnabled,
    sms_enabled: values.smsEnabled,
    price_multiplier: String(values.priceMultiplier),
    ...(trimmedApiKey ? { api_key: trimmedApiKey } : {}),
  }
}

export function getHeroSmsPreviewCustomerPrice(multiplier: number) {
  return Number((multiplier * 1).toFixed(2))
}

export function parseHeroSmsSettingsError(error: unknown) {
  if (isAxiosError(error)) {
    return {
      status: error.response?.status,
      message:
        (error.response?.data as { message?: string } | undefined)?.message ||
        error.message ||
        'HeroSMS settings request failed',
    }
  }

  if (error instanceof Error) {
    return { message: error.message }
  }

  return { message: 'HeroSMS settings request failed' }
}

export async function getHeroSmsSettings() {
  return unwrap<HeroSmsSettingsResponse>(
    api.get('/api/option/hero-sms', {
      skipBusinessError: true,
      skipErrorHandler: true,
    })
  )
}

export async function updateHeroSmsSettings(values: HeroSmsSettingsFormValues) {
  return unwrap<HeroSmsSettingsResponse>(
    api.put('/api/option/hero-sms', serializeHeroSmsSettingsUpdate(values), {
      skipBusinessError: true,
      skipErrorHandler: true,
    })
  )
}

export async function testHeroSmsConnection(candidateApiKey?: string) {
  const apiKey = candidateApiKey?.trim()
  return unwrap<{ ok?: boolean }>(
    api.post('/api/option/hero-sms/test', apiKey ? { api_key: apiKey } : {}, {
      skipBusinessError: true,
      skipErrorHandler: true,
    })
  )
}

export async function clearHeroSmsApiKey() {
  return unwrap<{ ok?: boolean }>(
    api.delete('/api/option/hero-sms/key', {
      skipBusinessError: true,
      skipErrorHandler: true,
    })
  )
}
