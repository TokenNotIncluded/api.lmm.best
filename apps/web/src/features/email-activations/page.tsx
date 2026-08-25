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
import {
  Alert02Icon,
  ArrowRight01Icon,
  CancelCircleIcon,
  CheckmarkCircle02Icon,
  InformationCircleIcon,
  Loading03Icon,
  MailSend01Icon,
  PackageSearchIcon,
  ReloadIcon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useQueryClient } from '@tanstack/react-query'
import { getRouteApi } from '@tanstack/react-router'
import type { ColumnDef } from '@tanstack/react-table'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import { useDataTable } from '@/components/data-table/hooks/use-data-table'
import { DataTablePage } from '@/components/data-table/layout/data-table-page'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout/components/section-page-layout'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Separator } from '@/components/ui/separator'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useDebounce, useMediaQuery } from '@/hooks'
import { useTableUrlState } from '@/hooks/use-table-url-state'
import dayjs from '@/lib/dayjs'
import { formatNumber } from '@/lib/format'

import {
  createHeroSmsIdempotencyKey,
  formatHeroSmsUSD,
  listHeroSmsProducts,
  parseHeroSmsError,
} from './api'
import {
  heroSmsQueryKeys,
  useCancelHeroSmsActivation,
  useCreateHeroSmsActivations,
  useCurrentHeroSmsActivation,
  useHeroSmsActivationDetail,
  useHeroSmsActivations,
  useHeroSmsProducts,
  useRefreshHeroSmsActivation,
  useReorderHeroSmsActivation,
} from './hooks'
import { HeroSmsSmsActivationPanel } from './sms-activation-panel'
import { HeroSmsStatusBadge } from './status'
import {
  canCancelHeroSmsActivation,
  canReorderHeroSmsActivation,
  getHeroSmsStatusOptions,
} from './status-meta'
import type {
  HeroSmsActivation,
  HeroSmsParsedError,
  HeroSmsProduct,
} from './types'
import { useHeroSmsTranslations } from './use-hero-sms-translations'

type InlineFeedback = {
  tone: 'default' | 'destructive'
  title: string
  description: string
}

type PurchaseConfirmation = {
  product: HeroSmsProduct
  quantity: number
  idempotencyKey: string
}

type ReorderConfirmation = {
  activation: HeroSmsActivation
  product: HeroSmsProduct
  idempotencyKey: string
}

const route = getRouteApi('/_authenticated/temporary-activations/')
const EMPTY_ACTIVATIONS: HeroSmsActivation[] = []

function formatDateTime(value: string | null | undefined) {
  if (!value) return '—'
  const parsed = dayjs(value)
  return parsed.isValid() ? parsed.format('YYYY-MM-DD HH:mm:ss') : '—'
}

function formatCancellationReason(
  reason: string,
  t: ReturnType<typeof useTranslation>['t']
) {
  switch (reason) {
    case 'user':
      return t('User requested cancellation')
    case 'price_changed':
      return t('Provider price changed')
    case 'currency_mismatch':
      return t('Provider currency mismatch')
    case 'bad_upstream':
      return t('Invalid provider response')
    default:
      return '—'
  }
}

function describeHeroSmsError(
  error: HeroSmsParsedError,
  t: ReturnType<typeof useTranslation>['t']
): InlineFeedback {
  if (
    error.code === 'HERO_SMS_PURCHASE_PENDING_RECONCILIATION' ||
    error.code === 'PURCHASE_PENDING_RECONCILIATION'
  ) {
    return {
      tone: 'default',
      title: t('Purchase reconciling'),
      description: t(
        'The provider is still reconciling your last purchase. Refresh this page in a moment before trying again.'
      ),
    }
  }

  if (error.code === 'NOT_CONFIGURED') {
    return {
      tone: 'destructive',
      title: t('Purchasing unavailable'),
      description: t('HeroSMS purchasing is disabled'),
    }
  }

  if (error.status === 402) {
    return {
      tone: 'destructive',
      title: t('Insufficient quota'),
      description: t(
        'Add quota in Wallet, then retry the purchase or reorder action.'
      ),
    }
  }

  if (error.status === 409) {
    return {
      tone: 'destructive',
      title: t('Price changed'),
      description: t(
        'The latest provider price no longer matches the quote shown here. Refresh products and confirm the new price before retrying.'
      ),
    }
  }

  if (error.status === 429) {
    return {
      tone: 'destructive',
      title: t('Too many requests'),
      description: t('Please wait a moment before sending another request.'),
    }
  }

  if ([502, 503, 504].includes(error.status ?? 0)) {
    return {
      tone: 'destructive',
      title: t('Temporary upstream issue'),
      description: t(
        'HeroSMS is temporarily unavailable. Keep this page open and try again shortly.'
      ),
    }
  }

  return {
    tone: 'destructive',
    title: t('Request failed'),
    description: t('An unexpected error occurred'),
  }
}

