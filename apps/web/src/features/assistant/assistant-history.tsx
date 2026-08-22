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
  Archive01Icon,
  ArchiveRestoreIcon,
  ShieldKeyIcon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { FolderOpen, Search } from 'lucide-react'
// `Search` stays imported: the admin audit picker below still uses it.
import { Fragment, useMemo, useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Response } from '@/components/ai-elements/response'
import { LmmBrandMark } from '@/components/lmm-brand-mark'
import {
  Alert,
  AlertAction,
  AlertDescription,
  AlertTitle,
} from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { searchUsers } from '@/features/users/api'
import { canViewUserAssistantHistory } from '@/features/users/lib/assistant-history-access'
import { toIntlLocale } from '@/i18n/languages'
import { ROLE } from '@/lib/roles'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import {
  archiveAssistantConversation,
  getAssistantConversationHistory,
  getAssistantConversationHistoryDetail,
  unarchiveAssistantConversation,
  type AssistantConversationHistoryDetail,
  type AssistantConversationHistoryItem,
  type AssistantConversationHistoryMessage,
} from './api'
import { redactAssistantMessageForDisplay } from './assistant-message-safety'

const historyTouchTargetClassName = 'min-h-11 sm:min-h-7'
const historyInputClassName = 'h-11 sm:h-8'

function assistantHistoryErrorStatus(error: unknown): number | null {
  const status = (error as { response?: { status?: unknown } } | null)?.response
    ?.status
  return typeof status === 'number' ? status : null
}

function HistoryMessage(props: {
  message: AssistantConversationHistoryMessage
  dateFormatter: Intl.DateTimeFormat
}) {
  const { t } = useTranslation()
  const safeMessage = redactAssistantMessageForDisplay(
    props.message.content,
    t(
      'Sensitive details are hidden until confirmation and remain visible only to you.'
    )
  )
  const isAssistant = props.message.role === 'assistant'
  const isCard = props.message.role === 'secure_card'
  return (
    <div
      className={cn(
        'flex gap-3 py-5',
        !isAssistant && !isCard && 'justify-end'
      )}
      data-testid='assistant-history-message'
    >
      {isAssistant ? (
        <div className='mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-md bg-[#19c37d] text-[#202123]'>
          <LmmBrandMark className='size-5' />
        </div>
      ) : null}
      <div
        className={cn(
          'min-w-0 max-w-[min(92%,52rem)]',
          !isAssistant && !isCard && 'rounded-2xl bg-muted px-4 py-3',
          isCard && 'w-full border-l-2 pl-4'
        )}
      >
        <div className='mb-1 flex items-center gap-2'>
          <p className='text-muted-foreground text-[11px] font-medium'>
            {isAssistant ? t('Service guide') : t('You')}
          </p>
          {props.message.created_at ? (
            <time
              className='text-muted-foreground text-[11px]'
              dateTime={new Date(props.message.created_at * 1000).toISOString()}
            >
              {props.dateFormatter.format(props.message.created_at * 1000)}
            </time>
          ) : null}
        </div>
        {isAssistant && safeMessage.content ? (
          <Response
            className='max-w-full text-sm leading-6 break-words [&_pre]:max-w-full [&_pre]:overflow-x-auto'
            final
          >
            {safeMessage.content}
          </Response>
        ) : !isCard && safeMessage.content ? (
          <p className='text-sm leading-6 break-words whitespace-pre-wrap'>
            {safeMessage.content}
          </p>
        ) : null}
        {props.message.cards?.length || safeMessage.redacted ? (
          <div className='text-success mt-2 flex items-center gap-1.5 text-xs leading-5'>
            <HugeiconsIcon
              icon={ShieldKeyIcon}
              className='size-3.5 shrink-0'
              strokeWidth={2}
              aria-hidden='true'
            />
            {props.message.cards
              ?.map((card) => card.label)
              .filter(Boolean)
              .join('、') ||
              t(
                'Sensitive details are hidden until confirmation and remain visible only to you.'
              )}
          </div>
        ) : null}
      </div>
    </div>
  )
}

