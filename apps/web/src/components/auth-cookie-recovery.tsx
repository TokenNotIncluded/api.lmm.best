/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useSyncExternalStore } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import {
  authCookieRecoveryMessage,
  authCookieStorageMessage,
  getAuthCookieBlockedState,
  subscribeAuthCookieState,
} from '@/lib/auth-cookie'

export function AuthCookieRecovery() {
  const { t } = useTranslation()
  const state = useSyncExternalStore(
    subscribeAuthCookieState,
    getAuthCookieBlockedState,
    () => null
  )
  if (!state) return null
  return (
    <Alert variant='destructive' className='relative z-50 rounded-none'>
      <AlertDescription>
        {t(
          state === 'uncertain'
            ? authCookieRecoveryMessage
            : authCookieStorageMessage
        )}
      </AlertDescription>
    </Alert>
  )
}
