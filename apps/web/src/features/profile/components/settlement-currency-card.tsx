/*
Copyright (C) 2026 LIghtJUNction
*/
import { useId } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Spinner } from '@/components/ui/spinner'
import { TitledCard } from '@/components/ui/titled-card'
import { useAuthStore } from '@/stores/auth-store'

import { useSettlementCurrency } from '../hooks/use-settlement-currency'
import {
  parseSettlementSettings,
  resolveSettlementCurrency,
} from '../lib/settlement-currency'
import type { UserProfile } from '../types'

type SettlementCurrencyCardProps = {
  profile: UserProfile | null
  loading?: boolean
  onProfileUpdate: () => void | Promise<void>
}

export function SettlementCurrencyCard(props: SettlementCurrencyCardProps) {
  const ownerKey = useAuthStore(({ auth }) =>
    JSON.stringify([auth.user?.id, auth.session?.sid ?? auth.accessToken])
  )
  return <SettlementCurrencyField key={ownerKey} {...props} />
}

function SettlementCurrencyField({
  profile,
  loading = false,
  onProfileUpdate,
}: SettlementCurrencyCardProps) {
  const { t, i18n } = useTranslation()
  const id = useId()
  const preference = useSettlementCurrency({
    profile,
    loading,
    onProfileUpdate,
  })
  const options = [
    { value: '', label: t('Follow language') },
    { value: 'CNY', label: 'CNY' },
    { value: 'USD', label: 'USD' },
  ]
  const effectiveCurrency = resolveSettlementCurrency(
    { ...parseSettlementSettings(profile?.setting), settlement_currency: '' },
    i18n.resolvedLanguage || i18n.language
  )
  const hasError = preference.failedValue !== null
  const disabled = !preference.available || preference.saving

  return (
    <TitledCard
      title={t('Fiat settlement currency')}
      description={t(
        'Applies to Waffo Pancake payments. Other payment methods use their listed currency.'
      )}
      disableHoverEffect
    >
      <FieldGroup>
        <Field
          orientation='responsive'
          data-disabled={disabled}
          data-invalid={hasError}
          aria-busy={preference.saving || loading}
        >
          <FieldContent>
            <FieldLabel htmlFor={id}>
              {t('Fiat settlement currency')}
            </FieldLabel>
            <FieldDescription id={`${id}-description`}>
              {preference.available &&
                t('Following language: {{currency}}', {
                  currency: effectiveCurrency,
                })}
            </FieldDescription>
          </FieldContent>
          <Select
            items={options}
            value={preference.available ? preference.value : null}
            disabled={disabled}
            onValueChange={(value) => {
              if (value === '' || value === 'CNY' || value === 'USD') {
                void preference.save(value)
              }
            }}
          >
            <SelectTrigger
              id={id}
              aria-label={t('Fiat settlement currency')}
              aria-describedby={`${id}-description ${id}-status${hasError ? ` ${id}-error` : ''}`}
              aria-invalid={hasError}
              aria-busy={preference.saving}
              className='w-full sm:min-w-48'
            >
              <SelectValue placeholder={t('Loading...')} />
            </SelectTrigger>
            <SelectContent alignItemWithTrigger={false}>
              <SelectGroup>
                {options.map((option) => (
                  <SelectItem key={option.value} value={option.value}>
                    {option.label}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
        </Field>
        <div
          id={`${id}-status`}
          role='status'
          aria-live='polite'
          className='text-muted-foreground flex items-center gap-2 text-sm'
        >
          {preference.saving ? (
            <>
              <Spinner aria-hidden='true' />
              {t('Saving...')}
            </>
          ) : loading ? (
            t('Loading...')
          ) : preference.available && preference.saved ? (
            t('Settlement currency preference saved')
          ) : null}
        </div>
        {hasError && preference.available && (
          <Alert variant='destructive' id={`${id}-error`}>
            <AlertDescription>
              {t('Could not save settlement currency. Try again.')}
              <Button
                variant='outline'
                size='sm'
                disabled={disabled}
                onClick={() => {
                  if (preference.failedValue !== null) {
                    void preference.save(preference.failedValue)
                  }
                }}
              >
                {t('Retry')}
              </Button>
            </AlertDescription>
          </Alert>
        )}
        {!loading && !preference.available && (
          <Alert>
            <AlertDescription>
              {t(
                'Profile unavailable. Reload your profile to change settlement currency.'
              )}
              <Button variant='outline' size='sm' onClick={onProfileUpdate}>
                {t('Retry')}
              </Button>
            </AlertDescription>
          </Alert>
        )}
      </FieldGroup>
    </TitledCard>
  )
}
