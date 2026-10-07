/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { CheckCircle2, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

/** Only render for an order already confirmed by the server, never a return URL. */
export function PaymentSuccessReceipt({
  balance,
  onViewBalance,
  onViewHistory,
  onDismiss,
}: {
  balance: string
  onViewBalance?: () => void
  onViewHistory?: () => void
  onDismiss: () => void
}) {
  const { t } = useTranslation()
  return (
    <section
      role='status'
      aria-live='polite'
      aria-atomic='true'
      data-testid='payment-success-receipt'
      className='bg-card relative rounded-xl border p-4 sm:p-5'
    >
      <div className='flex items-start gap-3 pr-10'>
        <CheckCircle2
          aria-hidden='true'
          className='text-success motion-safe:animate-in motion-safe:zoom-in-75 mt-0.5 size-6 shrink-0 motion-safe:duration-500'
        />
        <div className='min-w-0'>
          <h2 className='text-base font-semibold'>
            {t('Thank you for your support')}
          </h2>
          <p className='text-muted-foreground mt-1 text-sm'>
            {t('Order completed successfully')}
          </p>
          <p className='mt-2 text-sm'>
            {t('Wallet balance')}:{' '}
            <span className='tabular-nums'>{balance}</span>
          </p>
          {onViewBalance || onViewHistory ? (
            <div className='mt-3 flex flex-wrap gap-2'>
              {onViewBalance ? (
                <Button variant='outline' onClick={onViewBalance}>
                  {t('Wallet balance')}
                </Button>
              ) : null}
              {onViewHistory ? (
                <Button variant='ghost' onClick={onViewHistory}>
                  {t('Billing History')}
                </Button>
              ) : null}
            </div>
          ) : null}
        </div>
      </div>
      <Button
        variant='ghost'
        size='icon'
        className='absolute top-2 right-2 size-11'
        aria-label={t('Close')}
        onClick={onDismiss}
      >
        <X aria-hidden='true' />
      </Button>
    </section>
  )
}
