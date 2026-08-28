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
import { RefreshCw, ShieldCheck, Trash2, XCircle } from 'lucide-react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { ErrorState } from '@/components/error-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Skeleton } from '@/components/ui/skeleton'
import { formatTimestampToDate } from '@/lib/format'

import { formatHeroSmsPlatformAmount } from './api.js'
import type {
  HeroSmsSmsCountry,
  HeroSmsSmsComplaintReason,
  HeroSmsSmsOrder,
  HeroSmsSmsService,
} from './sms-api.js'
import { SmsComplaintDialog } from './sms-complaint-dialog.js'
import { SmsCountryIdentity, SmsServiceIdentity } from './sms-identities.js'
import { resolveHeroSmsPhoneNumber } from './sms-phone-number.js'
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

interface SmsComplaintMutationState {
  pendingOrderId?: string
  onOrder: (orderId: string, reason: HeroSmsSmsComplaintReason) => void
}

interface SmsActiveOrdersCardProps extends SmsOrderSectionProps {
  refresh: SmsOrderMutationState
  complaint: SmsComplaintMutationState
  cancel: SmsOrderMutationState
}

interface SmsOrderHistoryCardProps extends SmsOrderSectionProps {
  onOpenOrder: (orderId: string) => void
  onRemoveOrder: (orderId: string) => void
  onClearHistory: () => void
  cleanupPending: boolean
}

interface SmsOrderDetailDialogProps extends SmsOrderCatalog {
  open: boolean
  onOpenChange: (open: boolean) => void
  order?: HeroSmsSmsOrder
  isPending: boolean
  isError: boolean
  errorDescription: string
  onRetry: () => void
}

function ignoreOrderAction() {
  return undefined
}

