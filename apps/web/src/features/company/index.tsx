/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { useAuthStore } from '@/stores/auth-store'

import { CompanyBillingProfileCard } from './company-billing-profile-card'

export function Company() {
  const { t } = useTranslation()
  const ownerUserId = useAuthStore((state) => state.auth.user?.id)

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Company')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='mx-auto w-full max-w-4xl'>
          <CompanyBillingProfileCard
            key={ownerUserId ?? 'signed-out-company-profile'}
          />
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
