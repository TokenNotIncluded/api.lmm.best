/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useNavigate } from '@tanstack/react-router'
import { useState, useSyncExternalStore } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  authCookieRecoveryMessage,
  authCookieStorageMessage,
  getAuthCookieBlockedState,
  subscribeAuthCookieState,
} from '@/lib/auth-cookie'
import {
  captureLocalAuthRecoveryScope,
  endLocalAuthSessionForRecovery,
  type LocalAuthRecoveryScope,
} from '@/lib/auth-session'
import { useAuthStore } from '@/stores/auth-store'

export function AuthCookieRecovery({
  expectedScope,
  onLocalEnd,
}: {
  expectedScope?: LocalAuthRecoveryScope
  onLocalEnd?: () => void
} = {}) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const auth = useAuthStore((store) => store.auth)
  const [result, setResult] = useState<'ended' | 'superseded' | null>(null)
  const state = useSyncExternalStore(
    subscribeAuthCookieState,
    getAuthCookieBlockedState,
    () => null
  )
  // Capture the displayed account, never a potentially newer account at click time.
  const scope = expectedScope ?? captureLocalAuthRecoveryScope()
  const ended =
    result === 'ended' &&
    !auth.user &&
    !auth.pending2FAFlowToken &&
    auth.bootstrapState === 'complete'
  if (!state) return null
  const handleLocalEnd = () => {
    const outcome = endLocalAuthSessionForRecovery(scope)
    if (outcome.kind !== 'ended') {
      setResult('superseded')
      return
    }
    setResult('ended')
    onLocalEnd?.()
    void navigate({ to: '/', replace: true })
  }
  return (
    <Alert variant='destructive' className='relative z-50 rounded-none'>
      <AlertDescription className='space-y-3'>
        <p>
          {t(
            state === 'uncertain'
              ? authCookieRecoveryMessage
              : authCookieStorageMessage
          )}
        </p>
        {ended ? (
          <p role='status'>
            {t(
              "This tab's local session has ended. Server sign-out is not confirmed."
            )}
          </p>
        ) : (
          <>
            <p>
              {t(
                'This only clears sign-in state in this tab. Server sign-out is not confirmed.'
              )}
            </p>
            {result === 'superseded' && (
              <p role='status'>
                {t(
                  'Your session changed. Review the current account before trying again.'
                )}
              </p>
            )}
            <Button
              type='button'
              variant='outline'
              className='h-auto min-h-11 whitespace-normal'
              onClick={handleLocalEnd}
            >
              {t("End this tab's local session")}
            </Button>
          </>
        )}
      </AlertDescription>
    </Alert>
  )
}
