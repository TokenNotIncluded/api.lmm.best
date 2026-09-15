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
export type CCSwitchProviderDeepLinkOptions = {
  app: string
  name: string
  endpoint?: string
  apiKey?: string
  models?: Record<string, string>
  homepage?: string
  enabled?: boolean
  accountBalanceURL?: string
}

export function buildCCSwitchProviderURL(
  options: CCSwitchProviderDeepLinkOptions
): string {
  const params = new URLSearchParams()
  params.set('resource', 'provider')
  params.set('app', options.app)
  params.set('name', options.name)
  if (options.endpoint) params.set('endpoint', options.endpoint)
  if (options.apiKey) params.set('apiKey', options.apiKey)
  for (const [key, value] of Object.entries(options.models ?? {})) {
    if (value) params.set(key, value)
  }
  if (options.homepage) params.set('homepage', options.homepage)
  if (options.enabled !== undefined) {
    params.set('enabled', String(options.enabled))
  }
  if (options.accountBalanceURL) {
    params.set('usageEnabled', 'true')
    params.set('usageBaseUrl', options.accountBalanceURL)
    params.set('usageAutoInterval', '5')
    params.set('usageScript', btoa(CC_SWITCH_ACCOUNT_BALANCE_SCRIPT))
  }
  return `ccswitch://v1/import?${params.toString()}`
}

// ASCII-only script: credentials are resolved by CC Switch, not embedded here.
export const CC_SWITCH_ACCOUNT_BALANCE_SCRIPT = `({
  request: {
    url: "{{baseUrl}}",
    method: "GET",
    headers: { "Authorization": "Bearer {{apiKey}}", "Accept": "application/json" }
  },
  extractor: function(response) {
    if (response.valid !== true || response.scope !== "account" ||
        response.currency !== "USD" || typeof response.remaining !== "number" ||
        !isFinite(response.remaining)) {
      return { isValid: false, invalidMessage: "Account balance unavailable; check balance access permission" };
    }
    return { isValid: true, remaining: response.remaining, unit: "USD" };
  }
})`
