/*
Copyright (C) 2026 LIghtJUNction
*/
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'

import { SMS_MINIMUM_BALANCE_USD } from './sms-balance'

interface SmsBalanceNoticeProps {
  id?: string
  status: 'unknown' | 'below-minimum' | 'allowed'
  balanceUSD?: number
  serverDenied?: boolean
  isLoading: boolean
  isRefreshing: boolean
  onRefresh: () => void
}

export function SmsBalanceNotice({
  id = 'sms-purchase-balance-notice',
  status,
  balanceUSD,
  serverDenied,
  isLoading,
  isRefreshing,
  onRefresh,
}: SmsBalanceNoticeProps) {
  const { t, i18n } = useTranslation()
  if (status === 'allowed' && !serverDenied) return null

  return (
    <Alert id={id} role='status'>
      <AlertTitle>
        {t('Temporary SMS purchases require a balance of at least USD 10')}
      </AlertTitle>
      <AlertDescription className='flex flex-col gap-2'>
        <p>
          {balanceUSD !== undefined
            ? t(
                'Minimum balance: USD {{minimum}}. Current balance: USD {{balance}}.',
                {
                  minimum: SMS_MINIMUM_BALANCE_USD,
                  balance: new Intl.NumberFormat(i18n.language || 'en', {
                    minimumFractionDigits: 2,
                    maximumFractionDigits: 6,
                  }).format(balanceUSD),
                }
              )
            : isLoading
              ? t('Checking your wallet balance before buying a new number.')
              : t(
                  'Your balance could not be verified. Retry the balance check before buying a new number.'
                )}
        </p>
        <p>
          {t(
            'Existing orders can still receive codes, be cancelled, and receive eligible refunds.'
          )}
        </p>
        <Button
          type='button'
          variant='outline'
          size='sm'
          disabled={isRefreshing}
          onClick={onRefresh}
        >
          {t('Refresh balance')}
        </Button>
      </AlertDescription>
    </Alert>
  )
}
