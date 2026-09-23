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
import { useNavigate, useRouter } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { useAuthStore } from '@/stores/auth-store'

import { ErrorPageFrame } from './error-page-frame'
import { SignalTuner } from './signal-tuner'

export function ForbiddenError() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { history } = useRouter()
  const user = useAuthStore((state) => state.auth.user)

  // Signing in is the only real fix for a permissions wall, so the button that
  // moves the visitor forward sits first and carries the filled style.
  const signIn = () =>
    navigate({ to: '/sign-in', search: { redirect: '/wallet' } })

  return (
    <ErrorPageFrame
      status='403'
      title={t('This one is members only.')}
      description={t('Your account does not cover this page yet.')}
      note={
        user
          ? t('Ask an administrator for access, or check the guide.')
          : t('Sign in and the right pages will open themselves.')
      }
      actions={
        <>
          {user ? (
            <Button
              size='lg'
              className='error-editorial-action error-editorial-action-primary'
              onClick={() => navigate({ to: '/wallet' })}
            >
              {t('Open wallet')}
            </Button>
          ) : (
            <Button
              size='lg'
              className='error-editorial-action error-editorial-action-primary'
              onClick={signIn}
            >
              {t('Sign in to get started')}
            </Button>
          )}
          <Button
            variant='outline'
            className='error-editorial-action error-editorial-action-secondary'
            onClick={() => navigate({ to: '/' })}
          >
            {t('Back to Home')}
          </Button>
          <Button
            variant='outline'
            className='error-editorial-action error-editorial-action-secondary'
            onClick={() => history.go(-1)}
          >
            {t('Go Back')}
          </Button>
        </>
      }
      play={<SignalTuner />}
    />
  )
}