export function AssistantHistory(props: {
  active: boolean
  onOpenConversation: (conversation: AssistantConversationHistoryItem) => void
  ownerUser?: { id: number; username: string }
  presentation?: 'cards' | 'rows'
  limit?: number
  showFullPageLink?: boolean
}) {
  const { t, i18n } = useTranslation()
  const queryClient = useQueryClient()
  const authUser = useAuthStore((state) => state.auth.user)
  const canAudit = authUser?.role !== undefined && authUser.role >= ROLE.ADMIN
  const [scope, setScope] = useState<'self' | 'audit'>('self')
  const [auditUserIdInput, setAuditUserIdInput] = useState('')
  const [auditUserId, setAuditUserId] = useState<number | null>(null)
  const [auditInputError, setAuditInputError] = useState(false)
  const [auditSearch, setAuditSearch] = useState('')
  const [selectedAuditUser, setSelectedAuditUser] = useState<{
    id: number
    username: string
    display_name: string
    email?: string
    role: number
  } | null>(null)
  const [filter, setFilter] = useState<'active' | 'archived'>('active')
  const showingArchived = filter === 'archived'
  const fixedScope = props.ownerUser
    ? props.ownerUser.id === authUser?.id
      ? 'self'
      : 'audit'
    : null
  const effectiveScope = fixedScope ?? (canAudit ? scope : 'self')
  const activeUserId =
    effectiveScope === 'audit'
      ? (props.ownerUser?.id ??
        selectedAuditUser?.id ??
        auditUserId ??
        undefined)
      : undefined
  const historyLimit = props.limit
  const auditUsersQuery = useQuery({
    queryKey: ['assistant-audit-users', auditSearch],
    queryFn: () => searchUsers({ keyword: auditSearch.trim(), page_size: 24 }),
    enabled:
      props.active &&
      canAudit &&
      !props.ownerUser &&
      effectiveScope === 'audit',
    staleTime: 15_000,
    retry: false,
  })
  const historyQuery = useInfiniteQuery({
    queryKey: [
      'assistant-conversations',
      effectiveScope,
      activeUserId ?? null,
      filter,
      ...(historyLimit === undefined ? [] : [historyLimit]),
    ],
    queryFn: ({ pageParam }) =>
      getAssistantConversationHistory(
        showingArchived,
        activeUserId,
        historyLimit,
        pageParam
      ),
    initialPageParam: '',
    getNextPageParam: (lastPage) => lastPage.next_cursor || undefined,
    enabled:
      props.active && (effectiveScope === 'self' || activeUserId !== undefined),
    staleTime: 30_000,
    retry: false,
  })
  const archiveMutation = useMutation({
    mutationFn: ({ id, archived }: { id: number; archived: boolean }) =>
      archived
        ? unarchiveAssistantConversation(id)
        : archiveAssistantConversation(id),
    onSuccess: async (_result, variables) => {
      await queryClient.invalidateQueries({
        queryKey: ['assistant-conversations'],
      })
      toast.success(
        t(
          variables.archived
            ? 'Conversation restored.'
            : 'Conversation archived.'
        )
      )
    },
    onError: (_error, variables) => {
      toast.error(
        t(
          variables.archived
            ? 'Unable to restore conversation. Try again.'
            : 'Unable to archive conversation. Try again.'
        ),
        {
          action: {
            label: t('Retry'),
            onClick: () => archiveMutation.mutate(variables),
          },
        }
      )
    },
  })
  const dateFormatter = useMemo(
    () =>
      new Intl.DateTimeFormat(toIntlLocale(i18n.language), {
        dateStyle: 'medium',
        timeStyle: 'short',
      }),
    [i18n.language]
  )
  const dayFormatter = useMemo(
    () =>
      new Intl.DateTimeFormat(toIntlLocale(i18n.language), {
        dateStyle: 'medium',
      }),
    [i18n.language]
  )
  const conversations = useMemo(
    () => historyQuery.data?.pages.flatMap((page) => page.conversations) ?? [],
    [historyQuery.data?.pages]
  )
  const groupedConversations = useMemo(() => {
    const groups = new Map<string, AssistantConversationHistoryItem[]>()
    for (const conversation of conversations) {
      const day = dayFormatter.format(conversation.updated_at * 1000)
      const group = groups.get(day)
      if (group) group.push(conversation)
      else groups.set(day, [conversation])
    }
    return [...groups.entries()]
  }, [dayFormatter, conversations])
  const status = assistantHistoryErrorStatus(historyQuery.error)

  const selectSelfScope = () => {
    setScope('self')
    setAuditUserId(null)
    setSelectedAuditUser(null)
    setAuditInputError(false)
  }

  const selectAuditScope = () => {
    setScope('audit')
    setAuditInputError(false)
  }

  const submitAuditUserId = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const input = event.currentTarget.elements.namedItem(
      'assistant-history-audit-user-id'
    ) as HTMLInputElement | null
    const value = (input?.value ?? auditUserIdInput).trim()
    if (!/^[1-9]\d*$/.test(value)) {
      setAuditInputError(true)
      return
    }
    const nextUserId = Number(value)
    if (!Number.isSafeInteger(nextUserId) || nextUserId <= 0) {
      setAuditInputError(true)
      return
    }
    setAuditInputError(false)
    setAuditUserId(nextUserId)
    setSelectedAuditUser(null)
    setScope('audit')
  }

  const auditUsers = (auditUsersQuery.data?.data?.items ?? []).filter((user) =>
    canViewUserAssistantHistory(authUser, user)
  )

  const historyErrorDescription =
    status === 400
      ? t('Enter a positive integer')
      : status === 403
        ? t('Conversation history is not available to this account.')
        : status === 404
          ? t('This conversation no longer exists or is unavailable.')
          : t('Unable to load conversation history. Try again.')
  const historyCanRetry = status !== 400 && status !== 403 && status !== 404

  return (
    <div className='grid gap-6 py-2 sm:gap-8'>
      {canAudit && !props.ownerUser ? (
        <div className='grid gap-5 border-b pb-6'>
          <div className='flex flex-wrap gap-3'>
            <Button
              type='button'
              variant={effectiveScope === 'self' ? 'secondary' : 'outline'}
              size='sm'
              className={historyTouchTargetClassName}
              aria-pressed={effectiveScope === 'self'}
              onClick={selectSelfScope}
            >
              {t('My conversations')}
            </Button>
            <Button
              type='button'
              variant={effectiveScope === 'audit' ? 'secondary' : 'outline'}
              size='sm'
              className={historyTouchTargetClassName}
              aria-pressed={effectiveScope === 'audit'}
              onClick={selectAuditScope}
            >
              {t('User audit')}
            </Button>
          </div>
          {effectiveScope === 'audit' ? (
            <div className='grid gap-5'>
              <div className='grid gap-2'>
                <Label htmlFor='assistant-history-audit-search'>
                  {t('User audit')}
                </Label>
                <div className='relative max-w-xl'>
                  <Search
                    className='text-muted-foreground absolute top-1/2 left-3 size-4 -translate-y-1/2'
                    aria-hidden='true'
                  />
                  <Input
                    id='assistant-history-audit-search'
                    className={cn(historyInputClassName, 'pl-9')}
                    value={auditSearch}
                    onChange={(event) => {
                      setAuditSearch(event.target.value)
                      setSelectedAuditUser(null)
                      setAuditUserId(null)
                    }}
                    autoComplete='off'
                    placeholder={t('Search by username, name, or email')}
                  />
                </div>
              </div>
              <div
                className='grid max-h-64 overflow-y-auto border-y'
                data-testid='assistant-audit-user-list'
              >
                {auditUsersQuery.isLoading ? (
                  <p className='text-muted-foreground px-1 py-4 text-sm'>
                    {t('Loading...')}
                  </p>
                ) : auditUsersQuery.isError ? (
                  <p className='text-destructive px-1 py-4 text-sm'>
                    {t('Unable to load data')}
                  </p>
                ) : auditUsers.length === 0 ? (
                  <p className='text-muted-foreground px-1 py-4 text-sm'>
                    {t('No users')}
                  </p>
                ) : (
                  auditUsers.map((user) => (
                    <button
                      key={user.id}
                      type='button'
                      className={cn(
                        'flex min-w-0 items-center gap-3 border-b px-1 py-3 text-left last:border-b-0',
                        'hover:bg-muted/50 focus-visible:bg-muted/50 focus-visible:outline-none',
                        selectedAuditUser?.id === user.id && 'bg-muted/60'
                      )}
                      onClick={() => {
                        setSelectedAuditUser(user)
                        setAuditUserId(null)
                        setAuditInputError(false)
                      }}
                    >
                      <FolderOpen
                        className='text-muted-foreground size-4 shrink-0'
                        aria-hidden='true'
                      />
                      <span className='min-w-0 flex-1'>
                        <span className='block truncate text-sm font-medium'>
                          {user.display_name || user.username}
                        </span>
                        <span className='text-muted-foreground block truncate text-xs'>
                          @{user.username}
                          {user.email ? ` · ${user.email}` : ''}
                        </span>
                      </span>
                      <span className='text-muted-foreground shrink-0 text-xs'>
                        {user.assistant_conversation_count?.toLocaleString() ??
                          0}
                      </span>
                    </button>
                  ))
                )}
              </div>
              <details className='text-sm'>
                <summary className='text-muted-foreground min-h-11 cursor-pointer py-2 sm:min-h-0 sm:py-0'>
                  {t('User ID')}
                </summary>
                <form
                  className='mt-3 grid max-w-lg gap-3'
                  onSubmit={submitAuditUserId}
                >
                  <div className='flex gap-3'>
                    <Input
                      id='assistant-history-audit-user-id'
                      className={historyInputClassName}
                      value={auditUserIdInput}
                      onChange={(event) => {
                        setAuditUserIdInput(event.target.value)
                        setAuditUserId(null)
                        setSelectedAuditUser(null)
                        setAuditInputError(false)
                      }}
                      inputMode='numeric'
                      autoComplete='off'
                      placeholder={t('Enter a positive integer')}
                      aria-invalid={auditInputError}
                    />
                    <Button
                      type='submit'
                      variant='outline'
                      className={cn(historyTouchTargetClassName, 'shrink-0')}
                    >
                      {t('View')}
                    </Button>
                  </div>
                  {auditInputError ? (
                    <p className='text-destructive text-xs' role='alert'>
                      {t('Enter a positive integer')}
                    </p>
                  ) : null}
                </form>
              </details>
            </div>
          ) : null}
        </div>
      ) : null}
      {effectiveScope === 'audit' && activeUserId !== undefined ? (
        <div className='grid gap-1 border-b pb-5'>
          <p className='text-sm font-medium'>{t('User audit')}</p>
          <p className='text-muted-foreground text-xs leading-5'>
            {props.ownerUser?.username
              ? `${props.ownerUser.username} · `
              : selectedAuditUser
                ? `${selectedAuditUser.display_name || selectedAuditUser.username} · `
                : `${t('Lower-access user conversation')} · `}
            {t('User ID')}: {activeUserId}
          </p>
        </div>
      ) : null}
      <div className='flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between'>
        <div className='flex flex-wrap gap-3'>
          <Button
            type='button'
            variant={showingArchived ? 'ghost' : 'secondary'}
            size='sm'
            className={historyTouchTargetClassName}
            aria-pressed={!showingArchived}
            onClick={() => setFilter('active')}
          >
            {t('Active conversations')}
          </Button>
          <Button
            type='button'
            variant={showingArchived ? 'secondary' : 'ghost'}
            size='sm'
            className={historyTouchTargetClassName}
            aria-pressed={showingArchived}
            onClick={() => setFilter('archived')}
          >
            {t('Archived conversations')}
          </Button>
        </div>
        <div className='flex w-full items-center justify-end gap-3 sm:w-auto'>
          {props.showFullPageLink ? (
            <Button
              type='button'
              variant='ghost'
              size='sm'
              className={historyTouchTargetClassName}
              render={<Link to='/chat-management' />}
            >
              <FolderOpen data-icon='inline-start' aria-hidden='true' />
              {t('Conversation records')}
            </Button>
          ) : null}
        </div>
      </div>
      {historyQuery.isLoading ? (
        <div
          className='grid gap-3'
          aria-label={t('Loading conversation history...')}
        >
          <Skeleton className='h-16 w-full' />
          <Skeleton className='h-16 w-full' />
        </div>
      ) : historyQuery.isError ? (
        <Alert variant='destructive'>
          <HugeiconsIcon
            icon={Alert02Icon}
            strokeWidth={2}
            aria-hidden='true'
          />
          <AlertTitle>{t('Conversation history')}</AlertTitle>
          <AlertDescription>{historyErrorDescription}</AlertDescription>
          {historyCanRetry ? (
            <AlertAction className='static col-span-full mt-2 flex justify-end sm:absolute sm:top-2 sm:right-2 sm:col-auto sm:mt-0'>
              <Button
                type='button'
                variant='outline'
                size='sm'
                className={historyTouchTargetClassName}
                data-testid='assistant-history-retry'
                aria-label={t('Retry')}
                onClick={() => void historyQuery.refetch()}
                disabled={historyQuery.isFetching}
              >
                {historyQuery.isFetching ? t('Loading...') : t('Retry')}
              </Button>
            </AlertAction>
          ) : null}
        </Alert>
      ) : effectiveScope === 'audit' && activeUserId === undefined ? (
        <p className='text-muted-foreground py-8 text-center text-sm leading-6'>
          {t('Enter a positive integer')}
        </p>
      ) : conversations.length === 0 ? (
        <p className='text-muted-foreground py-8 text-center text-sm leading-6'>
          {effectiveScope === 'audit'
            ? `${t('No visible conversation history yet.')} · ${t('User ID')}: ${activeUserId}`
            : t(
                showingArchived
                  ? 'No archived conversations yet.'
                  : 'No active conversations yet.'
              )}
        </p>
      ) : (
        <div
          data-presentation={props.presentation ?? 'cards'}
          data-testid='assistant-history-list'
        >
          {groupedConversations.map(([day, dayConversations], groupIndex) => (
            <section
              key={day}
              aria-labelledby={`assistant-history-day-${groupIndex}`}
            >
              <h3
                id={`assistant-history-day-${groupIndex}`}
                className='text-muted-foreground mb-2 text-xs font-medium tracking-wide uppercase'
              >
                {day}
              </h3>
              {dayConversations.map((conversation, index) => {
                const safePreview = redactAssistantMessageForDisplay(
                  conversation.last_message_preview,
                  t(
                    'Sensitive details are hidden until confirmation and remain visible only to you.'
                  )
                ).content
                const canManage =
                  effectiveScope === 'self' && conversation.owner === 'self'
                const actionPending =
                  archiveMutation.isPending &&
                  archiveMutation.variables?.id === conversation.id
                return (
                  <Fragment key={conversation.id}>
                    {props.presentation === 'rows' &&
                    (index > 0 || groupIndex > 0) ? (
                      <Separator />
                    ) : null}
                    <article
                      className={cn(
                        'grid min-w-0 gap-2',
                        props.presentation === 'rows'
                          ? 'py-4'
                          : 'mb-3 rounded-lg border p-3 last:mb-0'
                      )}
                      data-testid='assistant-history-item'
                    >
                      <div className='flex min-w-0 items-start justify-between gap-3'>
                        <button
                          type='button'
                          className='min-w-0 flex-1 cursor-pointer text-left outline-none focus-visible:underline'
                          onClick={() => props.onOpenConversation(conversation)}
                        >
                          <p className='line-clamp-2 text-sm font-medium'>
                            <span className='sr-only'>
                              {conversation.owner === 'self'
                                ? `${t('Your conversation')}: `
                                : `${t('Lower-access user conversation')}: `}
                            </span>
                            {conversation.title}
                          </p>
                          <p className='text-muted-foreground mt-0.5 text-xs'>
                            {dateFormatter.format(
                              new Date(conversation.updated_at * 1000)
                            )}
                          </p>
                          <p className='text-muted-foreground mt-2 line-clamp-2 text-xs leading-5'>
                            {safePreview}
                          </p>
                        </button>
                        <div className='flex shrink-0 flex-wrap justify-end gap-2'>
                          <Button
                            type='button'
                            variant={
                              props.presentation === 'rows'
                                ? 'ghost'
                                : 'outline'
                            }
                            size='sm'
                            className={historyTouchTargetClassName}
                            aria-label={`${t('View')} ${conversation.title}`}
                            onClick={() =>
                              props.onOpenConversation(conversation)
                            }
                          >
                            {t('View')}
                          </Button>
                          {canManage ? (
                            <Button
                              type='button'
                              variant='ghost'
                              size='sm'
                              className={historyTouchTargetClassName}
                              aria-label={t(
                                showingArchived
                                  ? 'Restore conversation'
                                  : 'Archive conversation'
                              )}
                              disabled={actionPending}
                              onClick={(event) => {
                                event.stopPropagation()
                                archiveMutation.mutate({
                                  id: conversation.id,
                                  archived: showingArchived,
                                })
                              }}
                            >
                              <HugeiconsIcon
                                icon={
                                  showingArchived
                                    ? ArchiveRestoreIcon
                                    : Archive01Icon
                                }
                                className='size-4'
                                strokeWidth={2}
                                aria-hidden='true'
                              />
                              {t(showingArchived ? 'Restore' : 'Archive')}
                            </Button>
                          ) : null}
                        </div>
                      </div>
                    </article>
                  </Fragment>
                )
              })}
            </section>
          ))}
          {historyQuery.hasNextPage ? (
            <div className='flex justify-center border-t pt-4'>
              <Button
                type='button'
                variant='ghost'
                size='sm'
                className={historyTouchTargetClassName}
                disabled={historyQuery.isFetchingNextPage}
                onClick={() => void historyQuery.fetchNextPage()}
              >
                {historyQuery.isFetchingNextPage ? t('Loading...') : t('More')}
              </Button>
            </div>
          ) : null}
        </div>
      )}
    </div>
  )
}

