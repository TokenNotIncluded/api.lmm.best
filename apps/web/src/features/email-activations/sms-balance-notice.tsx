/*
Copyright (C) 2026 LIghtJUNction
*/
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'

import { useHeroSmsCurrency } from './use-hero-sms-currency'

interface SmsBalanceNoticeProps {
  id?: string
  status: 'unknown' | 'below-minimum' | 'allowed'
  balanceQuota?: number
  serverDenied?: boolean
  isLoading: boolean
  isRefreshing: boolean
  onRefresh: () => void
}

export function SmsBalanceNotice({
  id = 'sms-purchase-balance-notice',
  status,
  balanceQuota,
  serverDenied,
  isLoading,
  isRefreshing,
  onRefresh,
}: SmsBalanceNoticeProps) {
  const { t } = useTranslation()
  const { formatQuota, minimumBalance } = useHeroSmsCurrency()
  if (status === 'allowed' && !serverDenied) return null

  return (
    <Alert id={id} role='status' className='console-sms-balance'>
      <AlertTitle>
        {t('Temporary SMS purchases require a balance of at least {{amount}}', {
          amount: minimumBalance,
        })}
      </AlertTitle>
      <AlertDescription className='flex flex-col gap-2'>
        <p>
          {balanceQuota !== undefined
            ? t('Minimum balance: {{minimum}}. Current balance: {{balance}}.', {
                minimum: minimumBalance,
                balance: formatQuota(balanceQuota),
              })
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
        <div className='mt-1 flex flex-wrap items-center gap-2'>
          <Button size='sm' render={<a href='/wallet' />}>
            {t('Wallet')}
          </Button>
          <Button
            type='button'
            variant='outline'
            size='sm'
            disabled={isRefreshing}
            onClick={onRefresh}
          >
            {t('Refresh balance')}
          </Button>
        </div>
      </AlertDescription>
    </Alert>
  )
}
