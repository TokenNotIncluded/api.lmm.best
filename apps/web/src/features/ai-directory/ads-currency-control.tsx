/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { useId } from 'react'
import { useTranslation } from 'react-i18next'

import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { useWalletCurrency } from '@/hooks/use-wallet-currency'

export function DirectoryAdCurrencyControl() {
  const { t } = useTranslation()
  const id = useId()
  const wallet = useWalletCurrency()
  const options = [
    { value: '', label: t('Follow language') },
    { value: 'CREDIT', label: t('Credits') },
    { value: 'CNY', label: 'CNY' },
    { value: 'USD', label: 'USD' },
  ]
  return (
    <div className='grid gap-1'>
      <Select
        items={options}
        value={wallet.preference}
        disabled={wallet.saving}
        onValueChange={(value) => {
          if (
            value === '' ||
            value === 'CREDIT' ||
            value === 'CNY' ||
            value === 'USD'
          ) {
            void wallet.setPreference(value)
          }
        }}
      >
        <SelectTrigger
          aria-label={t('Balance display unit')}
          aria-busy={wallet.saving}
          aria-describedby={wallet.error ? `${id}-error` : undefined}
          className='h-11 min-w-36 sm:h-9'
        >
          <SelectValue>
            {options.find(({ value }) => value === wallet.preference)?.label}
          </SelectValue>
        </SelectTrigger>
        <SelectContent alignItemWithTrigger={false}>
          {options.map((option) => (
            <SelectItem key={option.value} value={option.value}>
              {option.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {wallet.error && (
        <p id={`${id}-error`} role='alert' className='text-destructive text-xs'>
          {t(wallet.error)}
        </p>
      )}
    </div>
  )
}
