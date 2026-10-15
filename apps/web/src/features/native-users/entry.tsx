/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { useAuthStore } from '@/stores/auth-store'

import {
  isCurrentNativeUsersScope,
  NativeUsersAccessBoundary,
  readNativeUsersAvailability,
  readNativeUsersScope,
  type NativeUsersScope,
} from './access'

type Props = {
  children: (scope: NativeUsersScope, recheck: () => void) => ReactNode
}

function UsersProtocolGate({
  scope,
  children,
}: Props & { scope: NativeUsersScope }) {
  const { t } = useTranslation()
  const [state, setState] = useState<
    'loading' | 'available' | 'unavailable' | 'forbidden'
  >('loading')
  const [probe, setProbe] = useState(0)
  const request = useRef<{
    alive: boolean
    generation: number
    controller?: AbortController
  }>({ alive: true, generation: 0 })
  const { userId, sessionId, role } = scope
  const denyAccess = useCallback(() => {
    request.current.generation++
    request.current.controller?.abort()
    // Workspace unmounting clears all private state and cancels its requests.
    // A public status response cannot automatically restore a denied view.
    setState('forbidden')
  }, [])
  useEffect(() => {
    const holder = request.current
    holder.alive = true
    const generation = ++holder.generation
    const controller = new AbortController()
    holder.controller = controller
    const captured = { userId, sessionId, role }
    const owns = () =>
      holder.alive &&
      holder.generation === generation &&
      isCurrentNativeUsersScope(captured)
    setState('loading')
    void readNativeUsersAvailability(captured, controller.signal).then(
      (available) => {
        if (owns()) setState(available ? 'available' : 'unavailable')
      },
      () => {
        if (owns()) setState('unavailable')
      }
    )
    return () => {
      holder.alive = false
      holder.generation++
      controller.abort()
    }
  }, [userId, sessionId, role, probe])
  const recheck = () => {
    request.current.generation++
    request.current.controller?.abort()
    // Remove all private workspace state before starting a new live probe.
    setState('loading')
    setProbe((value) => value + 1)
  }
  if (!isCurrentNativeUsersScope(scope)) return null
  if (state === 'available') {
    return (
      <NativeUsersAccessBoundary.Provider value={denyAccess}>
        {children(scope, recheck)}
      </NativeUsersAccessBoundary.Provider>
    )
  }
  return (
    <div className='space-y-4' aria-busy={state === 'loading'}>
      {state === 'loading' ? (
        <p role='status'>{t('Checking user management availability...')}</p>
      ) : (
        <>
          <Alert role='alert'>
            <AlertDescription>
              {state === 'forbidden'
                ? t(
                    'Administrator access or multi-factor verification is required.'
                  )
                : t('User management is unavailable on this server.')}
            </AlertDescription>
          </Alert>
          <Button
            type='button'
            variant='outline'
            className='min-h-11 sm:min-h-9'
            onClick={recheck}
          >
            {t('Try again')}
          </Button>
        </>
      )}
    </div>
  )
}

export function NativeAdminUsersEntry({ children }: Props) {
  const { t } = useTranslation()
  const auth = useAuthStore((state) => state.auth)
  const scope = readNativeUsersScope(auth)
  return (
    <SectionPageLayout className='users-page'>
      <SectionPageLayout.Title>{t('Users')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        {!scope ? (
          <Alert role='alert'>
            <AlertDescription>
              {t('A current administrator session is required.')}
            </AlertDescription>
          </Alert>
        ) : (
          <UsersProtocolGate
            key={`${scope.userId}:${scope.sessionId}:${scope.role}`}
            scope={scope}
          >
            {children}
          </UsersProtocolGate>
        )}
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