function HistoryMobileCards({
  items,
  loading,
  onOpenDetail,
  onCancel,
  onRefresh,
  onReorder,
}: {
  items: HeroSmsActivation[]
  loading: boolean
  onOpenDetail: (activation: HeroSmsActivation) => void
  onCancel: (activation: HeroSmsActivation) => void
  onRefresh: (activation: HeroSmsActivation) => void
  onReorder: (activation: HeroSmsActivation) => void
}) {
  const { t } = useTranslation()

  if (loading) {
    return <LoadingState message={t('Loading email activations...')} />
  }

  if (items.length === 0) {
    return (
      <div className='rounded-xl border p-4'>
        <p className='text-muted-foreground text-sm'>
          {t('No email activations match the current filter.')}
        </p>
      </div>
    )
  }

  return (
    <div className='space-y-3'>
      {items.map((activation) => {
        const canCancel = canCancelHeroSmsActivation(activation.status)
        const canReorder = canReorderHeroSmsActivation(activation.status)

        return (
          <Card key={String(activation.id)}>
            <CardHeader className='pb-0'>
              <div className='flex min-w-0 items-start justify-between gap-3'>
                <div className='min-w-0 space-y-1'>
                  <CardTitle className='truncate text-sm'>
                    {activation.email || t('Pending email assignment')}
                  </CardTitle>
                  <CardDescription className='truncate'>
                    {activation.site || '—'} · {activation.domain || '—'}
                  </CardDescription>
                </div>
                <HeroSmsStatusBadge status={activation.status} t={t} />
              </div>
            </CardHeader>
            <CardContent className='space-y-3'>
              <div className='grid grid-cols-2 gap-3 text-sm'>
                <MetaItem label={t('Code')} value={activation.code || '—'} />
                <MetaItem
                  label={t('Quota charge')}
                  value={formatNumber(activation.charge_quota)}
                />
                <MetaItem
                  label={t('Created')}
                  value={formatDateTime(activation.created_at)}
                />
              </div>
              <div className='flex flex-wrap gap-2'>
                <Button
                  size='sm'
                  variant='outline'
                  onClick={() => onOpenDetail(activation)}
                >
                  {t('View details')}
                </Button>
                <Button
                  size='sm'
                  variant='outline'
                  onClick={() => onRefresh(activation)}
                >
                  <HugeiconsIcon
                    icon={ReloadIcon}
                    data-icon='inline-start'
                    strokeWidth={2}
                  />
                  <span>{t('Refresh')}</span>
                </Button>
                <Button
                  size='sm'
                  variant='outline'
                  onClick={() => onCancel(activation)}
                  disabled={!canCancel}
                >
                  <HugeiconsIcon
                    icon={CancelCircleIcon}
                    data-icon='inline-start'
                    strokeWidth={2}
                  />
                  <span>{t('Cancel')}</span>
                </Button>
                <Button
                  size='sm'
                  variant='outline'
                  onClick={() => onReorder(activation)}
                  disabled={!canReorder}
                >
                  <HugeiconsIcon
                    icon={ArrowRight01Icon}
                    data-icon='inline-start'
                    strokeWidth={2}
                  />
                  <span>{t('Reorder')}</span>
                </Button>
              </div>
            </CardContent>
          </Card>
        )
      })}
    </div>
  )
}

function MetaItem({ label, value }: { label: string; value: string }) {
  return (
    <div className='min-w-0 space-y-1'>
      <p className='text-muted-foreground text-xs'>{label}</p>
      <p className='truncate text-sm font-medium'>{value}</p>
    </div>
  )
}

