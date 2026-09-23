/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  ArrowDown,
  ArrowUp,
  ExternalLink,
  Flag,
  MessageSquare,
  Plus,
  ShieldCheck,
  Star,
} from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { SectionPageLayout } from '@/components/layout'
import {
  Alert,
  AlertAction,
  AlertDescription,
  AlertTitle,
} from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Separator } from '@/components/ui/separator'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'
import { ChannelsProvider } from '@/features/channels/components/channels-provider'
import { ChannelMutateDrawer } from '@/features/channels/components/drawers/channel-mutate-drawer'
import { transformFormDataToCreatePayload } from '@/features/channels/lib/channel-form'
import {
  getSystemOptions,
  updateSystemOption,
} from '@/features/system-settings/api'
import { getUserGroups } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import {
  getPublicRelayConfig,
  getPublicRelayRouting,
  listPublicRelayReviews,
  listAdminPublicRelayReports,
  listAdminPublicRelays,
  listMyPublicRelays,
  listPublicRelays,
  reportPublicRelay,
  ratePublicRelay,
  tipPublicRelay,
  reviewPublicRelay,
  reviewPublicRelayReport,
  submitPublicRelay,
  withdrawPublicRelayTips,
  updatePublicRelayRouting,
} from './api'
import type { PublicRelay } from './types'

const emptyDraft = { name: '', base_url: '', models: '', description: '' }
const emptyShareChecklist = {
  credentialsAuthorized: false,
  permission: false,
  modelsVerified: false,
}

function formatDate(timestamp: number) {
  if (!timestamp) return '—'
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium' }).format(
    new Date(timestamp * 1000)
  )
}

function modelList(raw: string) {
  return [
    ...new Set(
      raw
        .split(/[\s,]+/)
        .map((value) => value.trim())
        .filter(Boolean)
    ),
  ].slice(0, 40)
}

function ratingLabel(value = 0) {
  return value > 0 ? value.toFixed(1) : '—'
}

const sortLabels = {
  rating: 'Top rated',
  recent: 'Recently updated',
  models: 'Most models',
} as const

function PublicRelayLoadError(props: {
  onRetry: () => void | Promise<unknown>
}) {
  const { t } = useTranslation()

  return (
    <Alert variant='destructive'>
      <AlertTitle>{t('Failed to load')}</AlertTitle>
      <AlertDescription>{t('Please try again later.')}</AlertDescription>
      <AlertAction>
        <Button
          type='button'
          size='sm'
          variant='outline'
          onClick={() => void props.onRetry()}
        >
          {t('Retry')}
        </Button>
      </AlertAction>
    </Alert>
  )
}

