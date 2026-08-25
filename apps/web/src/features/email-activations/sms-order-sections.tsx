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
/*
Copyright (C) 2026 LIghtJUNction
*/
import { RefreshCw, ShieldCheck, XCircle } from 'lucide-react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { ErrorState } from '@/components/error-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'

import { formatHeroSmsUSD } from './api.js'
import type {
  HeroSmsSmsCountry,
  HeroSmsSmsOrder,
  HeroSmsSmsService,
} from './sms-api.js'
import { SmsCountryIdentity, SmsServiceIdentity } from './sms-identities.js'
import { getHeroSmsCountryName } from './sms-selection.js'

interface SmsOrderCatalog {
  countries: Map<number, HeroSmsSmsCountry>
  services: Map<string, HeroSmsSmsService>
  language: string
}

interface SmsOrderMutationState {
  pendingOrderId?: string
  onOrder: (orderId: string) => void
}

interface SmsOrderSectionProps extends SmsOrderCatalog {
  orders: HeroSmsSmsOrder[]
  isPending: boolean
  isError: boolean
  errorTitle: string
  errorDescription: string
  onRetry: () => void
}

interface SmsActiveOrdersCardProps extends SmsOrderSectionProps {
  refresh: SmsOrderMutationState
  cancel: SmsOrderMutationState
}

function statusVariant(status: string) {
  if (status === 'completed') return 'default' as const
  if (status === 'cancelled' || status === 'failed') {
    return 'destructive' as const
  }
  return 'secondary' as const
}

function fallbackService(code: string): HeroSmsSmsService {
  return { code, name: code, popularity: 0 }
}

function fallbackCountry(id: number): HeroSmsSmsCountry {
  return {
    id,
    name: `#${id}`,
    english_name: '',
    chinese_name: '',
    popularity: 0,
  }
}

function resolveOrderCatalog(order: HeroSmsSmsOrder, catalog: SmsOrderCatalog) {
  return {
    service:
      catalog.services.get(order.service) ?? fallbackService(order.service),
    country:
      catalog.countries.get(order.country_id) ??
      fallbackCountry(order.country_id),
  }
}

function SmsOrderDetails({
  order,
  service,
  country,
  language,
  refreshPending,
  cancelPending,
  onRefresh,
  onCancel,
}: {
  order: HeroSmsSmsOrder
  service: HeroSmsSmsService
  country: HeroSmsSmsCountry
  language: string
  refreshPending: boolean
  cancelPending: boolean
  onRefresh: () => void
  onCancel: () => void
}) {
  const { t } = useTranslation()
  const operationPending = refreshPending || cancelPending
  const canCancel =
    order.can_cancel ??
    (order.status === 'active' && Boolean(order.provider_id))
  return (
    <div className='space-y-4 p-4'>
      <div className='flex flex-wrap items-start justify-between gap-3'>
        <div className='flex min-w-0 items-center gap-3'>
          <SmsCountryIdentity country={country} language={language} />
          <div className='min-w-0'>
            <p className='truncate font-medium'>
              {order.phone_number || t('Waiting for phone number')}
            </p>
            <p className='text-muted-foreground truncate text-xs'>
              {service.name} · {getHeroSmsCountryName(country, language)}
            </p>
          </div>
        </div>
        <Badge variant={statusVariant(order.status)}>{t(order.status)}</Badge>
      </div>

      <div className='grid gap-3 sm:grid-cols-2'>
        <div className='bg-muted/25 rounded-lg border p-3'>
          <p className='text-muted-foreground text-xs'>{t('Phone number')}</p>
          <div className='mt-1 flex items-center gap-2'>
            <code className='text-sm break-all'>
              {order.phone_number || '—'}
            </code>
            {order.phone_number ? (
              <CopyButton value={order.phone_number} />
            ) : null}
          </div>
        </div>
        <div className='bg-muted/25 rounded-lg border p-3'>
          <p className='text-muted-foreground text-xs'>
            {t('Verification code')}
          </p>
          <div className='mt-1 flex items-center gap-2'>
            <code className='text-lg font-semibold break-all'>
              {order.code || '—'}
            </code>
            {order.code ? <CopyButton value={order.code} /> : null}
          </div>
        </div>
      </div>

      {order.message ? (
        <div className='bg-muted/25 rounded-lg border p-3'>
          <p className='text-muted-foreground text-xs'>{t('SMS message')}</p>
          <p className='mt-1 text-sm break-words'>{order.message}</p>
        </div>
      ) : null}
      {order.last_error_message ? (
        <p className='text-destructive text-sm' role='alert'>
          {order.last_error_message}
        </p>
      ) : null}

      <div className='flex flex-wrap justify-end gap-2'>
        <Button
          type='button'
          variant='outline'
          size='sm'
          disabled={operationPending}
          onClick={onRefresh}
        >
          <RefreshCw data-icon='inline-start' />
          {t('Refresh')}
        </Button>
        <Button
          type='button'
          variant='destructive'
          size='sm'
          disabled={!canCancel || operationPending}
          onClick={onCancel}
        >
          <XCircle data-icon='inline-start' />
          {t('Cancel and refund')}
        </Button>
      </div>
    </div>
  )
}

