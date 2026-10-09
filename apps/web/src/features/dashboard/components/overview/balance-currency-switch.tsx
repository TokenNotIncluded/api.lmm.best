/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { useId } from 'react'
import { useTranslation } from 'react-i18next'

import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { useWalletCurrency } from '@/hooks/use-wallet-currency'
import { toIntlLocale } from '@/i18n/languages'

export function BalanceCurrencySwitch() {
  const { t, i18n } = useTranslation()
  const { currency, setPreference, saving, error } = useWalletCurrency()
  const statusId = useId()
  const errorId = useId()
  const names = new Intl.DisplayNames(
    [toIntlLocale(i18n.resolvedLanguage || i18n.language) ?? 'en'],
    { type: 'currency', style: 'long' }
  )

  return (
    <div className='overview-currency-control'>
      <ToggleGroup
        spacing={1}
        className='overview-currency-switch'
        aria-label={t('Balance display currency')}
        aria-busy={saving}
        aria-describedby={error ? errorId : saving ? statusId : undefined}
        value={[currency]}
        disabled={saving}
        onValueChange={([value]) => {
          if (
            !saving &&
            value !== currency &&
            (value === 'USD' || value === 'CNY' || value === 'CREDIT')
          ) {
            void setPreference(value)
          }
        }}
      >
        {(['USD', 'CNY', 'CREDIT'] as const).map((value) => (
          <ToggleGroupItem key={value} value={value} data-currency={value}>
            {value === 'CREDIT' ? t('credits') : names.of(value)}
          </ToggleGroupItem>
        ))}
      </ToggleGroup>
      <span id={statusId} role='status' className='sr-only'>
        {saving ? t('Saving') : ''}
      </span>
      {error && (
        <p id={errorId} role='alert' className='text-destructive mt-2 text-xs'>
          {t(error)}
        </p>
      )}
    </div>
  )
}
