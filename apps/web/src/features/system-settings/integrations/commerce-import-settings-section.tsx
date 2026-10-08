/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { commerceImportApi } from '@/features/store/commerce-import-api'
import { StoreError, StoreLoading } from '@/features/store/shared'
import { useAuthStore } from '@/stores/auth-store'

import { SettingsForm } from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { getOptionValue, useSystemOptions } from '../hooks/use-system-options'
import { useUpdateOptions } from '../hooks/use-update-option'
import { commerceImportTrustedOrigins } from './commerce-import-settings'
import { COMMERCE_IMPORT_SETTINGS_COPY as copy } from './commerce-import-settings-copy'

function originsText(raw: string) {
  try {
    const values: unknown = JSON.parse(raw)
    return Array.isArray(values) &&
      values.every((value) => typeof value === 'string')
      ? values.join('\n')
      : raw
  } catch {
    return raw
  }
}

export function CommerceImportSettingsSection() {
  const userId = useAuthStore((state) => state.auth.user?.id)
  const sessionId = useAuthStore((state) => state.auth.session?.sid)
  const root = useAuthStore((state) => (state.auth.user?.role ?? 0) >= 100)
  if (!root) return null
  return (
    <CommerceImportSettingsRoot
      key={`${userId}:${sessionId}`}
      userId={userId}
      sessionId={sessionId}
    />
  )
}
function CommerceImportSettingsRoot({
  userId,
  sessionId,
}: {
  userId: number | undefined
  sessionId: string | undefined
}) {
  const options = useSystemOptions()
  const config = useQuery({
    queryKey: ['store', 'commerce-import', userId, sessionId, 'config'],
    queryFn: ({ signal }) =>
      commerceImportApi.config({ userId, sessionId }, signal),
    retry: false,
    gcTime: 0,
  })
  if (options.isPending) return <StoreLoading />
  if (options.error) {
    return (
      <StoreError error={options.error} retry={() => void options.refetch()} />
    )
  }
  const values = getOptionValue(options.data?.data, {
    MerchantStoreCommerceImportEnabled: false,
    MerchantStoreCommerceImportTrustedOrigins: '[]',
  })
  return (
    <CommerceImportSettingsForm
      key={JSON.stringify(values)}
      enabled={values.MerchantStoreCommerceImportEnabled}
      origins={originsText(values.MerchantStoreCommerceImportTrustedOrigins)}
      environmentOverride={config.data?.environment_override === true}
      effectiveOrigins={config.data?.trusted_origins || []}
    />
  )
}
export function CommerceImportSettingsForm({
  enabled: initialEnabled,
  origins: initialOrigins,
  environmentOverride = false,
  effectiveOrigins = [],
}: {
  enabled: boolean
  origins: string
  environmentOverride?: boolean
  effectiveOrigins?: string[]
}) {
  const { t } = useTranslation()
  const id = useId()
  const client = useQueryClient()
  const updateOptions = useUpdateOptions()
  const [enabled, setEnabled] = useState(initialEnabled)
  const [input, setInput] = useState(initialOrigins)
  const [baseline, setBaseline] = useState({
    enabled: initialEnabled,
    origins: initialOrigins,
  })
  const [saved, setSaved] = useState(false)
  const origins = commerceImportTrustedOrigins(input)
  const invalid = origins === undefined
  const missingOrigins = enabled && origins?.length === 0
  const dirty = enabled !== baseline.enabled || input !== baseline.origins
  const busy = updateOptions.isPending
  const canSave = dirty && !invalid && !missingOrigins && !busy
  async function save() {
    if (!canSave || !origins) return
    setSaved(false)
    try {
      await updateOptions.mutateAsync({
        MerchantStoreCommerceImportEnabled: String(enabled),
        MerchantStoreCommerceImportTrustedOrigins: JSON.stringify(origins),
      })
      const normalized = origins.join('\n')
      setInput(normalized)
      setBaseline({ enabled, origins: normalized })
      setSaved(true)
      await client.invalidateQueries({ queryKey: ['store', 'commerce-import'] })
    } catch {
      /* The shared mutation reports failure and keeps this draft editable. */
    }
  }
  return (
    <SettingsSection title={t(copy.title)}>
      <SettingsForm
        onSubmit={(event) => {
          event.preventDefault()
          void save()
        }}
      >
        <SettingsPageFormActions
          onSave={() => void save()}
          isSaving={busy}
          isSaveDisabled={!canSave}
          saveLabel={copy.save}
        />
        <p className='text-muted-foreground text-sm'>{t(copy.description)}</p>
        {environmentOverride && (
          <Alert>
            <AlertDescription>
              <p>{t(copy.environmentOverride)}</p>
              <p className='mt-2 break-all'>
                {t(copy.effectiveOrigins)}: {effectiveOrigins.join(', ')}
              </p>
            </AlertDescription>
          </Alert>
        )}
        {saved && (
          <Alert>
            <AlertDescription>{t(copy.saved)}</AlertDescription>
          </Alert>
        )}
        <FieldGroup>
          <Field orientation='horizontal' data-disabled={busy}>
            <div className='flex-1 space-y-1'>
              <FieldLabel htmlFor={`${id}-enabled`}>
                {t(copy.enabled)}
              </FieldLabel>
              <FieldDescription>{t(copy.enabledHelp)}</FieldDescription>
            </div>
            <Switch
              id={`${id}-enabled`}
              checked={enabled}
              disabled={busy}
              onCheckedChange={(value) => {
                setEnabled(value)
                setSaved(false)
              }}
            />
          </Field>
          <Field data-invalid={invalid || missingOrigins} data-disabled={busy}>
            <FieldLabel htmlFor={`${id}-origins`}>{t(copy.origins)}</FieldLabel>
            <Textarea
              id={`${id}-origins`}
              className='min-h-40 font-mono'
              value={input}
              disabled={busy}
              aria-invalid={invalid || missingOrigins}
              onChange={(event) => {
                setInput(event.target.value)
                setSaved(false)
              }}
            />
            <FieldDescription>{t(copy.originsHelp)}</FieldDescription>
            {invalid && (
              <FieldDescription>{t(copy.invalidOrigins)}</FieldDescription>
            )}
            {missingOrigins && (
              <FieldDescription>
                {t(copy.enabledWithoutOrigins)}
              </FieldDescription>
            )}
          </Field>
          <Button type='submit' disabled={!canSave}>
            {t(copy.save)}
          </Button>
        </FieldGroup>
      </SettingsForm>
    </SettingsSection>
  )
}
