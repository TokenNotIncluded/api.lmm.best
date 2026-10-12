/*
Copyright (C) 2026 LIghtJUNction

SPDX-License-Identifier: AGPL-3.0-or-later
*/
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Input } from '@/components/ui/input'
import { GOOGLE_BUILT_IN_PRESET } from '@/features/auth/lib/google-oauth'

import { resolveOAuthSiteUrl } from './oauth-callback-url'

export function GoogleBuiltInSection({
  serverAddress,
}: {
  serverAddress: string
}) {
  const { t } = useTranslation()
  const callbackUrl = `${resolveOAuthSiteUrl(serverAddress, t('Site URL'))}${GOOGLE_BUILT_IN_PRESET.browser_callback}`
  const rows = [
    [t('Authorization callback URL'), callbackUrl],
    [
      t('Authorization Endpoint'),
      GOOGLE_BUILT_IN_PRESET.authorization_endpoint,
    ],
    [t('Token Endpoint'), GOOGLE_BUILT_IN_PRESET.token_endpoint],
    [t('User Info Endpoint'), GOOGLE_BUILT_IN_PRESET.user_info_endpoint],
    [t('Scopes'), GOOGLE_BUILT_IN_PRESET.scopes],
  ]

  return (
    <div className='grid min-w-0 gap-5 lg:grid-cols-2'>
      <Alert className='lg:col-span-2'>
        <AlertTitle>{t('Setup guide')}</AlertTitle>
        <AlertDescription className='space-y-3 text-sm'>
          <p>
            {t(
              'Set these values in the provider application before enabling login.'
            )}
          </p>
          <dl className='space-y-2'>
            {rows.map(([label, value]) => (
              <div
                key={label}
                className='flex min-w-0 flex-col gap-1 sm:flex-row sm:justify-between sm:gap-3'
              >
                <dt className='text-muted-foreground shrink-0'>{label}</dt>
                <dd className='flex min-w-0 items-center gap-2'>
                  <code className='bg-muted text-foreground min-w-0 rounded px-1.5 py-0.5 text-xs break-all'>
                    {value}
                  </code>
                  {value === callbackUrl && (
                    <CopyButton
                      value={value}
                      size='icon'
                      className='size-7'
                      tooltip={t('Copy callback URL')}
                      aria-label={t('Copy callback URL')}
                    />
                  )}
                </dd>
              </div>
            ))}
          </dl>
        </AlertDescription>
      </Alert>
      <div className='space-y-2 lg:col-span-2'>
        <p className='text-sm font-medium'>
          {t('Read-only')} · {t('Configuration File')}
        </p>
        <code className='block text-sm'>[identity.google]</code>
        <div className='grid gap-3 lg:grid-cols-2'>
          {['client_id', 'client_secret_file', 'redirect_uri'].map((key) => (
            <label key={key} className='space-y-1.5 text-sm'>
              <span>{key}</span>
              <Input
                readOnly
                value={key === 'redirect_uri' ? callbackUrl : ''}
                placeholder={t('Configuration File')}
              />
            </label>
          ))}
        </div>
      </div>
    </div>
  )
}
