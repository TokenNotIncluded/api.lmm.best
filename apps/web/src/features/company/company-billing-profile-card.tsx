/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import type { TFunction } from 'i18next'
import { Building2, CheckCircle2, Info, TriangleAlert } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
  FieldSet,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Spinner } from '@/components/ui/spinner'
import { Switch } from '@/components/ui/switch'
import { TitledCard } from '@/components/ui/titled-card'

import { CompanyBillingProfileValidationError } from './api'
import type {
  CompanyBillingProfile,
  CompanyBillingProfileField,
  CompanyBillingProfileInput,
} from './types'
import { useCompanyBillingProfile } from './use-company-billing-profile'

type FieldErrorKey =
  | 'Enter a valid two-letter country code.'
  | 'Postcode must be 32 characters or fewer.'
  | 'State or region must be 128 characters or fewer.'
  | 'Business name must be 255 characters or fewer.'
  | 'Tax ID must be 64 characters or fewer.'
  | 'Check this setting and try again.'

type FieldErrors = Partial<Record<CompanyBillingProfileField, FieldErrorKey>>

const EMPTY_PROFILE: CompanyBillingProfileInput = {
  country: '',
  isBusiness: false,
  postcode: '',
  state: '',
  businessName: '',
  taxId: '',
  useForInvoices: false,
}

function controlId(field: CompanyBillingProfileField) {
  return `company-billing-${field}`
}

function labelId(field: CompanyBillingProfileField) {
  return `${controlId(field)}-label`
}

function descriptionId(field: CompanyBillingProfileField) {
  return `${controlId(field)}-description`
}

function errorId(field: CompanyBillingProfileField) {
  return `${controlId(field)}-error`
}

function describedBy(field: CompanyBillingProfileField, error?: string) {
  return [descriptionId(field), error ? errorId(field) : null]
    .filter(Boolean)
    .join(' ')
}

function profileToForm(
  profile: CompanyBillingProfile | null
): CompanyBillingProfileInput {
  if (!profile) return { ...EMPTY_PROFILE }
  return {
    country: profile.country,
    isBusiness: profile.isBusiness,
    postcode: profile.postcode,
    state: profile.state,
    businessName: profile.businessName,
    taxId: profile.taxId,
    useForInvoices: profile.useForInvoices,
  }
}

function normalizeForm(
  profile: CompanyBillingProfileInput
): CompanyBillingProfileInput {
  return {
    country: profile.country.trim().toUpperCase(),
    isBusiness: profile.isBusiness,
    postcode: profile.postcode.trim(),
    state: profile.state.trim(),
    businessName: profile.businessName.trim(),
    taxId: profile.taxId.trim(),
    useForInvoices: profile.useForInvoices,
  }
}

function validateForm(profile: CompanyBillingProfileInput): FieldErrors {
  const errors: FieldErrors = {}
  if (!/^[A-Z]{2}$/.test(profile.country)) {
    errors.country = 'Enter a valid two-letter country code.'
  }
  if (profile.postcode.length > 32) {
    errors.postcode = 'Postcode must be 32 characters or fewer.'
  }
  if (profile.state.length > 128) {
    errors.state = 'State or region must be 128 characters or fewer.'
  }
  if (profile.businessName.length > 255) {
    errors.businessName = 'Business name must be 255 characters or fewer.'
  }
  if (profile.taxId.length > 64) {
    errors.taxId = 'Tax ID must be 64 characters or fewer.'
  }
  return errors
}

function serverFieldErrorKey(field: CompanyBillingProfileField): FieldErrorKey {
  switch (field) {
    case 'country':
      return 'Enter a valid two-letter country code.'
    case 'postcode':
      return 'Postcode must be 32 characters or fewer.'
    case 'state':
      return 'State or region must be 128 characters or fewer.'
    case 'businessName':
      return 'Business name must be 255 characters or fewer.'
    case 'taxId':
      return 'Tax ID must be 64 characters or fewer.'
    case 'isBusiness':
    case 'useForInvoices':
      return 'Check this setting and try again.'
  }
}

