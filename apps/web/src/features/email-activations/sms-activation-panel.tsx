/* oxlint-disable eslint/no-nested-ternary -- Query-state rendering intentionally branches through loading, error, data, and empty states. */
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Phone, RefreshCw, ShieldCheck, XCircle } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import { ErrorState } from '@/components/error-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'

import { formatHeroSmsUSD, parseHeroSmsError } from './api.js'
import {
  cancelHeroSmsSmsOrder,
  createHeroSmsSmsOrder,
  getCurrentHeroSmsSmsOrder,
  getHeroSmsSmsOffer,
  listHeroSmsSmsCountries,
  listHeroSmsSmsOrders,
  listHeroSmsSmsServices,
  refreshHeroSmsSmsOrder,
  type HeroSmsSmsOrder,
} from './sms-api.js'

const smsKeys = {
  all: ['hero-sms', 'sms'] as const,
  countries: ['hero-sms', 'sms', 'countries'] as const,
  services: ['hero-sms', 'sms', 'services'] as const,
  offer: (country: string, service: string, operator: string) =>
    ['hero-sms', 'sms', 'offer', country, service, operator] as const,
  current: ['hero-sms', 'sms', 'current'] as const,
  history: ['hero-sms', 'sms', 'history'] as const,
}

function isPending(order: HeroSmsSmsOrder | null | undefined) {
  return Boolean(
    order &&
    ['pending_provider', 'purchase_unknown', 'active'].includes(order.status)
  )
}

function statusVariant(status: string) {
  if (status === 'completed') {
    return 'default' as const
  }
  if (status === 'cancelled' || status === 'failed') {
    return 'destructive' as const
  }
  return 'secondary' as const
}

