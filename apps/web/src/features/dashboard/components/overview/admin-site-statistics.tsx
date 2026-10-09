/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { useQuery } from '@tanstack/react-query'
import { ChevronDown, RefreshCw, ShieldCheck } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { useWalletCurrency } from '@/hooks/use-wallet-currency'
import { ROLE } from '@/lib/roles'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import {
  formatSiteCredits,
  formatSitePaymentMicros,
  getAdminSiteStatistics,
  type AdminSiteStatistics,
} from '../../site-statistics'

export function AdminSiteStatisticsPanel() {
  const user = useAuthStore((state) => state.auth.user)
  const allowed = user?.role === ROLE.ADMIN || user?.role === ROLE.SUPER_ADMIN
  const { t, i18n } = useTranslation()
  const {
    formatExactQuota,
    label: currencyLabel,
    currency,
  } = useWalletCurrency()
  const query = useQuery({
    queryKey: ['admin-site-statistics', user?.id],
    queryFn: getAdminSiteStatistics,
    enabled: allowed,
    staleTime: 30_000,
    gcTime: 0,
    retry: false,
  })
  if (!allowed) return null
  const locale = i18n.resolvedLanguage || i18n.language || 'en'
  const data = query.isError ? undefined : query.data

  return (
    <section
      aria-labelledby='site-statistics-title'
      aria-busy={query.isFetching}
      className='overview-site-statistics min-w-0'
    >
      <div className='mb-6 flex flex-wrap items-center justify-between gap-3'>
        <div className='flex flex-wrap items-center gap-3'>
          <h3 id='site-statistics-title' className='text-base font-semibold'>
            {t('全站统计')}
          </h3>
          <span className='text-muted-foreground inline-flex items-center gap-1.5 text-xs'>
            <ShieldCheck className='size-3.5' aria-hidden='true' />
            {t('Administrator')}
          </span>
        </div>
        <Button
          type='button'
          variant='ghost'
          className='min-h-11 gap-2'
          disabled={query.isFetching}
          onClick={() => void query.refetch()}
        >
          <RefreshCw
            aria-hidden='true'
            className={cn(
              'size-3.5',
              query.isFetching && 'motion-safe:animate-spin'
            )}
          />
          {t('Refresh')}
        </Button>
      </div>
      {query.isError ? (
        <Alert variant='destructive'>
          <AlertTitle>{t('全站统计暂时无法读取')}</AlertTitle>
          <AlertDescription>
            {t('请刷新重试，当前金额未确认。')}
          </AlertDescription>
        </Alert>
      ) : !data ? (
        <div
          role='status'
          aria-label={t('Loading')}
          className='grid gap-4 sm:grid-cols-2'
        >
          <Skeleton className='h-28' />
          <Skeleton className='h-28' />
        </div>
      ) : (
        <div className='overview-ledger'>
          <dl className='overview-site-totals'>
            {(
              [
                ['全站累计使用', data.total_used_credits],
                ['当前全站总余额', data.total_balance_credits],
              ] as const
            ).map(([label, value]) => (
              <div key={label} className='min-w-0'>
                <dt className='text-muted-foreground text-xs'>{t(label)}</dt>
                <dd className='overview-site-value'>
                  {formatExactQuota(value, { showSymbol: false })}{' '}
                  <span className='overview-unit'>{currencyLabel}</span>
                </dd>
                {currency !== 'CREDIT' && (
                  <dd className='overview-exact-credits'>
                    {formatSiteCredits(value, locale)} {t('credits')}
                  </dd>
                )}
              </div>
            ))}
          </dl>
          <div className='overview-payment-groups'>
            <div aria-labelledby='site-statistics-cash-title'>
              <h4
                id='site-statistics-cash-title'
                className='mb-2 text-sm font-semibold'
              >
                {t('用户充值实际现金付款')}
              </h4>
              {data.recharge.currencies.length === 0 ? (
                <p className='text-muted-foreground py-5 text-sm'>
                  {t('暂无已确认的充值付款记录。')}
                </p>
              ) : (
                <PaymentRows rows={data.recharge.currencies} locale={locale} />
              )}
            </div>
            {data.recharge.virtual_units.length > 0 && (
              <div aria-labelledby='site-statistics-virtual-title'>
                <h4
                  id='site-statistics-virtual-title'
                  className='mb-2 text-sm font-semibold'
                >
                  {t('用户充值实际非现金点数付款')}
                </h4>
                <PaymentRows
                  rows={data.recharge.virtual_units}
                  locale={locale}
                />
              </div>
            )}
          </div>
          <div className='overview-notes'>
            {(data.recharge.unconfirmed_orders > 0 ||
              data.recharge.invalid_orders > 0) && (
              <details className='overview-disclosure overview-disclosure-warning'>
                <summary>
                  <span className='overview-warning-dot' aria-hidden='true' />
                  <span className='min-w-0 flex-1'>
                    {t('部分充值记录无法确认实付')}
                  </span>
                  <span className='overview-warning-count'>
                    {formatSiteCredits(
                      (
                        BigInt(data.recharge.unconfirmed_orders) +
                        BigInt(data.recharge.invalid_orders)
                      ).toString(),
                      locale
                    )}
                  </span>
                  <ChevronDown className='size-4 shrink-0' aria-hidden='true' />
                </summary>
                <p>
                  {t(
                    '缺少可信实付证据：{{unconfirmed}} 笔；金额记录异常：{{invalid}} 笔。以上付款总额未计入这些记录。',
                    {
                      unconfirmed: data.recharge.unconfirmed_orders,
                      invalid: data.recharge.invalid_orders,
                    }
                  )}
                </p>
              </details>
            )}
            <details className='overview-disclosure'>
              <summary>
                <span className='flex-1'>{t('How to read these tables')}</span>
                <ChevronDown className='size-4 shrink-0' aria-hidden='true' />
              </summary>
              <p>{t('仅管理员可见，包含所有保留账户的累计使用和当前余额。')}</p>
              <p>
                {t(
                  '充值优惠可能增加到账点数，实付金额按支付记录统计，不能用到账点数或当前余额反推付款。现金按订单原币种显示，LDC 等非现金点数单独列示，不兑换或合计为现金。净付款为原始付款减已记录退款，不包含赠额、站内转账、商店收入或订阅付款。'
                )}
              </p>
            </details>
          </div>
        </div>
      )}
    </section>
  )
}

function PaymentRows({
  rows,
  locale,
}: {
  rows: AdminSiteStatistics['recharge']['currencies']
  locale: string
}) {
  const { t } = useTranslation()
  return (
    <div className='overview-payments'>
      {rows.map((row) => (
        <dl key={row.currency} className='overview-payment-row'>
          <div className='overview-payment-currency'>
            <dt className='sr-only'>{t('Currency')}</dt>
            <dd>{row.currency}</dd>
          </div>
          {(
            [
              ['原始付款', row.gross_amount_micros, 'gross'],
              ['已记录退款', row.refunded_amount_micros, 'refund'],
              ['退款后净付款', row.net_amount_micros, 'net'],
            ] as const
          ).map(([label, value, kind]) => (
            <div key={kind} className={`overview-payment-${kind}`}>
              <dt className='text-muted-foreground text-xs'>{t(label)}</dt>
              <dd className='mt-1 [overflow-wrap:anywhere] break-words tabular-nums'>
                {formatSitePaymentMicros(value, locale)}{' '}
                <span className='sr-only'>{row.currency}</span>
              </dd>
            </div>
          ))}
        </dl>
      ))}
    </div>
  )
}