function translateFieldError(
  error: FieldErrorKey | undefined,
  t: TFunction
): string | undefined {
  switch (error) {
    case 'Enter a valid two-letter country code.':
      return t('Enter a valid two-letter country code.')
    case 'Postcode must be 32 characters or fewer.':
      return t('Postcode must be 32 characters or fewer.')
    case 'State or region must be 128 characters or fewer.':
      return t('State or region must be 128 characters or fewer.')
    case 'Business name must be 255 characters or fewer.':
      return t('Business name must be 255 characters or fewer.')
    case 'Tax ID must be 64 characters or fewer.':
      return t('Tax ID must be 64 characters or fewer.')
    case 'Check this setting and try again.':
      return t('Check this setting and try again.')
    default:
      return undefined
  }
}

interface CompanyTextFieldProps {
  field: Extract<
    CompanyBillingProfileField,
    'country' | 'postcode' | 'state' | 'businessName' | 'taxId'
  >
  label: string
  description: string
  value: string
  error?: string
  placeholder?: string
  maxLength?: number
  required?: boolean
  disabled: boolean
  autoComplete?: string
  onChange: (value: string) => void
}

function CompanyTextField({
  field,
  label,
  description,
  value,
  error,
  placeholder,
  maxLength,
  required,
  disabled,
  autoComplete,
  onChange,
}: CompanyTextFieldProps) {
  return (
    <Field data-invalid={Boolean(error)}>
      <FieldLabel htmlFor={controlId(field)}>{label}</FieldLabel>
      <Input
        id={controlId(field)}
        name={field}
        value={value}
        placeholder={placeholder}
        maxLength={maxLength}
        required={required}
        disabled={disabled}
        autoComplete={autoComplete}
        aria-invalid={Boolean(error)}
        aria-describedby={describedBy(field, error)}
        onChange={(event) => onChange(event.target.value)}
      />
      <FieldDescription id={descriptionId(field)}>
        {description}
      </FieldDescription>
      <FieldError id={errorId(field)}>{error}</FieldError>
    </Field>
  )
}

