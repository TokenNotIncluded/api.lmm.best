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
import { useCallback, useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { AssistantRegistrationStatus } from '@/features/assistant/assistant-registration-status'
import {
  getAuthenticatedLandingRoute,
  getOnboardingState,
} from '@/lib/console-activation'
import { useAuthStore } from '@/stores/auth-store'

import { L0Welcome } from './l0-welcome'
import { SetupWorkspace } from './setup-workspace'
import { useAuthUserRefresh } from './use-auth-user-refresh'

export function GettingStarted() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { refreshUser } = useAuthUserRefresh()
  const user = useAuthStore((state) => state.auth.user)
  const onboarding = getOnboardingState(user)
  const wasActivated = useRef(onboarding.activationComplete)
  useEffect(() => {
    const justActivated = !wasActivated.current && onboarding.activationComplete
    wasActivated.current = onboarding.activationComplete
    if (justActivated) {
      void navigate({ to: getAuthenticatedLandingRoute(user) })
    }
  }, [navigate, onboarding.activationComplete, user])
  const continueAfterActivation = useCallback(async () => {
    const refreshedUser = await refreshUser()
    if (refreshedUser?.developer_access_granted !== true) return
    await navigate({ to: getAuthenticatedLandingRoute(refreshedUser) })
  }, [refreshUser, navigate])

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
            <AssistantRegistrationStatus
              onApproved={continueAfterActivation}
              onContinueSetup={continueAfterActivation}
            />
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
