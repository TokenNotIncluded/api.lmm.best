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

import { ErrorPageFrame } from './error-page-frame'
import { SignalTuner } from './signal-tuner'

/** Public pages a lost visitor most likely meant to open. */
const RESCUE_LINKS = [
  { to: '/', label: 'Home' },
  { to: '/pricing', label: 'Models and pricing' },
  { to: '/guide', label: 'Guide' },
  { to: '/challenges', label: 'Challenges' },
] as const

export function NotFoundError() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { history } = useRouter()
  return (
    <ErrorPageFrame
      status='404'
      title={t('This page slipped through the grid.')}
      description={t('Nothing lives at this address.')}
      actions={
        <>
          <Button
            size='lg'
            className='error-editorial-action error-editorial-action-primary'
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
      note={
        <span className='flex flex-wrap gap-x-4 gap-y-2'>
          {RESCUE_LINKS.map((link) => (
            <button
              key={link.to}
              type='button'
              className='underline underline-offset-4 hover:no-underline'
              onClick={() => navigate({ to: link.to })}
            >
              {t(link.label)}
            </button>
          ))}
        </span>
      }
      play={<SignalTuner />}
    />
  )
}