function ignoreComplaintAction(_reason: HeroSmsSmsComplaintReason) {
  return undefined
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
  complaintPending,
  cancelPending,
  onRefresh,
  onComplaint,
  onCancel,
  showActions = true,
}: {
  order: HeroSmsSmsOrder
  service: HeroSmsSmsService
  country: HeroSmsSmsCountry
  language: string
  refreshPending: boolean
  complaintPending: boolean
  cancelPending: boolean
  onRefresh: () => void
  onComplaint: (reason: HeroSmsSmsComplaintReason) => void
  onCancel: () => void
  showActions?: boolean
}) {
  const { t } = useTranslation()
  const operationPending = refreshPending || complaintPending || cancelPending
  const phoneNumber = resolveHeroSmsPhoneNumber(order.phone_number)
  const canComplain = order.can_complain ?? false
  const complaintPendingUpstream = [
    'submitting',
    'submitted',
    'submit_unknown',
  ].includes(order.complaint_status || '')
  const cancellationPendingUpstream = order.status === 'cancel_pending'
  const canCancel =
    order.can_cancel ??
    (order.status === 'active' && Boolean(order.provider_id))
  let phoneSubscriber = ''
  if (phoneNumber) {
    phoneSubscriber = phoneNumber.callingCode
      ? phoneNumber.subscriberNumber
      : phoneNumber.e164
  }
  const showComplaintAvailabilityHint =
    order.status === 'active' && !canComplain && !complaintPendingUpstream
  return (
    <div className='space-y-4 p-4'>
      <div className='flex flex-wrap items-start justify-between gap-3'>
        <div className='flex min-w-0 items-center gap-3'>
          <SmsCountryIdentity country={country} language={language} />
          <div className='min-w-0'>
            <p className='truncate font-medium'>
              {phoneNumber?.display || t('Waiting for phone number')}
            </p>
            <p className='text-muted-foreground truncate text-xs'>
              {service.name} · {getHeroSmsCountryName(country, language)}
            </p>
            {order.status === 'active' && order.expires_at ? (
              <p className='text-muted-foreground truncate text-xs'>
                {t('Expires at')}{' '}
                <time
                  dateTime={new Date(order.expires_at * 1000).toISOString()}
                >
                  {formatTimestampToDate(order.expires_at)}
                </time>
              </p>
            ) : null}
          </div>
        </div>
        <Badge variant={statusVariant(order.status)}>{t(order.status)}</Badge>
      </div>

      <div className='grid gap-3 sm:grid-cols-2'>
        <div className='bg-muted/25 rounded-lg border p-3'>
          <p className='text-muted-foreground text-xs'>{t('Phone number')}</p>
          <div className='mt-1 flex items-center gap-2'>
            {phoneNumber ? (
              <code className='flex min-w-0 items-baseline gap-1 text-sm'>
                {phoneNumber.callingCode ? (
                  <span className='text-muted-foreground shrink-0 font-semibold'>
                    +{phoneNumber.callingCode}
                  </span>
                ) : null}
                <span className='break-all'>{phoneSubscriber}</span>
              </code>
            ) : (
              <code className='text-sm'>—</code>
            )}
            {phoneNumber && !phoneNumber.masked ? (
              <CopyButton value={phoneNumber.e164} />
            ) : null}
          </div>
        </div>
        <div className='bg-muted/25 rounded-lg border p-3'>
          <p className='text-muted-foreground text-xs'>
            {t('Verification code')}
          </p>
          <div
            className='mt-1 flex items-center gap-2'
            role='status'
            aria-live='polite'
            aria-atomic='true'
          >
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
          <div className='mt-1 flex items-start gap-2'>
            <p className='min-w-0 flex-1 text-sm break-words'>
              {order.message}
            </p>
            <CopyButton value={order.message} />
          </div>
        </div>
      ) : null}
      {order.last_error_message ? (
        <p className='text-destructive text-sm' role='alert'>
          {order.last_error_message}
        </p>
      ) : null}

      {complaintPendingUpstream ? (
        <p className='text-muted-foreground text-sm' role='status'>
          {t(
            'HeroSMS complaint pending. No platform refund is issued until upstream cancellation is confirmed.'
          )}
        </p>
      ) : null}
      {cancellationPendingUpstream ? (
        <p className='text-muted-foreground text-sm' role='status'>
          {t(
            'Cancellation is awaiting HeroSMS confirmation. No platform refund has been issued yet.'
          )}
        </p>
      ) : null}
      {showComplaintAvailabilityHint ? (
        <p className='text-muted-foreground text-sm'>
          {t('Complaints become available two minutes after purchase')}
        </p>
      ) : null}

      {showActions && order.status !== 'completed' ? (
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
          {order.status === 'active' && (
            <>
              <SmsComplaintDialog
                available={canComplain}
                showAvailabilityHint
                operationPending={operationPending}
                complaintPending={complaintPendingUpstream}
                onSubmit={onComplaint}
              />
              <Button
                type='button'
                variant='destructive'
                size='sm'
                disabled={!canCancel || operationPending}
                onClick={onCancel}
              >
                <XCircle data-icon='inline-start' />
                {t('Cancel and request refund')}
              </Button>
            </>
          )}
        </div>
      ) : null}
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
  complaint,
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
              complaintPending={complaint.pendingOrderId === order.id}
              cancelPending={cancel.pendingOrderId === order.id}
              onRefresh={() => refresh.onOrder(order.id)}
              onComplaint={(reason) => complaint.onOrder(order.id, reason)}
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
            {t('Current phone activation')}
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
  onOpenOrder,
  onRemoveOrder,
  onClearHistory,
  cleanupPending,
  ...state
}: SmsOrderHistoryCardProps) {
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
          const phoneNumber = resolveHeroSmsPhoneNumber(order.phone_number)
          return (
            <div
              key={order.id}
              className='grid gap-3 p-3 text-sm [content-visibility:auto] sm:grid-cols-[minmax(0,1fr)_auto_auto_auto] sm:items-center'
            >
              <div className='flex min-w-0 items-center gap-3'>
                <SmsServiceIdentity service={service} />
                <SmsCountryIdentity country={country} language={language} />
                <div className='min-w-0'>
                  <p className='truncate font-medium'>
                    {phoneNumber?.display || service.name}
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
                {formatHeroSmsPlatformAmount(Number(order.customer_price_usd))}
              </span>
              <div className='flex items-center gap-1 justify-self-start sm:justify-self-end'>
                <Button
                  type='button'
                  size='sm'
                  variant='outline'
                  onClick={() => onOpenOrder(order.id)}
                >
                  {t('View details')}
                </Button>
                <Button
                  type='button'
                  size='icon-sm'
                  variant='ghost'
                  disabled={cleanupPending}
                  aria-label={t('Remove from history')}
                  title={t('Remove from history')}
                  onClick={() => onRemoveOrder(order.id)}
                >
                  <Trash2 />
                </Button>
              </div>
            </div>
          )
        })}
      </div>
    ),
  })

  return (
    <Card>
      <CardHeader className='grid-cols-1 gap-3 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center'>
        <CardTitle className='text-base'>
          {t('Phone activation history')}
        </CardTitle>
        {orders.length > 0 ? (
          <Button
            type='button'
            size='sm'
            variant='outline'
            className='justify-self-start sm:justify-self-end'
            disabled={cleanupPending}
            onClick={onClearHistory}
          >
            <Trash2 data-icon='inline-start' />
            {t('Clear history')}
          </Button>
        ) : null}
      </CardHeader>
      <CardContent>{content}</CardContent>
    </Card>
  )
}