export function AssistantHistoryConversation(props: {
  conversation: AssistantConversationHistoryItem
  onContinue?: (detail: AssistantConversationHistoryDetail) => void
}) {
  const { t, i18n } = useTranslation()
  const historyQuery = useQuery({
    queryKey: ['assistant-conversation', props.conversation.id],
    queryFn: () => getAssistantConversationHistoryDetail(props.conversation.id),
    staleTime: 30_000,
    retry: false,
  })
  const status = assistantHistoryErrorStatus(historyQuery.error)
  if (historyQuery.isLoading) {
    return (
      <div
        className='grid gap-3'
        aria-label={t('Loading conversation history...')}
      >
        <Skeleton className='h-8 w-48' />
        <Skeleton className='h-20 w-full' />
        <Skeleton className='h-20 w-full' />
      </div>
    )
  }
  if (historyQuery.isError || !historyQuery.data) {
    const description =
      status === 403
        ? t('Conversation history is not available to this account.')
        : t('This conversation no longer exists or is unavailable.')
    return (
      <Alert variant='destructive'>
        <HugeiconsIcon icon={Alert02Icon} strokeWidth={2} aria-hidden='true' />
        <AlertTitle>{t('Conversation history')}</AlertTitle>
        <AlertDescription>{description}</AlertDescription>
        {status !== 403 && status !== 404 ? (
          <AlertAction className='static col-span-full mt-2 flex justify-end sm:absolute sm:top-2 sm:right-2 sm:col-auto sm:mt-0'>
            <Button
              type='button'
              variant='outline'
              size='sm'
              className={historyTouchTargetClassName}
              data-testid='assistant-history-detail-retry'
              aria-label={t('Retry')}
              onClick={() => void historyQuery.refetch()}
              disabled={historyQuery.isFetching}
            >
              {historyQuery.isFetching ? t('Loading...') : t('Retry')}
            </Button>
          </AlertAction>
        ) : null}
      </Alert>
    )
  }
  const conversation = historyQuery.data.conversation
  const dateFormatter = new Intl.DateTimeFormat(toIntlLocale(i18n.language), {
    dateStyle: 'medium',
    timeStyle: 'short',
  })
  return (
    <div className='grid gap-5'>
      <div className='border-b pb-4'>
        <div className='flex items-start justify-between gap-3'>
          <div className='min-w-0'>
            <p className='text-base font-medium tracking-tight'>
              {conversation.title}
            </p>
            <p className='text-muted-foreground mt-1 text-xs'>
              {dateFormatter.format(conversation.updated_at * 1000)} ·{' '}
              {historyQuery.data.messages.length.toLocaleString()}
            </p>
          </div>
          {props.onContinue &&
          conversation.owner === 'self' &&
          conversation.archived_at === 0 &&
          !conversation.restricted_at ? (
            <Button
              type='button'
              variant='outline'
              size='sm'
              className={cn(historyTouchTargetClassName, 'shrink-0')}
              onClick={() => props.onContinue?.(historyQuery.data)}
            >
              {t('Continue')}
            </Button>
          ) : null}
        </div>
        {conversation.owner !== 'self' ? (
          <p className='text-muted-foreground mt-3 text-xs leading-5'>
            {t(
              'This history is available because the account has a lower access level. Credential details remain visible only to their owner.'
            )}
          </p>
        ) : null}
      </div>
      <div className='divide-y'>
        {historyQuery.data.messages.map((message) => (
          <HistoryMessage
            key={message.id}
            message={message}
            dateFormatter={dateFormatter}
          />
        ))}
      </div>
    </div>
  )
}
