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
import { useNavigate, useRouter, useRouterState } from '@tanstack/react-router'
import { useMemo, useState, useSyncExternalStore } from 'react'
import { useTranslation } from 'react-i18next'

import { AuthCookieRecovery } from '@/components/auth-cookie-recovery'
import { Button } from '@/components/ui/button'
import {
  getAuthCookieBlockedState,
  subscribeAuthCookieState,
} from '@/lib/auth-cookie'
import { cn } from '@/lib/utils'

import {
  classifyError,
  copyErrorDiagnostics,
  createErrorDiagnostics,
  diagnosticRouteTemplate,
  isNonFailureError,
} from './error-diagnostics'
import { ErrorPageFrame } from './error-page-frame'

const FEEDBACK_URL = 'https://github.com/TokenNotIncluded/lmm-api/issues'

type GeneralErrorProps = React.HTMLAttributes<HTMLDivElement> & {
  minimal?: boolean
  error?: unknown
}

export function GeneralError({
  className,
  minimal = false,
  error,
}: GeneralErrorProps) {
  const blocked = useSyncExternalStore(
    subscribeAuthCookieState,
    getAuthCookieBlockedState,
    () => null
  )

  if (blocked) {
    return (
      <div className={cn('min-h-svh w-full', className)}>
        <AuthCookieRecovery />
      </div>
    )
  }

  if (isNonFailureError(error)) return null
  return (
    <GeneralFailure className={className} minimal={minimal} error={error} />
  )
}

function GeneralFailure({ className, minimal, error }: GeneralErrorProps) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const router = useRouter()
  const routeTemplate = useRouterState({
    select: (state) =>
      diagnosticRouteTemplate(state.matches, router.routesById),
  })
  const diagnostics = useMemo(
    () =>
      createErrorDiagnostics(
        error,
        [{ routeId: routeTemplate }],
        router.routesById
      ),
    [error, routeTemplate, router]
  )
  const [copyResult, setCopyResult] = useState<{
    incident: typeof diagnostics
    success: boolean
  } | null>(null)
  const [copying, setCopying] = useState(false)
  const status = classifyError(error).http_status
  const isRateLimited = status === 429
  const currentCopyResult =
    copyResult?.incident === diagnostics ? copyResult : null

  const copyDiagnostics = async () => {
    if (!diagnostics || copying) return
    setCopying(true)
    try {
      const success = await copyErrorDiagnostics(
        diagnostics,
        typeof navigator === 'undefined' ? undefined : navigator.clipboard
      )
      setCopyResult({ incident: diagnostics, success })
    } catch {
      setCopyResult({ incident: diagnostics, success: false })
    } finally {
      setCopying(false)
    }
  }

  return (
    <div className={cn('min-h-svh w-full', className)}>
      <ErrorPageFrame
        status={status ?? undefined}
        showStatus={!minimal}
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
          !minimal ? (
            <>
              <p>{t('Still broken? Report it on GitHub Issues.')}</p>
              <p>
                {t(
                  'The event ID is local to this page, not a server request ID.'
                )}
              </p>
              <p role='status' aria-live='polite'>
                {!diagnostics
                  ? t('Diagnostic information is unavailable in this browser.')
                  : currentCopyResult
                    ? currentCopyResult.success
                      ? t('Diagnostic information copied.')
                      : t('Could not copy diagnostic information. Try again.')
                    : t(
                        'Only time, build, route template, local event ID, HTTP status and error type are copied.'
                      )}
              </p>
            </>
          ) : undefined
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
                disabled={!diagnostics || copying}
                onClick={() => void copyDiagnostics()}
              >
                {t('Copy diagnostic information')}
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
      />
    </div>
  )
}