export function CompanyBillingProfileCard() {
  const { t } = useTranslation()
  const {
    ownerUserId,
    profile,
    loading,
    loadError,
    retrying,
    retry,
    save,
    saving,
    saved,
    saveError,
    resetSave,
  } = useCompanyBillingProfile()
  const [form, setForm] = useState<CompanyBillingProfileInput>(() =>
    profileToForm(profile)
  )
  const [clientErrorKeys, setClientErrorKeys] = useState<FieldErrors>({})
  const initializedOwner = useRef<number | null>(null)

  useEffect(() => {
    if (initializedOwner.current !== ownerUserId) {
      initializedOwner.current = null
      setForm({ ...EMPTY_PROFILE })
      setClientErrorKeys({})
      resetSave()
    }
    if (
      ownerUserId === null ||
      loading ||
      loadError ||
      initializedOwner.current === ownerUserId
    ) {
      return
    }
    initializedOwner.current = ownerUserId
    setForm(profileToForm(profile))
  }, [loadError, loading, ownerUserId, profile, resetSave])

  if (loading) {
    return (
      <TitledCard
        title={t('Company billing profile')}
        description={t('Save billing and tax details for your account.')}
        icon={<Building2 />}
        iconTone='info'
        disableHoverEffect
      >
        <div
          role='status'
          aria-live='polite'
          aria-busy='true'
          className='text-muted-foreground flex min-h-40 items-center justify-center gap-2 text-sm'
        >
          <Spinner aria-hidden='true' />
          <span>{t('Loading company profile...')}</span>
        </div>
      </TitledCard>
    )
  }

  if (loadError) {
    return (
      <TitledCard
        title={t('Company billing profile')}
        description={t('Save billing and tax details for your account.')}
        icon={<Building2 />}
        iconTone='info'
        disableHoverEffect
      >
        <Alert variant='destructive' role='alert'>
          <TriangleAlert />
          <AlertTitle>{t('Unable to load company profile')}</AlertTitle>
          <AlertDescription>
            {t("We couldn't load your company billing profile. Try again.")}
          </AlertDescription>
        </Alert>
        <Button
          type='button'
          variant='outline'
          className='mt-4 w-full sm:w-auto'
          disabled={retrying}
          onClick={() => void retry()}
        >
          {retrying ? <Spinner aria-hidden='true' /> : null}
          {t('Retry')}
        </Button>
      </TitledCard>
    )
  }

  const serverErrorKeys: FieldErrors = {}
  if (saveError instanceof CompanyBillingProfileValidationError) {
    for (const field of saveError.fields) {
      serverErrorKeys[field] = serverFieldErrorKey(field)
    }
  }
  const fieldErrorKeys = { ...serverErrorKeys, ...clientErrorKeys }
  const hasFieldErrors = Object.keys(fieldErrorKeys).length > 0
  const genericSaveError = Boolean(saveError) && !hasFieldErrors

  function clearFieldFeedback(field: CompanyBillingProfileField) {
    setClientErrorKeys((current) => {
      if (!current[field]) return current
      const next = { ...current }
      delete next[field]
      return next
    })
    if (saved || saveError) resetSave()
  }

  function updateField<Field extends keyof CompanyBillingProfileInput>(
    field: Field,
    value: CompanyBillingProfileInput[Field]
  ) {
    setForm((current) => ({ ...current, [field]: value }))
    clearFieldFeedback(field)
  }

  function handleSubmit(event: React.SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    const normalized = normalizeForm(form)
    const errors = validateForm(normalized)
    setForm(normalized)
    setClientErrorKeys(errors)
    resetSave()

    const firstInvalid = Object.keys(errors)[0] as
      | CompanyBillingProfileField
      | undefined
    if (firstInvalid) {
      document.getElementById(controlId(firstInvalid))?.focus()
      return
    }

    save(normalized, {
      onSuccess: (serverProfile) => {
        setForm(profileToForm(serverProfile))
        setClientErrorKeys({})
      },
      onError: (error) => {
        if (error instanceof CompanyBillingProfileValidationError) {
          const firstInvalid = error.fields[0]
          if (firstInvalid) {
            requestAnimationFrame(() => {
              document.getElementById(controlId(firstInvalid))?.focus()
            })
          }
        }
      },
    })
  }

  return (
    <TitledCard
      title={t('Company billing profile')}
      description={t('Save billing and tax details for your account.')}
      icon={<Building2 />}
      iconTone='info'
      disableHoverEffect
    >
      <form noValidate aria-busy={saving} onSubmit={handleSubmit}>
        {!profile ? (
          <p className='text-muted-foreground mb-5 text-sm'>
            {t('No company billing profile has been saved yet.')}
          </p>
        ) : null}

        {hasFieldErrors ? (
          <Alert variant='destructive' role='alert' className='mb-5'>
            <TriangleAlert />
            <AlertTitle>{t('Unable to save company profile')}</AlertTitle>
            <AlertDescription>
              {t('Check the highlighted fields and try again.')}
            </AlertDescription>
          </Alert>
        ) : null}

        {genericSaveError ? (
          <Alert variant='destructive' role='alert' className='mb-5'>
            <TriangleAlert />
            <AlertTitle>{t('Unable to save company profile')}</AlertTitle>
            <AlertDescription>
              {t("We couldn't save your company billing profile. Try again.")}
            </AlertDescription>
          </Alert>
        ) : null}

        {saved ? (
          <Alert role='status' aria-live='polite' className='mb-5'>
            <CheckCircle2 />
            <AlertTitle>{t('Saved')}</AlertTitle>
            <AlertDescription>
              {t('Company billing profile saved.')}
            </AlertDescription>
          </Alert>
        ) : null}

        <FieldSet disabled={saving}>
          <FieldGroup className='grid grid-cols-1 gap-5 sm:grid-cols-2'>
            <CompanyTextField
              field='country'
              label={t('Country')}
              description={t('Two-letter ISO country code.')}
              placeholder={t('e.g. US')}
              value={form.country}
              error={translateFieldError(fieldErrorKeys.country, t)}
              required
              disabled={saving}
              autoComplete='country-code'
              onChange={(value) =>
                updateField('country', value.trim().toUpperCase())
              }
            />

            <CompanyTextField
              field='postcode'
              label={t('Postcode')}
              description={t('Postal or ZIP code, if applicable.')}
              value={form.postcode}
              error={translateFieldError(fieldErrorKeys.postcode, t)}
              maxLength={32}
              disabled={saving}
              autoComplete='postal-code'
              onChange={(value) => updateField('postcode', value)}
            />

            <Field
              orientation='horizontal'
              data-invalid={Boolean(fieldErrorKeys.isBusiness)}
              className='rounded-lg border p-4 sm:col-span-2'
            >
              <FieldContent>
                <FieldLabel
                  id={labelId('isBusiness')}
                  htmlFor={controlId('isBusiness')}
                >
                  {t('Business account')}
                </FieldLabel>
                <FieldDescription id={descriptionId('isBusiness')}>
                  {t(
                    'Turn on if these billing details belong to a business or organization.'
                  )}
                </FieldDescription>
                <FieldError id={errorId('isBusiness')}>
                  {translateFieldError(fieldErrorKeys.isBusiness, t)}
                </FieldError>
              </FieldContent>
              <Switch
                id={controlId('isBusiness')}
                name='isBusiness'
                checked={form.isBusiness}
                disabled={saving}
                aria-invalid={Boolean(fieldErrorKeys.isBusiness)}
                aria-labelledby={labelId('isBusiness')}
                aria-describedby={describedBy(
                  'isBusiness',
                  fieldErrorKeys.isBusiness
                )}
                onCheckedChange={(checked) =>
                  updateField('isBusiness', checked)
                }
              />
            </Field>

            <CompanyTextField
              field='state'
              label={t('State or region')}
              description={t('State, province, or region, if applicable.')}
              value={form.state}
              error={translateFieldError(fieldErrorKeys.state, t)}
              maxLength={128}
              disabled={saving}
              autoComplete='address-level1'
              onChange={(value) => updateField('state', value)}
            />

            <CompanyTextField
              field='businessName'
              label={t('Business name')}
              description={t('Legal business name, if applicable.')}
              value={form.businessName}
              error={translateFieldError(fieldErrorKeys.businessName, t)}
              maxLength={255}
              disabled={saving}
              autoComplete='organization'
              onChange={(value) => updateField('businessName', value)}
            />

            <CompanyTextField
              field='taxId'
              label={t('Tax ID')}
              description={t('Tax or VAT identifier, if applicable.')}
              value={form.taxId}
              error={translateFieldError(fieldErrorKeys.taxId, t)}
              maxLength={64}
              disabled={saving}
              autoComplete='off'
              onChange={(value) => updateField('taxId', value)}
            />

            <Field
              orientation='horizontal'
              data-invalid={Boolean(fieldErrorKeys.useForInvoices)}
              className='rounded-lg border p-4 sm:col-span-2'
            >
              <FieldContent>
                <FieldLabel
                  id={labelId('useForInvoices')}
                  htmlFor={controlId('useForInvoices')}
                >
                  {t('Use for invoices and checkout')}
                </FieldLabel>
                <FieldDescription id={descriptionId('useForInvoices')}>
                  {t(
                    'When enabled, future orders and invoices automatically use your saved company billing details. When disabled, the profile stays saved but is not sent at checkout.'
                  )}
                </FieldDescription>
                <FieldError id={errorId('useForInvoices')}>
                  {translateFieldError(fieldErrorKeys.useForInvoices, t)}
                </FieldError>
              </FieldContent>
              <Switch
                id={controlId('useForInvoices')}
                name='useForInvoices'
                checked={form.useForInvoices}
                disabled={saving}
                aria-invalid={Boolean(fieldErrorKeys.useForInvoices)}
                aria-labelledby={labelId('useForInvoices')}
                aria-describedby={describedBy(
                  'useForInvoices',
                  fieldErrorKeys.useForInvoices
                )}
                onCheckedChange={(checked) =>
                  updateField('useForInvoices', checked)
                }
              />
            </Field>
          </FieldGroup>
        </FieldSet>

        <Alert role='note' className='mt-5'>
          <Info />
          <AlertDescription>
            {t(
              'Payment providers may request additional details and validate them during checkout.'
            )}
          </AlertDescription>
        </Alert>

        <div className='mt-5 flex flex-col-reverse items-stretch gap-3 sm:flex-row sm:items-center sm:justify-end'>
          <Button type='submit' disabled={saving} className='w-full sm:w-auto'>
            {saving ? <Spinner aria-hidden='true' /> : null}
            {saving ? t('Saving...') : t('Save company profile')}
          </Button>
        </div>
      </form>
    </TitledCard>
  )
}
