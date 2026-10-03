/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Separator } from '@/components/ui/separator'
import { Switch } from '@/components/ui/switch'
import { toIntlLocale } from '@/i18n/languages'

import type { ProfileShareState } from '../api'
import {
  MAX_LINKED_PROFILES,
  prepareLinkedProfiles,
  profileDraft,
  type LinkedProfileDraft,
} from '../lib/linked-profiles'
import type {
  ProfileAggregateSettings,
  ProfileAggregateSource,
  LinkedProfileProvider,
  LinkedUsageProfile,
  ProfileSnapshotPeriod,
} from '../types'

const statusKeys = {
  live: 'Automatically read',
  snapshot: 'Imported snapshot',
  login_required: 'Sign-in required',
  unavailable: 'Unavailable',
  unsupported: 'Snapshot needed',
  disabled: 'Sharing is off',
} as const

const periodKeys: Record<string, string> = {
  all: 'All time',
  '7d': 'Last 7 days',
  '30d': 'Last 30 days',
  '365d': 'Last 365 days',
  reported: 'Reported period',
  custom: 'Custom period',
}

export function AggregateSourceSummary({
  source,
}: {
  source: ProfileAggregateSource
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language) ?? 'en'
  const available = source.status === 'live' || source.status === 'snapshot'
  const time = source.observed_at || source.fetched_at
  const parsedTime = time ? new Date(time) : undefined
  return (
    <div className='space-y-2 text-sm' data-source-status={source.status}>
      <div className='flex flex-wrap items-center gap-x-3 gap-y-2'>
        <Badge variant={available ? 'secondary' : 'outline'}>
          {t(statusKeys[source.status])}
        </Badge>
        {available ? (
          <span className='text-muted-foreground'>
            {t(
              source.source === 'native'
                ? 'LMM account data'
                : source.source === 'public_ssr'
                  ? 'Public profile data'
                  : 'Manually entered; does not update automatically'
            )}
          </span>
        ) : null}
      </div>
      {available ? (
        <>
          <div className='flex flex-wrap gap-x-5 gap-y-1 tabular-nums'>
            {(['tokens', 'requests', 'messages', 'agents'] as const).map(
              (key) =>
                source[key] !== undefined ? (
                  <span key={key}>
                    {source.approximate
                      ? t('About {{number}}', {
                          number: source[key].toLocaleString(locale),
                        })
                      : source[key].toLocaleString(locale)}{' '}
                    {t(
                      {
                        tokens: 'Tokens',
                        requests: 'Requests',
                        messages: 'Messages',
                        agents: 'Agents',
                      }[key]
                    )}
                  </span>
                ) : null
            )}
          </div>
          <p className='text-muted-foreground text-xs leading-relaxed'>
            {source.status === 'snapshot' &&
            source.period !== 'all' &&
            source.observed_at
              ? t('{{period}} as observed on {{date}}', {
                  period: t(periodKeys[source.period] ?? 'Reported period'),
                  date: source.observed_at.slice(0, 10),
                })
              : t(periodKeys[source.period] ?? 'Reported period')}
            {source.period_start && source.period_end
              ? ` · ${source.period_start} – ${source.period_end}`
              : ''}
            {source.period_timezone === 'unspecified'
              ? ` · ${t('Source time zone not specified')}`
              : ''}
          </p>
          {parsedTime && Number.isFinite(parsedTime.getTime()) ? (
            <p className='text-muted-foreground text-xs'>
              {t(source.observed_at ? 'Observed {{time}}' : 'Read {{time}}', {
                time: parsedTime.toLocaleString(locale),
              })}
            </p>
          ) : null}
          {source.snapshot_source ? (
            <p className='text-muted-foreground text-xs'>
              {source.snapshot_source}
            </p>
          ) : null}
        </>
      ) : (
        <p className='text-muted-foreground text-xs leading-relaxed'>
          {t(
            source.status === 'login_required'
              ? 'This provider does not expose usage publicly. Import numbers you can see in your account.'
              : source.status === 'disabled'
                ? 'Enable sharing to include this account. LMM model sharing has its own permission.'
                : source.status === 'unsupported'
                  ? 'This profile cannot be read automatically. You can import a usage snapshot.'
                  : 'Usage could not be read. Retry or import a snapshot; missing data is not zero.'
          )}
        </p>
      )}
    </div>
  )
}

