/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { Plus, X } from 'lucide-react'
import { useState, useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

import {
  parsePaymentAmountOptions,
  normalizePaymentAmount,
  paymentAmountCredits,
  serializePaymentAmountOptions,
  type PaymentAmountUnit,
  isPaymentAmountInput,
} from './payment-amount-options'

type AmountOptionsVisualEditorProps = {
  value: string
  onChange: (value: string) => void
  unit?: PaymentAmountUnit
}

export function AmountOptionsVisualEditor({
  value,
  onChange,
  unit = 'USD',
}: AmountOptionsVisualEditorProps) {
  const { t } = useTranslation()
  const [newAmount, setNewAmount] = useState('')

  const parsedAmounts = useMemo(
    () => parsePaymentAmountOptions(value, unit),
    [value, unit]
  )
  const amounts = useMemo(
    () =>
      [...(parsedAmounts ?? [])].sort((a, b) => {
        const delta =
          paymentAmountCredits(a, unit) - paymentAmountCredits(b, unit)
        return delta < 0n ? -1 : delta > 0n ? 1 : 0
      }),
    [parsedAmounts, unit]
  )
  const normalizedNewAmount = normalizePaymentAmount(newAmount, unit)
  const canAdd =
    parsedAmounts !== null &&
    normalizedNewAmount !== null &&
    !amounts.includes(normalizedNewAmount) &&
    amounts.length < 100
  const handleAdd = () => {
    if (!canAdd || normalizedNewAmount === null) return
    const updated = [...amounts, normalizedNewAmount].sort((a, b) => {
      const delta =
        paymentAmountCredits(a, unit) - paymentAmountCredits(b, unit)
      return delta < 0n ? -1 : delta > 0n ? 1 : 0
    })
    onChange(serializePaymentAmountOptions(updated))
    setNewAmount('')
  }
  const handleRemove = (amount: string) => {
    if (parsedAmounts === null) return
    onChange(
      serializePaymentAmountOptions(amounts.filter((item) => item !== amount))
    )
  }

  const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter') {
      e.preventDefault()
      handleAdd()
    }
  }

  return (
    <div className='space-y-4'>
      <div>
        <p className='text-muted-foreground mb-3 text-sm'>
          {t('Preset recharge amounts displayed to users')}
        </p>

        {parsedAmounts === null ? (
          <p role='alert' className='text-destructive text-sm'>
            {t('JSON structure is invalid')}
          </p>
        ) : amounts.length === 0 ? (
          <div className='text-muted-foreground rounded-lg border border-dashed p-6 text-center text-sm'>
            {t(
              'No amount options configured. Add amounts below to get started.'
            )}
          </div>
        ) : (
          <div className='flex flex-wrap gap-2'>
            {amounts.map((amount) => (
              <StatusBadge
                key={amount}
                variant='neutral'
                className='text-base'
                copyable={false}
              >
                <span className='font-mono'>
                  {amount} {unit}
                </span>
                <Button
                  type='button'
                  variant='ghost'
                  size='icon-sm'
                  onClick={(e) => {
                    e.preventDefault()
                    e.stopPropagation()
                    handleRemove(amount)
                  }}
                  className='hover:bg-muted-foreground/20 size-auto p-0.5'
                  aria-label={`${t('Remove')} ${amount} ${unit}`}
                >
                  <X className='h-3.5 w-3.5' />
                </Button>
              </StatusBadge>
            ))}
          </div>
        )}
      </div>

      <div className='flex flex-col gap-2 sm:flex-row sm:items-end'>
        <div className='flex-1'>
          <Label htmlFor='new-amount' className='mb-2 block'>
            {t('Add new amount')} ({unit})
          </Label>
          <Input
            id='new-amount'
            type='number'
            step={unit === 'USD' ? 'any' : '1'}
            min={unit === 'USD' ? '0.000002' : '1'}
            max={unit === 'USD' ? '18014398509.481982' : '9007199254740991'}
            placeholder={t('e.g., 100')}
            value={newAmount}
            onChange={(e) => setNewAmount(e.target.value)}
            onKeyDown={handleKeyDown}
          />
          {newAmount && !isPaymentAmountInput(newAmount, unit) && (
            <p role='alert' className='text-destructive mt-2 text-sm'>
              {unit === 'CREDIT'
                ? t('Enter a positive integer')
                : t('Enter a positive USD amount that equals whole credits.')}
            </p>
          )}
        </div>
        <Button
          type='button'
          onClick={(e) => {
            e.preventDefault()
            e.stopPropagation()
            handleAdd()
          }}
          disabled={!canAdd}
          className='w-full sm:w-auto'
        >
          <Plus className='h-4 w-4 sm:mr-2' />
          <span className='sm:inline'>{t('Add')}</span>
        </Button>
      </div>
    </div>
  )
}
