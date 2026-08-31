/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import {
  AlertTriangle,
  ArrowLeft,
  RefreshCw,
  Search,
  ShieldCheck,
} from 'lucide-react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { ErrorState } from '@/components/error-state'
import { Header } from '@/components/layout'
import { SectionPageLayout } from '@/components/layout/components/section-page-layout'
import { ProfileDropdown } from '@/components/profile-dropdown'
import { ThemeSwitch } from '@/components/theme-switch'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { useDebounce } from '@/hooks/use-debounce'
import { formatQuota } from '@/lib/format'

import {
  executeSubscriptionReset,
  getAdminPlans,
  getSubscriptionResetEligible,
  previewSubscriptionReset,
} from './api'
import { formatTimestamp } from './lib'
import type {
  AdminSubscriptionResetEligible,
  SubscriptionResetBatchResult,
  SubscriptionResetMode,
  SubscriptionResetPreviewResult,
} from './types'

const PAGE_SIZE = 20
const PREVIEW_TARGET_DISPLAY_LIMIT = 100
const MAX_PLAN_FILTERS = 100
const MAX_USER_FILTERS = 100

function parseResetUserIds(value: string) {
  const trimmed = value.trim()
  if (!trimmed) return { ids: [] as number[], invalid: false }

  const ids: number[] = []
  const seen = new Set<number>()
  for (const raw of trimmed.split(',')) {
    const token = raw.trim()
    if (!/^\d+$/.test(token)) return { ids: [] as number[], invalid: true }
    const id = Number(token)
    if (!Number.isSafeInteger(id) || id <= 0) {
      return { ids: [] as number[], invalid: true }
    }
    if (seen.has(id)) continue
    seen.add(id)
    ids.push(id)
    if (ids.length > MAX_USER_FILTERS) {
      return { ids: [] as number[], invalid: true }
    }
  }
  return { ids, invalid: false }
}

function targetKey(
  target: Pick<AdminSubscriptionResetEligible, 'user_id' | 'plan_id'>
) {
  return `${target.user_id}:${target.plan_id}`
}

