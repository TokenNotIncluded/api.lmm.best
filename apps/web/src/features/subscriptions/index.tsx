/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { Link } from '@tanstack/react-router'
import { Info, RotateCcw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { SubscriptionRecords } from './components/subscription-records'
import { SubscriptionsDialogs } from './components/subscriptions-dialogs'
import { SubscriptionsPrimaryButtons } from './components/subscriptions-primary-buttons'
import { SubscriptionsProvider } from './components/subscriptions-provider'
import { SubscriptionsTable } from './components/subscriptions-table'

export function Subscriptions() {
  const { t } = useTranslation()
  const role = useAuthStore((state) => state.auth.user?.role ?? 0)
  const [tab, setTab] = useState<'plans' | 'records'>('plans')
  const isRoot = role >= ROLE.SUPER_ADMIN

  return (
    <SubscriptionsProvider>
      <SectionPageLayout>
        <SectionPageLayout.Title>{t('Subscriptions')}</SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <div className='flex flex-wrap items-center gap-2'>
            {isRoot && (
              <Button
                variant='outline'
                size='sm'
                render={<Link to='/subscriptions/reset' />}
              >
                <RotateCcw aria-hidden='true' />
                {t('Subscription reset workspace')}
              </Button>
            )}
            {tab === 'plans' && <SubscriptionsPrimaryButtons />}
          </div>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <Alert variant='default' className='mb-4 hidden px-3 py-2 sm:flex'>
            <Info className='h-4 w-4' />
            <AlertDescription className='text-xs'>
              {t(
                'Subscription plan operations are permission-sensitive and financially impactful. Review records before making changes.'
              )}
            </AlertDescription>
          </Alert>

          <Tabs
            value={tab}
            onValueChange={(value) => setTab(value as typeof tab)}
          >
            <TabsList>
              <TabsTrigger value='plans'>{t('Plans')}</TabsTrigger>
              <TabsTrigger value='records'>
                {t('Subscription records')}
              </TabsTrigger>
            </TabsList>
            <TabsContent value='plans' className='mt-4'>
              <SubscriptionsTable />
            </TabsContent>
            <TabsContent value='records' className='mt-4'>
              <SubscriptionRecords />
            </TabsContent>
          </Tabs>
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <SubscriptionsDialogs />
    </SubscriptionsProvider>
  )
}
