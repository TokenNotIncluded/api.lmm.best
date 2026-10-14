/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useNavigate, useRouter } from '@tanstack/react-router'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { AuthCookieRecovery } from '@/components/auth-cookie-recovery'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { GeneralError } from '@/features/errors/general-error'
import type { AuthBootstrapPendingError } from '@/lib/auth-bootstrap-navigation'

export function AuthBootstrapPending({
  error,
}: {
  error: AuthBootstrapPendingError
}) {
  const router = useRouter()
  const navigate = useNavigate()
  const { t } = useTranslation()
  const [failure, setFailure] = useState<{
    owner: AuthBootstrapPendingError
    error: unknown
  } | null>(null)

  useEffect(() => {
    let active = true
    const isCurrent = () =>
      active &&
      router.state.status === 'idle' &&
      !router.state.isLoading &&
      router.state.location.href === error.href &&
      router.state.matches.some(
        (match) => match.error === error && match.status === 'error'
      )
    const failed = (cause: unknown) => {
      if (isCurrent()) setFailure({ owner: error, error: cause })
    }
    void error.task.then((outcome) => {
      if (!isCurrent()) return
      if (outcome.kind === 'transient_error') {
        // A completed failed check offers explicit retry; invalidating here
        // would otherwise dispatch a second refresh without user input.
        failed(outcome.error)
        return
      }
      void router.invalidate({ filter: (match) => match.error === error })
    }, failed)
    return () => {
      active = false
    }
  }, [error, router])

  if (failure?.owner === error) return <GeneralError error={failure.error} />
  return (
    <div className='flex min-h-svh flex-col'>
      <AuthCookieRecovery />
      <div className='m-auto flex w-full max-w-lg flex-col items-center gap-4 px-4 py-8'>
        <div role='status' aria-live='polite'>
          <LoadingState />
        </div>
        <div className='flex flex-wrap justify-center gap-3'>
          <Button
            onClick={() =>
              void router.invalidate({
                filter: (match) => match.error === error,
              })
            }
          >
            {t('Retry')}
          </Button>
          <Button variant='outline' onClick={() => void navigate({ to: '/' })}>
            {t('Back to Home')}
          </Button>
        </div>
      </div>
    </div>
  )
}
