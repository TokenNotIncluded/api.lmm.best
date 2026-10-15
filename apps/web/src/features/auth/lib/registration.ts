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
import type { SystemStatus } from '../types'
import { isBuiltInGoogleConfigured } from './google-oauth'

type RegistrationSetting =
  | 'register_enabled'
  | 'password_register_enabled'
  | 'oauth_register_enabled'

function hasText(value: string | undefined): boolean {
  return Boolean(value?.trim())
}

/**
 * OAuth flags are not sufficient to launch a provider. The login handlers
 * intentionally return early when a required credential is missing, so do
 * not advertise those providers as clickable entry points in that state.
 */
export function isOAuthProviderConfigured(
  status: SystemStatus | null | undefined,
  method: string
): boolean {
  if (!status) return false

  const normalizedMethod = method.trim().toLowerCase()
  switch (normalizedMethod) {
    case 'wechat':
      return status.wechat_login === true
    case 'google':
      return isBuiltInGoogleConfigured(status)
    case 'github':
      return status.github_oauth === true && hasText(status.github_client_id)
    case 'discord':
      return status.discord_oauth === true && hasText(status.discord_client_id)
    case 'oidc':
      return (
        status.oidc_enabled === true &&
        hasText(status.oidc_authorization_endpoint) &&
        hasText(status.oidc_client_id)
      )
    case 'linuxdo':
      return status.linuxdo_oauth === true && hasText(status.linuxdo_client_id)
    case 'telegram':
      return status.telegram_oauth === true && hasText(status.telegram_bot_name)
    default:
      if (!normalizedMethod.startsWith('custom:')) return false
      {
        const slug = normalizedMethod.slice('custom:'.length)
        return (status.custom_oauth_providers ?? []).some(
          (provider) =>
            provider.slug.trim().toLowerCase() === slug &&
            hasText(provider.authorization_endpoint) &&
            hasText(provider.client_id)
        )
      }
  }
}

export function hasOAuthLoginProvider(
  status: SystemStatus | null | undefined
): boolean {
  return (
    [
      'wechat',
      'google',
      'github',
      'discord',
      'oidc',
      'linuxdo',
      'telegram',
    ].some((method) => isOAuthProviderConfigured(status, method)) ||
    (status?.custom_oauth_providers ?? []).some((provider) =>
      isOAuthProviderConfigured(status, `custom:${provider.slug}`)
    )
  )
}

function readRegistrationSetting(
  status: SystemStatus | null | undefined,
  key: RegistrationSetting
): boolean | undefined {
  const direct = status?.[key]
  if (typeof direct === 'boolean') return direct

  const nested = status?.data?.[key]
  return typeof nested === 'boolean' ? nested : undefined
}

export function isRegistrationEnabled(
  status: SystemStatus | null | undefined
): boolean {
  return readRegistrationSetting(status, 'register_enabled') !== false
}

export function isPasswordRegistrationEnabled(
  status: SystemStatus | null | undefined
): boolean {
  return (
    isRegistrationEnabled(status) &&
    readRegistrationSetting(status, 'password_register_enabled') !== false
  )
}

export function isOAuthRegistrationEnabled(
  status: SystemStatus | null | undefined
): boolean {
  return (
    isRegistrationEnabled(status) &&
    readRegistrationSetting(status, 'oauth_register_enabled') !== false
  )
}

export function getDisabledOAuthRegistrationMethods(
  status: SystemStatus | null | undefined
): ReadonlySet<string> {
  const methods =
    status?.oauth_registration_disabled_methods ??
    status?.data?.oauth_registration_disabled_methods ??
    []

  return new Set(
    methods
      .map((method) => method.trim().toLowerCase())
      .filter((method) => method.length > 0)
  )
}

/**
 * Returns whether at least one configured provider can create a new account.
 * Existing sign-in providers intentionally do not use this helper: the
 * registration policy must never hide or disable login.
 */
export function hasOAuthRegistrationProvider(
  status: SystemStatus | null | undefined
): boolean {
  if (!status || !isOAuthRegistrationEnabled(status)) return false

  const disabled = getDisabledOAuthRegistrationMethods(status)
  const isAllowed = (method: string) => !disabled.has(method)

  if (isOAuthProviderConfigured(status, 'wechat') && isAllowed('wechat')) {
    return true
  }
  if (isOAuthProviderConfigured(status, 'google') && isAllowed('google')) {
    return true
  }
  if (isOAuthProviderConfigured(status, 'github') && isAllowed('github')) {
    return true
  }
  if (isOAuthProviderConfigured(status, 'discord') && isAllowed('discord')) {
    return true
  }
  if (isOAuthProviderConfigured(status, 'oidc') && isAllowed('oidc')) {
    return true
  }
  if (isOAuthProviderConfigured(status, 'linuxdo') && isAllowed('linuxdo')) {
    return true
  }
  if (isOAuthProviderConfigured(status, 'telegram') && isAllowed('telegram')) {
    return true
  }

  return (status.custom_oauth_providers ?? []).some(
    (provider) =>
      isOAuthProviderConfigured(status, `custom:${provider.slug}`) &&
      isAllowed(`custom:${provider.slug.trim().toLowerCase()}`)
  )
}

export function hasRegistrationMethod(
  status: SystemStatus | null | undefined
): boolean {
  return (
    isPasswordRegistrationEnabled(status) ||
    hasOAuthRegistrationProvider(status)
  )
}

/**
 * Registration links must wait for a live capability response. Cached status
 * is useful for layout, but it must not advertise a sign-up path that the
 * current server may have disabled.
 */
export function canOfferRegistration(
  status: SystemStatus | null | undefined,
  capabilitiesReady: boolean,
  localPreview = false
): boolean {
  return localPreview || (capabilitiesReady && hasRegistrationMethod(status))
}
