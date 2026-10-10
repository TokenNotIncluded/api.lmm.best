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
import { Pencil, Plus, Trash2 } from 'lucide-react'
import { useState, useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { StaticRowActions } from '@/components/data-table/static/static-row-actions'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'

import {
  AmountDiscountDialog,
  type AmountDiscountData,
} from './amount-discount-dialog'
import {
  parsePaymentAmountDiscounts,
  paymentAmountCredits,
  type PaymentAmountUnit,
} from './payment-amount-options'

type AmountDiscountVisualEditorProps = {
  value: string
  onChange: (value: string) => void
  unit?: PaymentAmountUnit
}

export function AmountDiscountVisualEditor({
  value,
  onChange,
  unit = 'USD',
}: AmountDiscountVisualEditorProps) {
  const { t } = useTranslation()
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editData, setEditData] = useState<AmountDiscountData | null>(null)

  const parsedDiscounts = useMemo(
    () => parsePaymentAmountDiscounts(value, unit),
    [value, unit]
  )
  const discounts = useMemo(
    () =>
      Object.entries(parsedDiscounts ?? {})
        .map(([amount, discountRate]) => ({ amount, discountRate }))
        .sort((a, b) => {
          const delta =
            paymentAmountCredits(a.amount, unit) -
            paymentAmountCredits(b.amount, unit)
          return delta < 0n ? -1 : delta > 0n ? 1 : 0
        }),
    [parsedDiscounts, unit]
  )
  const handleSave = (data: AmountDiscountData) => {
    if (parsedDiscounts === null) return
    const next = { ...parsedDiscounts }
    if (editData && editData.amount !== data.amount) {
      delete next[editData.amount]
    }
    next[data.amount] = data.discountRate
    onChange(JSON.stringify(next, null, 2))
  }
  const handleDelete = (amount: string) => {
    if (parsedDiscounts === null) return
    const next = { ...parsedDiscounts }
    delete next[amount]
    onChange(JSON.stringify(next, null, 2))
  }

  const handleEdit = (discount: AmountDiscountData) => {
    setEditData(discount)
    setDialogOpen(true)
  }

  const handleAdd = () => {
    if (parsedDiscounts === null) return
    setEditData(null)
    setDialogOpen(true)
  }

  const formatPercentage = (rate: number) => {
    if (rate >= 1) return 0
    const discount = Math.round((1 - rate) * 100)
    return discount
  }

  return (
    <div className='space-y-4'>
      <div className='flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between'>
        <p className='text-muted-foreground text-sm'>
          {t('Configure discount rates based on recharge amounts')}
        </p>
        <Button
          type='button'
          onClick={(e) => {
            e.preventDefault()
            e.stopPropagation()
            handleAdd()
          }}
          size='sm'
          disabled={parsedDiscounts === null}
          className='w-full sm:w-auto'
        >
          <Plus className='h-4 w-4 sm:mr-2' />
          <span className='sm:inline'>{t('Add discount tier')}</span>
        </Button>
      </div>

      {parsedDiscounts === null ? (
        <p role='alert' className='text-destructive text-sm'>
          {t('JSON structure is invalid')}
        </p>
      ) : discounts.length === 0 ? (
        <div className='text-muted-foreground rounded-lg border border-dashed p-6 text-center text-sm'>
          {t(
            'No discount tiers configured. Click "Add discount tier" to get started.'
          )}
        </div>
      ) : (
        <div className='rounded-md border'>
          {/* Desktop table view */}
          <StaticDataTable
            className='hidden rounded-none border-0 sm:block'
            data={discounts}
            getRowKey={(discount) => discount.amount}
            columns={[
              {
                id: 'amount',
                header: `${t('Recharge Amount')} (${unit})`,
                cell: (discount) => (
                  <span className='font-mono text-sm'>
                    {discount.amount} {unit}
                  </span>
                ),
              },
              {
                id: 'discount-rate',
                header: t('Discount Rate'),
                cell: (discount) => (
                  <code className='bg-muted rounded px-1.5 py-0.5 text-sm'>
                    {discount.discountRate.toFixed(2)}
                  </code>
                ),
              },
              {
                id: 'discount',
                header: t('Discount'),
                cell: (discount) => (
                  <StatusBadge
                    variant={discount.discountRate < 1 ? 'info' : 'neutral'}
                    className='font-mono'
                    copyable={false}
                  >
                    {t('{{percent}}% off', {
                      percent: formatPercentage(discount.discountRate),
                    })}
                  </StatusBadge>
                ),
              },
              {
                id: 'actions',
                header: t('Actions'),
                className: 'text-right',
                cellClassName: 'text-right',
                cell: (discount) => (
                  <StaticRowActions
                    editLabel={t('Edit')}
                    deleteLabel={t('Delete')}
                    menuLabel={t('Open menu')}
                    onEdit={() => handleEdit(discount)}
                    onDelete={() => handleDelete(discount.amount)}
                  />
                ),
              },
            ]}
          />

          {/* Mobile card view */}
          <div className='divide-y sm:hidden'>
            {discounts.map((discount) => (
              <div key={discount.amount} className='p-4'>
                <div className='mb-3 flex items-start justify-between'>
                  <div className='flex-1'>
                    <div className='mb-2 font-mono text-base font-medium'>
                      {discount.amount} {unit}
                    </div>
                    <StatusBadge
                      variant={discount.discountRate < 1 ? 'info' : 'neutral'}
                      className='font-mono'
                      copyable={false}
                    >
                      {t('{{percent}}% off', {
                        percent: formatPercentage(discount.discountRate),
                      })}
                    </StatusBadge>
                  </div>
                  <div className='flex gap-1'>
                    <Button
                      type='button'
                      variant='ghost'
                      size='sm'
                      aria-label={`${t('Edit')} ${discount.amount} ${unit}`}
                      onClick={(e) => {
                        e.preventDefault()
                        e.stopPropagation()
                        handleEdit(discount)
                      }}
                    >
                      <Pencil className='h-4 w-4' />
                    </Button>
                    <Button
                      type='button'
                      variant='ghost'
                      size='sm'
                      aria-label={`${t('Delete')} ${discount.amount} ${unit}`}
                      onClick={(e) => {
                        e.preventDefault()
                        e.stopPropagation()
                        handleDelete(discount.amount)
                      }}
                    >
                      <Trash2 className='h-4 w-4' />
                    </Button>
                  </div>
                </div>
                <div className='text-sm'>
                  <span className='text-muted-foreground'>
                    {t('Discount Rate:')}{' '}
                  </span>
                  <code className='bg-muted rounded px-1.5 py-0.5 text-xs'>
                    {discount.discountRate.toFixed(2)}
                  </code>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      <AmountDiscountDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        onSave={handleSave}
        editData={editData}
        unit={unit}
      />
    </div>
  )
}