function SmsOrderDetailMeta({
  label,
  value,
  copyValue,
}: {
  label: string
  value: string
  copyValue?: string
}) {
  return (
    <div className='bg-muted/25 min-w-0 rounded-lg border p-3'>
      <dt className='text-muted-foreground text-xs'>{label}</dt>
      <dd className='mt-1 flex min-w-0 items-center gap-2 text-sm'>
        <span className='min-w-0 flex-1 truncate' title={value}>
          {value}
        </span>
        {copyValue ? <CopyButton value={copyValue} /> : null}
      </dd>
    </div>
  )
}

export function SmsOrderDetailDialog({
  open,
  onOpenChange,
  order,
  countries,
  services,
  language,
  isPending,
  isError,
  errorDescription,
  onRetry,
}: SmsOrderDetailDialogProps) {
  const { t } = useTranslation()
  const resolved = order
    ? resolveOrderCatalog(order, { countries, services, language })
    : null

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='max-h-[min(85vh,48rem)] overflow-y-auto sm:max-w-2xl'>
        <DialogHeader>
          <DialogTitle>{t('Phone activation details')}</DialogTitle>
          <DialogDescription>
            {t(
              'View the full phone number, verification code, message, and order metadata.'
            )}
          </DialogDescription>
        </DialogHeader>

        {isPending ? (
          <div className='space-y-3 py-2' aria-busy='true'>
            <Skeleton className='h-16 w-full' />
            <div className='grid gap-3 sm:grid-cols-2'>
              <Skeleton className='h-20 w-full' />
              <Skeleton className='h-20 w-full' />
            </div>
            <Skeleton className='h-24 w-full' />
          </div>
        ) : null}
        {!isPending && (isError || !order || !resolved) ? (
          <ErrorState
            className='min-h-64'
            title={t('Unable to load current phone activation')}
            description={errorDescription}
            onRetry={onRetry}
          />
        ) : null}
        {!isPending && !isError && order && resolved ? (
          <div className='space-y-4'>
            <SmsOrderDetails
              order={order}
              service={resolved.service}
              country={resolved.country}
              language={language}
              refreshPending={false}
              complaintPending={false}
              cancelPending={false}
              onRefresh={ignoreOrderAction}
              onComplaint={ignoreComplaintAction}
              onCancel={ignoreOrderAction}
              showActions={false}
            />
            <dl className='grid gap-3 sm:grid-cols-2'>
              <SmsOrderDetailMeta
                label={t('Order ID')}
                value={order.id}
                copyValue={order.id}
              />
              <SmsOrderDetailMeta
                label={t('Operator')}
                value={order.operator || '—'}
              />
              <SmsOrderDetailMeta
                label={t('Price')}
                value={formatHeroSmsPlatformAmount(Number(order.customer_price_usd))}
              />
              <SmsOrderDetailMeta
                label={t('Created')}
                value={formatTimestampToDate(order.created_at)}
              />
              <SmsOrderDetailMeta
                label={t('Updated')}
                value={formatTimestampToDate(order.updated_at)}
              />
            </dl>
          </div>
        ) : null}
      </DialogContent>
    </Dialog>
  )
}
