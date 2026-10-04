/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useTranslation } from 'react-i18next'

import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'

import { SettingsSection } from '../components/settings-section'
import type { SecuritySettings } from '../types'
import { SecurityAuditPanel } from './security-audit'

type AdvancedSecuritySectionProps = {
  defaultValues: Pick<
    SecuritySettings,
    | 'AdvancedSecurityEnabled'
    | 'AdvancedSecurityOnPromptEnabled'
    | 'AdvancedSecurityAction'
    | 'AdvancedSecurityRules'
  >
}

export function AdvancedSecuritySection({
  defaultValues,
}: AdvancedSecuritySectionProps) {
  const { t } = useTranslation()
  return (
    <SettingsSection title={t('Historical safety rules')}>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Literal blocking rules are retired. Configure asynchronous OpenAI Moderation to review content.'
        )}
      </p>
      <a
        href='/system-settings/security/moderation'
        className='text-primary text-sm underline underline-offset-4'
      >
        {t('Configure group modes and category fines')}
      </a>
      <dl className='grid gap-3 text-sm sm:grid-cols-2'>
        <div>
          <dt className='text-muted-foreground'>
            {t('Saved legacy rule setting')}
          </dt>
          <dd>
            {defaultValues.AdvancedSecurityEnabled
              ? t('Enabled')
              : t('Disabled')}
          </dd>
        </div>
        <div>
          <dt className='text-muted-foreground'>
            {t('Saved legacy prompt inspection')}
          </dt>
          <dd>
            {defaultValues.AdvancedSecurityOnPromptEnabled
              ? t('Enabled')
              : t('Disabled')}
          </dd>
        </div>
        <div>
          <dt className='text-muted-foreground'>
            {t('Saved legacy response action')}
          </dt>
          <dd>
            {t(
              defaultValues.AdvancedSecurityAction === 'block'
                ? 'Block (legacy saved setting)'
                : 'Audit (legacy saved setting)'
            )}
          </dd>
        </div>
      </dl>
      <div className='space-y-2'>
        <Label htmlFor='legacy-security-rules'>
          {t('Historical rule configuration')}
        </Label>
        <Textarea
          id='legacy-security-rules'
          value={defaultValues.AdvancedSecurityRules}
          readOnly
          className='min-h-48 font-mono text-xs'
        />
      </div>
      <SecurityAuditPanel />
    </SettingsSection>
  )
}
