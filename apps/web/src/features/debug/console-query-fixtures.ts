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
import type { AxiosAdapter } from 'axios'

// Some read-only queries use POST for model lists or a local payment quote. Keep
// these exact paths separate from the general GET fixtures; no purchase,
// payment, reset, refund, or administrative mutation is permitted.
export function withConsoleQueryFixtures(fallback: AxiosAdapter): AxiosAdapter {
  return async (config) => {
    const url = new URL(config.url ?? '', window.location.origin)
    const method = (config.method ?? 'get').toUpperCase()
    if (url.origin !== window.location.origin) return fallback(config)

    let data: unknown
    if (method === 'POST' && url.pathname === '/api/pricing/runtime') {
      data = { success: true, data: {} }
    } else if (method === 'POST' && url.pathname === '/api/user/amount') {
      let body: unknown = config.data
      if (typeof body === 'string') {
        try {
          body = JSON.parse(body)
        } catch {
          return fallback(config)
        }
      }
      if (!body || typeof body !== 'object' || Array.isArray(body)) {
        return fallback(config)
      }
      const request = body as Record<string, unknown>
      const amount = request.amount
      if (
        typeof amount !== 'number' ||
        !Number.isFinite(amount) ||
        amount < 1 ||
        amount > 1_000_000 ||
        (request.payment_method !== undefined &&
          request.payment_method !== 'alipay') ||
        (request.discount_code !== undefined && request.discount_code !== '') ||
        Object.keys(request).some(
          (key) => !['amount', 'payment_method', 'discount_code'].includes(key)
        )
      ) {
        return fallback(config)
      }
      // The review method uses a fixed 1:1 USD unit. This only reads a quote;
      // /pay, /topup and every other mutation still use the blocking adapter.
      data = {
        success: true,
        data: amount.toFixed(2),
        settlement_currency: 'USD',
      }
    } else if (
      method === 'GET' &&
      url.pathname === '/api/subscription/root/reset-targets'
    ) {
      data = {
        success: true,
        data: { items: [], total: 0, page: 1, page_size: 20 },
      }
    } else {
      return fallback(config)
    }
    return {
      data,
      status: 200,
      statusText: 'Local read-only query fixture',
      headers: {},
      config,
    }
  }
}
