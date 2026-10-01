/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { DashboardSquare01Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { Link, useNavigate } from '@tanstack/react-router'
import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'
import {
  getAuthenticatedLandingRoute,
  getOnboardingState,
} from '@/lib/console-activation'
import { useAuthStore } from '@/stores/auth-store'

import { AccessRequestDetails } from './access-request-details'
import { L0Welcome } from './l0-welcome'
import { SetupWorkspace } from './setup-workspace'
import { useAccountNextStep } from './use-account-next-step'
import { useAuthUserRefresh } from './use-auth-user-refresh'

export function GettingStarted() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { refreshUser } = useAuthUserRefresh()
  const user = useAuthStore((state) => state.auth.user)
  const onboarding = getOnboardingState(user)
  const { request } = useAccountNextStep()
  const accessRequest = request.data
  const requestLoaded = request.isSuccess
  const { refetch: refetchAccessRequest } = request

  useEffect(() => {
    if (onboarding.activationComplete) return
    const onFocus = () => {
      void refetchAccessRequest()
    }
    window.addEventListener('focus', onFocus)
    return () => window.removeEventListener('focus', onFocus)
  }, [onboarding.activationComplete, refetchAccessRequest])

  useEffect(() => {
    if (!requestLoaded || accessRequest?.status !== 'approved') {
      return
    }

    void refreshUser().then(async (refreshedUser) => {
      if (refreshedUser?.developer_access_granted !== true) {
        return
      }
      await navigate({ to: getAuthenticatedLandingRoute(refreshedUser) })
    })
  }, [accessRequest?.status, navigate, refreshUser, requestLoaded])

  const continueAfterApproval = async () => {
    const refreshedUser = await refreshUser()
    if (refreshedUser?.developer_access_granted !== true) return
    await navigate({ to: getAuthenticatedLandingRoute(refreshedUser) })
  }

  if (!onboarding.activationComplete) {
    return (
      <SectionPageLayout>
        <SectionPageLayout.Title>
          {t('Getting started')}
        </SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <Button variant='ghost' size='sm' render={<Link to='/pricing' />}>
            <HugeiconsIcon
              icon={DashboardSquare01Icon}
              strokeWidth={2}
              data-icon='inline-start'
              aria-hidden='true'
            />
            {t('Models and pricing')}
          </Button>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <L0Welcome user={user}>
            <AccessRequestDetails inline />
            {requestLoaded && accessRequest?.status === 'approved' && (
              <Button
                type='button'
                size='sm'
                className='mt-3 w-fit'
                onClick={() => void continueAfterApproval()}
              >
                {t('Continue setup')}
              </Button>
            )}
          </L0Welcome>
        </SectionPageLayout.Content>
      </SectionPageLayout>
    )
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Getting started')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <SetupWorkspace />
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