interface LinkedUsageProfilesProps {
  state: ProfileShareState
  accountName?: string
  onSave: (settings: ProfileAggregateSettings) => Promise<void>
  onRefresh: () => void
  refreshing: boolean
  onToggleModelSharing: () => void
  modelSharingBusy: boolean
  sharingBusy: boolean
}

export function LinkedUsageProfiles({
  state,
  accountName,
  onSave,
  onRefresh,
  refreshing,
  onToggleModelSharing,
  modelSharingBusy,
  sharingBusy,
}: LinkedUsageProfilesProps) {
  const { t } = useTranslation()
  const signature = JSON.stringify(state.linked_profiles ?? [])
  const savedEnabled = Boolean(state.aggregate_usage_enabled)
  const [drafts, setDrafts] = useState<LinkedProfileDraft[]>(() =>
    (state.linked_profiles ?? []).map((profile) => profileDraft(profile))
  )
  const [enabled, setEnabled] = useState(Boolean(state.aggregate_usage_enabled))
  const [dirty, setDirty] = useState(false)
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState(false)
  const [saved, setSaved] = useState(false)
  const [errors, setErrors] = useState<Record<string, string>>({})
  const busy = saving || sharingBusy
  useEffect(() => {
    const profiles = JSON.parse(signature) as LinkedUsageProfile[]
    setDrafts(profiles.map((profile) => profileDraft(profile)))
    setDirty(false)
    setErrors({})
  }, [signature])
  useEffect(() => setEnabled(savedEnabled), [savedEnabled])
  const change = (id: string, patch: Partial<LinkedProfileDraft>) => {
    setDrafts((rows) =>
      rows.map((row) => (row.id === id ? { ...row, ...patch } : row))
    )
    setDirty(true)
    setSaved(false)
    setSaveError(false)
    setErrors((previous) => ({ ...previous, [id]: '' }))
  }
  const save = async () => {
    const prepared = prepareLinkedProfiles(drafts)
    setErrors(prepared.errors)
    if (!prepared.valid) return
    setSaving(true)
    setSaveError(false)
    try {
      await onSave({
        aggregate_usage_enabled: enabled,
        linked_profiles: prepared.profiles,
      })
      setDirty(false)
      setSaved(true)
    } catch {
      setSaveError(true)
    } finally {
      setSaving(false)
    }
  }
  const lmm = state.aggregate_sources?.find(
    (source) => source.provider === 'lmm'
  )
  return (
    <section aria-labelledby='linked-profiles-heading' className='space-y-5'>
      <div className='flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between'>
        <div>
          <h2
            id='linked-profiles-heading'
            className='text-lg font-semibold tracking-tight'
          >
            {t('Accounts in your badge')}
          </h2>
          <p className='text-muted-foreground mt-1 max-w-2xl text-sm leading-relaxed'>
            {t(
              'Choose a provider and paste its public profile URL. You can add several accounts from the same provider.'
            )}
          </p>
        </div>
        <Button
          variant='outline'
          className='min-h-11'
          disabled={refreshing || busy}
          onClick={onRefresh}
        >
          {t(refreshing ? 'Reading profiles…' : 'Refresh usage')}
        </Button>
      </div>
      <FieldGroup className='gap-5'>
        <Field orientation='horizontal' data-disabled={busy}>
          <div className='min-h-11 flex-1'>
            <FieldLabel htmlFor='aggregate-sharing' className='min-h-11'>
              {t('Share linked accounts in one SVG')}
            </FieldLabel>
            <FieldDescription>
              {t(
                'Profile links and imported usage become public when sharing is enabled.'
              )}
            </FieldDescription>
          </div>
          <Switch
            id='aggregate-sharing'
            checked={enabled}
            disabled={busy}
            onCheckedChange={(value) => {
              setEnabled(value)
              setDirty(true)
              setSaved(false)
            }}
          />
        </Field>
      </FieldGroup>
      <Separator />
      <div className='space-y-3' data-testid='lmm-self-profile'>
        <div className='flex flex-wrap items-center justify-between gap-3'>
          <div>
            <h3 className='font-medium'>LMM Forge</h3>
            <p className='text-muted-foreground text-sm'>
              {accountName || t('Your current account')}
            </p>
          </div>
          <Button
            variant='outline'
            className='min-h-11'
            disabled={modelSharingBusy || busy}
            onClick={onToggleModelSharing}
          >
            {t(
              state.model_usage_enabled
                ? 'Turn off model sharing'
                : 'Turn on model sharing'
            )}
          </Button>
        </div>
        {lmm ? (
          <AggregateSourceSummary source={lmm} />
        ) : (
          <p className='text-muted-foreground text-sm'>
            {t(
              'LMM model sharing has its own permission. Your current account is included automatically.'
            )}
          </p>
        )}
        <p className='text-muted-foreground text-xs leading-relaxed'>
          {t(
            'Only total tokens and requests are included here. Model names and spend use the separate model-sharing permission.'
          )}
        </p>
      </div>
      {drafts.map((draft, index) => {
        const source = state.aggregate_sources?.find(
          (entry) =>
            entry.provider === draft.provider && entry.url === draft.url.trim()
        )
        const prefix = `linked-profile-${draft.id}`
        return (
          <div
            key={draft.id}
            data-testid='linked-profile-row'
            className='space-y-4'
          >
            <Separator />
            <div className='flex items-center justify-between gap-3'>
              <h3 className='font-medium'>
                {t('Account {{number}}', { number: index + 1 })}
              </h3>
              <Button
                variant='ghost'
                className='min-h-11'
                disabled={busy}
                aria-label={t('Remove account {{number}}', {
                  number: index + 1,
                })}
                onClick={() => {
                  setDrafts((rows) => rows.filter((row) => row.id !== draft.id))
                  setDirty(true)
                  setSaved(false)
                }}
              >
                {t('Remove')}
              </Button>
            </div>
            <FieldGroup className='grid gap-4 sm:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]'>
              <Field data-disabled={busy}>
                <FieldLabel htmlFor={`${prefix}-provider`}>
                  {t('Provider')}
                </FieldLabel>
                <NativeSelect
                  id={`${prefix}-provider`}
                  className='w-full'
                  value={draft.provider}
                  disabled={busy}
                  onChange={(event) =>
                    change(draft.id, {
                      ...profileDraft(),
                      id: draft.id,
                      provider: event.target.value as LinkedProfileProvider,
                    })
                  }
                >
                  <NativeSelectOption value='cursor'>Cursor</NativeSelectOption>
                  <NativeSelectOption value='chatgpt'>
                    ChatGPT
                  </NativeSelectOption>
                  <NativeSelectOption value='custom'>
                    {t('Other provider')}
                  </NativeSelectOption>
                </NativeSelect>
              </Field>
              <Field
                data-invalid={Boolean(errors[draft.id])}
                data-disabled={busy}
              >
                <FieldLabel htmlFor={`${prefix}-url`}>
                  {t('Public profile URL')}
                </FieldLabel>
                <Input
                  id={`${prefix}-url`}
                  type='url'
                  className='min-h-11'
                  value={draft.url}
                  maxLength={512}
                  disabled={busy}
                  aria-invalid={Boolean(errors[draft.id])}
                  placeholder={
                    draft.provider === 'cursor'
                      ? 'https://cursor.com/@your-handle'
                      : draft.provider === 'chatgpt'
                        ? 'https://chatgpt.com/u/your-handle'
                        : 'https://example.com/profile/your-handle'
                  }
                  onChange={(event) =>
                    change(draft.id, { url: event.target.value })
                  }
                />
              </Field>
              <Field className='sm:col-span-2' data-disabled={busy}>
                <FieldLabel htmlFor={`${prefix}-label`}>
                  {t('Account label (optional)')}
                </FieldLabel>
                <Input
                  id={`${prefix}-label`}
                  className='min-h-11'
                  value={draft.label}
                  disabled={busy}
                  onChange={(event) =>
                    change(draft.id, { label: event.target.value })
                  }
                />
              </Field>
            </FieldGroup>
            {source ? (
              <AggregateSourceSummary source={source} />
            ) : (
              <p className='text-muted-foreground text-sm'>
                {t('Save this account to read its usage.')}
              </p>
            )}
            <details>
              <summary className='focus-visible:ring-ring flex min-h-11 cursor-pointer items-center text-sm font-medium focus-visible:ring-2 focus-visible:outline-none'>
                {t('Import a usage snapshot')}
              </summary>
              <FieldGroup className='gap-4 pt-3'>
                <Field orientation='horizontal' data-disabled={busy}>
                  <FieldLabel
                    htmlFor={`${prefix}-import`}
                    className='min-h-11 flex-1'
                  >
                    {t('Use manually entered numbers')}
                  </FieldLabel>
                  <Switch
                    id={`${prefix}-import`}
                    checked={draft.importSnapshot}
                    disabled={busy}
                    onCheckedChange={(value) =>
                      change(draft.id, { importSnapshot: value })
                    }
                  />
                </Field>
                <FieldDescription>
                  {t(
                    'Copy numbers from your provider account. A snapshot keeps its observation date and does not refresh automatically.'
                  )}
                </FieldDescription>
                {draft.importSnapshot ? (
                  <>
                    <FieldGroup className='grid gap-4 sm:grid-cols-3'>
                      {(['tokens', 'requests', 'messages'] as const).map(
                        (key) => (
                          <Field key={key} data-disabled={busy}>
                            <FieldLabel htmlFor={`${prefix}-${key}`}>
                              {t(
                                {
                                  tokens: 'Tokens',
                                  requests: 'Requests',
                                  messages: 'Messages',
                                }[key]
                              )}
                            </FieldLabel>
                            <Input
                              id={`${prefix}-${key}`}
                              className='min-h-11'
                              inputMode='numeric'
                              value={draft[key]}
                              disabled={busy}
                              placeholder={t('Leave unknown values blank')}
                              onChange={(event) =>
                                change(draft.id, { [key]: event.target.value })
                              }
                            />
                          </Field>
                        )
                      )}
                    </FieldGroup>
                    <FieldGroup className='grid gap-4 sm:grid-cols-2'>
                      <Field data-disabled={busy}>
                        <FieldLabel htmlFor={`${prefix}-period`}>
                          {t('Snapshot period')}
                        </FieldLabel>
                        <NativeSelect
                          id={`${prefix}-period`}
                          className='w-full'
                          value={draft.period}
                          disabled={busy}
                          onChange={(event) =>
                            change(draft.id, {
                              period: event.target
                                .value as ProfileSnapshotPeriod,
                              start: '',
                              end: '',
                            })
                          }
                        >
                          {Object.entries(periodKeys)
                            .filter(([key]) => key !== 'reported')
                            .map(([key, label]) => (
                              <NativeSelectOption key={key} value={key}>
                                {t(label)}
                              </NativeSelectOption>
                            ))}
                        </NativeSelect>
                      </Field>
                      <Field data-disabled={busy}>
                        <FieldLabel htmlFor={`${prefix}-observed`}>
                          {t('Observed at')}
                        </FieldLabel>
                        <Input
                          id={`${prefix}-observed`}
                          className='min-h-11'
                          type='datetime-local'
                          step='1'
                          value={draft.observedAt}
                          disabled={busy}
                          onChange={(event) =>
                            change(draft.id, { observedAt: event.target.value })
                          }
                        />
                        <FieldDescription>
                          {t('Uses your local time zone.')}
                        </FieldDescription>
                      </Field>
                      {draft.period === 'custom' || draft.start || draft.end
                        ? (['start', 'end'] as const).map((key) => (
                            <Field key={key} data-disabled={busy}>
                              <FieldLabel htmlFor={`${prefix}-${key}`}>
                                {t(
                                  key === 'start'
                                    ? 'Period start'
                                    : 'Period end'
                                )}
                              </FieldLabel>
                              <Input
                                id={`${prefix}-${key}`}
                                className='min-h-11'
                                type='date'
                                value={draft[key]}
                                disabled={busy}
                                onChange={(event) =>
                                  change(draft.id, {
                                    [key]: event.target.value,
                                  })
                                }
                              />
                            </Field>
                          ))
                        : null}
                      <Field className='sm:col-span-2' data-disabled={busy}>
                        <FieldLabel htmlFor={`${prefix}-source`}>
                          {t('Source note (optional)')}
                        </FieldLabel>
                        <Input
                          id={`${prefix}-source`}
                          className='min-h-11'
                          value={draft.source}
                          disabled={busy}
                          onChange={(event) =>
                            change(draft.id, { source: event.target.value })
                          }
                        />
                      </Field>
                      <Field
                        orientation='horizontal'
                        className='sm:col-span-2'
                        data-disabled={busy}
                      >
                        <FieldLabel
                          htmlFor={`${prefix}-approximate`}
                          className='min-h-11 flex-1'
                        >
                          {t('These numbers are approximate')}
                        </FieldLabel>
                        <Switch
                          id={`${prefix}-approximate`}
                          checked={draft.approximate}
                          disabled={busy}
                          onCheckedChange={(value) =>
                            change(draft.id, { approximate: value })
                          }
                        />
                      </Field>
                    </FieldGroup>
                  </>
                ) : null}
              </FieldGroup>
            </details>
            {errors[draft.id] ? (
              <FieldError>{t(errors[draft.id])}</FieldError>
            ) : null}
          </div>
        )
      })}
      <div className='flex flex-wrap items-center gap-3'>
        <Button
          data-testid='add-linked-account'
          variant='outline'
          className='min-h-11'
          disabled={busy || drafts.length >= MAX_LINKED_PROFILES}
          onClick={() => {
            setDrafts((rows) => [...rows, profileDraft()])
            setDirty(true)
            setSaved(false)
          }}
        >
          {t('Add account')}
        </Button>
        <span className='text-muted-foreground text-xs tabular-nums'>
          {t('{{count}} of {{max}} accounts', {
            count: drafts.length,
            max: MAX_LINKED_PROFILES,
          })}
        </span>
      </div>
      <Separator />
      <p className='text-muted-foreground max-w-2xl text-sm leading-relaxed'>
        {t(
          'Each source keeps its own period. Different periods are shown separately, not added into a misleading total.'
        )}
      </p>
      <div className='flex flex-col gap-3 sm:flex-row sm:items-center'>
        <Button
          data-testid='save-linked-accounts'
          className='min-h-11'
          disabled={busy || !dirty}
          onClick={() => void save()}
        >
          {t(saving ? 'Saving…' : 'Save linked accounts')}
        </Button>
        <span role='status' className='text-muted-foreground text-sm'>
          {saved
            ? t('Linked accounts saved.')
            : dirty
              ? t('Unsaved changes')
              : t('All changes saved')}
        </span>
      </div>
      {saveError ? (
        <FieldError>
          {t(
            'Could not save linked accounts. Your changes are still here. Try saving again.'
          )}
        </FieldError>
      ) : null}
    </section>
  )
}
