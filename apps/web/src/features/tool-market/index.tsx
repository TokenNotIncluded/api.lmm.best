/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  CheckCircle2,
  ChevronRight,
  CircleDashed,
  CircleX,
  Clock,
  PackageSearch,
  PackageX,
  PauseCircle,
  Store,
  XCircle,
} from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout/components/section-page-layout'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from '@/components/ui/empty'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Separator } from '@/components/ui/separator'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'
import { useAuthStore } from '@/stores/auth-store'

import {
  marketAPI,
  type CallResponse,
  type Grant,
  type Installation,
  type MarketConfig,
  type MarketService,
  type MarketSummary,
  type MarketTool,
} from './api'
import { MarketConnections } from './connections'
import { marketStatus, marketPermissionList } from './copy'
import { creditAmount } from './money'
import { ServiceEditor } from './service-editor'
import { CallDialog, CallResult, GrantDialog } from './tool-actions'

/** A small icon paired with the status text, so state reads at a glance. */
function MarketStatusIcon({ value }: { value: string }) {
  switch (value) {
    case 'published':
    case 'settled':
    case 'succeeded':
    case 'released':
      return (
        <CheckCircle2
          aria-hidden='true'
          className='text-success size-3.5 shrink-0'
        />
      )
    case 'pending':
    case 'reserved':
    case 'held':
    case 'running':
      return (
        <Clock aria-hidden='true' className='text-warning size-3.5 shrink-0' />
      )
    case 'paused':
    case 'suspended':
      return (
        <PauseCircle
          aria-hidden='true'
          className='text-muted-foreground size-3.5 shrink-0'
        />
      )
    case 'rejected':
    case 'failed':
      return (
        <XCircle
          aria-hidden='true'
          className='text-destructive size-3.5 shrink-0'
        />
      )
    case 'cancelled':
      return (
        <CircleX
          aria-hidden='true'
          className='text-muted-foreground size-3.5 shrink-0'
        />
      )
    default:
      return (
        <CircleDashed
          aria-hidden='true'
          className='text-muted-foreground size-3.5 shrink-0'
        />
      )
  }
}