export function PublicRelay() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const user = useAuthStore((state) => state.auth.user)
  const isAdmin = (user?.role ?? 0) >= ROLE.ADMIN
  const isRoot = (user?.role ?? 0) >= ROLE.SUPER_ADMIN
  const [submitOpen, setSubmitOpen] = useState(false)
  const [reportTarget, setReportTarget] = useState<PublicRelay | null>(null)
  const [reviewTarget, setReviewTarget] = useState<PublicRelay | null>(null)
  const [tipTarget, setTipTarget] = useState<PublicRelay | null>(null)
  const [withdrawTarget, setWithdrawTarget] = useState<PublicRelay | null>(null)
  const [draft, setDraft] = useState(emptyDraft)
  const [shareChecklist, setShareChecklist] = useState(emptyShareChecklist)
  const [reportReason, setReportReason] = useState('')
  const [reviewRating, setReviewRating] = useState(5)
  const [reviewComment, setReviewComment] = useState('')
  const [tipAmount, setTipAmount] = useState('1')
  const [tipMessage, setTipMessage] = useState('')
  const [withdrawGroup, setWithdrawGroup] = useState('')
  const [reviewNote, setReviewNote] = useState<Record<number, string>>({})
  const [publicGroup, setPublicGroup] = useState('')
  const [sortMode, setSortMode] = useState<'rating' | 'recent' | 'models'>(
    'rating'
  )
  const [activeTab, setActiveTab] = useState('all')
  const [routingDisabled, setRoutingDisabled] = useState<number[]>([])
  const [routingOrder, setRoutingOrder] = useState<number[]>([])

  const configQuery = useQuery({
    queryKey: ['public-relays', 'config'],
    queryFn: getPublicRelayConfig,
    staleTime: 5 * 60 * 1000,
  })
  const groupsQuery = useQuery({
    queryKey: ['public-relays', 'user-groups'],
    queryFn: getUserGroups,
    staleTime: 60_000,
    enabled: activeTab === 'mine',
  })
  const allQuery = useQuery({
    queryKey: ['public-relays', 'all'],
    queryFn: listPublicRelays,
    enabled: activeTab === 'all',
  })
  const mineQuery = useQuery({
    queryKey: ['public-relays', 'mine'],
    queryFn: listMyPublicRelays,
    enabled: activeTab === 'mine',
  })
  const routingQuery = useQuery({
    queryKey: ['public-relays', 'routing'],
    queryFn: getPublicRelayRouting,
    enabled: activeTab === 'routing',
  })
  const reviewsQuery = useQuery({
    queryKey: ['public-relays', 'reviews', reviewTarget?.id],
    queryFn: () => {
      if (!reviewTarget) throw new Error('A review target is required')
      return listPublicRelayReviews(reviewTarget.id)
    },
    enabled: reviewTarget != null,
  })
  const adminQuery = useQuery({
    queryKey: ['public-relays', 'admin'],
    queryFn: () => listAdminPublicRelays('pending'),
    enabled: isAdmin && activeTab === 'review',
  })
  const reportsQuery = useQuery({
    queryKey: ['public-relays', 'reports'],
    queryFn: listAdminPublicRelayReports,
    enabled: isAdmin && activeTab === 'review',
  })
  const systemOptionsQuery = useQuery({
    queryKey: ['public-relays', 'system-options'],
    queryFn: getSystemOptions,
    enabled: isRoot,
  })
  useEffect(() => {
    const value = systemOptionsQuery.data?.data?.find(
      (option) => option.key === 'public_relay_setting.group'
    )?.value
    if (value) setPublicGroup(value)
  }, [systemOptionsQuery.data])
  useEffect(() => {
    if (!routingQuery.data) return
    setRoutingDisabled(
      routingQuery.data.items
        .filter((item) => item.disabled)
        .map((item) => item.channel_id)
    )
    setRoutingOrder(
      [...routingQuery.data.items]
        .sort((a, b) => a.position - b.position)
        .map((item) => item.channel_id)
    )
  }, [routingQuery.data])
  const saveGroupMutation = useMutation({
    mutationFn: (group: string) =>
      updateSystemOption({ key: 'public_relay_setting.group', value: group }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['public-relays'] })
      toast.success(t('Settings saved'))
    },
    onError: (error) =>
      toast.error(error instanceof Error ? error.message : t('Request failed')),
  })

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ['public-relays'] })
  }
  const handleSubmitOpenChange = (open: boolean) => {
    setSubmitOpen(open)
    if (!open) {
      setDraft(emptyDraft)
      setShareChecklist(emptyShareChecklist)
    }
  }
  const submitMutation = useMutation({
    mutationFn: submitPublicRelay,
    onSuccess: () => {
      setSubmitOpen(false)
      setDraft(emptyDraft)
      setShareChecklist(emptyShareChecklist)
      invalidate()
      toast.success(t('Submission sent for review'))
    },
    onError: (error) =>
      toast.error(error instanceof Error ? error.message : t('Request failed')),
  })
  const reportMutation = useMutation({
    mutationFn: ({ id, reason }: { id: number; reason: string }) =>
      reportPublicRelay(id, reason),
    onSuccess: () => {
      setReportTarget(null)
      setReportReason('')
      toast.success(t('Report sent to administrators'))
    },
    onError: (error) =>
      toast.error(error instanceof Error ? error.message : t('Request failed')),
  })
  const ratingMutation = useMutation({
    mutationFn: ({
      id,
      rating,
      comment,
    }: {
      id: number
      rating: number
      comment: string
    }) => ratePublicRelay(id, rating, comment),
    onSuccess: () => {
      setReviewTarget(null)
      setReviewComment('')
      invalidate()
      toast.success(t('Review submitted'))
    },
    onError: (error) =>
      toast.error(error instanceof Error ? error.message : t('Request failed')),
  })
  const tipMutation = useMutation({
    mutationFn: ({
      id,
      amount,
      message,
    }: {
      id: number
      amount: number
      message: string
    }) => tipPublicRelay(id, amount, message),
    onSuccess: () => {
      setTipTarget(null)
      setTipAmount('1')
      setTipMessage('')
      invalidate()
      toast.success(t('Tip sent'))
    },
    onError: (error) =>
      toast.error(error instanceof Error ? error.message : t('Request failed')),
  })
  const withdrawMutation = useMutation({
    mutationFn: ({ id, group }: { id: number; group: string }) =>
      withdrawPublicRelayTips(id, group),
    onSuccess: () => {
      setWithdrawTarget(null)
      setWithdrawGroup('')
      invalidate()
      toast.success(t('Tips withdrawn'))
    },
    onError: (error) =>
      toast.error(error instanceof Error ? error.message : t('Request failed')),
  })
  const routingMutation = useMutation({
    mutationFn: () => updatePublicRelayRouting(routingDisabled, routingOrder),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ['public-relays', 'routing'],
      })
      toast.success(t('Routing preferences saved'))
    },
    onError: (error) =>
      toast.error(error instanceof Error ? error.message : t('Request failed')),
  })
  const reviewMutation = useMutation({
    mutationFn: ({ id, approve }: { id: number; approve: boolean }) =>
      reviewPublicRelay(id, approve, reviewNote[id] ?? ''),
    onSuccess: invalidate,
    onError: (error) =>
      toast.error(error instanceof Error ? error.message : t('Request failed')),
  })
  const reportReviewMutation = useMutation({
    mutationFn: ({ id, close }: { id: number; close: boolean }) =>
      reviewPublicRelayReport(id, close, ''),
    onSuccess: invalidate,
  })

  const renderRelay = (item: PublicRelay, mine = false) => (
    <article
      key={item.id}
      className='grid gap-3 py-5 sm:grid-cols-[1fr_auto] sm:gap-8'
    >
      <div className='min-w-0 space-y-2'>
        <div className='flex flex-wrap items-center gap-x-3 gap-y-1'>
          <h3 className='font-medium'>{item.name}</h3>
          <span className='text-muted-foreground text-xs'>{item.group}</span>
          <span className='text-muted-foreground text-xs'>
            {formatDate(item.created_at)}
          </span>
        </div>
        <a
          className='text-primary inline-flex max-w-full items-center gap-1 truncate text-sm hover:underline'
          href={item.base_url}
          target='_blank'
          rel='noreferrer'
        >
          <span className='truncate'>{item.base_url}</span>
          <ExternalLink className='size-3.5 shrink-0' />
        </a>
        <p className='text-muted-foreground text-sm'>
          {item.description || t('No description')}
        </p>
        <div className='text-muted-foreground flex flex-wrap items-center gap-x-4 gap-y-2 text-xs'>
          <span className='inline-flex items-center gap-1'>
            <Star className='size-3.5' />
            {ratingLabel(item.rating_average)} ({item.rating_count ?? 0})
          </span>
          <span>
            {t('Contributor')}: {item.contributor_email}
          </span>
          {mine ? (
            <>
              <span>
                {t('Status')}: {item.status}
              </span>
              <span>
                {t('Tips')}: ${item.tip_quota_usd?.toFixed(2) ?? '0.00'}
              </span>
              {item.status === 'approved' &&
              (item.tip_quota_usd ?? 0) >=
                (configQuery.data?.minimum_withdrawal_usd ?? 10) ? (
                <Button
                  variant='ghost'
                  size='sm'
                  className='h-auto px-1 py-0 text-xs'
                  onClick={() => {
                    setWithdrawTarget(item)
                    setWithdrawGroup(
                      Object.keys(groupsQuery.data?.data ?? {})[0] ?? ''
                    )
                  }}
                >
                  {t('Withdraw tips')}
                </Button>
              ) : null}
            </>
          ) : null}
        </div>
        <div className='flex flex-wrap gap-1.5'>
          {modelList(item.models).map((model) => (
            <span
              key={model}
              className='bg-muted text-muted-foreground max-w-48 truncate rounded-md px-2 py-1 text-xs'
            >
              {model}
            </span>
          ))}
          {!item.models ? (
            <span className='text-muted-foreground text-xs'>
              {t('No models listed')}
            </span>
          ) : null}
        </div>
      </div>
      {!mine ? (
        <div className='flex flex-wrap items-start gap-1'>
          <Button
            variant='ghost'
            size='sm'
            className='self-start'
            onClick={() => setReviewTarget(item)}
          >
            <MessageSquare className='size-4' />
            {t('Review')}
          </Button>
          <Button
            variant='ghost'
            size='sm'
            className='self-start'
            onClick={() => setTipTarget(item)}
          >
            {t('Tip contributor')}
          </Button>
          <Button
            variant='ghost'
            size='sm'
            className='self-start'
            onClick={() => setReportTarget(item)}
          >
            <Flag className='size-4' />
            {t('Report')}
          </Button>
        </div>
      ) : null}
    </article>
  )

  const allItems = allQuery.data?.items ?? []
  const mineItems = mineQuery.data?.items ?? []
  const routingItems = routingQuery.data?.items ?? []
  const pendingItems = adminQuery.data?.items ?? []
  const openReports = reportsQuery.data?.items ?? []
  const sortedAllItems = [...allItems].sort((left, right) => {
    if (sortMode === 'recent') return right.updated_at - left.updated_at
    if (sortMode === 'models') {
      return modelList(right.models).length - modelList(left.models).length
    }
    return (right.rating_average ?? 0) - (left.rating_average ?? 0)
  })
  const routingByChannel = new Map(
    routingItems.map((item) => [item.channel_id, item])
  )
  const orderedRoutingItems = [
    ...routingOrder
      .map((id) => routingByChannel.get(id))
      .filter((item): item is (typeof routingItems)[number] => item != null),
    ...routingItems.filter((item) => !routingOrder.includes(item.channel_id)),
  ]

  const moveRoutingItem = (channelId: number, delta: number) => {
    const next = [...routingOrder]
    const index = next.indexOf(channelId)
    const target = index + delta
    if (index < 0 || target < 0 || target >= next.length) return
    ;[next[index], next[target]] = [next[target], next[index]]
    setRoutingOrder(next)
  }

  const shareReady = Boolean(
    draft.description.trim() && Object.values(shareChecklist).every(Boolean)
  )
  const shareChecklistItems: Array<[keyof typeof emptyShareChecklist, string]> =
    [
      [
        'credentialsAuthorized',
        t(
          'I confirm that these credentials are authorized for this shared channel.'
        ),
      ],
      ['permission', t('I have permission to share this endpoint.')],
      [
        'modelsVerified',
        t('I have verified that the listed models are available.'),
      ],
    ]

  return (
    <>
      <SectionPageLayout>
        <SectionPageLayout.Title>
          {t('Channel marketplace')}
        </SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <Button type='button' onClick={() => setSubmitOpen(true)}>
            <Plus className='size-4' />
            {t('Share a channel')}
          </Button>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <div className='mx-auto w-full max-w-5xl'>
            <div className='text-muted-foreground mb-8 flex flex-wrap items-center gap-x-5 gap-y-2 text-sm'>
              <span>
                {t('Public group')}:{' '}
                <strong className='text-foreground'>
                  {configQuery.data?.group ?? 'FREE'}
                </strong>
              </span>
              <span>
                {t('Every submission is reviewed before it is listed.')}
              </span>
              <span>
                {t('The contributor account email is shown publicly.')}
              </span>
            </div>
            {isRoot ? (
              <div className='border-border/70 mb-8 grid gap-3 border-y py-4 sm:grid-cols-[1fr_auto] sm:items-end'>
                <div className='grid gap-1.5'>
                  <Label>{t('Public channel group')}</Label>
                  <Input
                    value={publicGroup}
                    onChange={(event) => setPublicGroup(event.target.value)}
                    placeholder='FREE'
                  />
                  <p className='text-muted-foreground text-xs'>
                    {t(
                      'All approved shared channels use this administrator-configured group.'
                    )}
                  </p>
                </div>
                <Button
                  disabled={!publicGroup.trim() || saveGroupMutation.isPending}
                  onClick={() => saveGroupMutation.mutate(publicGroup.trim())}
                >
                  {t('Save settings')}
                </Button>
              </div>
            ) : null}
            <Tabs value={activeTab} onValueChange={setActiveTab}>
              <TabsList>
                <TabsTrigger value='routing'>
                  {t('Channel routing')}
                </TabsTrigger>
                <TabsTrigger value='all'>{t('All channels')}</TabsTrigger>
                <TabsTrigger value='mine'>{t('My channels')}</TabsTrigger>
                {isAdmin ? (
                  <TabsTrigger value='review'>{t('Review')}</TabsTrigger>
                ) : null}
              </TabsList>
              <TabsContent value='routing' className='mt-3'>
                <div className='text-muted-foreground mb-4 flex flex-wrap items-center justify-between gap-3 text-sm'>
                  <span>
                    {t(
                      'Configure your own public pool. It affects only your requests; administrator routing priority remains unchanged.'
                    )}
                  </span>
                  <Button
                    size='sm'
                    disabled={routingMutation.isPending}
                    onClick={() => routingMutation.mutate()}
                  >
                    {t('Save routing')}
                  </Button>
                </div>
                {routingQuery.isError ? (
                  <PublicRelayLoadError
                    onRetry={() => routingQuery.refetch()}
                  />
                ) : orderedRoutingItems.length ? (
                  orderedRoutingItems.map((item, index) => (
                    <div
                      key={item.channel_id}
                      className='flex flex-wrap items-center gap-3 border-t py-4'
                    >
                      <button
                        type='button'
                        className='text-muted-foreground hover:text-foreground'
                        onClick={() =>
                          setRoutingDisabled((current) =>
                            current.includes(item.channel_id)
                              ? current.filter((id) => id !== item.channel_id)
                              : [...current, item.channel_id]
                          )
                        }
                      >
                        {item.disabled ? t('Disabled') : t('Enabled')}
                      </button>
                      <div className='min-w-0 flex-1'>
                        <div className='truncate font-medium'>{item.name}</div>
                        <div className='text-muted-foreground truncate text-xs'>
                          {modelList(item.models).join(' · ')}
                        </div>
                      </div>
                      <div className='flex items-center gap-1'>
                        <Button
                          variant='ghost'
                          size='icon-sm'
                          disabled={index === 0}
                          onClick={() => moveRoutingItem(item.channel_id, -1)}
                          aria-label={t('Move up')}
                        >
                          <ArrowUp className='size-4' />
                        </Button>
                        <Button
                          variant='ghost'
                          size='icon-sm'
                          disabled={index === orderedRoutingItems.length - 1}
                          onClick={() => moveRoutingItem(item.channel_id, 1)}
                          aria-label={t('Move down')}
                        >
                          <ArrowDown className='size-4' />
                        </Button>
                      </div>
                    </div>
                  ))
                ) : (
                  <p className='text-muted-foreground py-12 text-center'>
                    {t('No linked public channels yet.')}
                  </p>
                )}
              </TabsContent>
              <TabsContent value='all' className='mt-3'>
                <div className='mb-2 flex flex-wrap gap-2'>
                  {(['rating', 'recent', 'models'] as const).map((mode) => (
                    <Button
                      key={mode}
                      size='sm'
                      variant={sortMode === mode ? 'secondary' : 'ghost'}
                      onClick={() => setSortMode(mode)}
                    >
                      {t(sortLabels[mode])}
                    </Button>
                  ))}
                </div>
                {allQuery.isError ? (
                  <PublicRelayLoadError onRetry={() => allQuery.refetch()} />
                ) : sortedAllItems.length ? (
                  sortedAllItems.map((item) => renderRelay(item))
                ) : (
                  <p className='text-muted-foreground py-12 text-center'>
                    {t('No approved channels yet.')}
                  </p>
                )}
              </TabsContent>
              <TabsContent value='mine' className='mt-3'>
                {mineQuery.isError ? (
                  <PublicRelayLoadError onRetry={() => mineQuery.refetch()} />
                ) : mineItems.length ? (
                  mineItems.map((item) => renderRelay(item, true))
                ) : (
                  <p className='text-muted-foreground py-12 text-center'>
                    {t('You have not uploaded a channel yet.')}
                  </p>
                )}
              </TabsContent>
              {isAdmin ? (
                <TabsContent value='review' className='mt-3'>
                  <div className='mb-8 flex items-center gap-2 text-sm'>
                    <ShieldCheck className='size-4' />
                    {t('{{count}} submissions waiting for review', {
                      count: pendingItems.length,
                    })}
                  </div>
                  {pendingItems.map((item) => (
                    <div
                      key={item.id}
                      className='border-border/70 border-t py-5'
                    >
                      {renderRelay(item, true)}
                      <div className='grid gap-2 sm:grid-cols-[1fr_auto_auto]'>
                        <Input
                          value={reviewNote[item.id] ?? ''}
                          onChange={(event) =>
                            setReviewNote((current) => ({
                              ...current,
                              [item.id]: event.target.value,
                            }))
                          }
                          placeholder={t(
                            'Review note (required when rejecting)'
                          )}
                        />
                        <Button
                          variant='outline'
                          onClick={() =>
                            reviewMutation.mutate({
                              id: item.id,
                              approve: false,
                            })
                          }
                        >
                          {t('Reject')}
                        </Button>
                        <Button
                          onClick={() =>
                            reviewMutation.mutate({
                              id: item.id,
                              approve: true,
                            })
                          }
                        >
                          {t('Approve')}
                        </Button>
                      </div>
                    </div>
                  ))}
                  {openReports.length ? (
                    <>
                      <Separator className='my-6' />
                      <h3 className='font-medium'>{t('Open reports')}</h3>
                      {openReports.map((report) => (
                        <div
                          key={report.id}
                          className='flex items-center justify-between gap-4 border-b py-4 text-sm'
                        >
                          <span>{report.reason}</span>
                          <Button
                            size='sm'
                            variant='outline'
                            onClick={() =>
                              reportReviewMutation.mutate({
                                id: report.id,
                                close: true,
                              })
                            }
                          >
                            {t('Close report')}
                          </Button>
                        </div>
                      ))}
                    </>
                  ) : null}
                </TabsContent>
              ) : null}
            </Tabs>
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <ChannelsProvider>
        <ChannelMutateDrawer
          open={submitOpen}
          onOpenChange={handleSubmitOpenChange}
          submission={{
            title: t('Share a channel'),
            description: t(
              'Complete the full channel configuration below. The submission remains pending until an administrator approves it.'
            ),
            submitLabel: t('Submit for review'),
            isPending: submitMutation.isPending,
            fixedGroup: configQuery.data?.group ?? '',
            disabled: !shareReady || !configQuery.data?.group,
            onSubmit: async (values) => {
              const publicGroup = configQuery.data?.group ?? 'FREE'
              const channelConfig = transformFormDataToCreatePayload({
                ...values,
                group: [publicGroup],
              })
              await submitMutation.mutateAsync({
                name: values.name,
                base_url: values.base_url ?? '',
                models: values.models ?? '',
                description: draft.description,
                channel_config: channelConfig,
              })
            },
            supplement: (
              <section className='border-border/70 bg-card scroll-mt-4 rounded-xl border p-4 sm:p-5'>
                <div className='flex items-start gap-3'>
                  <div className='bg-primary/10 text-primary flex size-9 shrink-0 items-center justify-center rounded-lg'>
                    <ShieldCheck className='size-4' aria-hidden='true' />
                  </div>
                  <div className='min-w-0 flex-1'>
                    <h3 className='font-medium'>{t('Sharing information')}</h3>
                    <p className='text-muted-foreground mt-1 text-sm'>
                      {t(
                        'Add the public description and confirm that this channel can be shared.'
                      )}
                    </p>
                  </div>
                  <span className='border-border bg-muted/30 rounded-md border px-2 py-1 text-xs font-medium'>
                    {configQuery.data?.group ?? 'FREE'}
                  </span>
                </div>

                <div className='mt-5 grid gap-5'>
                  <div className='grid gap-2'>
                    <Label htmlFor='share-description'>
                      {t('Description')} *
                    </Label>
                    <Textarea
                      id='share-description'
                      value={draft.description}
                      onChange={(event) =>
                        setDraft({ ...draft, description: event.target.value })
                      }
                      placeholder={t(
                        'Describe the endpoint, limits, and best use cases for other users.'
                      )}
                      className='min-h-24'
                    />
                    <p className='text-muted-foreground text-xs'>
                      {t(
                        'This information is shown in the public channel market after approval.'
                      )}
                    </p>
                  </div>

                  <div className='border-border/70 bg-muted/20 grid gap-3 rounded-lg border p-4'>
                    {shareChecklistItems.map(([key, label]) => {
                      const id = `share-check-${key}`
                      return (
                        <div key={key} className='flex items-start gap-3'>
                          <Checkbox
                            id={id}
                            checked={shareChecklist[key]}
                            onCheckedChange={(checked) =>
                              setShareChecklist((current) => ({
                                ...current,
                                [key]: checked === true,
                              }))
                            }
                          />
                          <Label
                            htmlFor={id}
                            className='cursor-pointer text-sm leading-5 font-normal'
                          >
                            {label}
                          </Label>
                        </div>
                      )
                    })}
                  </div>
                </div>
              </section>
            ),
          }}
        />
      </ChannelsProvider>

      <Dialog
        open={!!reportTarget}
        onOpenChange={(open) => !open && setReportTarget(null)}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('Report channel')}</DialogTitle>
            <DialogDescription>
              {t(
                'Tell administrators what should be checked. The report will not immediately disable the channel.'
              )}
            </DialogDescription>
          </DialogHeader>
          <Textarea
            value={reportReason}
            onChange={(event) => setReportReason(event.target.value)}
            placeholder={t('Reason for reporting')}
          />
          <DialogFooter>
            <DialogClose render={<Button variant='ghost' />}>
              {t('Cancel')}
            </DialogClose>
            <Button
              disabled={
                reportMutation.isPending || reportReason.trim().length < 2
              }
              onClick={() =>
                reportTarget &&
                reportMutation.mutate({
                  id: reportTarget.id,
                  reason: reportReason,
                })
              }
            >
              {t('Send report')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog
        open={!!reviewTarget}
        onOpenChange={(open) => !open && setReviewTarget(null)}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('Review channel')}</DialogTitle>
            <DialogDescription>{reviewTarget?.name}</DialogDescription>
          </DialogHeader>
          <div className='grid gap-4'>
            <div className='flex items-center gap-1' aria-label={t('Rating')}>
              {[1, 2, 3, 4, 5].map((value) => (
                <button
                  key={value}
                  type='button'
                  className={
                    value <= reviewRating
                      ? 'text-yellow-500'
                      : 'text-muted-foreground'
                  }
                  onClick={() => setReviewRating(value)}
                  aria-label={`${value}/5`}
                >
                  <Star className='size-5 fill-current' />
                </button>
              ))}
            </div>
            <Textarea
              value={reviewComment}
              onChange={(event) => setReviewComment(event.target.value)}
              placeholder={t('Write a comment (optional)')}
            />
            <div className='space-y-3'>
              <h4 className='text-sm font-medium'>{t('Recent comments')}</h4>
              {reviewsQuery.data?.items?.length ? (
                reviewsQuery.data.items.map((review) => (
                  <div
                    key={review.id}
                    className='border-border/70 border-t py-2 text-sm'
                  >
                    <div className='flex items-center gap-2'>
                      <span className='text-yellow-500'>
                        {'★'.repeat(review.rating)}
                      </span>
                      <span className='text-muted-foreground text-xs'>
                        {formatDate(review.updated_at)}
                      </span>
                    </div>
                    <p className='text-muted-foreground'>
                      {review.comment || '—'}
                    </p>
                  </div>
                ))
              ) : (
                <p className='text-muted-foreground text-sm'>
                  {t('No reviews yet.')}
                </p>
              )}
            </div>
          </div>
          <DialogFooter>
            <DialogClose render={<Button variant='ghost' />}>
              {t('Cancel')}
            </DialogClose>
            <Button
              disabled={ratingMutation.isPending}
              onClick={() =>
                reviewTarget &&
                ratingMutation.mutate({
                  id: reviewTarget.id,
                  rating: reviewRating,
                  comment: reviewComment,
                })
              }
            >
              {t('Submit review')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog
        open={!!tipTarget}
        onOpenChange={(open) => !open && setTipTarget(null)}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('Tip contributor')}</DialogTitle>
            <DialogDescription>
              {t(
                'Use your balance to thank this contributor. Tips are transferred immediately and cannot be reversed.'
              )}
            </DialogDescription>
          </DialogHeader>
          <div className='grid gap-4'>
            <div className='grid gap-2'>
              <Label>{t('Tip amount')}</Label>
              <div className='flex flex-wrap gap-2'>
                {['0.5', '1', '3', '5'].map((amount) => (
                  <Button
                    key={amount}
                    type='button'
                    size='sm'
                    variant={tipAmount === amount ? 'secondary' : 'outline'}
                    onClick={() => setTipAmount(amount)}
                  >
                    ${amount}
                  </Button>
                ))}
                <Input
                  inputMode='decimal'
                  value={tipAmount}
                  onChange={(event) => setTipAmount(event.target.value)}
                  aria-label={t('Custom tip amount')}
                  className='w-28'
                  placeholder='0.00'
                />
              </div>
            </div>
            <div className='grid gap-2'>
              <Label>{t('Message (optional)')}</Label>
              <Textarea
                value={tipMessage}
                onChange={(event) => setTipMessage(event.target.value)}
                placeholder={t('Leave a short thank-you message')}
                maxLength={500}
              />
            </div>
          </div>
          <DialogFooter>
            <DialogClose render={<Button variant='ghost' />}>
              {t('Cancel')}
            </DialogClose>
            <Button
              disabled={
                tipMutation.isPending ||
                !tipTarget ||
                !Number.isFinite(Number(tipAmount)) ||
                Number(tipAmount) <= 0
              }
              onClick={() => {
                if (!tipTarget) return
                tipMutation.mutate({
                  id: tipTarget.id,
                  amount: Number(tipAmount),
                  message: tipMessage,
                })
              }}
            >
              {t('Send tip')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog
        open={!!withdrawTarget}
        onOpenChange={(open) => !open && setWithdrawTarget(null)}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('Withdraw tips')}</DialogTitle>
            <DialogDescription>
              {t(
                'Move available tips into your balance. Choose the group you want to use for future requests.'
              )}
            </DialogDescription>
          </DialogHeader>
          <div className='grid gap-2'>
            <Label>{t('Target group')}</Label>
            <select
              className='border-input bg-background h-9 rounded-md border px-3 text-sm'
              value={withdrawGroup}
              onChange={(event) => setWithdrawGroup(event.target.value)}
            >
              <option value=''>{t('Select a group')}</option>
              {Object.keys(groupsQuery.data?.data ?? {}).map((group) => (
                <option key={group} value={group}>
                  {group}
                </option>
              ))}
            </select>
          </div>
          <DialogFooter>
            <DialogClose render={<Button variant='ghost' />}>
              {t('Cancel')}
            </DialogClose>
            <Button
              disabled={
                withdrawMutation.isPending || !withdrawTarget || !withdrawGroup
              }
              onClick={() => {
                if (!withdrawTarget || !withdrawGroup) return
                withdrawMutation.mutate({
                  id: withdrawTarget.id,
                  group: withdrawGroup,
                })
              }}
            >
              {t('Withdraw')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
