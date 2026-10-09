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
import { Link, useSearch } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { sanitizeAuthRedirect } from '@/features/auth/lib/auth-redirect'
import { useStatus } from '@/hooks/use-status'
import { isLocalPreview } from '@/lib/local-preview'

import { AuthLayout } from '../auth-layout'
import { TermsFooter } from '../components/terms-footer'
import {
  hasRegistrationMethod,
  isRegistrationEnabled,
} from '../lib/registration'
import { SignUpForm } from './components/sign-up-form'

export function SignUp() {
  const { t } = useTranslation()
  const { redirect } = useSearch({ from: '/(auth)/sign-up' })
  const requestedPath = sanitizeAuthRedirect(redirect, window.location.origin)
  const { status, error, capabilitiesReady, refetch } = useStatus()
  const localPreview = isLocalPreview()

  // Do not render a registration form from an old localStorage status. The
  // server is authoritative, and registration must fail closed until its live
  // status has been read. Local preview intentionally has no server status.
  if (!localPreview && !capabilitiesReady && !error) {
    return (
      <AuthLayout>
        <p className='text-muted-foreground text-center text-sm'>
          {t('Loading registration settings...')}
        </p>
      </AuthLayout>
    )
  }

  if (!localPreview && error) {
    return (
      <AuthLayout>
        <div className='w-full space-y-4 text-left'>
          <h2 className='text-2xl font-semibold tracking-tight'>
            {t('Unable to load registration settings')}
          </h2>
          <p className='text-muted-foreground text-sm sm:text-base'>
            {t(
              'The server did not return registration capabilities. Check your connection and try again.'
            )}
          </p>
          <Button
            type='button'
            variant='outline'
            onClick={() => void refetch()}
          >
            {t('Retry')}
          </Button>
          <p className='text-muted-foreground text-sm sm:text-base'>
            {t('Already have an account?')}{' '}
            <Link
              to='/sign-in'
              search={requestedPath ? { redirect: requestedPath } : {}}
              className='hover:text-primary font-medium underline underline-offset-4'
            >
              {t('Sign in')}
            </Link>
            .
          </p>
        </div>
      </AuthLayout>
    )
  }

  const registrationEnabled =
    localPreview || (capabilitiesReady && isRegistrationEnabled(status))
  const hasAvailableMethod =
    localPreview || (capabilitiesReady && hasRegistrationMethod(status))

  if (!registrationEnabled || !hasAvailableMethod) {
    return (
      <AuthLayout>
        <div className='w-full space-y-4 text-left'>
          <h2 className='text-2xl font-semibold tracking-tight'>
            {t('Registration is currently unavailable')}
          </h2>
          <p className='text-muted-foreground text-sm sm:text-base'>
            {t(
              'New account registration is disabled or no registration method is available.'
            )}
          </p>
          <p className='text-muted-foreground text-sm sm:text-base'>
            {t('Already have an account?')}{' '}
            <Link
              to='/sign-in'
              search={requestedPath ? { redirect: requestedPath } : {}}
              className='hover:text-primary font-medium underline underline-offset-4'
            >
              {t('Sign in')}
            </Link>
            .
          </p>
        </div>
      </AuthLayout>
    )
  }

  return (
    <AuthLayout>
      <div className='w-full space-y-8'>
        <div className='space-y-2'>
          <h2 className='text-center text-2xl font-semibold tracking-tight sm:text-left'>
            {t('Create an account')}
          </h2>
          <p className='text-muted-foreground text-left text-sm sm:text-base'>
            {t('Already have an account?')}{' '}
            <Link
              to='/sign-in'
              search={requestedPath ? { redirect: requestedPath } : {}}
              className='hover:text-primary font-medium underline underline-offset-4'
            >
              {t('Sign in')}
            </Link>
            .
          </p>
        </div>

        <SignUpForm redirectTo={requestedPath ?? undefined} />

        <TermsFooter
          variant='sign-up'
          status={status}
          className='text-center'
        />
      </div>
    </AuthLayout>
  )
}