export function EmailActivationsPage() {
  const { t } = useTranslation()
  const [activationKind, setActivationKind] = useState<'sms' | 'email'>('sms')
  useHeroSmsTranslations()
  const isMobile = useMediaQuery('(max-width: 640px)')
  const queryClient = useQueryClient()
  const emailMode = activationKind === 'email'

  const [selectedSite, setSelectedSite] = useState('')
  const [selectedDomainId, setSelectedDomainId] = useState('')
  const [quantity, setQuantity] = useState(1)
  const [purchaseFeedback, setPurchaseFeedback] =
    useState<InlineFeedback | null>(null)
  const [actionFeedback, setActionFeedback] = useState<InlineFeedback | null>(
    null
  )
  const [detailTarget, setDetailTarget] = useState<HeroSmsActivation | null>(
    null
  )
  const [cancelTarget, setCancelTarget] = useState<HeroSmsActivation | null>(
    null
  )
  const [purchaseTarget, setPurchaseTarget] =
    useState<PurchaseConfirmation | null>(null)
  const [reorderTarget, setReorderTarget] =
    useState<ReorderConfirmation | null>(null)
  const [reorderQuoteLoadingId, setReorderQuoteLoadingId] = useState<
    string | null
  >(null)

  const {
    columnFilters,
    onColumnFiltersChange,
    pagination,
    onPaginationChange,
    ensurePageInRange,
  } = useTableUrlState({
    search: route.useSearch(),
    navigate: route.useNavigate(),
    pagination: { defaultPage: 1, defaultPageSize: isMobile ? 10 : 20 },
    globalFilter: { enabled: false },
    columnFilters: [{ columnId: 'status', searchKey: 'status', type: 'array' }],
  })

  const statusFilter =
    (columnFilters.find((filter) => filter.id === 'status')?.value as
      | string[]
      | undefined) ?? []
  const statusFilterValue = statusFilter[0] ?? 'all'

  const trimmedSite = selectedSite.trim()
  const debouncedSite = useDebounce(trimmedSite, 450)
  const productsQuery = useHeroSmsProducts(debouncedSite, emailMode)
  const currentActivationQuery = useCurrentHeroSmsActivation({
    enabled: emailMode,
  })
  const activationsQuery = useHeroSmsActivations({
    page: pagination.pageIndex + 1,
    size: pagination.pageSize,
    status: statusFilterValue,
    enabled: emailMode,
    pollEnabled: emailMode,
  })

  const createMutation = useCreateHeroSmsActivations()
  const refreshMutation = useRefreshHeroSmsActivation()
  const cancelMutation = useCancelHeroSmsActivation()
  const reorderMutation = useReorderHeroSmsActivation()
  const detailQuery = useHeroSmsActivationDetail(
    detailTarget?.id ?? null,
    emailMode && !!detailTarget
  )

  const productsLoading =
    !!trimmedSite && (debouncedSite !== trimmedSite || productsQuery.isLoading)
  const products =
    debouncedSite === trimmedSite ? (productsQuery.data?.items ?? []) : []
  const siteProducts = products
  const resolvedDomainId =
    selectedDomainId &&
    siteProducts.some((item) => String(item.id) === selectedDomainId)
      ? selectedDomainId
      : String(
          siteProducts.find((item) => item.available)?.id ??
            siteProducts[0]?.id ??
            ''
        )
  const selectedProduct = useMemo(
    () =>
      siteProducts.find((item) => String(item.id) === resolvedDomainId) ?? null,
    [resolvedDomainId, siteProducts]
  )
  const maxQuantity = Math.min(10, Math.max(1, selectedProduct?.count ?? 10))

  const activations = activationsQuery.data?.items ?? EMPTY_ACTIVATIONS
  const currentActivation = currentActivationQuery.data ?? null

  const statusOptions = useMemo(() => getHeroSmsStatusOptions(t), [t])

  const columns: ColumnDef<HeroSmsActivation>[] = [
    {
      accessorKey: 'email',
      header: t('Email'),
      cell: ({ row }) => {
        const activation = row.original
        return (
          <div className='min-w-0'>
            <div className='flex items-center gap-2'>
              <span className='truncate font-medium'>
                {activation.email || t('Pending email assignment')}
              </span>
              {activation.email ? (
                <CopyButton value={activation.email} />
              ) : null}
            </div>
            <p className='text-muted-foreground truncate text-xs'>
              {activation.site || '—'} · {activation.domain || '—'}
            </p>
          </div>
        )
      },
    },
    {
      accessorKey: 'status',
      header: t('Status'),
      cell: ({ row }) => (
        <HeroSmsStatusBadge status={row.original.status} t={t} />
      ),
    },
    {
      accessorKey: 'code',
      header: t('Code'),
      cell: ({ row }) => {
        const code = row.original.code || ''
        return (
          <div className='flex items-center gap-2'>
            <span className='font-medium'>{code || '—'}</span>
            {code ? <CopyButton value={code} /> : null}
          </div>
        )
      },
    },
    {
      accessorKey: 'charge_quota',
      header: t('Quota charge'),
      cell: ({ row }) => formatNumber(row.original.charge_quota),
    },
    {
      accessorKey: 'created_at',
      header: t('Created'),
      cell: ({ row }) => formatDateTime(row.original.created_at),
    },
    {
      accessorKey: 'domain',
      header: t('Domain'),
      cell: ({ row }) => row.original.domain || '—',
    },
    {
      id: 'actions',
      header: t('Actions'),
      enableSorting: false,
      cell: ({ row }) => {
        const activation = row.original
        const canCancel = canCancelHeroSmsActivation(activation.status)
        const canReorder = canReorderHeroSmsActivation(activation.status)

        return (
          <div className='flex flex-wrap justify-end gap-2'>
            <Button
              size='sm'
              variant='outline'
              onClick={() => setDetailTarget(activation)}
            >
              {t('View')}
            </Button>
            <Button
              size='sm'
              variant='outline'
              onClick={() => void handleRefresh(activation)}
            >
              {t('Refresh')}
            </Button>
            <Button
              size='sm'
              variant='outline'
              onClick={() => setCancelTarget(activation)}
              disabled={!canCancel}
            >
              {t('Cancel')}
            </Button>
            <Button
              size='sm'
              variant='outline'
              onClick={() => void prepareReorder(activation)}
              disabled={
                !canReorder || reorderQuoteLoadingId === String(activation.id)
              }
            >
              {t('Reorder')}
            </Button>
          </div>
        )
      },
    },
  ]

  const { table } = useDataTable({
    data: activations,
    columns,
    columnFilters,
    onColumnFiltersChange,
    pagination,
    onPaginationChange,
    manualPagination: true,
    manualFiltering: true,
    totalCount: activationsQuery.data?.total || 0,
    ensurePageInRange,
  })

  async function invalidateHeroSmsQueries() {
    await queryClient.invalidateQueries({ queryKey: heroSmsQueryKeys.all })
  }

  async function handlePurchase(target: PurchaseConfirmation) {
    setPurchaseFeedback(null)
    try {
      const result = await createMutation.mutateAsync({
        domain_id: target.product.id,
        quantity: target.quantity,
        idempotencyKey: target.idempotencyKey,
      })
      setPurchaseTarget(null)
      await invalidateHeroSmsQueries()
      const orderStatus = String(result.order?.status ?? '').toLowerCase()
      if (orderStatus === 'failed') {
        setPurchaseFeedback({
          tone: 'destructive',
          title: t('Purchase failed'),
          description: t(
            'The provider purchase failed and the reserved quota was refunded.'
          ),
        })
        toast.error(t('Purchase failed'))
      } else if (
        ['purchase_unknown', 'reconciling', 'pending_provider'].includes(
          orderStatus
        )
      ) {
        setPurchaseFeedback({
          tone: 'default',
          title: t('Purchase reconciling'),
          description: t(
            'The provider is reconciling this purchase. Do not submit another order; this activation will update automatically.'
          ),
        })
        toast.info(t('Purchase submitted for reconciliation'))
      } else {
        toast.success(t('Email activation purchased'))
      }
      if (result.activations[0]) {
        setDetailTarget(result.activations[0])
      }
    } catch (error) {
      const parsed = parseHeroSmsError(error)
      const feedback = describeHeroSmsError(parsed, t)
      setPurchaseFeedback(feedback)
      if (parsed.status === 409) {
        setPurchaseTarget(null)
        await queryClient.invalidateQueries({
          queryKey: ['hero-sms', 'products'],
        })
      }
      toast.error(feedback.title)
    }
  }

  async function prepareReorder(activation: HeroSmsActivation) {
    const site = activation.site?.trim() ?? ''
    const domain = activation.domain?.trim().toLowerCase() ?? ''
    if (!site || !domain) {
      setActionFeedback({
        tone: 'destructive',
        title: t('Reorder unavailable'),
        description: t(
          'This activation does not contain a reusable site and domain.'
        ),
      })
      return
    }

    setActionFeedback(null)
    setReorderQuoteLoadingId(String(activation.id))
    try {
      const productsPage = await listHeroSmsProducts({
        page: 1,
        size: 100,
        site,
      })
      const product = productsPage.items.find(
        (item) => item.available && item.domain.trim().toLowerCase() === domain
      )
      if (!product) {
        setActionFeedback({
          tone: 'destructive',
          title: t('Reorder unavailable'),
          description: t(
            'No matching HeroSMS inventory is available for this activation.'
          ),
        })
        return
      }
      setReorderTarget({
        activation,
        product,
        idempotencyKey: createHeroSmsIdempotencyKey(),
      })
    } catch (error) {
      const feedback = describeHeroSmsError(parseHeroSmsError(error), t)
      setActionFeedback(feedback)
      toast.error(feedback.title)
    } finally {
      setReorderQuoteLoadingId(null)
    }
  }

  async function handleRefresh(activation: HeroSmsActivation) {
    setActionFeedback(null)
    try {
      await refreshMutation.mutateAsync(activation.id)
      await queryClient.invalidateQueries({
        queryKey: ['hero-sms', 'activations'],
      })
      await queryClient.invalidateQueries({
        queryKey: heroSmsQueryKeys.activation(activation.id),
      })
      toast.success(t('Activation refreshed'))
    } catch (error) {
      const feedback = describeHeroSmsError(parseHeroSmsError(error), t)
      setActionFeedback(feedback)
      toast.error(feedback.title)
    }
  }

  async function handleConfirmCancel() {
    if (!cancelTarget) return

    setActionFeedback(null)
    try {
      await cancelMutation.mutateAsync(cancelTarget.id)
      setCancelTarget(null)
      await queryClient.invalidateQueries({
        queryKey: ['hero-sms', 'activations'],
      })
      toast.success(t('Cancellation requested'))
    } catch (error) {
      const feedback = describeHeroSmsError(parseHeroSmsError(error), t)
      setActionFeedback(feedback)
      toast.error(feedback.title)
    }
  }

  async function handleConfirmReorder() {
    if (!reorderTarget) return

    setActionFeedback(null)
    try {
      const result = await reorderMutation.mutateAsync({
        activationId: reorderTarget.activation.id,
        domain_id: reorderTarget.product.id,
        idempotencyKey: reorderTarget.idempotencyKey,
      })
      setReorderTarget(null)
      await invalidateHeroSmsQueries()
      const orderStatus = String(result.order?.status ?? '').toLowerCase()
      if (orderStatus === 'failed') {
        setActionFeedback({
          tone: 'destructive',
          title: t('Purchase failed'),
          description: t(
            'The provider purchase failed and the reserved quota was refunded.'
          ),
        })
        toast.error(t('Purchase failed'))
      } else if (
        ['purchase_unknown', 'reconciling', 'pending_provider'].includes(
          orderStatus
        )
      ) {
        setActionFeedback({
          tone: 'default',
          title: t('Purchase reconciling'),
          description: t(
            'The provider is reconciling this purchase. Do not submit another order; this activation will update automatically.'
          ),
        })
        toast.info(t('Purchase submitted for reconciliation'))
      } else {
        toast.success(t('Reorder submitted'))
      }
      if (result.activations[0]) {
        setDetailTarget(result.activations[0])
      }
    } catch (error) {
      const parsed = parseHeroSmsError(error)
      const feedback = describeHeroSmsError(parsed, t)
      setActionFeedback(feedback)
      if (parsed.status === 409) {
        setReorderTarget(null)
        await queryClient.invalidateQueries({
          queryKey: ['hero-sms', 'products'],
        })
      }
      toast.error(feedback.title)
    }
  }

  const hasHardError =
    !productsQuery.data &&
    productsQuery.isError &&
    !activationsQuery.data &&
    activationsQuery.isError
  const activationTabs = (
    <Tabs
      value={activationKind}
      onValueChange={(value) => setActivationKind(value as 'sms' | 'email')}
    >
      <TabsList aria-label={t('Temporary activation type')}>
        <TabsTrigger value='sms'>{t('Phone number')}</TabsTrigger>
        <TabsTrigger value='email'>{t('Email address')}</TabsTrigger>
      </TabsList>
    </Tabs>
  )

  if (activationKind === 'sms') {
    return (
      <SectionPageLayout>
        <SectionPageLayout.Title>
          {t('Temporary activations')}
        </SectionPageLayout.Title>
        <SectionPageLayout.Content>
          {activationTabs}
          <HeroSmsSmsActivationPanel />
        </SectionPageLayout.Content>
      </SectionPageLayout>
    )
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Temporary activations')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button
          variant='outline'
          onClick={() => void invalidateHeroSmsQueries()}
          disabled={productsQuery.isFetching || activationsQuery.isFetching}
        >
          <HugeiconsIcon
            icon={ReloadIcon}
            data-icon='inline-start'
            strokeWidth={2}
          />
          <span>{t('Refresh')}</span>
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        {activationTabs}
        {hasHardError ? (
          <ErrorState
            title={t('Unable to load HeroSMS email activations')}
            description={t(
              'Retry to fetch the latest products and activation history.'
            )}
            onRetry={() => void invalidateHeroSmsQueries()}
          />
        ) : (
          <div className='space-y-4'>
            {activationsQuery.isError ? (
              <InlineAlert
                feedback={describeHeroSmsError(
                  parseHeroSmsError(activationsQuery.error),
                  t
                )}
              />
            ) : null}

            <div className='grid gap-4 xl:grid-cols-[minmax(0,360px)_minmax(0,1fr)]'>
              <Card>
                <CardHeader>
                  <div className='flex items-start gap-3'>
                    <div className='rounded-lg border p-2'>
                      <HugeiconsIcon icon={MailSend01Icon} strokeWidth={2} />
                    </div>
                    <div className='space-y-1'>
                      <CardTitle>{t('Purchase activation')}</CardTitle>
                      <CardDescription>
                        {t(
                          'Choose a site, domain, and quantity, then confirm the latest stock and quota charge before purchasing.'
                        )}
                      </CardDescription>
                    </div>
                  </div>
                </CardHeader>
                <CardContent className='space-y-4'>
                  {purchaseFeedback ? (
                    <InlineAlert feedback={purchaseFeedback} />
                  ) : null}
                  {productsQuery.isError ? (
                    <InlineAlert
                      feedback={describeHeroSmsError(
                        parseHeroSmsError(productsQuery.error),
                        t
                      )}
                    />
                  ) : null}

                  <Field label={t('Site')} controlId='hero-sms-site'>
                    <Input
                      id='hero-sms-site'
                      value={selectedSite}
                      onChange={(event) => {
                        setSelectedSite(event.target.value)
                        setSelectedDomainId('')
                      }}
                      placeholder={t('Enter target site first')}
                    />
                  </Field>

                  {!trimmedSite ? (
                    <Alert>
                      <HugeiconsIcon
                        icon={InformationCircleIcon}
                        strokeWidth={2}
                        aria-hidden='true'
                      />
                      <AlertTitle>{t('Enter target site first')}</AlertTitle>
                      <AlertDescription>
                        {t(
                          'HeroSMS only returns purchasable email domains after you provide a non-empty target site.'
                        )}
                      </AlertDescription>
                    </Alert>
                  ) : null}

                  {trimmedSite && productsLoading && products.length === 0 ? (
                    <LoadingState inline message={t('Loading products...')} />
                  ) : null}

                  {trimmedSite &&
                  products.length === 0 &&
                  !productsLoading &&
                  !productsQuery.isError ? (
                    <Alert>
                      <HugeiconsIcon
                        icon={InformationCircleIcon}
                        strokeWidth={2}
                        aria-hidden='true'
                      />
                      <AlertTitle>{t('Purchasing unavailable')}</AlertTitle>
                      <AlertDescription>
                        {t(
                          'No HeroSMS email products are available for the target site right now.'
                        )}
                      </AlertDescription>
                    </Alert>
                  ) : null}

                  <>
                    <Field label={t('Domain')} controlId='hero-sms-domain'>
                      <Select
                        value={resolvedDomainId}
                        onValueChange={(value) =>
                          setSelectedDomainId(value ?? '')
                        }
                        disabled={!trimmedSite || siteProducts.length === 0}
                      >
                        <SelectTrigger id='hero-sms-domain' className='w-full'>
                          <SelectValue placeholder={t('Choose a domain')}>
                            {selectedProduct?.domain}
                          </SelectValue>
                        </SelectTrigger>
                        <SelectContent>
                          {siteProducts.map((product) => (
                            <SelectItem
                              key={String(product.id)}
                              value={String(product.id)}
                              disabled={!product.available}
                            >
                              {product.domain} ·{' '}
                              {t('{{count}} available', {
                                count: product.count,
                              })}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    </Field>

                    <Field label={t('Quantity')} controlId='hero-sms-quantity'>
                      <Input
                        id='hero-sms-quantity'
                        type='number'
                        min={1}
                        max={maxQuantity}
                        value={String(quantity)}
                        onChange={(event) => {
                          const next = Number(event.target.value)
                          setQuantity(
                            Number.isFinite(next)
                              ? Math.min(
                                  maxQuantity,
                                  Math.max(1, Math.trunc(next))
                                )
                              : 1
                          )
                        }}
                      />
                    </Field>

                    <div className='rounded-xl border p-3'>
                      <div className='grid gap-3 sm:grid-cols-2'>
                        <MetaItem
                          label={t('Inventory')}
                          value={formatNumber(selectedProduct?.count ?? 0)}
                        />
                        <MetaItem
                          label={t('Quote')}
                          value={formatHeroSmsUSD(
                            selectedProduct?.customer_price_usd ?? 0
                          )}
                        />
                        <MetaItem
                          label={t('Final quota price')}
                          value={formatNumber(
                            (selectedProduct?.charge_quota ?? 0) * quantity
                          )}
                        />
                      </div>
                    </div>

                    {trimmedSite &&
                    selectedProduct &&
                    !selectedProduct.available ? (
                      <Alert>
                        <HugeiconsIcon
                          icon={InformationCircleIcon}
                          strokeWidth={2}
                          aria-hidden='true'
                        />
                        <AlertTitle>{t('Out of stock')}</AlertTitle>
                        <AlertDescription>
                          {t(
                            'Choose another domain or refresh to check for replenished inventory.'
                          )}
                        </AlertDescription>
                      </Alert>
                    ) : null}

                    <Button
                      className='w-full'
                      onClick={() => {
                        if (!selectedProduct) return
                        setPurchaseTarget({
                          product: selectedProduct,
                          quantity,
                          idempotencyKey: createHeroSmsIdempotencyKey(),
                        })
                      }}
                      disabled={
                        !selectedProduct ||
                        !selectedProduct.available ||
                        selectedProduct.count < quantity ||
                        createMutation.isPending ||
                        productsLoading
                      }
                    >
                      {createMutation.isPending ? (
                        <HugeiconsIcon
                          icon={Loading03Icon}
                          data-icon='inline-start'
                          className='animate-spin'
                          strokeWidth={2}
                        />
                      ) : (
                        <HugeiconsIcon
                          icon={PackageSearchIcon}
                          data-icon='inline-start'
                          strokeWidth={2}
                        />
                      )}
                      <span>{t('Buy activation')}</span>
                    </Button>
                  </>
                </CardContent>
              </Card>

              <Card>
                <CardHeader>
                  <div className='flex items-start gap-3'>
                    <div className='rounded-lg border p-2'>
                      <HugeiconsIcon
                        icon={CheckmarkCircle02Icon}
                        strokeWidth={2}
                      />
                    </div>
                    <div className='space-y-1'>
                      <CardTitle>{t('Current activation')}</CardTitle>
                      <CardDescription>
                        {t(
                          'Keep the latest active email and verification code visible while you complete sign-up or login.'
                        )}
                      </CardDescription>
                    </div>
                  </div>
                </CardHeader>
                <CardContent className='space-y-4'>
                  {actionFeedback ? (
                    <InlineAlert feedback={actionFeedback} />
                  ) : null}
                  {currentActivationQuery.isError ? (
                    <InlineAlert
                      feedback={describeHeroSmsError(
                        parseHeroSmsError(currentActivationQuery.error),
                        t
                      )}
                    />
                  ) : null}

                  {currentActivation ? (
                    <>
                      <div className='rounded-xl border p-4'>
                        <div className='flex flex-wrap items-start justify-between gap-3'>
                          <div className='min-w-0 space-y-2'>
                            <HeroSmsStatusBadge
                              status={currentActivation.status}
                              t={t}
                            />
                            <div className='min-w-0'>
                              <p className='text-muted-foreground text-xs'>
                                {t('Email')}
                              </p>
                              <div className='flex items-center gap-2'>
                                <p className='truncate text-base font-semibold'>
                                  {currentActivation.email ||
                                    t('Pending email assignment')}
                                </p>
                                {currentActivation.email ? (
                                  <CopyButton value={currentActivation.email} />
                                ) : null}
                              </div>
                            </div>
                          </div>
                          <Button
                            variant='outline'
                            size='sm'
                            onClick={() =>
                              void handleRefresh(currentActivation)
                            }
                            disabled={refreshMutation.isPending}
                          >
                            <HugeiconsIcon
                              icon={ReloadIcon}
                              data-icon='inline-start'
                              strokeWidth={2}
                            />
                            <span>{t('Refresh')}</span>
                          </Button>
                        </div>

                        <Separator className='my-4' />

                        <div className='grid gap-4 sm:grid-cols-2'>
                          <div className='min-w-0'>
                            <p className='text-muted-foreground text-xs'>
                              {t('Verification code')}
                            </p>
                            <div className='mt-1 flex items-center gap-2'>
                              <p className='text-lg font-semibold'>
                                {currentActivation.code ||
                                  t('Waiting for code')}
                              </p>
                              {currentActivation.code ? (
                                <CopyButton value={currentActivation.code} />
                              ) : null}
                            </div>
                          </div>
                          <MetaItem
                            label={t('Quota charge')}
                            value={formatNumber(currentActivation.charge_quota)}
                          />
                        </div>
                      </div>

                      {currentActivation.message ? (
                        <Alert>
                          <HugeiconsIcon
                            icon={InformationCircleIcon}
                            strokeWidth={2}
                            aria-hidden='true'
                          />
                          <AlertTitle>{t('Latest provider update')}</AlertTitle>
                          <AlertDescription>
                            {currentActivation.message}
                          </AlertDescription>
                        </Alert>
                      ) : null}

                      <div className='flex flex-wrap gap-2'>
                        <Button
                          variant='outline'
                          onClick={() => setDetailTarget(currentActivation)}
                        >
                          {t('Open details')}
                        </Button>
                        <Button
                          variant='outline'
                          onClick={() => setCancelTarget(currentActivation)}
                          disabled={
                            !canCancelHeroSmsActivation(
                              currentActivation.status
                            )
                          }
                        >
                          <HugeiconsIcon
                            icon={CancelCircleIcon}
                            data-icon='inline-start'
                            strokeWidth={2}
                          />
                          <span>{t('Cancel')}</span>
                        </Button>
                        <Button
                          variant='outline'
                          onClick={() => void prepareReorder(currentActivation)}
                          disabled={
                            !canReorderHeroSmsActivation(
                              currentActivation.status
                            ) ||
                            reorderQuoteLoadingId ===
                              String(currentActivation.id)
                          }
                        >
                          <HugeiconsIcon
                            icon={ArrowRight01Icon}
                            data-icon='inline-start'
                            strokeWidth={2}
                          />
                          <span>{t('Reorder')}</span>
                        </Button>
                      </div>
                    </>
                  ) : (
                    <Alert>
                      <HugeiconsIcon
                        icon={InformationCircleIcon}
                        strokeWidth={2}
                        aria-hidden='true'
                      />
                      <AlertTitle>{t('No active email activation')}</AlertTitle>
                      <AlertDescription>
                        {t(
                          'Your next purchased activation will appear here until it completes, expires, or is cancelled.'
                        )}
                      </AlertDescription>
                    </Alert>
                  )}
                </CardContent>
              </Card>
            </div>

            <Card>
              <CardHeader>
                <CardTitle>{t('History')}</CardTitle>
                <CardDescription>
                  {t(
                    'Review current and past HeroSMS email activations, filter by status, and reopen order details.'
                  )}
                </CardDescription>
              </CardHeader>
              <CardContent>
                <DataTablePage
                  table={table}
                  columns={columns}
                  isLoading={
                    activationsQuery.isLoading && activations.length === 0
                  }
                  isFetching={activationsQuery.isFetching}
                  emptyTitle={t('No email activations found')}
                  emptyDescription={t(
                    'Purchase an activation to start receiving temporary email logins here.'
                  )}
                  toolbarProps={{
                    filters: [
                      {
                        columnId: 'status',
                        title: t('Status'),
                        options: statusOptions,
                        singleSelect: true,
                      },
                    ],
                  }}
                  mobile={
                    <HistoryMobileCards
                      items={activations}
                      loading={
                        activationsQuery.isLoading && activations.length === 0
                      }
                      onOpenDetail={setDetailTarget}
                      onCancel={setCancelTarget}
                      onRefresh={(activation) => void handleRefresh(activation)}
                      onReorder={(activation) =>
                        void prepareReorder(activation)
                      }
                    />
                  }
                  showPagination
                  paginationInFooter={false}
                  tableClassName='overflow-x-auto'
                />
              </CardContent>
            </Card>
          </div>
        )}

        <Sheet
          open={!!detailTarget}
          onOpenChange={(open) => !open && setDetailTarget(null)}
        >
          <SheetContent
            side={isMobile ? 'bottom' : 'right'}
            className='max-h-[88dvh] w-full overflow-y-auto sm:max-w-xl'
          >
            <SheetHeader>
              <SheetTitle>{t('Activation details')}</SheetTitle>
              <SheetDescription>
                {t(
                  'Review the latest provider status, timestamps, and order identifiers for this activation.'
                )}
              </SheetDescription>
            </SheetHeader>

            <div className='mt-6 space-y-4'>
              {detailQuery.isLoading && !detailQuery.data ? (
                <LoadingState message={t('Loading activation details...')} />
              ) : null}

              {(() => {
                const detailActivation =
                  detailQuery.data?.activation ?? detailTarget
                if (!detailActivation) {
                  return null
                }

                return (
                  <>
                    <div className='flex flex-wrap items-start justify-between gap-3'>
                      <div className='min-w-0 space-y-2'>
                        <HeroSmsStatusBadge
                          status={detailActivation.status}
                          t={t}
                        />
                        <div className='min-w-0'>
                          <p className='text-muted-foreground text-xs'>
                            {t('Email')}
                          </p>
                          <div className='flex items-center gap-2'>
                            <p className='truncate font-semibold'>
                              {detailActivation.email || '—'}
                            </p>
                            {detailActivation.email ? (
                              <CopyButton value={detailActivation.email} />
                            ) : null}
                          </div>
                        </div>
                        <div className='min-w-0'>
                          <p className='text-muted-foreground text-xs'>
                            {t('Verification code')}
                          </p>
                          <div className='flex items-center gap-2'>
                            <p className='font-semibold'>
                              {detailActivation.code || '—'}
                            </p>
                            {detailActivation.code ? (
                              <CopyButton value={detailActivation.code} />
                            ) : null}
                          </div>
                        </div>
                      </div>
                      <Badge variant='outline'>
                        {t('Order #{{id}}', { id: detailActivation.order_id })}
                      </Badge>
                    </div>

                    {detailActivation.message ? (
                      <Alert>
                        <HugeiconsIcon
                          icon={InformationCircleIcon}
                          strokeWidth={2}
                          aria-hidden='true'
                        />
                        <AlertTitle>{t('Provider message')}</AlertTitle>
                        <AlertDescription>
                          {detailActivation.message}
                        </AlertDescription>
                      </Alert>
                    ) : null}

                    <div className='grid gap-4 rounded-xl border p-4 sm:grid-cols-2'>
                      <MetaItem
                        label={t('Site')}
                        value={detailActivation.site || '—'}
                      />
                      <MetaItem
                        label={t('Domain')}
                        value={detailActivation.domain || '—'}
                      />
                      <MetaItem
                        label={t('Created')}
                        value={formatDateTime(detailActivation.created_at)}
                      />
                      <MetaItem
                        label={t('Updated')}
                        value={formatDateTime(detailActivation.updated_at)}
                      />
                      <MetaItem
                        label={t('Quota charge')}
                        value={formatNumber(detailActivation.charge_quota)}
                      />
                      <MetaItem
                        label={t('Cancellation reason')}
                        value={formatCancellationReason(
                          detailActivation.cancel_reason,
                          t
                        )}
                      />
                    </div>
                  </>
                )
              })()}
            </div>
          </SheetContent>
        </Sheet>

        <ConfirmDialog
          open={!!purchaseTarget}
          onOpenChange={(open) => !open && setPurchaseTarget(null)}
          title={t('Confirm paid purchase')}
          desc={
            purchaseTarget
              ? t(
                  'Purchase {{quantity}} × {{domain}} for {{quota}} quota ({{price}} customer price)?',
                  {
                    quantity: purchaseTarget.quantity,
                    domain: purchaseTarget.product.domain,
                    quota: formatNumber(
                      purchaseTarget.product.charge_quota *
                        purchaseTarget.quantity
                    ),
                    price: formatHeroSmsUSD(
                      purchaseTarget.product.customer_price_usd *
                        purchaseTarget.quantity
                    ),
                  }
                )
              : ''
          }
          confirmText={t('Confirm purchase')}
          isLoading={createMutation.isPending}
          handleConfirm={() =>
            purchaseTarget && void handlePurchase(purchaseTarget)
          }
        />

        <ConfirmDialog
          open={!!cancelTarget}
          onOpenChange={(open) => !open && setCancelTarget(null)}
          title={t('Cancel activation')}
          desc={t(
            'Cancel this activation to stop waiting for a code. Voluntary cancellation does not guarantee or issue a local quota refund.'
          )}
          confirmText={t('Confirm cancel')}
          destructive
          isLoading={cancelMutation.isPending}
          handleConfirm={() => void handleConfirmCancel()}
        />

        <ConfirmDialog
          open={!!reorderTarget}
          onOpenChange={(open) => !open && setReorderTarget(null)}
          title={t('Reorder paid activation')}
          desc={
            reorderTarget
              ? t(
                  'Reorder {{domain}} for {{quota}} quota ({{price}} customer price)? This creates a new paid activation.',
                  {
                    domain: reorderTarget.product.domain,
                    quota: formatNumber(reorderTarget.product.charge_quota),
                    price: formatHeroSmsUSD(
                      reorderTarget.product.customer_price_usd
                    ),
                  }
                )
              : ''
          }
          confirmText={t('Confirm reorder')}
          isLoading={reorderMutation.isPending}
          handleConfirm={() => void handleConfirmReorder()}
        />
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

function Field({
  label,
  controlId,
  children,
}: {
  label: string
  controlId: string
  children: React.ReactNode
}) {
  return (
    <div className='space-y-2'>
      <label htmlFor={controlId} className='text-sm font-medium'>
        {label}
      </label>
      {children}
    </div>
  )
}

function InlineAlert({ feedback }: { feedback: InlineFeedback }) {
  return (
    <Alert
      variant={feedback.tone === 'destructive' ? 'destructive' : 'default'}
    >
      <HugeiconsIcon
        icon={
          feedback.tone === 'destructive' ? Alert02Icon : InformationCircleIcon
        }
        strokeWidth={2}
        aria-hidden='true'
      />
      <AlertTitle>{feedback.title}</AlertTitle>
      <AlertDescription>{feedback.description}</AlertDescription>
    </Alert>
  )
}
