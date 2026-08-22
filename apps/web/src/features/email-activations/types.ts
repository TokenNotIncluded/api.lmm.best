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
export interface HeroSmsEnvelope<T> {
  success: boolean
  code?: string
  message?: string
  data: T
}

export interface HeroSmsProduct {
  id: number | string
  domain: string
  site: string
  cost_usd: number
  customer_price_usd: number
  charge_quota: number
  count: number
  available: boolean
}

export interface HeroSmsProductsPage {
  items: HeroSmsProduct[]
  page: number
  size: number
  total: number
  price_multiplier: number
  currency: 'USD'
  currency_code: 840
}

export interface HeroSmsActivation {
  id: number | string
  order_id: number | string
  domain_id: string
  email: string
  code?: string | null
  message?: string | null
  site?: string | null
  domain?: string | null
  status: string
  charge_quota: number
  cost_usd: number
  currency: string
  currency_code: number
  cancel_reason: string
  created_at: string
  updated_at: string
}

export interface HeroSmsActivationOrder {
  id?: number | string
  status?: string
  quantity?: number
  charge_quota?: number
  cost_usd?: number
  created_at?: string
  updated_at?: string
  [key: string]: unknown
}

export interface HeroSmsActivationsPage {
  items: HeroSmsActivation[]
  page: number
  size: number
  total: number
}

export interface HeroSmsActivationDetail {
  activation: HeroSmsActivation
}

export interface HeroSmsCreateActivationsResult {
  order?: HeroSmsActivationOrder | null
  activations: HeroSmsActivation[]
}

export interface HeroSmsListProductsParams {
  page?: number
  size?: number
  site?: string
}

export interface HeroSmsListActivationsParams {
  page: number
  size: number
  status?: string
}

export interface HeroSmsCreateActivationsInput {
  domain_id: number | string
  quantity: number
  idempotencyKey: string
}

export interface HeroSmsReorderInput {
  activationId: number | string
  domain_id: number | string
  idempotencyKey: string
}

export interface HeroSmsApiErrorShape {
  success?: false
  code?: string
  message?: string
}

export interface HeroSmsParsedError {
  status?: number
  code?: string
  message: string
}