export function SubscriptionResetWorkspace() {
  const { t } = useTranslation()
  const [mode, setMode] = useState<SubscriptionResetMode>('hard')
  const [query, setQuery] = useState('')
  const [userIdsInput, setUserIdsInput] = useState('')
  const [page, setPage] = useState(1)
  const [planIds, setPlanIds] = useState<number[]>([])
  const [selected, setSelected] = useState<
    Map<string, AdminSubscriptionResetEligible>
  >(new Map())
  const [allMatching, setAllMatching] = useState(false)
  const [preview, setPreview] = useState<SubscriptionResetPreviewResult | null>(
    null
  )
  const [result, setResult] = useState<SubscriptionResetBatchResult | null>(
    null
  )
  const [operationId, setOperationId] = useState('')
  const [previewing, setPreviewing] = useState(false)
  const [executing, setExecuting] = useState(false)
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [actionError, setActionError] = useState('')
  const previewRequestId = useRef(0)
  const debouncedQuery = useDebounce(query.trim(), 300)
  const planKey = useMemo(
    () => [...planIds].sort((a, b) => a - b).join(','),
    [planIds]
  )
  const userFilter = useMemo(
    () => parseResetUserIds(userIdsInput),
    [userIdsInput]
  )
  const userKey = userFilter.ids.join(',')

  const invalidateApproval = useCallback(() => {
    previewRequestId.current += 1
    setPreviewing(false)
    setPreview(null)
    setResult(null)
    setOperationId('')
    setConfirmOpen(false)
    setActionError('')
  }, [])

  useEffect(() => {
    setPage(1)
    setSelected(new Map())
    setAllMatching(false)
    invalidateApproval()
  }, [query, planKey, userIdsInput, invalidateApproval])

  const plansQuery = useQuery({
    queryKey: ['admin-subscription-plans', 'reset-workspace'],
    queryFn: () => getAdminPlans(true),
    staleTime: 30_000,
  })
  const eligibleQuery = useQuery({
    queryKey: [
      'subscription-reset-eligible',
      page,
      debouncedQuery,
      planKey,
      userKey,
    ],
    queryFn: ({ signal }) =>
      getSubscriptionResetEligible(
        {
          page,
          pageSize: PAGE_SIZE,
          query: debouncedQuery || undefined,
          planIds,
          userIds: userFilter.ids,
        },
        signal
      ),
    placeholderData: keepPreviousData,
    enabled: !userFilter.invalid,
  })

  const plans = plansQuery.data?.data ?? []
  const eligible = eligibleQuery.data?.data?.items ?? []
  const total = eligibleQuery.data?.data?.total ?? 0
  const pages = Math.max(1, Math.ceil(total / PAGE_SIZE))
  const loadedSelected = eligible.filter((item) =>
    selected.has(targetKey(item))
  ).length
  const filtersSettled =
    !userFilter.invalid &&
    query.trim() === debouncedQuery &&
    !eligibleQuery.isFetching &&
    !eligibleQuery.isError
  const canPreview =
    filtersSettled && (allMatching ? total > 0 : selected.size > 0)

  const toggleTarget = (
    target: AdminSubscriptionResetEligible,
    checked: boolean
  ) => {
    const next = new Map(selected)
    if (checked) next.set(targetKey(target), target)
    else next.delete(targetKey(target))
    setSelected(next)
    invalidateApproval()
  }

  const toggleLoaded = (checked: boolean) => {
    const next = new Map(selected)
    for (const target of eligible) {
      if (checked) next.set(targetKey(target), target)
      else next.delete(targetKey(target))
    }
    setSelected(next)
    invalidateApproval()
  }

  const requestPreview = async () => {
    if (!canPreview) return
    const requestId = ++previewRequestId.current
    setPreviewing(true)
    setConfirmOpen(false)
    setPreview(null)
    setOperationId('')
    setActionError('')
    setResult(null)
    try {
      const response = await previewSubscriptionReset({
        mode,
        all_matching: allMatching,
        targets: allMatching
          ? undefined
          : [...selected.values()].map((target) => ({
              user_id: target.user_id,
              plan_id: target.plan_id,
            })),
        filter: {
          query: debouncedQuery || undefined,
          plan_ids: planIds.length ? planIds : undefined,
          user_ids: userFilter.ids.length ? userFilter.ids : undefined,
        },
      })
      if (requestId !== previewRequestId.current) return
      if (!response.success || !response.data) {
        throw new Error(response.message || t('Preview failed'))
      }
      setPreview(response.data)
      setOperationId(crypto.randomUUID())
    } catch (error) {
      if (requestId !== previewRequestId.current) return
      setPreview(null)
      setOperationId('')
      setActionError(
        error instanceof Error ? error.message : t('Preview failed')
      )
    } finally {
      if (requestId === previewRequestId.current) setPreviewing(false)
    }
  }

  const execute = async () => {
    if (!preview || !operationId) return
    setExecuting(true)
    setActionError('')
    try {
      const response = await executeSubscriptionReset({
        preview_token: preview.token,
        operation_id: operationId,
      })
      if (!response.success || !response.data) {
        throw new Error(response.message || t('Reset failed'))
      }
      setResult(response.data)
      setConfirmOpen(false)
      toast.success(t('Subscription reset completed'))
      void eligibleQuery.refetch()
    } catch (error) {
      setActionError(error instanceof Error ? error.message : t('Reset failed'))
      setConfirmOpen(false)
    } finally {
      setExecuting(false)
    }
  }

  if (eligibleQuery.isError && !eligibleQuery.data) {
    return (
      <>
        <Header>
          <div className='ms-auto flex items-center gap-4'>
            <ThemeSwitch />
            <ProfileDropdown />
          </div>
        </Header>
        <SectionPageLayout>
          <SectionPageLayout.Title>
            {t('Subscription reset workspace')}
          </SectionPageLayout.Title>
          <SectionPageLayout.Content>
            <ErrorState
              title={t('Failed to load eligible subscriptions')}
              description={t(
                'No reset action was performed. Retry the read-only request.'
              )}
              onRetry={() => void eligibleQuery.refetch()}
            />
          </SectionPageLayout.Content>
        </SectionPageLayout>
      </>
    )
  }

  return (
    <>
      <Header>
        <div className='ms-auto flex items-center gap-4'>
          <ThemeSwitch />
          <ProfileDropdown />
        </div>
      </Header>
      <SectionPageLayout>
        <SectionPageLayout.Breadcrumb>
          <Button
            variant='ghost'
            size='sm'
            render={<Link to='/subscriptions' />}
          >
            <ArrowLeft aria-hidden='true' />
            {t('Back to subscriptions')}
          </Button>
        </SectionPageLayout.Breadcrumb>
        <SectionPageLayout.Title>
          {t('Subscription reset workspace')}
        </SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <Badge variant='outline'>
            <ShieldCheck aria-hidden='true' />
            {t('Root only')}
          </Badge>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <div className='space-y-4'>
            <Alert>
              <AlertTriangle aria-hidden='true' />
              <AlertTitle>{t('Preview is required')}</AlertTitle>
              <AlertDescription>
                {t(
                  'Hard reset changes only used subscription quota. It never extends a subscription or advances its reset schedule.'
                )}
              </AlertDescription>
            </Alert>

            <section
              className='space-y-3 rounded-md border p-3 sm:p-4'
              aria-labelledby='reset-scope-heading'
            >
              <div className='flex flex-wrap items-center justify-between gap-3'>
                <div>
                  <h3 id='reset-scope-heading' className='font-semibold'>
                    {t('1. Choose reset scope')}
                  </h3>
                  <p className='text-muted-foreground text-sm'>
                    {t(
                      'Filter eligible active subscriptions, then choose explicit rows or every matching result.'
                    )}
                  </p>
                </div>
                <Select
                  value={mode}
                  disabled={executing}
                  onValueChange={(value) => {
                    const nextMode = value as SubscriptionResetMode
                    if (nextMode === mode) return
                    invalidateApproval()
                    setMode(nextMode)
                  }}
                >
                  <SelectTrigger className='w-52' aria-label={t('Reset mode')}>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value='hard'>{t('Hard reset now')}</SelectItem>
                    <SelectItem value='soft'>
                      {t('Issue banked reset voucher')}
                    </SelectItem>
                  </SelectContent>
                </Select>
              </div>

              <div className='grid gap-3 sm:grid-cols-2'>
                <div className='relative self-end'>
                  <Search className='text-muted-foreground pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2' />
                  <Input
                    value={query}
                    maxLength={200}
                    disabled={executing}
                    onChange={(event) => setQuery(event.target.value)}
                    placeholder={t('Search eligible users or plans')}
                    aria-label={t('Search eligible subscriptions')}
                    className='pl-9'
                  />
                </div>
                <div className='space-y-1'>
                  <Label htmlFor='subscription-reset-user-ids'>
                    {t('User IDs')}
                  </Label>
                  <Input
                    id='subscription-reset-user-ids'
                    value={userIdsInput}
                    disabled={executing}
                    onChange={(event) => setUserIdsInput(event.target.value)}
                    placeholder={t('For example: 12, 34, 56')}
                    aria-invalid={userFilter.invalid}
                    aria-describedby='subscription-reset-user-ids-help'
                  />
                  <p
                    id='subscription-reset-user-ids-help'
                    className={
                      userFilter.invalid
                        ? 'text-destructive text-xs'
                        : 'text-muted-foreground text-xs'
                    }
                  >
                    {userFilter.invalid
                      ? t(
                          'User IDs must be positive integers separated by commas, with at most {{count}} entries.',
                          { count: MAX_USER_FILTERS }
                        )
                      : t('Optional comma-separated user ID filter.')}
                  </p>
                </div>
              </div>

              <fieldset className='space-y-2'>
                <legend className='text-sm font-medium'>{t('Plans')}</legend>
                <div className='flex max-h-32 flex-wrap gap-2 overflow-y-auto rounded-md border p-2'>
                  {plansQuery.isPending ? (
                    <Skeleton className='h-8 w-full' />
                  ) : plansQuery.isError ? (
                    <div className='flex w-full items-center justify-between gap-2 text-sm'>
                      <span>{t('Failed to load plans')}</span>
                      <Button
                        variant='outline'
                        size='sm'
                        onClick={() => void plansQuery.refetch()}
                      >
                        {t('Retry')}
                      </Button>
                    </div>
                  ) : (
                    plans.map((record) => {
                      const id = record.plan.id
                      if (!id) return null
                      const checked = planIds.includes(id)
                      return (
                        <label
                          key={id}
                          className='hover:bg-muted flex cursor-pointer items-center gap-2 rounded-sm px-2 py-1 text-sm'
                        >
                          <Checkbox
                            disabled={
                              executing ||
                              (!checked && planIds.length >= MAX_PLAN_FILTERS)
                            }
                            checked={checked}
                            onCheckedChange={(value) => {
                              setPlanIds((current) =>
                                value
                                  ? [...current, id]
                                  : current.filter((item) => item !== id)
                              )
                            }}
                          />
                          <span>{record.plan.title}</span>
                          {(record.plan.archived_at ?? 0) > 0 && (
                            <Badge variant='outline'>{t('Archived')}</Badge>
                          )}
                        </label>
                      )
                    })
                  )}
                  {plansQuery.isSuccess && plans.length === 0 && (
                    <span className='text-muted-foreground text-sm'>
                      {t('No plans available')}
                    </span>
                  )}
                  {planIds.length >= MAX_PLAN_FILTERS && (
                    <span className='text-muted-foreground w-full text-xs'>
                      {t('You can select up to {{count}} plan filters.', {
                        count: MAX_PLAN_FILTERS,
                      })}
                    </span>
                  )}
                </div>
              </fieldset>

              <label className='bg-muted/40 flex items-start gap-3 rounded-md border p-3'>
                <Checkbox
                  disabled={executing || !filtersSettled || total === 0}
                  checked={allMatching}
                  onCheckedChange={(value) => {
                    const checked = value === true
                    setAllMatching(checked)
                    if (checked) setSelected(new Map())
                    invalidateApproval()
                  }}
                  aria-label={t('Select all matching subscriptions')}
                />
                <span>
                  <span className='block text-sm font-medium'>
                    {t('Select all {{count}} matching user-plan pairs', {
                      count: total,
                    })}
                  </span>
                  <span className='text-muted-foreground block text-xs'>
                    {t(
                      'This includes matching rows on pages that are not currently loaded.'
                    )}
                  </span>
                </span>
              </label>

              <div
                id='subscription-reset-query-status'
                className='text-muted-foreground flex min-h-5 items-center gap-2 text-xs'
                role='status'
                aria-live='polite'
                aria-atomic='true'
              >
                {eligibleQuery.isFetching ? (
                  <>
                    <RefreshCw
                      className='size-3.5 shrink-0 animate-spin motion-reduce:animate-none'
                      aria-hidden='true'
                    />
                    <span>
                      {eligibleQuery.isPending
                        ? t('Loading...')
                        : t('Refreshing...')}
                    </span>
                  </>
                ) : null}
              </div>

              <div
                className='overflow-hidden rounded-md border'
                aria-busy={eligibleQuery.isFetching}
                aria-describedby='subscription-reset-query-status'
              >
                <div className='overflow-x-auto'>
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead className='w-10'>
                          <Checkbox
                            disabled={
                              executing ||
                              !filtersSettled ||
                              allMatching ||
                              eligible.length === 0
                            }
                            checked={
                              eligible.length > 0 &&
                              loadedSelected === eligible.length
                            }
                            onCheckedChange={(value) =>
                              toggleLoaded(value === true)
                            }
                            aria-label={t('Select loaded rows')}
                          />
                        </TableHead>
                        <TableHead>{t('User')}</TableHead>
                        <TableHead>{t('Plan')}</TableHead>
                        <TableHead className='text-right'>
                          {t('Active subscriptions')}
                        </TableHead>
                        <TableHead className='text-right'>
                          {t('Used quota')}
                        </TableHead>
                        <TableHead className='text-right'>
                          {t('Banked resets')}
                        </TableHead>
                        <TableHead>{t('Next reset')}</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {eligibleQuery.isPending ? (
                        Array.from({ length: 5 }, (_, index) => (
                          <TableRow key={index}>
                            <TableCell colSpan={7}>
                              <Skeleton className='h-7 w-full' />
                            </TableCell>
                          </TableRow>
                        ))
                      ) : eligible.length === 0 ? (
                        <TableRow>
                          <TableCell colSpan={7} className='h-36 text-center'>
                            <p className='font-medium'>
                              {t('No eligible subscriptions')}
                            </p>
                            <p className='text-muted-foreground mt-1 text-sm'>
                              {t(
                                'Only active, non-expired subscriptions can be reset.'
                              )}
                            </p>
                          </TableCell>
                        </TableRow>
                      ) : (
                        eligible.map((item) => {
                          const key = targetKey(item)
                          return (
                            <TableRow
                              key={key}
                              data-state={
                                selected.has(key) ? 'selected' : undefined
                              }
                            >
                              <TableCell>
                                <Checkbox
                                  disabled={
                                    executing || !filtersSettled || allMatching
                                  }
                                  checked={selected.has(key)}
                                  onCheckedChange={(value) =>
                                    toggleTarget(item, value === true)
                                  }
                                  aria-label={t('Select {{user}} on {{plan}}', {
                                    user: item.username,
                                    plan: item.plan_title,
                                  })}
                                />
                              </TableCell>
                              <TableCell>
                                <div className='font-medium'>
                                  {item.username || `#${item.user_id}`}
                                </div>
                                <div className='text-muted-foreground text-xs'>
                                  {item.email || `ID ${item.user_id}`}
                                </div>
                              </TableCell>
                              <TableCell>
                                {item.plan_title}
                                {item.plan_archived_at > 0 && (
                                  <Badge variant='outline' className='ml-2'>
                                    {t('Archived')}
                                  </Badge>
                                )}
                              </TableCell>
                              <TableCell className='text-right tabular-nums'>
                                {item.active_subscription_count}
                              </TableCell>
                              <TableCell className='text-right tabular-nums'>
                                {formatQuota(item.amount_used)}
                              </TableCell>
                              <TableCell className='text-right tabular-nums'>
                                {item.banked_voucher_count}
                              </TableCell>
                              <TableCell className='text-muted-foreground whitespace-nowrap'>
                                {formatTimestamp(item.next_reset_time)}
                              </TableCell>
                            </TableRow>
                          )
                        })
                      )}
                    </TableBody>
                  </Table>
                </div>
              </div>

              <div className='flex flex-wrap items-center justify-between gap-3'>
                <span className='text-muted-foreground text-sm'>
                  {allMatching
                    ? t('All matching rows selected')
                    : t('{{count}} explicit pairs selected', {
                        count: selected.size,
                      })}
                </span>
                <div className='flex items-center gap-2'>
                  <Button
                    variant='outline'
                    size='sm'
                    disabled={
                      executing || page <= 1 || eligibleQuery.isFetching
                    }
                    onClick={() => setPage((value) => Math.max(1, value - 1))}
                  >
                    {t('Previous')}
                  </Button>
                  <span className='min-w-20 text-center text-sm tabular-nums'>
                    {page} / {pages}
                  </span>
                  <Button
                    variant='outline'
                    size='sm'
                    disabled={
                      executing || page >= pages || eligibleQuery.isFetching
                    }
                    onClick={() =>
                      setPage((value) => Math.min(pages, value + 1))
                    }
                  >
                    {t('Next')}
                  </Button>
                </div>
              </div>
            </section>

            <section
              className='space-y-3 rounded-md border p-3 sm:p-4'
              aria-labelledby='reset-preview-heading'
            >
              <div className='flex flex-wrap items-center justify-between gap-3'>
                <div>
                  <h3 id='reset-preview-heading' className='font-semibold'>
                    {t('2. Preview and confirm')}
                  </h3>
                  <p className='text-muted-foreground text-sm'>
                    {t('Execution is locked to this server-generated preview.')}
                  </p>
                </div>
                <Button
                  disabled={!canPreview || previewing || executing}
                  onClick={() => void requestPreview()}
                >
                  {previewing ? t('Preparing preview…') : t('Prepare preview')}
                </Button>
              </div>

              {eligibleQuery.isError && eligibleQuery.data && (
                <Alert variant='destructive' role='alert'>
                  <AlertTriangle aria-hidden='true' />
                  <AlertTitle>
                    {t('Failed to load eligible subscriptions')}
                  </AlertTitle>
                  <AlertDescription className='flex flex-wrap items-center justify-between gap-2'>
                    <span>{t('Review the filters and try again.')}</span>
                    <Button
                      variant='outline'
                      size='sm'
                      onClick={() => void eligibleQuery.refetch()}
                    >
                      {t('Retry')}
                    </Button>
                  </AlertDescription>
                </Alert>
              )}

              {actionError && (
                <Alert variant='destructive' role='alert' aria-live='assertive'>
                  <AlertTriangle aria-hidden='true' />
                  <AlertTitle>{t('Reset action failed')}</AlertTitle>
                  <AlertDescription>{actionError}</AlertDescription>
                </Alert>
              )}

              {preview && (
                <div className='bg-muted/30 grid gap-3 rounded-md border p-3 sm:grid-cols-2 lg:grid-cols-4'>
                  <p className='sr-only' role='status'>
                    {t('Preview ready for {{count}} user-plan pairs.', {
                      count: preview.target_count,
                    })}
                  </p>
                  <div>
                    <span className='text-muted-foreground text-xs'>
                      {t('User-plan pairs')}
                    </span>
                    <strong className='block text-lg tabular-nums'>
                      {preview.target_count}
                    </strong>
                  </div>
                  <div>
                    <span className='text-muted-foreground text-xs'>
                      {t('Active subscriptions')}
                    </span>
                    <strong className='block text-lg tabular-nums'>
                      {preview.active_subscriptions}
                    </strong>
                  </div>
                  <div>
                    <span className='text-muted-foreground text-xs'>
                      {t('Used quota')}
                    </span>
                    <strong className='block text-lg tabular-nums'>
                      {formatQuota(preview.quota_to_restore)}
                    </strong>
                  </div>
                  <div>
                    <span className='text-muted-foreground text-xs'>
                      {mode === 'soft'
                        ? t('Voucher expires')
                        : t('Preview expires')}
                    </span>
                    <strong className='block text-sm'>
                      {formatTimestamp(
                        mode === 'soft'
                          ? preview.voucher_expires_at
                          : preview.expires_at
                      )}
                    </strong>
                  </div>
                  <div className='space-y-2 sm:col-span-2 lg:col-span-4'>
                    <div className='flex flex-wrap items-center justify-between gap-2'>
                      <h4 className='text-sm font-medium'>
                        {t('Preview targets')}
                      </h4>
                      {preview.targets.length >
                        PREVIEW_TARGET_DISPLAY_LIMIT && (
                        <span className='text-muted-foreground text-xs'>
                          {t(
                            'Showing first {{count}} of {{total}} user-plan pairs.',
                            {
                              count: PREVIEW_TARGET_DISPLAY_LIMIT,
                              total: preview.targets.length,
                            }
                          )}
                        </span>
                      )}
                    </div>
                    {mode === 'soft' &&
                      preview.targets.some(
                        (target) => target.banked_voucher_count > 0
                      ) && (
                        <Alert>
                          <AlertTriangle aria-hidden='true' />
                          <AlertTitle>{t('Banked resets')}</AlertTitle>
                          <AlertDescription>
                            {t(
                              '{{count}} selected user-plan pairs already have banked resets. Issuing again will add another voucher.',
                              {
                                count: preview.targets.filter(
                                  (target) => target.banked_voucher_count > 0
                                ).length,
                              }
                            )}
                          </AlertDescription>
                        </Alert>
                      )}
                    <div className='bg-background max-h-64 overflow-auto rounded-md border'>
                      <Table>
                        <TableHeader>
                          <TableRow>
                            <TableHead>{t('User')}</TableHead>
                            <TableHead>{t('Plan')}</TableHead>
                            <TableHead className='text-right'>
                              {t('Active subscriptions')}
                            </TableHead>
                            <TableHead className='text-right'>
                              {t('Used quota')}
                            </TableHead>
                            <TableHead className='text-right'>
                              {t('Banked resets')}
                            </TableHead>
                          </TableRow>
                        </TableHeader>
                        <TableBody>
                          {preview.targets
                            .slice(0, PREVIEW_TARGET_DISPLAY_LIMIT)
                            .map((target) => (
                              <TableRow key={targetKey(target)}>
                                <TableCell>
                                  <div className='font-medium'>
                                    {target.username || `#${target.user_id}`}
                                  </div>
                                  <div className='text-muted-foreground text-xs'>
                                    {target.email || `ID ${target.user_id}`}
                                  </div>
                                </TableCell>
                                <TableCell>
                                  {target.plan_title}
                                  {target.plan_archived_at > 0 && (
                                    <Badge variant='outline' className='ml-2'>
                                      {t('Archived')}
                                    </Badge>
                                  )}
                                </TableCell>
                                <TableCell className='text-right tabular-nums'>
                                  {target.active_subscription_count}
                                </TableCell>
                                <TableCell className='text-right tabular-nums'>
                                  {formatQuota(target.amount_used)}
                                </TableCell>
                                <TableCell className='text-right tabular-nums'>
                                  {target.banked_voucher_count}
                                </TableCell>
                              </TableRow>
                            ))}
                        </TableBody>
                      </Table>
                    </div>
                  </div>
                  <div className='flex justify-end sm:col-span-2 lg:col-span-4'>
                    <Button
                      variant={mode === 'hard' ? 'destructive' : 'default'}
                      disabled={!!result || executing}
                      onClick={() => setConfirmOpen(true)}
                    >
                      {mode === 'hard'
                        ? t('Confirm hard reset')
                        : t('Confirm voucher issue')}
                    </Button>
                  </div>
                </div>
              )}

              {result && (
                <Alert role='status' aria-live='polite'>
                  <ShieldCheck aria-hidden='true' />
                  <AlertTitle>{t('Reset operation completed')}</AlertTitle>
                  <AlertDescription>
                    {result.mode === 'hard'
                      ? t(
                          'Reset {{subscriptions}} subscriptions and restored {{quota}}.',
                          {
                            subscriptions: result.reset_subscriptions,
                            quota: formatQuota(result.restored_quota),
                          }
                        )
                      : t('Issued {{count}} banked reset vouchers.', {
                          count: result.vouchers_issued,
                        })}
                  </AlertDescription>
                </Alert>
              )}
            </section>
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>

      {preview && (
        <ConfirmDialog
          open={confirmOpen}
          onOpenChange={setConfirmOpen}
          title={
            mode === 'hard'
              ? t('Confirm hard subscription reset')
              : t('Confirm banked reset vouchers')
          }
          desc={
            mode === 'hard'
              ? t(
                  'Set used quota to zero for exactly {{count}} previewed active subscriptions? Subscription expiry and reset schedule will not change.',
                  { count: preview.active_subscriptions }
                )
              : t(
                  'Issue one single-use voucher for each of {{count}} previewed user-plan pairs? Each voucher expires in one calendar month.',
                  { count: preview.target_count }
                )
          }
          confirmText={
            mode === 'hard' ? t('Reset used quota') : t('Issue vouchers')
          }
          destructive={mode === 'hard'}
          isLoading={executing}
          handleConfirm={() => void execute()}
        />
      )}
    </>
  )
}
