/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { toIntlLocale } from '@/i18n/languages'

import { getBountyKind, type BountyKind, type BountyProject, type BountyPublisherType } from './types'
import { isBountyDeliveryUrl, isBountyRecruitmentOpen } from './validation'

type Metadata = {
  kind: BountyKind
  publisherType: BountyPublisherType
  deadlineAt: string
  repositoryUrl: string
}

export function GeneralBountyFields<T extends Metadata>({
  value, onChange, disabled, generalSupported, deadlineError,
}: {
  value: T
  onChange: (value: T) => void
  disabled: boolean
  generalSupported: boolean
  deadlineError?: string
}) {
  const { t } = useTranslation()
  if (!generalSupported) return null
  return (
    <div className='grid gap-5 sm:grid-cols-2'>
      <fieldset disabled={disabled} className='min-w-0 space-y-2'>
        <legend className='text-sm font-medium'>{t('Task')} · {t('Type')}</legend>
        <div className='flex flex-wrap gap-2'>
          {(['general', 'open_source'] as const).map((kind) => (
            <Button key={kind} type='button' aria-pressed={value.kind === kind}
              variant={value.kind === kind ? 'default' : 'outline'}
              onClick={() => onChange({ ...value, kind, repositoryUrl: kind === 'general' ? '' : value.repositoryUrl })}>
              {t(kind === 'general' ? 'General' : 'Open source')}
            </Button>
          ))}
        </div>
      </fieldset>
      <fieldset disabled={disabled} className='min-w-0 space-y-2'>
        <legend className='text-sm font-medium'>{t('Personal')} / {t('Company')}</legend>
        <div className='flex flex-wrap gap-2'>
          {(['individual', 'company'] as const).map((publisherType) => (
            <Button key={publisherType} type='button' aria-pressed={value.publisherType === publisherType}
              variant={value.publisherType === publisherType ? 'default' : 'outline'}
              onClick={() => onChange({ ...value, publisherType })}>
              {t(publisherType === 'company' ? 'Company' : 'Personal')}
            </Button>
          ))}
        </div>
      </fieldset>
      <div className='space-y-2 sm:col-span-2'>
        <Label htmlFor='bounty-deadline'>{t('Accept challenge')} · {t('Expires at')}</Label>
        <Input id='bounty-deadline' type='datetime-local' max='9999-12-31T23:59'
          value={value.deadlineAt} disabled={disabled}
          onChange={(event) => onChange({ ...value, deadlineAt: event.target.value })}
          aria-invalid={Boolean(deadlineError)}
          aria-describedby={deadlineError ? 'bounty-deadline-error' : 'bounty-deadline-help'} />
        {deadlineError ? (
          <p id='bounty-deadline-error' role='alert' className='text-destructive text-sm'>{deadlineError}</p>
        ) : <p id='bounty-deadline-help' className='text-muted-foreground text-xs'>{t('Optional')} · {t('Never expires')}</p>}
        {value.deadlineAt && !disabled ? (
          <Button type='button' variant='ghost' size='sm' onClick={() => onChange({ ...value, deadlineAt: '' })}>
            {t('Never expires')}
          </Button>
        ) : null}
      </div>
    </div>
  )
}

export function BountyMetadata({ project }: { project: BountyProject }) {
  const { t, i18n } = useTranslation()
  return (
    <div className='text-muted-foreground flex flex-wrap gap-x-4 gap-y-2 text-xs'>
      <span>{t(getBountyKind(project) === 'general' ? 'General' : 'Open source')}</span>
      <span>{t(project.publisher_type === 'company' ? 'Company' : 'Personal')}</span>
      <span>{t('Accept challenge')} · {project.deadline_at ? (
        <time dateTime={new Date(project.deadline_at * 1000).toISOString()}>
          {new Date(project.deadline_at * 1000).toLocaleString(toIntlLocale(i18n.resolvedLanguage || i18n.language))}
        </time>
      ) : t('Never expires')}
      {!isBountyRecruitmentOpen(project) ? ` · ${t('Expired')}` : ''}</span>
    </div>
  )
}

export function BountyDeliveryLink({ url }: { url?: string }) {
  const { t } = useTranslation()
  // Do not trust display values merely because they arrived from an API.
  if (!url || !isBountyDeliveryUrl(url)) return null
  return (
    <Button variant='outline' render={<a href={url} target='_blank' rel='noopener noreferrer' />}>
      {t('Delivery evidence')}
    </Button>
  )
}
