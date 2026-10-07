/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from '@/components/ui/empty'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import {
  Table,
  TableBody,
  TableCaption,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useIsAdmin } from '@/hooks/use-admin'
import { useAuthStore } from '@/stores/auth-store'

import { storeAnalyticsApi } from './analytics-api'
import { STORE_ANALYTICS_COPY as copy } from './analytics-copy'
import type {
  StoreAnalyticsAuthScope,
  StoreAnalyticsConfig,
  StoreAnalyticsCounts,
  StoreAnalyticsDays,
  StoreAnalyticsPage,
  StoreAnalyticsScope,
  StoreProductAnalyticsRow,
} from './analytics-types'
import { STORE_SALES_LIMIT_COPY as salesCopy } from './sales-limit-copy'
import { StoreError, StoreLoading } from './shared'
import { useStoreViewer } from './store-viewer'

function analyticsOwner(
  viewer: string,
  sessionId: string | undefined,
  role: number
) {
  return `${viewer}:${sessionId ?? 'no-session'}:${role}`
}

export function StoreProductAnalytics() {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const user = useAuthStore((state) => state.auth.user)
  const sessionId = useAuthStore((state) => state.auth.session?.sid)
  if (!user) return null
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger render={<Button variant='outline' />}>
        {t(copy.title)}
      </DialogTrigger>
      <DialogContent className='sm:max-w-6xl'>
        <DialogHeader>
          <DialogTitle>{t(copy.title)}</DialogTitle>
          <DialogDescription>{t(copy.description)}</DialogDescription>
        </DialogHeader>
        {open && (
          <StoreAnalyticsPanel
            key={`${user.id}:${sessionId ?? ''}:${user.role}`}
          />
        )}
      </DialogContent>
    </Dialog>
  )
}

export function StoreAnalyticsPanel() {
  const { t } = useTranslation()
  const id = useId()
  const user = useAuthStore((state) => state.auth.user)
  const sessionId = useAuthStore((state) => state.auth.session?.sid)
  const viewer = useStoreViewer()
  const isAdmin = useIsAdmin()
  const [scope, setScope] = useState<StoreAnalyticsScope>('mine')
  const [days, setDays] = useState<StoreAnalyticsDays>(30)
  const [page, setPage] = useState(1)
  const effectiveScope = isAdmin ? scope : 'mine'
  const owner = analyticsOwner(viewer, sessionId, user?.role ?? 0)
  const query = useQuery({
    queryKey: ['store', 'analytics', owner, effectiveScope, days, page],
    queryFn: ({ signal }) =>
      storeAnalyticsApi.read(
        effectiveScope,
        days,
        page,
        { userId: user?.id, sessionId },
        signal
      ),
    enabled: Boolean(user),
    retry: false,
    gcTime: 0,
    refetchOnWindowFocus: false,
  })
  if (!user) return null
  const data = query.data
  return (
    <div className='space-y-5'>
      <div className='flex flex-wrap items-end justify-between gap-3'>
        <div className='flex flex-wrap items-end gap-3'>
          {isAdmin && (
            <div className='flex gap-1' role='group' aria-label={t(copy.scope)}>
              {(['mine', 'all'] as const).map((value) => (
                <Button
                  key={value}
                  variant={effectiveScope === value ? 'secondary' : 'outline'}
                  aria-pressed={effectiveScope === value}
                  onClick={() => {
                    setScope(value)
                    setPage(1)
                  }}
                >
                  {t(value === 'all' ? copy.all : copy.mine)}
                </Button>
              ))}
            </div>
          )}
          <div className='space-y-1.5'>
            <Label htmlFor={`${id}-period`}>{t(copy.period)}</Label>
            <NativeSelect
              id={`${id}-period`}
              value={days}
              onChange={(event) => {
                const value = event.target.value
                if (!['7', '30', '90', 'all'].includes(value)) return
                setDays(
                  value === 'all'
                    ? 'all'
                    : (Number(value) as StoreAnalyticsDays)
                )
                setPage(1)
              }}
            >
              <NativeSelectOption value='7'>{t(copy.seven)}</NativeSelectOption>
              <NativeSelectOption value='30'>
                {t(copy.thirty)}
              </NativeSelectOption>
              <NativeSelectOption value='90'>
                {t(copy.ninety)}
              </NativeSelectOption>
              <NativeSelectOption value='all'>
                {t(copy.lifetime)}
              </NativeSelectOption>
            </NativeSelect>
          </div>
        </div>
        <Button
          variant='outline'
          disabled={query.isFetching}
          onClick={() => void query.refetch()}
        >
          {t('Refresh')}
        </Button>
      </div>
      <p className='text-muted-foreground text-sm'>{t(copy.orderWindow)}</p>
      <StoreError error={query.error} retry={() => void query.refetch()} />
      {query.isPending ? (
        <StoreLoading />
      ) : (
        data && (
          <>
            <TrafficNotice data={data} days={days} />
            <AnalyticsSummary counts={data.totals} />
            <p className='text-muted-foreground text-sm'>
              {t(copy.refundHelp)}
            </p>
            <AnalyticsProducts
              data={data}
              days={days}
              showSeller={effectiveScope === 'all'}
            />
            <div className='flex items-center justify-between gap-3'>
              <Button
                variant='outline'
                disabled={page === 1 || query.isFetching}
                onClick={() => setPage((value) => Math.max(1, value - 1))}
              >
                {t('Previous')}
              </Button>
              <span className='text-muted-foreground text-sm'>
                {t(copy.page, { page })}
              </span>
              <Button
                variant='outline'
                disabled={!data.has_more || query.isFetching}
                onClick={() => setPage((value) => value + 1)}
              >
                {t('Next')}
              </Button>
            </div>
          </>
        )
      )}
      {user.role >= 100 && (
        <AnalyticsSettings
          owner={owner}
          userId={user.id}
          sessionId={sessionId}
        />
      )}
    </div>
  )
}