export function ToolMarket() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const cache = useQueryClient()
  const key = ['tool-market', user?.id]
  const [tab, setTab] = useState('market')
  const [searchInput, setSearchInput] = useState('')
  const [search, setSearch] = useState('')
  const [offset, setOffset] = useState(0)
  const [selected, setSelected] = useState<{
    id: string
    mode: 'published' | 'draft' | 'review'
  } | null>(null)
  const [editor, setEditor] = useState(false)
  const [client, setClient] = useState('web-market')
  const [grantTool, setGrantTool] = useState<MarketTool | null>(null)
  const [callTool, setCallTool] = useState<{
    tool: MarketTool
    grant: Grant
  } | null>(null)
  const [reviewNote, setReviewNote] = useState('')
  const [record, setRecord] = useState<CallResponse | null>(null)
  const config = useQuery({
    queryKey: [...key, 'config'],
    queryFn: marketAPI.config,
  })
  const catalog = useQuery({
    queryKey: [...key, 'catalog', search, offset],
    queryFn: () => marketAPI.list(search, offset),
    enabled: tab === 'market',
  })
  const mine = useQuery({
    queryKey: [...key, 'services'],
    queryFn: () => marketAPI.mine<MarketService>('services'),
    enabled: tab === 'mine',
  })
  const favorites = useQuery({
    queryKey: [...key, 'favorites'],
    queryFn: () => marketAPI.mine<MarketSummary>('favorites'),
    enabled: tab === 'market',
  })
  const installs = useQuery({
    queryKey: [...key, 'installations'],
    queryFn: () => marketAPI.mine<Installation>('installations'),
  })
  const grants = useQuery({
    queryKey: [...key, 'grants'],
    queryFn: () => marketAPI.mine<Grant>('grants'),
  })
  const detail = useQuery({
    queryKey: [...key, 'detail', selected?.id, selected?.mode],
    queryFn: () => {
      if (!selected) throw new Error('Missing service')
      return marketAPI.detail(selected.id, selected.mode)
    },
    enabled: !!selected && !editor,
  })
  const calls = useQuery({
    queryKey: [...key, 'calls'],
    queryFn: marketAPI.calls,
    enabled: tab === 'records',
  })
  const income = useQuery({
    queryKey: [...key, 'income'],
    queryFn: marketAPI.income,
    enabled: tab === 'records',
  })
  const reviews = useQuery({
    queryKey: [...key, 'reviews'],
    queryFn: marketAPI.reviews,
    enabled: tab === 'review' && (user?.role ?? 0) >= 10,
  })
  const action = useMutation({
    retry: false,
    mutationFn: (operation: () => Promise<unknown>) => operation(),
    onSuccess: () => {
      void cache.invalidateQueries({ queryKey: ['tool-market'] })
    },
  })
  const units = config.data?.quota_per_unit ?? 500000
  const chooseTab = (value: string) => {
    setTab(value)
    setSelected(null)
    setEditor(false)
    action.reset()
  }
  const current = detail.data
  const accessReady = installs.isSuccess && grants.isSuccess
  const openPublisher = () => {
    setSelected(null)
    setEditor(true)
  }
  const browse = (items: MarketSummary[]) => (
    <div className='divide-border divide-y'>
      {items.map((item) => (
        <button
          type='button'
          key={item.id}
          className='hover:bg-muted/40 focus-visible:ring-ring group flex w-full flex-col items-start justify-between gap-3 rounded-sm px-2 py-5 text-left transition-colors outline-none focus-visible:ring-2 motion-safe:active:scale-[0.995] sm:flex-row sm:gap-4'
          onClick={() => setSelected({ id: item.id, mode: 'published' })}
        >
          <span className='min-w-0'>
            <strong className='block break-words group-hover:underline'>
              {item.name}
            </strong>
            <span className='text-muted-foreground mt-1 line-clamp-2 block max-w-3xl text-sm'>
              {item.description}
            </span>
            <span className='text-muted-foreground mt-2 block text-xs'>
              {t('Provider account {{id}}', { id: item.owner_id })}
            </span>
          </span>
          <span className='flex shrink-0 items-center gap-2'>
            <Badge variant='outline'>
              {item.execution_type === 'remote'
                ? 'Remote MCP'
                : 'Serverless MCP'}
            </Badge>
            <ChevronRight
              aria-hidden='true'
              className='text-muted-foreground size-4 transition-transform group-hover:translate-x-0.5 motion-reduce:transition-none'
            />
          </span>
        </button>
      ))}
    </div>
  )
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Tool market')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button variant='outline' onClick={() => chooseTab('connections')}>
          {t('Connect MCP')}
        </Button>
        <Button
          variant={selected || editor ? 'outline' : 'default'}
          onClick={openPublisher}
        >
          {t('Publish a tool')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='space-y-6'>
          <p className='text-muted-foreground max-w-3xl text-sm'>
            {t(
              'Discover MCP tools, choose what each client can use, and pay only for successful calls.'
            )}
          </p>
          {config.isError && (
            <Alert variant='destructive'>
              <AlertTitle>{t('Could not load market settings')}</AlertTitle>
              <AlertDescription>
                <Button variant='outline' onClick={() => void config.refetch()}>
                  {t('Retry')}
                </Button>
              </AlertDescription>
            </Alert>
          )}
          {config.data && !config.data.enabled && (
            <Alert>
              <AlertTitle>{t('New tool calls are paused')}</AlertTitle>
              <AlertDescription>
                {t(
                  'You can still browse tools and manage drafts, connections and existing records.'
                )}
              </AlertDescription>
            </Alert>
          )}
          {editor ? (
            <ServiceEditor
              key={selected?.id ?? 'new'}
              initial={selected ? current : undefined}
              units={units}
              onCancel={() => setEditor(false)}
              onSaved={(id) => {
                setEditor(false)
                setSelected({ id, mode: 'draft' })
              }}
            />
          ) : (
            <Tabs
              value={tab}
              onValueChange={(value) => chooseTab(String(value))}
            >
              <div className='max-w-full overflow-x-auto'>
                <TabsList variant='line'>
                  <TabsTrigger value='market'>{t('Discover')}</TabsTrigger>
                  <TabsTrigger value='mine'>{t('My publications')}</TabsTrigger>
                  <TabsTrigger value='connections'>
                    {t('Connections and limits')}
                  </TabsTrigger>
                  <TabsTrigger value='records'>{t('Call records')}</TabsTrigger>
                  {(user?.role ?? 0) >= 10 && (
                    <TabsTrigger value='review'>
                      {t('Review queue')}
                    </TabsTrigger>
                  )}
                </TabsList>
              </div>
              {selected ? (
                <section className='space-y-5 pt-4'>
                  <Button variant='ghost' onClick={() => setSelected(null)}>
                    {t('Back to list')}
                  </Button>
                  {detail.isPending && <p role='status'>{t('Loading…')}</p>}
                  {detail.isError && (
                    <p role='alert' className='text-destructive'>
                      {t(
                        'This service is unavailable or you do not have access.'
                      )}{' '}
                      <Button
                        variant='outline'
                        onClick={() => void detail.refetch()}
                      >
                        {t('Retry')}
                      </Button>
                    </p>
                  )}
                  {current && (
                    <>
                      <div className='flex flex-wrap items-start justify-between gap-4'>
                        <div className='min-w-0 space-y-2'>
                          <h3 className='text-xl font-semibold break-words'>
                            {current.version.name}
                          </h3>
                          <p className='text-muted-foreground max-w-3xl text-sm whitespace-pre-wrap'>
                            {current.version.description}
                          </p>
                          <p className='text-muted-foreground text-xs'>
                            {t('Provider account {{id}}', {
                              id: current.service.owner_id,
                            })}
                          </p>
                        </div>
                        <Badge variant='secondary'>
                          <MarketStatusIcon value={current.version.status} />
                          {marketStatus(current.version.status, t)}
                        </Badge>
                      </div>
                      <dl className='grid gap-3 text-sm sm:grid-cols-2'>
                        <div>
                          <dt className='text-muted-foreground'>
                            {t('Data recipient')}
                          </dt>
                          <dd className='break-all'>
                            {current.version.endpoint}
                          </dd>
                        </div>
                        <div>
                          <dt className='text-muted-foreground'>
                            {t('Version')}
                          </dt>
                          <dd className='font-mono text-xs break-all'>
                            {current.version.id}
                          </dd>
                        </div>
                      </dl>
                      {selected.mode === 'published' ? (
                        <>
                          <Field className='max-w-md'>
                            <FieldLabel htmlFor='market-active-client'>
                              {t('Client ID')}
                            </FieldLabel>
                            <Input
                              id='market-active-client'
                              value={client}
                              maxLength={128}
                              onChange={(e) => setClient(e.target.value)}
                            />
                            <p className='text-muted-foreground text-xs'>
                              {t(
                                'Use web-market for this browser, or the client ID from your MCP connection.'
                              )}
                            </p>
                          </Field>
                          <Button
                            variant='outline'
                            disabled={action.isPending}
                            onClick={() =>
                              action.mutate(() =>
                                marketAPI.favorite(
                                  current.service.id,
                                  !favorites.data?.some(
                                    (item) => item.id === current.service.id
                                  )
                                )
                              )
                            }
                          >
                            {favorites.data?.some(
                              (item) => item.id === current.service.id
                            )
                              ? t('Remove favorite')
                              : t('Favorite')}
                          </Button>
                          {current.service.owner_id === user?.id && (
                            <Button
                              variant='outline'
                              onClick={() => setEditor(true)}
                            >
                              {t('Edit draft')}
                            </Button>
                          )}
                        </>
                      ) : (
                        <div className='space-y-3'>
                          <p className='text-sm'>
                            {current.validated
                              ? t(
                                  'Connection and definitions checked. This is not a guarantee of tool safety.'
                                )
                              : t(
                                  'Validate the connection and definitions before review.'
                                )}
                          </p>
                          {current.version.review_note && (
                            <p className='text-sm'>
                              {current.version.review_note}
                            </p>
                          )}
                          {selected.mode === 'draft' ? (
                            <div className='flex flex-wrap gap-2'>
                              <Button
                                variant='outline'
                                disabled={
                                  action.isPending ||
                                  current.version.status === 'pending'
                                }
                                onClick={() => setEditor(true)}
                              >
                                {t('Edit draft')}
                              </Button>
                              <Button
                                variant='outline'
                                disabled={action.isPending}
                                onClick={() =>
                                  action.mutate(() =>
                                    marketAPI.validate(current.service.id)
                                  )
                                }
                              >
                                {t('Validate connection')}
                              </Button>
                              <Button
                                disabled={
                                  action.isPending ||
                                  !current.validated ||
                                  current.version.status === 'pending'
                                }
                                onClick={() =>
                                  action.mutate(() =>
                                    marketAPI.submit(
                                      current.service.id,
                                      current.version.id
                                    )
                                  )
                                }
                              >
                                {t('Submit for review')}
                              </Button>
                            </div>
                          ) : (
                            <FieldGroup>
                              <Field>
                                <FieldLabel htmlFor='market-review-note'>
                                  {t('Review reason')}
                                </FieldLabel>
                                <Textarea
                                  id='market-review-note'
                                  value={reviewNote}
                                  maxLength={1000}
                                  onChange={(e) =>
                                    setReviewNote(e.target.value)
                                  }
                                />
                              </Field>
                              <div className='flex gap-2'>
                                <Button
                                  disabled={
                                    action.isPending || !reviewNote.trim()
                                  }
                                  onClick={() =>
                                    action.mutate(async () => {
                                      await marketAPI.review(
                                        current.service.id,
                                        current.version.id,
                                        true,
                                        reviewNote
                                      )
                                      setSelected(null)
                                    })
                                  }
                                >
                                  {t('Approve and publish')}
                                </Button>
                                <Button
                                  variant='outline'
                                  disabled={
                                    action.isPending || !reviewNote.trim()
                                  }
                                  onClick={() =>
                                    action.mutate(async () => {
                                      await marketAPI.review(
                                        current.service.id,
                                        current.version.id,
                                        false,
                                        reviewNote
                                      )
                                      setSelected(null)
                                    })
                                  }
                                >
                                  {t('Reject')}
                                </Button>
                              </div>
                            </FieldGroup>
                          )}
                        </div>
                      )}
                      <Separator />
                      {selected.mode === 'published' &&
                        (installs.isPending || grants.isPending) && (
                          <p
                            role='status'
                            className='text-muted-foreground text-sm'
                          >
                            {t('Loading…')}
                          </p>
                        )}
                      {selected.mode === 'published' &&
                        (installs.isError || grants.isError) && (
                          <div role='alert' className='space-y-2 text-sm'>
                            <p className='text-destructive'>
                              {t(
                                'Could not load tool access. Retry before changing permissions.'
                              )}
                            </p>
                            <Button
                              variant='outline'
                              onClick={() =>
                                void Promise.allSettled([
                                  installs.refetch(),
                                  grants.refetch(),
                                ])
                              }
                            >
                              {t('Retry')}
                            </Button>
                          </div>
                        )}
                      {current.tools.map((tool) => {
                        const loaded = installs.data?.some(
                          (item) =>
                            item.client_id === client &&
                            item.tool_id === tool.tool_id &&
                            item.version_id === tool.version_id
                        )
                        const grant = grants.data?.find(
                          (item) =>
                            item.client_id === client &&
                            item.tool_id === tool.tool_id &&
                            item.version_id === tool.version_id &&
                            !item.revoked_at &&
                            item.expires_at > Date.now() / 1000
                        )
                        return (
                          <article
                            key={tool.tool_id}
                            className='space-y-3 border-b pb-5'
                            data-tool-version={tool.version_id}
                          >
                            <div className='flex flex-wrap justify-between gap-3'>
                              <h4 className='font-semibold break-all'>
                                {tool.name}
                              </h4>
                              <p className='text-sm tabular-nums'>
                                {tool.price_quota === 0
                                  ? t('Free')
                                  : t(
                                      '{{amount}} credits per successful call',
                                      {
                                        amount: creditAmount(
                                          tool.price_quota,
                                          units
                                        ),
                                      }
                                    )}
                              </p>
                            </div>
                            <p className='text-muted-foreground text-sm whitespace-pre-wrap'>
                              {tool.description}
                            </p>
                            <p className='text-muted-foreground text-xs break-words'>
                              {t('Declared permissions')}:{' '}
                              {marketPermissionList(tool.permissions, t)}
                            </p>
                            <details className='text-sm'>
                              <summary className='cursor-pointer'>
                                {t('Parameter schema')}
                              </summary>
                              <pre className='bg-muted mt-2 max-h-56 overflow-auto p-3 text-xs'>
                                {JSON.stringify(
                                  JSON.parse(tool.input_schema),
                                  null,
                                  2
                                )}
                              </pre>
                            </details>
                            {selected.mode === 'published' && (
                              <div className='flex flex-wrap gap-2'>
                                <Button
                                  variant='outline'
                                  disabled={
                                    !accessReady ||
                                    action.isPending ||
                                    !client.trim()
                                  }
                                  onClick={() =>
                                    action.mutate(() =>
                                      marketAPI.install(
                                        {
                                          client_id: client,
                                          tool_id: tool.tool_id,
                                          version_id: tool.version_id,
                                        },
                                        !loaded
                                      )
                                    )
                                  }
                                >
                                  {loaded ? t('Unload') : t('Load')}
                                </Button>
                                <Button
                                  variant='outline'
                                  disabled={!accessReady || !client.trim()}
                                  onClick={() => setGrantTool(tool)}
                                >
                                  {grant
                                    ? t('New authorization')
                                    : t('Authorize')}
                                </Button>
                                {client === 'web-market' && (
                                  <Button
                                    disabled={
                                      !accessReady ||
                                      !loaded ||
                                      !grant ||
                                      !config.data?.enabled
                                    }
                                    onClick={() =>
                                      grant && setCallTool({ tool, grant })
                                    }
                                  >
                                    {t('Run tool')}
                                  </Button>
                                )}
                              </div>
                            )}
                          </article>
                        )
                      })}
                    </>
                  )}
                </section>
              ) : (
                <>
                  <TabsContent value='market' className='space-y-4 pt-4'>
                    <form
                      className='flex gap-2'
                      onSubmit={(e) => {
                        e.preventDefault()
                        setSearch(searchInput)
                        setOffset(0)
                      }}
                    >
                      <Input
                        aria-label={t('Search tools')}
                        placeholder={t('Search tools')}
                        value={searchInput}
                        maxLength={120}
                        onChange={(e) => setSearchInput(e.target.value)}
                      />
                      <Button variant='outline' type='submit'>
                        {t('Search')}
                      </Button>
                    </form>
                    {catalog.isPending && <p role='status'>{t('Loading…')}</p>}
                    {catalog.isError && (
                      <p role='alert'>
                        {t('Could not load tools.')}{' '}
                        <Button
                          variant='outline'
                          onClick={() => void catalog.refetch()}
                        >
                          {t('Retry')}
                        </Button>
                      </p>
                    )}
                    {catalog.data?.length === 0 && (
                      <Empty className='px-3 py-10'>
                        <EmptyHeader>
                          <EmptyMedia variant='icon'>
                            {search ? (
                              <PackageSearch aria-hidden='true' />
                            ) : (
                              <PackageX aria-hidden='true' />
                            )}
                          </EmptyMedia>
                          <EmptyTitle>
                            {t('No published tools found')}
                          </EmptyTitle>
                          <EmptyDescription>
                            {t(
                              'Try another search, or publish a Remote MCP service for review.'
                            )}
                          </EmptyDescription>
                        </EmptyHeader>
                        <EmptyContent>
                          <Button
                            variant='outline'
                            onClick={
                              search
                                ? () => {
                                    setSearch('')
                                    setSearchInput('')
                                    setOffset(0)
                                  }
                                : openPublisher
                            }
                          >
                            {search ? t('Clear filters') : t('Publish a tool')}
                          </Button>
                        </EmptyContent>
                      </Empty>
                    )}
                    {catalog.data && browse(catalog.data)}
                    <div className='flex justify-end gap-2'>
                      <Button
                        variant='outline'
                        disabled={offset === 0}
                        onClick={() => setOffset(Math.max(0, offset - 30))}
                      >
                        {t('Previous')}
                      </Button>
                      <Button
                        variant='outline'
                        disabled={catalog.data?.length !== 30}
                        onClick={() => setOffset(offset + 30)}
                      >
                        {t('Next')}
                      </Button>
                    </div>
                    {!!favorites.data?.length && (
                      <>
                        <h3 className='font-semibold'>{t('Favorites')}</h3>
                        {browse(favorites.data)}
                      </>
                    )}
                  </TabsContent>
                  <TabsContent value='mine' className='space-y-4 pt-4'>
                    {mine.isPending && <p>{t('Loading…')}</p>}
                    {mine.isError && (
                      <div
                        role='alert'
                        className='flex flex-wrap items-center gap-2 text-sm'
                      >
                        <XCircle
                          aria-hidden='true'
                          className='text-destructive size-4'
                        />
                        <p>{t('Could not load tools.')}</p>
                        <Button
                          variant='outline'
                          onClick={() => void mine.refetch()}
                        >
                          {t('Retry')}
                        </Button>
                      </div>
                    )}
                    {mine.data?.length === 0 && (
                      <Empty className='px-3 py-10'>
                        <EmptyHeader>
                          <EmptyMedia variant='icon'>
                            <Store aria-hidden='true' />
                          </EmptyMedia>
                          <EmptyTitle>
                            {t('You have not published any services yet.')}
                          </EmptyTitle>
                        </EmptyHeader>
                        <EmptyContent>
                          <Button variant='outline' onClick={openPublisher}>
                            {t('Publish a tool')}
                          </Button>
                        </EmptyContent>
                      </Empty>
                    )}
                    {mine.data?.map((item) => (
                      <div
                        key={item.id}
                        className='flex flex-wrap items-center justify-between gap-3 border-b py-4'
                      >
                        <div className='min-w-0'>
                          <p className='font-medium break-all'>
                            {item.name || item.id}
                          </p>
                          <p className='text-muted-foreground flex items-center gap-1.5 text-sm'>
                            <MarketStatusIcon value={item.status} />
                            {marketStatus(item.status, t)}
                          </p>
                        </div>
                        <div className='flex gap-2'>
                          {item.draft_version_id && (
                            <Button
                              variant='outline'
                              onClick={() =>
                                setSelected({ id: item.id, mode: 'draft' })
                              }
                            >
                              {t('Open draft')}
                            </Button>
                          )}
                          {item.live_version_id && (
                            <>
                              <Button
                                variant='outline'
                                onClick={() =>
                                  setSelected({
                                    id: item.id,
                                    mode: 'published',
                                  })
                                }
                              >
                                {t('View')}
                              </Button>
                              <Button
                                variant='outline'
                                disabled={action.isPending}
                                onClick={() =>
                                  action.mutate(() =>
                                    marketAPI.pause(
                                      item.id,
                                      item.status === 'published'
                                    )
                                  )
                                }
                              >
                                {item.status === 'published'
                                  ? t('Pause')
                                  : t('Resume')}
                              </Button>
                            </>
                          )}
                        </div>
                      </div>
                    ))}
                  </TabsContent>
                  <TabsContent value='connections' className='pt-4'>
                    {config.data && <MarketConnections config={config.data} />}
                  </TabsContent>
                  <TabsContent value='records' className='space-y-6 pt-4'>
                    {(calls.isPending || income.isPending) && (
                      <p role='status' className='text-muted-foreground'>
                        {t('Loading…')}
                      </p>
                    )}
                    {(calls.isError || income.isError) && (
                      <div role='alert' className='space-y-2'>
                        <p>{t('Could not load records.')}</p>
                        <Button
                          variant='outline'
                          onClick={() =>
                            void Promise.allSettled([
                              calls.refetch(),
                              income.refetch(),
                            ])
                          }
                        >
                          {t('Retry')}
                        </Button>
                      </div>
                    )}
                    <h3 className='font-semibold'>{t('Recent calls')}</h3>
                    {calls.isSuccess && calls.data.length === 0 && (
                      <p className='text-muted-foreground'>
                        {t('No calls yet')}
                      </p>
                    )}
                    {calls.data?.map((item) => (
                      <div
                        key={item.id}
                        className='flex flex-wrap items-center justify-between gap-3 border-b py-3 text-sm'
                      >
                        <div className='min-w-0'>
                          <p>
                            {new Date(item.created_at * 1000).toLocaleString()}{' '}
                            · {item.client_id}
                          </p>
                          <p className='text-muted-foreground break-all'>
                            {item.id}
                          </p>
                          <p className='flex flex-wrap items-center gap-1 tabular-nums'>
                            <MarketStatusIcon value={item.execution_status} />
                            {marketStatus(item.execution_status, t)} /{' '}
                            {marketStatus(item.settlement_status, t)} ·{' '}
                            {item.settlement_status === 'held'
                              ? t('Reserved')
                              : t('Amount')}
                            :{' '}
                            {creditAmount(
                              item.settlement_status === 'released'
                                ? 0
                                : item.price_quota,
                              units
                            )}
                          </p>
                        </div>
                        <Button
                          variant='outline'
                          onClick={() =>
                            action.mutate(async () => {
                              setRecord(await marketAPI.result(item.id))
                            })
                          }
                        >
                          {t('View result')}
                        </Button>
                      </div>
                    ))}
                    {record && <CallResult response={record} units={units} />}
                    <h3 className='font-semibold'>{t('Income transfers')}</h3>
                    {income.data?.length === 0 && (
                      <p className='text-muted-foreground'>
                        {t('No income transfers yet')}
                      </p>
                    )}
                    {income.data?.map((item) => (
                      <div
                        key={item.id}
                        className='flex flex-wrap justify-between gap-2 border-b py-3 text-sm'
                      >
                        <span className='text-muted-foreground break-all'>
                          {item.call_id}
                        </span>
                        <span className='tabular-nums'>
                          +
                          {t('{{amount}} credits', {
                            amount: creditAmount(item.quota, units),
                          })}
                        </span>
                      </div>
                    ))}
                  </TabsContent>
                  {(user?.role ?? 0) >= 10 && (
                    <TabsContent value='review' className='space-y-6 pt-4'>
                      {reviews.isError && (
                        <div
                          role='alert'
                          className='flex flex-wrap items-center gap-2 text-sm'
                        >
                          <XCircle
                            aria-hidden='true'
                            className='text-destructive size-4'
                          />
                          <p>{t('Could not load tools.')}</p>
                          <Button
                            variant='outline'
                            onClick={() => void reviews.refetch()}
                          >
                            {t('Retry')}
                          </Button>
                        </div>
                      )}
                      {reviews.data?.length === 0 && (
                        <p className='text-muted-foreground'>
                          {t('No pending reviews')}
                        </p>
                      )}
                      {reviews.data?.map((item) => (
                        <div
                          key={item.id}
                          className='flex flex-wrap items-center justify-between gap-3 border-b py-4'
                        >
                          <span className='text-sm break-all'>
                            {item.id} ·{' '}
                            {t('Provider account {{id}}', {
                              id: item.owner_id,
                            })}
                          </span>
                          <Button
                            variant='outline'
                            onClick={() =>
                              setSelected({ id: item.id, mode: 'review' })
                            }
                          >
                            {t('Review')}
                          </Button>
                        </div>
                      ))}
                      {(user?.role ?? 0) >= 100 && config.data && (
                        <MarketSettings
                          key={`${config.data.enabled}:${config.data.fee_bps}:${config.data.recipient_id}`}
                          config={config.data}
                        />
                      )}
                    </TabsContent>
                  )}
                </>
              )}
            </Tabs>
          )}
          {action.isPending && (
            <p role='status' className='text-muted-foreground text-sm'>
              {t('Processing…')}
            </p>
          )}
          {action.isError && (
            <p role='alert' className='text-destructive text-sm'>
              {t(
                'The operation failed. Check the current status, permissions and configuration, then retry.'
              )}
            </p>
          )}
          {grantTool && current && (
            <GrantDialog
              tool={grantTool}
              endpoint={current.version.endpoint}
              clientID={client}
              units={units}
              onClose={() => setGrantTool(null)}
            />
          )}
          {callTool && current && (
            <CallDialog
              tool={callTool.tool}
              grant={callTool.grant}
              endpoint={current.version.endpoint}
              units={units}
              onClose={() => setCallTool(null)}
            />
          )}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

function MarketSettings({ config }: { config: MarketConfig }) {
  const { t } = useTranslation()
  const cache = useQueryClient()
  const [enabled, setEnabled] = useState(config.enabled)
  const [fee, setFee] = useState(String(config.fee_bps / 100))
  const [recipient, setRecipient] = useState(String(config.recipient_id || ''))
  const save = useMutation({
    retry: false,
    mutationFn: () => {
      const bps = Math.round(Number(fee) * 100),
        id = Number(recipient)
      if (
        !Number.isSafeInteger(bps) ||
        bps < 0 ||
        bps > 10000 ||
        !Number.isSafeInteger(id) ||
        id <= 0
      ) {
        throw new Error('Invalid settings')
      }
      return marketAPI.configure({ enabled, fee_bps: bps, recipient_id: id })
    },
    onSuccess: () => {
      void cache.invalidateQueries({ queryKey: ['tool-market'] })
    },
  })
  return (
    <section className='max-w-xl space-y-4 border-t pt-6'>
      <h3 className='font-semibold'>{t('Market settings')}</h3>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Successful calls transfer the fee to this super administrator account and the remainder directly to the author.'
        )}
      </p>
      <form
        onSubmit={(e) => {
          e.preventDefault()
          save.mutate()
        }}
      >
        <FieldGroup>
          <Field>
            <FieldLabel htmlFor='market-enabled'>{t('New calls')}</FieldLabel>
            <select
              id='market-enabled'
              className='border-input bg-background h-9 rounded-md border px-3 text-sm'
              value={enabled ? 'enabled' : 'paused'}
              onChange={(e) => setEnabled(e.target.value === 'enabled')}
            >
              <option value='paused'>{t('Paused')}</option>
              <option value='enabled'>{t('Enabled')}</option>
            </select>
          </Field>
          <Field>
            <FieldLabel htmlFor='market-fee'>
              {t('Platform fee (%)')}
            </FieldLabel>
            <Input
              id='market-fee'
              type='number'
              min={0}
              max={100}
              step={0.01}
              required
              value={fee}
              onChange={(e) => setFee(e.target.value)}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor='market-recipient'>
              {t('Super administrator account ID')}
            </FieldLabel>
            <Input
              id='market-recipient'
              type='number'
              min={1}
              required
              value={recipient}
              onChange={(e) => setRecipient(e.target.value)}
            />
          </Field>
          {save.isError && (
            <p role='alert' className='text-destructive text-sm'>
              {t(
                'Settings could not be saved. Check the fee and recipient account.'
              )}
            </p>
          )}
          <Button disabled={save.isPending}>
            {t('Confirm market settings')}
          </Button>
        </FieldGroup>
      </form>
    </section>
  )
}
