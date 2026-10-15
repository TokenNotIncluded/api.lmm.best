/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { getHoneypotPolicies, type HoneypotScope } from './api'
import { HoneypotFeedback } from './feedback'
import { HoneypotPolicyEditor } from './policy-editor'
import { HoneypotUsersPanel } from './users-panel'

export function HoneypotWorkspace(props: { scope: HoneypotScope }) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const policyKey = [
    'honeypot-policies',
    props.scope.userId,
    props.scope.sessionId,
  ]
  const policies = useQuery({
    queryKey: policyKey,
    queryFn: () => getHoneypotPolicies(props.scope),
    retry: false,
    refetchOnWindowFocus: false,
  })
  return (
    <Tabs defaultValue='policies' className='space-y-4'>
      <TabsList variant='navigation' aria-label={t('Honeypot')}>
        <TabsTrigger value='policies'>{t('Policies')}</TabsTrigger>
        <TabsTrigger value='users'>{t('Users')}</TabsTrigger>
      </TabsList>
      <TabsContent value='policies' keepMounted className='space-y-3'>
        <HoneypotFeedback error={policies.error} />
        {policies.isPending && <p role='status'>{t('Loading...')}</p>}
        {policies.data && (
          <HoneypotPolicyEditor
            initial={policies.data}
            scope={props.scope}
            onSnapshot={(snapshot) => client.setQueryData(policyKey, snapshot)}
          />
        )}
        {policies.isError && (
          <Button variant='outline' onClick={() => void policies.refetch()}>
            {t('Retry')}
          </Button>
        )}
      </TabsContent>
      <TabsContent value='users' keepMounted>
        <HoneypotUsersPanel scope={props.scope} />
      </TabsContent>
    </Tabs>
  )
}

export function HoneypotPage() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const sessionID = useAuthStore((state) => state.auth.session?.sid)
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Honeypot')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        {user?.role === ROLE.SUPER_ADMIN && sessionID ? (
          <HoneypotWorkspace
            key={`${user.id}:${sessionID}`}
            scope={{ userId: user.id, sessionId: sessionID }}
          />
        ) : (
          <p role='alert'>{t('Super administrator access required.')}</p>
        )}
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