function TrafficNotice({
  data,
  days,
}: {
  data: StoreAnalyticsPage
  days: StoreAnalyticsDays
}) {
  const { t, i18n } = useTranslation()
  const since = data.traffic_since
  const earliest =
    typeof since === 'number' && Number.isFinite(since) && since > 0
      ? new Date(since * 1000).toLocaleDateString(i18n.language, {
          timeZone: 'UTC',
        })
      : null
  return (
    <Alert>
      <AlertDescription>
        <p>
          {t(
            data.traffic_supported
              ? copy.trafficWindow
              : copy.trafficUnavailable,
            { days: data.traffic_retention_days }
          )}
        </p>
        {data.traffic_supported && earliest && (
          <p>{t(copy.trafficSince, { date: earliest })}</p>
        )}
        {days === 'all' && <p>{t(copy.allWindow)}</p>}
        {data.traffic_supported &&
          days !== 'all' &&
          days > data.traffic_retention_days && <p>{t(copy.shortWindow)}</p>}
      </AlertDescription>
    </Alert>
  )
}

function AnalyticsNumber({ value }: { value: number | null }) {
  const { t, i18n } = useTranslation()
  return (
    <span className='tabular-nums'>
      {value === null ? t(copy.disabled) : value.toLocaleString(i18n.language)}
    </span>
  )
}

function AnalyticsSummary({ counts }: { counts: StoreAnalyticsCounts }) {
  const { t } = useTranslation()
  const metrics = [
    [copy.impressions, counts.impressions],
    [copy.clicks, counts.clicks],
    [copy.orders, counts.orders],
    [copy.paid, counts.paid_orders],
    [copy.refunded, counts.refunded_orders],
    [copy.netQuantity, counts.net_paid_quantity],
  ] as const
  return (
    <div
      role='group'
      aria-label={t(copy.totals)}
      className='grid grid-cols-2 gap-3 lg:grid-cols-6'
    >
      {metrics.map(([label, value]) => (
        <Card key={label} size='sm'>
          <CardHeader>
            <CardTitle>{t(label)}</CardTitle>
          </CardHeader>
          <CardContent className='text-xl font-semibold'>
            <AnalyticsNumber value={value} />
            {label === copy.refunded && (
              <div className='text-muted-foreground mt-2 space-y-1 text-xs font-normal'>
                {typeof counts.quantity_refunded_orders === 'number' && (
                  <p>
                    {t(copy.quantityRefunded)}:{' '}
                    <AnalyticsNumber value={counts.quantity_refunded_orders} />
                  </p>
                )}
                {typeof counts.amount_refunded_orders === 'number' && (
                  <p>
                    {t(copy.amountRefunded)}:{' '}
                    <AnalyticsNumber value={counts.amount_refunded_orders} />
                  </p>
                )}
              </div>
            )}
          </CardContent>
        </Card>
      ))}
    </div>
  )
}