function resolveOrderSectionContent({
  isPending,
  isError,
  errorTitle,
  errorDescription,
  onRetry,
  orders,
  emptyText,
  children,
}: Pick<
  SmsOrderSectionProps,
  | 'isPending'
  | 'isError'
  | 'errorTitle'
  | 'errorDescription'
  | 'onRetry'
  | 'orders'
> & {
  emptyText: string
  children: ReactNode
}) {
  if (isPending) return <Skeleton className='h-40 w-full' />
  if (isError) {
    return (
      <ErrorState
        title={errorTitle}
        description={errorDescription}
        onRetry={onRetry}
      />
    )
  }
  if (orders.length > 0) return children
  return (
    <div className='text-muted-foreground py-12 text-center text-sm'>
      {emptyText}
    </div>
  )
}

export function SmsActiveOrdersCard({
  orders,
  countries,
  services,
  language,
  refresh,
  cancel,
  ...state
}: SmsActiveOrdersCardProps) {
  const { t } = useTranslation()
  const catalog = { countries, services, language }
  const content = resolveOrderSectionContent({
    ...state,
    orders,
    emptyText: t('No active phone activation'),
    children: (
      <div className='divide-y rounded-lg border'>
        {orders.map((order) => {
          const resolved = resolveOrderCatalog(order, catalog)
          return (
            <SmsOrderDetails
              key={order.id}
              order={order}
              service={resolved.service}
              country={resolved.country}
              language={language}
              refreshPending={refresh.pendingOrderId === order.id}
              cancelPending={cancel.pendingOrderId === order.id}
              onRefresh={() => refresh.onOrder(order.id)}
              onCancel={() => cancel.onOrder(order.id)}
            />
          )
        })}
      </div>
    ),
  })

  return (
    <Card>
      <CardHeader>
        <CardTitle className='flex items-center justify-between gap-3 text-base'>
          <span className='flex items-center gap-2'>
            <ShieldCheck className='size-4' />
            {t('Active phone activations')}
          </span>
          {orders.length > 0 ? (
            <Badge variant='secondary' className='tabular-nums'>
              {orders.length}
            </Badge>
          ) : null}
        </CardTitle>
      </CardHeader>
      <CardContent>{content}</CardContent>
    </Card>
  )
}

export function SmsOrderHistoryCard({
  orders,
  countries,
  services,
  language,
  ...state
}: SmsOrderSectionProps) {
  const { t } = useTranslation()
  const catalog = { countries, services, language }
  const content = resolveOrderSectionContent({
    ...state,
    orders,
    emptyText: t('No phone activation history'),
    children: (
      <div className='divide-y rounded-lg border'>
        {orders.map((order) => {
          const { service, country } = resolveOrderCatalog(order, catalog)
          return (
            <div
              key={order.id}
              className='grid gap-3 p-3 text-sm [content-visibility:auto] sm:grid-cols-[minmax(0,1fr)_auto_auto] sm:items-center'
            >
              <div className='flex min-w-0 items-center gap-3'>
                <SmsServiceIdentity service={service} />
                <SmsCountryIdentity country={country} language={language} />
                <div className='min-w-0'>
                  <p className='truncate font-medium'>
                    {order.phone_number || service.name}
                  </p>
                  <p className='text-muted-foreground truncate text-xs'>
                    {service.name} · {getHeroSmsCountryName(country, language)}
                  </p>
                </div>
              </div>
              <Badge variant={statusVariant(order.status)}>
                {t(order.status)}
              </Badge>
              <span className='font-medium tabular-nums'>
                {formatHeroSmsUSD(Number(order.customer_price_usd))}
              </span>
            </div>
          )
        })}
      </div>
    ),
  })

  return (
    <Card>
      <CardHeader>
        <CardTitle className='text-base'>
          {t('Phone activation history')}
        </CardTitle>
      </CardHeader>
      <CardContent>{content}</CardContent>
    </Card>
  )
}
