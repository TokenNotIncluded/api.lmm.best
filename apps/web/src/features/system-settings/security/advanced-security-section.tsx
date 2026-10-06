/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useTranslation } from 'react-i18next'

import { SettingsSection } from '../components/settings-section'
import { SecurityAuditPanel } from './security-audit'

export function AdvancedSecuritySection() {
  const { t } = useTranslation()
  return (
    <SettingsSection title={t('Safety audit and business overview')}>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Current reviews, recorded deductions and appeals are shown here. Configure policies separately.'
        )}
      </p>
      <a
        href='/system-settings/security/moderation'
        className='text-primary text-sm underline underline-offset-4'
      >
        {t('Configure group modes and category fines')}
      </a>
      <SecurityAuditPanel />
    </SettingsSection>
  )
}