function AnalyticsRate({
  numerator,
  denominator,
}: {
  numerator: number | null
  denominator: number | null
}) {
  const { i18n } = useTranslation()
  return (
    <span className='tabular-nums'>
      {numerator === null || denominator === null || denominator <= 0
        ? '—'
        : (numerator / denominator).toLocaleString(i18n.language, {
            style: 'percent',
            maximumFractionDigits: 1,
          })}
    </span>
  )
}

function ProductIdentity({
  item,
  showSeller,
}: {
  item: StoreProductAnalyticsRow
  showSeller: boolean
}) {
  const { t } = useTranslation()
  return (
    <TableCell className='min-w-44 whitespace-normal'>
      <div className='font-medium break-words'>{item.title}</div>
      <div className='mt-1 flex flex-wrap items-center gap-2'>
        <Badge variant='secondary'>
          {t(
            item.status === 'off_shelf' ? salesCopy.offShelfStatus : item.status
          )}
        </Badge>
        {showSeller && (
          <span className='text-muted-foreground text-xs'>
            {t(copy.seller)}: {item.seller_id}
          </span>
        )}
      </div>
    </TableCell>
  )
}

function AnalyticsProducts({
  data,
  days,
  showSeller,
}: {
  data: StoreAnalyticsPage
  days: StoreAnalyticsDays
  showSeller: boolean
}) {
  const { t } = useTranslation()
  const showRates =
    data.traffic_supported &&
    days !== 'all' &&
    days <= data.traffic_retention_days
  if (!data.items.length) {
    return (
      <Empty>
        <EmptyHeader>
          <EmptyTitle>{t(copy.empty)}</EmptyTitle>
          <EmptyDescription>{t(copy.emptyHelp)}</EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }
  return (
    <Tabs defaultValue='funnel'>
      <TabsList>
        <TabsTrigger value='funnel'>{t(copy.funnel)}</TabsTrigger>
        <TabsTrigger value='quantity'>{t(copy.quantity)}</TabsTrigger>
      </TabsList>
      <TabsContent value='funnel'>
        <Table>
          {showRates && <TableCaption>{t(copy.rateHelp)}</TableCaption>}
          <TableHeader>
            <TableRow>
              <TableHead>{t(copy.product)}</TableHead>
              <TableHead>{t(copy.impressions)}</TableHead>
              <TableHead>{t(copy.clicks)}</TableHead>
              {showRates && <TableHead>{t(copy.ctr)}</TableHead>}
              <TableHead>{t(copy.orders)}</TableHead>
              {showRates && <TableHead>{t(copy.conversion)}</TableHead>}
              <TableHead>{t(copy.paid)}</TableHead>
              <TableHead>{t(copy.refunded)}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {data.items.map((item) => (
              <TableRow key={item.product_id}>
                <ProductIdentity item={item} showSeller={showSeller} />
                <TableCell>
                  <AnalyticsNumber value={item.impressions} />
                </TableCell>
                <TableCell>
                  <AnalyticsNumber value={item.clicks} />
                </TableCell>
                {showRates && (
                  <TableCell>
                    <AnalyticsRate
                      numerator={item.clicks}
                      denominator={item.impressions}
                    />
                  </TableCell>
                )}
                <TableCell>
                  <AnalyticsNumber value={item.orders} />
                </TableCell>
                {showRates && (
                  <TableCell>
                    <AnalyticsRate
                      numerator={item.orders}
                      denominator={item.clicks}
                    />
                  </TableCell>
                )}
                <TableCell>
                  <AnalyticsNumber value={item.paid_orders} />
                </TableCell>
                <TableCell>
                  <AnalyticsNumber value={item.refunded_orders} />
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </TabsContent>
      <TabsContent value='quantity'>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t(copy.product)}</TableHead>
              <TableHead>{t(copy.orderedQuantity)}</TableHead>
              <TableHead>{t(copy.paidQuantity)}</TableHead>
              <TableHead>{t(copy.refundedQuantity)}</TableHead>
              <TableHead>{t(copy.netQuantity)}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {data.items.map((item) => (
              <TableRow key={item.product_id}>
                <ProductIdentity item={item} showSeller={showSeller} />
                <TableCell>
                  <AnalyticsNumber value={item.ordered_quantity} />
                </TableCell>
                <TableCell>
                  <AnalyticsNumber value={item.paid_quantity} />
                </TableCell>
                <TableCell>
                  <AnalyticsNumber value={item.refunded_quantity} />
                </TableCell>
                <TableCell>
                  <AnalyticsNumber value={item.net_paid_quantity} />
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </TabsContent>
    </Tabs>
  )
}

function AnalyticsSettings({
  owner,
  userId,
  sessionId,
}: {
  owner: string
  userId: number
  sessionId: string | undefined
}) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  return (
    <details
      className='rounded-lg border p-4'
      onToggle={(event) => setOpen(event.currentTarget.open)}
    >
      <summary className='cursor-pointer text-sm font-medium'>
        {t(copy.settings)}
      </summary>
      {open && (
        <AnalyticsSettingsEditor
          key={owner}
          owner={owner}
          authScope={{ userId, sessionId }}
        />
      )}
    </details>
  )
}

function AnalyticsSettingsEditor({
  owner,
  authScope,
}: {
  owner: string
  authScope: StoreAnalyticsAuthScope
}) {
  const client = useQueryClient()
  const query = useQuery({
    queryKey: ['store', 'analytics-config', owner],
    queryFn: ({ signal }) => storeAnalyticsApi.config(authScope, signal),
    retry: false,
    gcTime: 0,
    refetchOnWindowFocus: false,
  })
  return (
    <div className='mt-4 space-y-3'>
      <StoreError error={query.error} retry={() => void query.refetch()} />
      {query.isPending ? (
        <StoreLoading />
      ) : (
        query.data && (
          <AnalyticsSettingsForm
            initial={query.data}
            authScope={authScope}
            onSaved={async (config) => {
              client.setQueryData(['store', 'analytics-config', owner], config)
              await client.invalidateQueries({
                queryKey: ['store', 'analytics', owner],
              })
            }}
          />
        )
      )}
    </div>
  )
}

function AnalyticsSettingsForm({
  initial,
  authScope,
  onSaved,
}: {
  initial: StoreAnalyticsConfig
  authScope: StoreAnalyticsAuthScope
  onSaved: (config: StoreAnalyticsConfig) => Promise<void>
}) {
  const { t } = useTranslation()
  const id = useId()
  const [retention, setRetention] = useState(String(initial.retention_days))
  const [dedupe, setDedupe] = useState(String(initial.dedupe_days))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const [saved, setSaved] = useState(false)
  function stillOwner() {
    const auth = useAuthStore.getState().auth
    return (
      auth.user?.id === authScope.userId &&
      auth.session?.sid === authScope.sessionId &&
      auth.user &&
      auth.user.role >= 100
    )
  }
  return (
    <form
      className='space-y-3'
      onSubmit={async (event) => {
        event.preventDefault()
        if (busy || !stillOwner()) return
        const body = {
          retention_days: Number(retention),
          dedupe_days: Number(dedupe),
        }
        setSaved(false)
        if (
          !Number.isSafeInteger(body.retention_days) ||
          body.retention_days < 1 ||
          body.retention_days > 3650 ||
          !Number.isSafeInteger(body.dedupe_days) ||
          body.dedupe_days < 1 ||
          body.dedupe_days > 30 ||
          body.dedupe_days > body.retention_days
        ) {
          setError(new Error(copy.invalidSettings))
          return
        }
        setBusy(true)
        setError(null)
        try {
          const config = await storeAnalyticsApi.saveConfig(body, authScope)
          if (!stillOwner()) return
          await onSaved(config)
          setSaved(true)
        } catch (issue) {
          if (stillOwner()) setError(issue)
        } finally {
          if (stillOwner()) setBusy(false)
        }
      }}
    >
      <div className='grid gap-3 sm:grid-cols-2'>
        <div className='space-y-1.5'>
          <Label htmlFor={`${id}-retention`}>{t(copy.retention)}</Label>
          <Input
            id={`${id}-retention`}
            type='number'
            min={1}
            max={3650}
            step={1}
            required
            value={retention}
            disabled={busy}
            onChange={(event) => {
              setRetention(event.target.value)
              setSaved(false)
            }}
          />
        </div>
        <div className='space-y-1.5'>
          <Label htmlFor={`${id}-dedupe`}>{t(copy.dedupe)}</Label>
          <Input
            id={`${id}-dedupe`}
            type='number'
            min={1}
            max={30}
            step={1}
            required
            value={dedupe}
            disabled={busy}
            onChange={(event) => {
              setDedupe(event.target.value)
              setSaved(false)
            }}
          />
        </div>
      </div>
      <p className='text-muted-foreground text-sm'>{t(copy.retentionHelp)}</p>
      <StoreError error={error} />
      {saved && (
        <p role='status' className='text-sm'>
          {t(copy.saved)}
        </p>
      )}
      <Button type='submit' disabled={busy}>
        {t(copy.saveSettings)}
      </Button>
    </form>
  )
}
