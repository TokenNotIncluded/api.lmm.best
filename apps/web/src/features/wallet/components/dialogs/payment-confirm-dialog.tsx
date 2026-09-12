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
*/
import { Loading03Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Badge } from '@/components/ui/badge'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'

import { DEFAULT_DISCOUNT_RATE } from '../../constants'
import {
  formatCreditBalance as formatPlatformCreditBalanceBase,
  formatPaymentAmount,
  formatSettlementAmount,
  getPaymentIcon,
  getPaymentSettlementUnit,
  isPositivePaymentAmount,
  isWaffoPancakePayment,
} from '../../lib'
import { discountCodeSavings } from '../../lib/discount-state'
import {
  formatSettlementQuote,
  parseSettlementQuote,
  type SettlementQuote,
} from '../../lib/settlement-quote'
import type { PaymentMethod } from '../../types'

interface PaymentConfirmDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onConfirm: () => void
  topupAmount: number
  paymentAmount: number
  settlementQuote?: SettlementQuote | null
  paymentMethod: PaymentMethod | undefined
  calculating: boolean
  processing: boolean
  discountRate?: number
  discountCode?: string
  discountPercent?: number | null
  neutralMode?: boolean
}

export function PaymentConfirmDialog({
  open,
  onOpenChange,
  onConfirm,
  topupAmount,
  paymentAmount,
  settlementQuote,
  paymentMethod,
  calculating,
  processing,
  discountRate = DEFAULT_DISCOUNT_RATE,
  discountCode = '',
  discountPercent = null,
  neutralMode = false,
}: PaymentConfirmDialogProps) {
  const { t } = useTranslation()
  const formatPlatformCreditBalance = (amount: number) =>
    formatPlatformCreditBalanceBase(amount, t('Platform'))
  const usesSettlementQuote = isWaffoPancakePayment(paymentMethod?.type ?? '')
  const quote = parseSettlementQuote(settlementQuote)
  const hasPaymentAmount = usesSettlementQuote
    ? quote !== null
    : isPositivePaymentAmount(paymentAmount)
  const effectivePaymentAmount =
    usesSettlementQuote && quote ? Number(quote.amount) : paymentAmount
  const codeSavings = hasPaymentAmount
    ? discountCodeSavings(effectivePaymentAmount, discountPercent)
    : 0
  const hasDiscount =
    !usesSettlementQuote &&
    hasPaymentAmount &&
    discountRate > 0 &&
    discountRate < 1
  const originalAmount = hasDiscount ? effectivePaymentAmount / discountRate : 0
  const discountAmount = hasDiscount
    ? originalAmount - effectivePaymentAmount
    : 0
  const settlementUnit = usesSettlementQuote
    ? null
    : getPaymentSettlementUnit(paymentMethod, true)
  const formatSelectedPaymentAmount = (amount: number) =>
    usesSettlementQuote
      ? quote
        ? amount === Number(quote.amount)
          ? formatSettlementQuote(quote)
          : formatPaymentAmount(amount, quote.currency)
        : t('Payment unavailable')
      : settlementUnit
        ? formatSettlementAmount(amount, settlementUnit.label)
        : formatPaymentAmount(amount, 'USD')
  const paymentMethodLabel = neutralMode
    ? t('Payment Method')
    : paymentMethod?.name

  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent className='max-h-[calc(100dvh-2rem)] overflow-y-auto overscroll-contain max-sm:w-[calc(100vw-1.5rem)] sm:max-w-md'>
        <AlertDialogHeader>
          <AlertDialogTitle className='text-xl font-semibold'>
            {t('Confirm Payment')}
          </AlertDialogTitle>
          <AlertDialogDescription>
            {t('Review your payment details')}
          </AlertDialogDescription>
        </AlertDialogHeader>

        <div className='flex flex-col gap-4 py-3 sm:py-4'>
          <div className='bg-muted/50 rounded-lg border p-3'>
            <div className='text-muted-foreground text-sm'>
              {t('Destination')}
            </div>
            <div className='mt-1 font-medium'>
              {neutralMode
                ? t('Current account balance')
                : t('Current signed-in account · API usage balance')}
            </div>
          </div>

          <div className='flex items-center justify-between'>
            <span className='text-muted-foreground text-sm'>
              {t('Balance credited')}
            </span>
            <span className='text-lg font-semibold'>
              {formatPlatformCreditBalance(topupAmount)}
            </span>
          </div>

          <div className='flex items-center justify-between'>
            <span className='text-muted-foreground text-sm'>
              {t('You top up')}
            </span>
            {calculating ? (
              <Skeleton className='h-6 w-24' />
            ) : hasPaymentAmount ? (
              <div className='flex items-baseline gap-2'>
                <span className='text-2xl font-semibold'>
                  {formatSelectedPaymentAmount(effectivePaymentAmount)}
                </span>
                {(hasDiscount || codeSavings > 0) && (
                  <span className='text-muted-foreground text-sm line-through'>
                    {formatSelectedPaymentAmount(
                      hasDiscount
                        ? originalAmount
                        : effectivePaymentAmount + codeSavings
                    )}
                  </span>
                )}
              </div>
            ) : (
              <span className='text-muted-foreground font-medium'>
                {t('Payment unavailable')}
              </span>
            )}
          </div>

          {hasDiscount && !calculating && (
            <div className='bg-muted/50 rounded-lg border p-3'>
              <div className='flex items-center justify-between text-sm'>
                <span className='text-muted-foreground'>{t('You save')}</span>
                <Badge variant='secondary'>
                  {formatSelectedPaymentAmount(discountAmount)}
                </Badge>
              </div>
            </div>
          )}

          {discountCode && codeSavings > 0 && !calculating && (
            <div className='bg-primary/5 rounded-lg border p-3'>
              <div className='flex items-center justify-between gap-3 text-sm'>
                <div className='flex min-w-0 flex-col'>
                  <span className='text-foreground font-medium'>
                    {discountPercent !== null && discountPercent !== undefined
                      ? t('Discount applied: {{percent}}% off', {
                          percent: discountPercent,
                        })
                      : t('Discount code')}
                  </span>
                  <span className='text-muted-foreground text-xs'>
                    {t('Discount code saves {{amount}}', {
                      amount: formatSelectedPaymentAmount(codeSavings),
                    })}
                  </span>
                </div>
                <Badge variant='secondary' className='shrink-0 font-mono'>
                  {discountCode}
                </Badge>
              </div>
            </div>
          )}

          {(settlementUnit || quote) && !calculating && hasPaymentAmount && (
            <div className='bg-muted/50 rounded-lg border p-3 text-sm'>
              {t('Credit {{amount}}; pay {{payment}}', {
                amount: formatPlatformCreditBalance(topupAmount),
                payment: formatSelectedPaymentAmount(effectivePaymentAmount),
              })}
            </div>
          )}

          <Separator />

          <div>
            <div className='flex items-center justify-between'>
              <span className='text-muted-foreground text-sm'>
                {t('Payment Method')}
              </span>
              <div className='flex items-center gap-2'>
                {getPaymentIcon(
                  paymentMethod?.type,
                  'h-4 w-4',
                  paymentMethod?.icon,
                  paymentMethodLabel,
                  paymentMethod?.color
                )}
                <span className='font-medium'>{paymentMethodLabel}</span>
              </div>
            </div>
          </div>

          <Alert>
            <AlertTitle>{t('Refund policy')}</AlertTitle>
            <AlertDescription>
              <p>
                {t(
                  'Unused top-up balance may be refunded within 7 days. Used or partially used balance is generally non-refundable.'
                )}
              </p>
              <a
                href='/user-agreement'
                target='_blank'
                rel='noopener noreferrer'
              >
                {t('View full Terms and refund policy')}
              </a>
            </AlertDescription>
          </Alert>
        </div>

        <AlertDialogFooter className='grid grid-cols-2 gap-2 sm:flex'>
          <AlertDialogCancel disabled={processing}>
            {t('Cancel')}
          </AlertDialogCancel>
          <AlertDialogAction
            onClick={(event) => {
              event.preventDefault()
              onConfirm()
            }}
            disabled={calculating || processing || !hasPaymentAmount}
          >
            {processing && (
              <HugeiconsIcon
                icon={Loading03Icon}
                className='animate-spin'
                data-icon='inline-start'
              />
            )}
            {t('Confirm Payment')}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