export function HeroSmsSmsActivationPanel() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [country, setCountry] = useState('')
  const [service, setService] = useState('')
  const [operator, setOperator] = useState('')
  const [confirmOpen, setConfirmOpen] = useState(false)

  const countriesQuery = useQuery({
    queryKey: smsKeys.countries,
    queryFn: listHeroSmsSmsCountries,
    staleTime: 5 * 60 * 1000,
  })
  const servicesQuery = useQuery({
    queryKey: smsKeys.services,
    queryFn: listHeroSmsSmsServices,
    staleTime: 5 * 60 * 1000,
  })
  const offerQuery = useQuery({
    queryKey: smsKeys.offer(country, service, operator),
    queryFn: () =>
      getHeroSmsSmsOffer({
        country: Number(country),
        service,
        operator: operator.trim() || undefined,
      }),
    enabled: country !== '' && service !== '',
    retry: false,
  })
  const currentQuery = useQuery({
    queryKey: smsKeys.current,
    queryFn: getCurrentHeroSmsSmsOrder,
    refetchInterval: (query) =>
      isPending(query.state.data?.order) ? 5_000 : false,
  })
  const historyQuery = useQuery({
    queryKey: smsKeys.history,
    queryFn: () => listHeroSmsSmsOrders(1, 20),
  })

  const current = currentQuery.data?.order ?? null
  const serviceName = useMemo(
    () =>
      servicesQuery.data?.find((item) => item.code === service)?.name ??
      service,
    [service, servicesQuery.data]
  )

  const invalidate = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: smsKeys.current }),
      queryClient.invalidateQueries({ queryKey: smsKeys.history }),
      queryClient.invalidateQueries({ queryKey: ['user'] }),
    ])
  }

  const purchaseMutation = useMutation({
    mutationFn: () => createHeroSmsSmsOrder(offerQuery.data?.id ?? ''),
    onSuccess: async () => {
      setConfirmOpen(false)
      toast.success(t('Phone number purchased'))
      await invalidate()
    },
    onError: (error) => toast.error(t(parseHeroSmsError(error).message)),
  })
  const refreshMutation = useMutation({
    mutationFn: (orderId: string) => refreshHeroSmsSmsOrder(orderId),
    onSuccess: invalidate,
    onError: (error) => toast.error(t(parseHeroSmsError(error).message)),
  })
  const cancelMutation = useMutation({
    mutationFn: (orderId: string) => cancelHeroSmsSmsOrder(orderId),
    onSuccess: async () => {
      toast.success(t('Phone activation cancelled and refunded'))
      await invalidate()
    },
    onError: (error) => toast.error(t(parseHeroSmsError(error).message)),
  })

  if (countriesQuery.isError || servicesQuery.isError) {
    return (
      <ErrorState
        title={t('Unable to load phone activation catalog')}
        description={t(
          parseHeroSmsError(countriesQuery.error || servicesQuery.error).message
        )}
        onRetry={() => {
          void countriesQuery.refetch()
          void servicesQuery.refetch()
        }}
      />
    )
  }

  return (
    <div className='space-y-6'>
      <div className='grid gap-4 xl:grid-cols-[minmax(0,380px)_minmax(0,1fr)]'>
        <Card>
          <CardHeader>
            <CardTitle className='flex items-center gap-2 text-base'>
              <Phone className='size-4' />
              {t('Purchase temporary phone number')}
            </CardTitle>
          </CardHeader>
          <CardContent className='space-y-4'>
            {countriesQuery.isPending || servicesQuery.isPending ? (
              <div className='space-y-3'>
                <Skeleton className='h-9 w-full' />
                <Skeleton className='h-9 w-full' />
              </div>
            ) : (
              <>
                <div className='space-y-2'>
                  <Label htmlFor='hero-sms-country'>{t('Country')}</Label>
                  <Select
                    value={country}
                    onValueChange={(value) => setCountry(value ?? '')}
                  >
                    <SelectTrigger id='hero-sms-country'>
                      <SelectValue placeholder={t('Select a country')} />
                    </SelectTrigger>
                    <SelectContent>
                      {(countriesQuery.data ?? []).map((item) => (
                        <SelectItem key={item.id} value={String(item.id)}>
                          {item.name}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                <div className='space-y-2'>
                  <Label htmlFor='hero-sms-service'>{t('Service')}</Label>
                  <Select
                    value={service}
                    onValueChange={(value) => setService(value ?? '')}
                  >
                    <SelectTrigger id='hero-sms-service'>
                      <SelectValue placeholder={t('Select a service')} />
                    </SelectTrigger>
                    <SelectContent>
                      {(servicesQuery.data ?? []).map((item) => (
                        <SelectItem key={item.code} value={item.code}>
                          {item.name}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                <div className='space-y-2'>
                  <Label htmlFor='hero-sms-operator'>{t('Operator')}</Label>
                  <Input
                    id='hero-sms-operator'
                    value={operator}
                    onChange={(event) => setOperator(event.target.value)}
                    placeholder={t('Optional; leave blank for any operator')}
                    maxLength={64}
                  />
                </div>
              </>
            )}

            {offerQuery.isFetching ? (
              <Skeleton className='h-24 w-full' />
            ) : offerQuery.data ? (
              <div className='bg-muted/40 grid grid-cols-2 gap-3 rounded-lg border p-3 text-sm'>
                <div>
                  <p className='text-muted-foreground'>{t('Inventory')}</p>
                  <p className='font-medium'>{offerQuery.data.inventory}</p>
                </div>
                <div>
                  <p className='text-muted-foreground'>{t('Multiplier')}</p>
                  <p className='font-medium'>
                    ×{offerQuery.data.price_multiplier}
                  </p>
                </div>
                <div>
                  <p className='text-muted-foreground'>
                    {t('HeroSMS upstream price')}
                  </p>
                  <p className='font-medium'>
                    ¥{offerQuery.data.provider_price_cny}
                  </p>
                </div>
                <div>
                  <p className='text-muted-foreground'>
                    {t('Platform balance charge')}
                  </p>
                  <p className='font-medium'>
                    {formatHeroSmsUSD(
                      Number(offerQuery.data.customer_price_usd)
                    )}
                  </p>
                </div>
              </div>
            ) : offerQuery.isError ? (
              <p className='text-destructive text-sm' role='alert'>
                {t(parseHeroSmsError(offerQuery.error).message)}
              </p>
            ) : null}

            <Button
              className='w-full'
              disabled={
                !offerQuery.data ||
                offerQuery.data.inventory < 1 ||
                purchaseMutation.isPending ||
                isPending(current)
              }
              onClick={() => setConfirmOpen(true)}
            >
              {t('Buy phone activation')}
            </Button>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className='flex items-center gap-2 text-base'>
              <ShieldCheck className='size-4' />
              {t('Current phone activation')}
            </CardTitle>
          </CardHeader>
          <CardContent>
            {currentQuery.isPending ? (
              <Skeleton className='h-40 w-full' />
            ) : currentQuery.isError ? (
              <ErrorState
                title={t('Unable to load current phone activation')}
                description={t(parseHeroSmsError(currentQuery.error).message)}
                onRetry={() => void currentQuery.refetch()}
              />
            ) : current ? (
              <div className='space-y-4'>
                <div className='flex flex-wrap items-center justify-between gap-3'>
                  <Badge variant={statusVariant(current.status)}>
                    {t(current.status)}
                  </Badge>
                  <div className='flex gap-2'>
                    <Button
                      variant='outline'
                      size='sm'
                      disabled={refreshMutation.isPending}
                      onClick={() => refreshMutation.mutate(current.id)}
                    >
                      <RefreshCw className='size-4' />
                      {t('Refresh')}
                    </Button>
                    {isPending(current) ? (
                      <Button
                        variant='destructive'
                        size='sm'
                        disabled={
                          !current.provider_id || cancelMutation.isPending
                        }
                        onClick={() => cancelMutation.mutate(current.id)}
                      >
                        <XCircle className='size-4' />
                        {t('Cancel and refund')}
                      </Button>
                    ) : null}
                  </div>
                </div>
                <div className='grid gap-3 sm:grid-cols-2'>
                  <div className='rounded-lg border p-3'>
                    <p className='text-muted-foreground text-xs'>
                      {t('Phone number')}
                    </p>
                    <div className='mt-1 flex items-center gap-2'>
                      <code className='text-sm break-all'>
                        {current.phone_number || '—'}
                      </code>
                      {current.phone_number ? (
                        <CopyButton value={current.phone_number} />
                      ) : null}
                    </div>
                  </div>
                  <div className='rounded-lg border p-3'>
                    <p className='text-muted-foreground text-xs'>
                      {t('Verification code')}
                    </p>
                    <div className='mt-1 flex items-center gap-2'>
                      <code className='text-lg font-semibold break-all'>
                        {current.code || '—'}
                      </code>
                      {current.code ? (
                        <CopyButton value={current.code} />
                      ) : null}
                    </div>
                  </div>
                </div>
                {current.message ? (
                  <div className='rounded-lg border p-3'>
                    <p className='text-muted-foreground text-xs'>
                      {t('SMS message')}
                    </p>
                    <p className='mt-1 text-sm break-words'>
                      {current.message}
                    </p>
                  </div>
                ) : null}
                {current.last_error_message ? (
                  <p className='text-destructive text-sm' role='alert'>
                    {current.last_error_message}
                  </p>
                ) : null}
              </div>
            ) : (
              <div className='text-muted-foreground py-12 text-center text-sm'>
                {t('No active phone activation')}
              </div>
            )}
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader>
          <CardTitle className='text-base'>
            {t('Phone activation history')}
          </CardTitle>
        </CardHeader>
        <CardContent>
          {historyQuery.isPending ? (
            <Skeleton className='h-32 w-full' />
          ) : historyQuery.isError ? (
            <ErrorState
              title={t('Unable to load phone activation history')}
              description={t(parseHeroSmsError(historyQuery.error).message)}
              onRetry={() => void historyQuery.refetch()}
            />
          ) : historyQuery.data?.items.length ? (
            <div className='divide-y rounded-lg border'>
              {historyQuery.data.items.map((order) => (
                <div
                  key={order.id}
                  className='grid gap-2 p-3 text-sm sm:grid-cols-[minmax(0,1fr)_auto_auto] sm:items-center'
                >
                  <div className='min-w-0'>
                    <p className='truncate font-medium'>
                      {order.phone_number || order.service}
                    </p>
                    <p className='text-muted-foreground truncate text-xs'>
                      {order.id}
                    </p>
                  </div>
                  <Badge variant={statusVariant(order.status)}>
                    {t(order.status)}
                  </Badge>
                  <span className='font-medium'>
                    {formatHeroSmsUSD(Number(order.customer_price_usd))}
                  </span>
                </div>
              ))}
            </div>
          ) : (
            <div className='text-muted-foreground py-10 text-center text-sm'>
              {t('No phone activation history')}
            </div>
          )}
        </CardContent>
      </Card>

      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={t('Confirm phone activation purchase')}
        desc={t(
          'Purchase {{service}} in {{country}} for {{price}} of platform balance?',
          {
            service: serviceName,
            country:
              countriesQuery.data?.find((item) => String(item.id) === country)
                ?.name ?? country,
            price: formatHeroSmsUSD(
              Number(offerQuery.data?.customer_price_usd ?? 0)
            ),
          }
        )}
        confirmText={t('Confirm purchase')}
        handleConfirm={() => purchaseMutation.mutate()}
        isLoading={purchaseMutation.isPending}
      />
    </div>
  )
}
