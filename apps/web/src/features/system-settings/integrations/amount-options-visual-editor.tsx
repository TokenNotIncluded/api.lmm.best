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
import { formatPlatformAmount } from '@/lib/currency'

import {
  paymentAmountOptionsSchema,
  isPaymentAmountInput,
} from './payment-amount-options'

type AmountOptionsVisualEditorProps = {
  value: string
  onChange: (value: string) => void
}

export function AmountOptionsVisualEditor({
  value,
  onChange,
}: AmountOptionsVisualEditorProps) {
  const { t } = useTranslation()
  const [newAmount, setNewAmount] = useState('')

  const amounts = useMemo(() => {
    const parsed = paymentAmountOptionsSchema.safeParse(value)
    if (!parsed.success) return []
    return (JSON.parse(parsed.data) as number[]).sort((a, b) => a - b)
  }, [value])

  const handleAdd = () => {
    const amount = Number(newAmount)
    if (!isPaymentAmountInput(newAmount)) {
      return
    }

    try {
      const updatedAmounts = [...amounts, amount]
        .filter((v, i, a) => a.indexOf(v) === i) // Remove duplicates
        .sort((a, b) => a - b)

      onChange(JSON.stringify(updatedAmounts, null, 2))
      setNewAmount('')
    } catch (_error) {
      // eslint-disable-next-line no-console
      console.error('Failed to add amount:', _error)
    }
  }

  const handleRemove = (amount: number) => {
    try {
      const updatedAmounts = amounts.filter((a) => a !== amount)
      onChange(JSON.stringify(updatedAmounts, null, 2))
    } catch (_error) {
      // eslint-disable-next-line no-console
      console.error('Failed to remove amount:', _error)
    }
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

        {amounts.length === 0 ? (
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
                  {formatPlatformAmount(amount)}
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
                  aria-label={t('Remove ${{amount}}', { amount })}
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
            {t('Add new amount')}
          </Label>
          <Input
            id='new-amount'
            type='number'
            step='1'
            min='1'
            max={Number.MAX_SAFE_INTEGER}
            placeholder={t('e.g., 100')}
            value={newAmount}
            onChange={(e) => setNewAmount(e.target.value)}
            onKeyDown={handleKeyDown}
          />
          {newAmount && !isPaymentAmountInput(newAmount) && (
            <p role='alert' className='text-destructive mt-2 text-sm'>
              {t('Enter a positive integer')}
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
          disabled={!isPaymentAmountInput(newAmount)}
          className='w-full sm:w-auto'
        >
          <Plus className='h-4 w-4 sm:mr-2' />
          <span className='sm:inline'>{t('Add')}</span>
        </Button>
      </div>
    </div>
  )
}
