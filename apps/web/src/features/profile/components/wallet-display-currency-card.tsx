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
import { resolveWalletDisplayCurrency } from '@/lib/currency'
import { useAuthStore } from '@/stores/auth-store'

import { useWalletDisplayCurrency } from '../hooks/use-wallet-display-currency'
import { walletDisplayOwnerKey } from '../lib/wallet-display-currency'
import type { UserProfile } from '../types'

type WalletDisplayCurrencyCardProps = {
  profile: UserProfile | null
  loading?: boolean
  onProfileUpdate: () => void | Promise<void>
}

export function WalletDisplayCurrencyCard(
  props: WalletDisplayCurrencyCardProps
) {
  const ownerKey = useAuthStore(({ auth }) => walletDisplayOwnerKey(auth))
  return <WalletDisplayCurrencyField key={ownerKey} {...props} />
}

function WalletDisplayCurrencyField({
  profile,
  loading = false,
  onProfileUpdate,
}: WalletDisplayCurrencyCardProps) {
  const { t, i18n } = useTranslation()
  const id = useId()
  const preference = useWalletDisplayCurrency({
    profile,
    loading,
    onProfileUpdate,
  })
  const options = [
    { value: '', label: t('Follow language') },
    { value: 'CREDIT', label: t('Credits') },
    { value: 'CNY', label: 'CNY' },
    { value: 'USD', label: 'USD' },
  ]
  const autoCurrency = resolveWalletDisplayCurrency(
    '',
    i18n.resolvedLanguage || i18n.language
  )
  const description =
    preference.value === ''
      ? t('Following language: {{currency}}', { currency: autoCurrency })
      : t('Wallet display unit: {{currency}}', {
          currency:
            preference.value === 'CREDIT' ? t('Credits') : preference.value,
        })
  const hasError = preference.failedValue !== null
  const disabled = !preference.available || preference.saving

  return (
    <TitledCard
      title={t('Balance display unit')}
      description={t(
        'Applies to balance and quota displays. Payment settlement currency is configured separately.'
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
            <FieldLabel htmlFor={id}>{t('Balance display unit')}</FieldLabel>
            <FieldDescription id={`${id}-description`}>
              {preference.available && description}
            </FieldDescription>
          </FieldContent>
          <Select
            items={options}
            value={preference.available ? preference.value : null}
            disabled={disabled}
            onValueChange={(value) => {
              if (
                value === '' ||
                value === 'CREDIT' ||
                value === 'CNY' ||
                value === 'USD'
              ) {
                void preference.save(value)
              }
            }}
          >
            <SelectTrigger
              id={id}
              aria-label={t('Balance display unit')}
              aria-describedby={`${id}-description ${id}-status${hasError ? ` ${id}-error` : ''}`}
              aria-invalid={hasError}
              aria-busy={preference.saving}
              className='h-11 w-full sm:h-9 sm:min-w-48'
            >
              <SelectValue placeholder={t('Loading...')}>
                {preference.available
                  ? options.find((option) => option.value === preference.value)
                      ?.label
                  : null}
              </SelectValue>
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
            t('Wallet display preference saved')
          ) : null}
        </div>
        {hasError && preference.available ? (
          <Alert variant='destructive' id={`${id}-error`}>
            <AlertDescription>
              {t('Could not save wallet display preference. Try again.')}
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
        ) : null}
        {!loading && !preference.available ? (
          <Alert>
            <AlertDescription>
              {t(
                'Profile unavailable. Reload your profile to change wallet display units.'
              )}
              <Button
                variant='outline'
                size='sm'
                onClick={() => {
                  void Promise.resolve()
                    .then(onProfileUpdate)
                    .catch(() => undefined)
                }}
              >
                {t('Retry')}
              </Button>
            </AlertDescription>
          </Alert>
        ) : null}
      </FieldGroup>
    </TitledCard>
  )
}
