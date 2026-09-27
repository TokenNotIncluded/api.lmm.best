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
import { useNavigate } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

import { ErrorPageFrame } from './error-page-frame'
import { SignalTuner } from './signal-tuner'

const FEEDBACK_URL = 'https://github.com/TokenNotIncluded/api.lmm.best/issues'

type GeneralErrorProps = React.HTMLAttributes<HTMLDivElement> & {
  minimal?: boolean
  error?: unknown
}

function getHttpStatus(error: unknown): number | undefined {
  if (typeof error !== 'object' || error === null) return undefined
  const response = (error as Record<string, unknown>).response
  if (typeof response !== 'object' || response === null) return undefined
  const status = (response as Record<string, unknown>).status
  return typeof status === 'number' ? status : undefined
}

export function GeneralError({
  className,
  minimal = false,
  error,
}: GeneralErrorProps) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const status = getHttpStatus(error)
  const isRateLimited = status === 429

  return (
    <div className={cn('min-h-svh w-full', className)}>
      <ErrorPageFrame
        status={status ?? 500}
        showStatus={!minimal}
        artSrc='/error-recovery-oat.png'
        title={
          isRateLimited
            ? t('Too many requests. Give it a beat.')
            : t('The request fell over.')
        }
        description={
          isRateLimited
            ? t('Slow down for a second, then send it again.')
            : t('Reload the page to try again.')
        }
        note={
          !minimal ? t('Still broken? Report it on GitHub Issues.') : undefined
        }
        actions={
          !minimal ? (
            <>
              <Button
                size='lg'
                className='error-editorial-action error-editorial-action-primary'
                onClick={() => window.location.reload()}
              >
                {t('Retry')}
              </Button>
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
                render={
                  <a
                    href={FEEDBACK_URL}
                    target='_blank'
                    rel='noopener noreferrer'
                  />
                }
              >
                {t('Report an issue')}
              </Button>
            </>
          ) : undefined
        }
        play={!minimal ? <SignalTuner /> : undefined}
      />
    </div>
  )
}
