/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Empty, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Skeleton } from '@/components/ui/skeleton'
import { ROLE } from '@/lib/roles'
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
    <section aria-labelledby='site-statistics-title' className='min-w-0'>
      <div className='mb-5 flex flex-wrap items-center justify-between gap-3'>
        <div>
          <h3 id='site-statistics-title' className='text-base font-semibold'>
            {t('全站统计')}
          </h3>
          <p className='text-muted-foreground mt-1 text-xs'>
            {t('仅管理员可见，包含所有保留账户的累计使用和当前余额。')}
          </p>
        </div>
        <Button
          variant='outline'
          size='sm'
          disabled={query.isFetching}
          onClick={() => void query.refetch()}
        >
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
          <Skeleton className='h-24' />
          <Skeleton className='h-24' />
        </div>
      ) : (
        <div className='flex flex-col gap-5'>
          <dl className='grid gap-5 sm:grid-cols-2'>
            <div className='min-w-0'>
              <dt className='text-muted-foreground text-sm'>
                {t('全站累计使用')}
              </dt>
              <dd className='mt-1 text-xl font-semibold break-all tabular-nums'>
                {formatSiteCredits(data.total_used_credits, locale)}{' '}
                {t('credits')}
              </dd>
            </div>
            <div className='min-w-0'>
              <dt className='text-muted-foreground text-sm'>
                {t('当前全站总余额')}
              </dt>
              <dd className='mt-1 text-xl font-semibold break-all tabular-nums'>
                {formatSiteCredits(data.total_balance_credits, locale)}{' '}
                {t('credits')}
              </dd>
            </div>
          </dl>
          <div aria-labelledby='site-statistics-cash-title'>
            <h4
              id='site-statistics-cash-title'
              className='mb-3 text-sm font-semibold'
            >
              {t('用户充值实际现金付款')}
            </h4>
            {data.recharge.currencies.length === 0 ? (
              <Empty>
                <EmptyHeader>
                  <EmptyTitle>{t('暂无已确认的充值付款记录。')}</EmptyTitle>
                </EmptyHeader>
              </Empty>
            ) : (
              <PaymentRows rows={data.recharge.currencies} locale={locale} />
            )}
          </div>
          {data.recharge.virtual_units.length > 0 && (
            <div aria-labelledby='site-statistics-virtual-title'>
              <h4
                id='site-statistics-virtual-title'
                className='mb-3 text-sm font-semibold'
              >
                {t('用户充值实际非现金点数付款')}
              </h4>
              <PaymentRows rows={data.recharge.virtual_units} locale={locale} />
            </div>
          )}
          <p className='text-muted-foreground text-xs leading-relaxed'>
            {t(
              '充值优惠可能增加到账点数，实付金额按支付记录统计，不能用到账点数或当前余额反推付款。现金按订单原币种显示，LDC 等非现金点数单独列示，不兑换或合计为现金。净付款为原始付款减已记录退款，不包含赠额、站内转账、商店收入或订阅付款。'
            )}
          </p>
          {(data.recharge.unconfirmed_orders > 0 ||
            data.recharge.invalid_orders > 0) && (
            <Alert>
              <AlertTitle>{t('部分充值记录无法确认实付')}</AlertTitle>
              <AlertDescription>
                {t(
                  '缺少可信实付证据：{{unconfirmed}} 笔；金额记录异常：{{invalid}} 笔。以上付款总额未计入这些记录。',
                  {
                    unconfirmed: data.recharge.unconfirmed_orders,
                    invalid: data.recharge.invalid_orders,
                  }
                )}
              </AlertDescription>
            </Alert>
          )}
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
    <div className='flex flex-col gap-4'>
      {rows.map((row) => (
        <dl
          key={row.currency}
          className='bg-muted/40 grid min-w-0 gap-3 rounded-lg p-4 sm:grid-cols-3'
        >
          <div>
            <dt className='text-muted-foreground text-xs'>{t('原始付款')}</dt>
            <dd className='mt-1 text-sm font-medium break-all tabular-nums'>
              {formatSitePaymentMicros(row.gross_amount_micros, locale)}{' '}
              {row.currency}
            </dd>
          </div>
          <div>
            <dt className='text-muted-foreground text-xs'>{t('已记录退款')}</dt>
            <dd className='mt-1 text-sm break-all tabular-nums'>
              {formatSitePaymentMicros(row.refunded_amount_micros, locale)}{' '}
              {row.currency}
            </dd>
          </div>
          <div>
            <dt className='text-muted-foreground text-xs'>
              {t('退款后净付款')}
            </dt>
            <dd className='mt-1 text-sm font-medium break-all tabular-nums'>
              {formatSitePaymentMicros(row.net_amount_micros, locale)}{' '}
              {row.currency}
            </dd>
          </div>
        </dl>
      ))}
    </div>
  )
}
