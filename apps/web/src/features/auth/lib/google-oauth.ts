/*
Copyright (C) 2026 LIghtJUNction

SPDX-License-Identifier: AGPL-3.0-or-later
*/
import type { CustomOAuthProviderInfo, SystemStatus } from '../types'

// These endpoints are owned by the built-in provider, not editable custom
// templates. Browser entry must use the cookie bridge, never the native API.
export const GOOGLE_BUILT_IN_PRESET = Object.freeze({
  key: 'google',
  name: 'Google',
  authorization_endpoint: 'https://accounts.google.com/o/oauth2/v2/auth',
  token_endpoint: 'https://oauth2.googleapis.com/token',
  user_info_endpoint: 'https://openidconnect.googleapis.com/v1/userinfo',
  scopes: 'openid profile email',
  browser_start: '/api/oauth/google',
  browser_callback: '/api/oauth/google/callback',
})

/** google_oauth must advertise browser support, not merely native OAuth. */
export function isBuiltInGoogleConfigured(
  status: SystemStatus | null | undefined
): boolean {
  return (
    status?.google_oauth === true &&
    typeof status.google_client_id === 'string' &&
    status.google_client_id.trim().length > 0
  )
}

export function isCustomGoogleProvider(
  provider: Pick<CustomOAuthProviderInfo, 'name' | 'slug' | 'icon'>
): boolean {
  return [provider.name, provider.slug, provider.icon].some(
    (value) => value.trim().toLowerCase() === 'google'
  )
}

/** Only a positively advertised built-in browser provider supersedes buttons. */
export function shouldShowCustomGoogleProvider(
  status: SystemStatus | null | undefined,
  provider: Pick<CustomOAuthProviderInfo, 'name' | 'slug' | 'icon'>,
  builtInAllowed = true
): boolean {
  return !(
    builtInAllowed &&
    isBuiltInGoogleConfigured(status) &&
    isCustomGoogleProvider(provider)
  )
}

/** Legal consent is supplied to the server and checked before starting. */
export function buildGoogleBrowserStartRequest(
  status: SystemStatus | null | undefined,
  acceptedLegal: boolean
): { accepted_legal: boolean } | null {
  if (!isBuiltInGoogleConfigured(status)) return null
  if (
    (status?.user_agreement_enabled === true ||
      status?.privacy_policy_enabled === true) &&
    !acceptedLegal
  ) {
    return null
  }
  return { accepted_legal: acceptedLegal }
}

/** The server response cannot choose a foreign source or a native callback. */
export function validateGoogleBrowserAuthorizationUrl(
  value: unknown,
  clientId: unknown,
  origin: string,
  allowLocalDevelopment = false
): string {
  const reject = () => {
    throw new Error('Untrusted Google browser authorization URL')
  }
  if (typeof value !== 'string' || typeof clientId !== 'string') return reject()
  try {
    const site = new URL(origin)
    const target = new URL(value)
    const official = new URL(GOOGLE_BUILT_IN_PRESET.authorization_endpoint)
    const callback = new URL(target.searchParams.get('redirect_uri') || '')
    const loopback = ['localhost', '127.0.0.1', '[::1]'].includes(site.hostname)
    const scope = target.searchParams
      .get('scope')
      ?.split(/\s+/)
      .sort()
      .join(' ')
    if (
      (site.protocol !== 'https:' &&
        !(allowLocalDevelopment && loopback && site.protocol === 'http:')) ||
      site.username ||
      site.password ||
      target.origin !== official.origin ||
      target.pathname !== official.pathname ||
      target.username ||
      target.password ||
      target.hash ||
      target.searchParams.get('client_id') !== clientId ||
      target.searchParams.get('response_type') !== 'code' ||
      scope !== 'email openid profile' ||
      !target.searchParams.get('state') ||
      !target.searchParams.get('nonce') ||
      !target.searchParams.get('code_challenge') ||
      target.searchParams.get('code_challenge_method') !== 'S256' ||
      callback.origin !== site.origin ||
      callback.pathname !== GOOGLE_BUILT_IN_PRESET.browser_callback ||
      callback.username ||
      callback.password ||
      callback.search ||
      callback.hash
    ) {
      return reject()
    }
    // Reject duplicate parameters: validation and provider parsing must agree.
    for (const key of target.searchParams.keys()) {
      if (target.searchParams.getAll(key).length !== 1) return reject()
    }
    return target.toString()
  } catch {
    return reject()
  }
}
