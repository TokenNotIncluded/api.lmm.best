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
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import {
  IconDiscord,
  IconGithub,
  IconGoogle,
  IconLinuxDo,
  IconTelegram,
  IconWeChat,
} from '@/assets/brand-icons'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

import { useOAuthLogin } from '../hooks/use-oauth-login'
import {
  getDisabledOAuthRegistrationMethods,
  isOAuthProviderConfigured,
} from '../lib/registration'
import type { SystemStatus } from '../types'
import { TelegramLoginDialog } from './telegram-login-dialog'

type OAuthProvidersProps = {
  status: SystemStatus | null
  disabled?: boolean
  className?: string
  onWeChatLogin?: () => void
  isWeChatLoading?: boolean
  redirectTo?: string
  acceptedLegal?: boolean
  featureGoogle?: boolean
  registrationOnly?: boolean
}

type ProviderButton = {
  key: string
  label: string
  onClick: () => void
  icon?: ReactNode
  disabled?: boolean
  shortLabel: string
  featured?: boolean
}

function isGoogleProvider(provider: {
  name: string
  slug: string
  icon: string
}) {
  return [provider.name, provider.slug, provider.icon].some(
    (value) => value.trim().toLowerCase() === 'google'
  )
}

export function OAuthProviders({
  status,
  disabled = false,
  className,
  onWeChatLogin,
  isWeChatLoading = false,
  redirectTo,
  acceptedLegal = false,
  featureGoogle = false,
  registrationOnly = false,
}: OAuthProvidersProps) {
  const { t } = useTranslation()
  const {
    isLoading,
    githubButtonText,
    githubButtonDisabled,
    handleGitHubLogin,
    handleDiscordLogin,
    handleOIDCLogin,
    handleLinuxDOLogin,
    handleTelegramLogin,
    handleCustomOAuthLogin,
    isTelegramDialogOpen,
    isTelegramPending,
    handleTelegramAuthorization,
    handleTelegramDialogChange,
  } = useOAuthLogin(status, redirectTo, acceptedLegal)

  const providerButtons: ProviderButton[] = []
  const disabledRegistrationMethods = registrationOnly
    ? getDisabledOAuthRegistrationMethods(status)
    : new Set<string>()
  const registrationAllowed = (method: string) =>
    !disabledRegistrationMethods.has(method)

  if (
    isOAuthProviderConfigured(status, 'wechat') &&
    onWeChatLogin &&
    registrationAllowed('wechat')
  ) {
    providerButtons.push({
      key: 'wechat',
      label: t('Continue with WeChat'),
      shortLabel: 'WeChat',
      onClick: onWeChatLogin,
      icon: <IconWeChat className='h-4 w-4' />,
      disabled: isWeChatLoading,
    })
  }

  if (
    isOAuthProviderConfigured(status, 'github') &&
    registrationAllowed('github')
  ) {
    providerButtons.push({
      key: 'github',
      label: githubButtonText || t('Continue with GitHub'),
      shortLabel: 'GitHub',
      onClick: handleGitHubLogin,
      icon: <IconGithub className='h-4 w-4' />,
      disabled: githubButtonDisabled,
    })
  }

  if (
    isOAuthProviderConfigured(status, 'discord') &&
    registrationAllowed('discord')
  ) {
    providerButtons.push({
      key: 'discord',
      label: t('Continue with Discord'),
      shortLabel: 'Discord',
      onClick: handleDiscordLogin,
      icon: <IconDiscord className='h-4 w-4' />,
    })
  }

  if (
    isOAuthProviderConfigured(status, 'oidc') &&
    registrationAllowed('oidc')
  ) {
    const oidcDisplayName = status?.oidc_display_name?.trim() || 'OIDC'
    providerButtons.push({
      key: 'oidc',
      label: t('Continue with {{name}}', {
        name: oidcDisplayName,
      }),
      shortLabel: oidcDisplayName,
      onClick: handleOIDCLogin,
    })
  }

  if (
    isOAuthProviderConfigured(status, 'linuxdo') &&
    registrationAllowed('linuxdo')
  ) {
    providerButtons.push({
      key: 'linuxdo',
      label: t('Continue with LinuxDO'),
      shortLabel: 'LinuxDO',
      onClick: handleLinuxDOLogin,
      icon: <IconLinuxDo className='h-4 w-4' />,
    })
  }

  if (
    isOAuthProviderConfigured(status, 'telegram') &&
    registrationAllowed('telegram')
  ) {
    providerButtons.push({
      key: 'telegram',
      label: t('Continue with Telegram'),
      shortLabel: 'Telegram',
      onClick: handleTelegramLogin,
      icon: <IconTelegram data-icon='inline-start' />,
    })
  }

  // Custom OAuth providers
  const customProviders = status?.custom_oauth_providers
  if (customProviders && customProviders.length > 0) {
    for (const provider of customProviders) {
      if (
        !isOAuthProviderConfigured(status, `custom:${provider.slug}`) ||
        !registrationAllowed(`custom:${provider.slug}`)
      ) {
        continue
      }
      const google = isGoogleProvider(provider)
      providerButtons.push({
        key: `custom-${provider.slug}`,
        label: google
          ? t('Continue with Google')
          : t('Continue with {{name}}', { name: provider.name }),
        shortLabel: google ? 'Google' : provider.name,
        onClick: () => handleCustomOAuthLogin(provider),
        icon: google ? <IconGoogle className='size-[18px]' /> : undefined,
        featured: featureGoogle && google,
      })
    }
  }

  if (providerButtons.length === 0) return null

  const featuredProvider = providerButtons.find((provider) => provider.featured)
  const otherProviders = providerButtons.filter(
    (provider) => !provider.featured
  )
  const showProviderDivider = !featuredProvider || otherProviders.length > 0

  const renderProviderButton = (provider: ProviderButton, compact: boolean) => (
    <Button
      key={provider.key}
      variant='outline'
      type='button'
      aria-label={provider.label}
      disabled={disabled || isLoading || provider.disabled}
      onClick={provider.onClick}
      className={cn(
        'w-full justify-center gap-2 rounded-xl shadow-none',
        compact ? 'h-10 px-2 text-sm' : 'h-11',
        compact &&
          'text-muted-foreground border-transparent bg-transparent hover:bg-muted/50 hover:text-foreground',
        provider.featured &&
          'border-[#747775] bg-white font-sans text-sm font-medium tracking-normal text-[#1f1f1f] hover:bg-[#f8faff] hover:text-[#1f1f1f] dark:border-[#8e918f] dark:bg-[#131314] dark:text-[#e3e3e3] dark:hover:bg-[#1f1f1f] dark:hover:text-white'
      )}
    >
      {provider.icon}
      {provider.featured || !compact ? provider.label : provider.shortLabel}
    </Button>
  )

  return (
    <>
      <div className={cn('space-y-3', className)}>
        {featuredProvider
          ? renderProviderButton(featuredProvider, false)
          : null}

        {showProviderDivider ? (
          <div className='relative py-0.5' aria-hidden='true'>
            <div className='absolute inset-0 flex items-center'>
              <span className='w-full border-t' />
            </div>
            <div className='relative flex justify-center text-xs'>
              <span className='bg-background text-muted-foreground px-3'>
                {featuredProvider ? t('Or') : t('Or continue with')}
              </span>
            </div>
          </div>
        ) : null}

        {otherProviders.length > 0 ? (
          <div
            className={cn(
              featureGoogle ? 'grid grid-cols-2 gap-2' : 'flex flex-col gap-2'
            )}
          >
            {otherProviders.map((provider) =>
              renderProviderButton(provider, featureGoogle)
            )}
          </div>
        ) : null}
      </div>

      <TelegramLoginDialog
        open={isTelegramDialogOpen}
        botName={status?.telegram_bot_name ?? ''}
        pending={isTelegramPending}
        onOpenChange={handleTelegramDialogChange}
        onAuthorization={handleTelegramAuthorization}
      />
    </>
  )
}
