/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { MessageCircle, Users } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import { StoreAuthGate, StoreError, StoreLoading } from './shared'
import { supportApi, type SupportRole } from './support-api'
import { StoreSupportChat } from './support-chat'
import { StoreSupportCustomers } from './support-customers'
import type { SupportDraft, SupportSearch } from './support-helpers'
import { storeDate } from './utils'

export function StoreSupportPage({ search }: { search: SupportSearch }) {
  return (
    <StoreAuthGate>
      <StoreSupportWorkspace search={search} />
    </StoreAuthGate>
  )
}

function StoreSupportWorkspace({ search }: { search: SupportSearch }) {
  const { t, i18n } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)!
  const client = useQueryClient()
  const [tab, setTab] = useState(search.tab)
  const [role, setRole] = useState<SupportRole>(search.role)
  const [status, setStatus] = useState('')
  const [unread, setUnread] = useState(false)
  const [page, setPage] = useState(1)
  const [selected, setSelected] = useState(search.conversation || '')
  const [drafts, setDrafts] = useState<Record<string, SupportDraft>>({})
  const [opening, setOpening] = useState(false)
  const [openError, setOpenError] = useState<unknown>(null)
  const [openAttempt, setOpenAttempt] = useState(0)
  const list = useQuery({
    queryKey: ['store-support', user.id, 'threads', role, status, unread, page],
    queryFn: ({ signal }) =>
      supportApi.conversations(role, status, unread, page, signal),
    enabled: tab === 'messages',
    refetchInterval: 12000,
    refetchIntervalInBackground: false,
    gcTime: 0,
    retry: false,
  })
  useEffect(() => {
    setTab(search.tab)
    setRole(search.role)
    setSelected(search.conversation || '')
    setPage(1)
  }, [search.tab, search.role, search.conversation])
  useEffect(() => {
    if (!search.product && !search.order) return
    const abort = new AbortController()
    setOpening(true)
    setOpenError(null)
    void supportApi
      .open(
        { product_id: search.product, order_id: search.order },
        abort.signal
      )
      .then((conversation) => {
        if (
          abort.signal.aborted ||
          useAuthStore.getState().auth.user?.id !== user.id
        )
          return
        setTab('messages')
        setRole(conversation.seller_id === user.id ? 'seller' : 'buyer')
        setSelected(conversation.id)
        void client.invalidateQueries({
          queryKey: ['store-support', user.id, 'threads'],
        })
      })
      .catch((error: unknown) => {
        if (!abort.signal.aborted) setOpenError(error)
      })
      .finally(() => {
        if (!abort.signal.aborted) setOpening(false)
      })
    return () => abort.abort()
  }, [search.product, search.order, user.id, client, openAttempt])

  function selectConversation(id: string, nextRole = role) {
    setSelected(id)
    setRole(nextRole)
    setTab('messages')
    setPage(1)
  }
  return (
    <div className='min-w-0 space-y-5' data-testid='store-support-workspace'>
      <header className='space-y-3'>
        <h1 className='console-page-title text-2xl font-semibold tracking-tight'>
          {t('Store messages')}
        </h1>
        <p className='text-muted-foreground max-w-2xl text-sm'>
          {t(
            'Talk about products and orders. Keep private delivery codes out of messages.'
          )}
        </p>
        <div className='flex flex-wrap gap-2'>
          <Button
            variant={tab === 'messages' ? 'secondary' : 'ghost'}
            className='min-h-11 rounded-full'
            aria-pressed={tab === 'messages'}
            onClick={() => setTab('messages')}
          >
            <MessageCircle className='size-4' aria-hidden='true' />
            {t('Messages')}
          </Button>
          <Button
            variant={tab === 'customers' ? 'secondary' : 'ghost'}
            className='min-h-11 rounded-full'
            aria-pressed={tab === 'customers'}
            onClick={() => setTab('customers')}
          >
            <Users className='size-4' aria-hidden='true' />
            {t('Customer management')}
          </Button>
        </div>
      </header>
      <StoreError
        error={openError}
        retry={() => setOpenAttempt((value) => value + 1)}
      />
      {opening && <StoreLoading />}
      {tab === 'customers' ? (
        <StoreSupportCustomers
          userId={user.id}
          onConversation={(id) => selectConversation(id, 'seller')}
        />
      ) : (
        <div className='grid min-w-0 gap-5 md:grid-cols-[17rem_minmax(0,1fr)]'>
          <aside
            aria-label={t('Conversations')}
            className={cn('min-w-0 space-y-4', selected && 'hidden md:block')}
          >
            <div className='bg-muted/40 flex gap-1 rounded-full p-1'>
              {(['buyer', 'seller'] as const).map((value) => (
                <Button
                  key={value}
                  className='min-h-11 flex-1 rounded-full'
                  variant={role === value ? 'secondary' : 'ghost'}
                  aria-pressed={role === value}
                  disabled={opening}
                  onClick={() => {
                    setRole(value)
                    setPage(1)
                    setSelected('')
                  }}
                >
                  {t(value === 'buyer' ? 'My purchases' : 'My sales')}
                </Button>
              ))}
            </div>
            <div className='flex flex-wrap gap-1'>
              {[
                ['', 'All'],
                ['open', 'Open conversations'],
                ['resolved', 'Resolved conversations'],
              ].map(([value, label]) => (
                <Button
                  key={value}
                  size='sm'
                  className='min-h-11 rounded-full'
                  variant={status === value ? 'secondary' : 'ghost'}
                  aria-pressed={status === value}
                  onClick={() => {
                    setStatus(value)
                    setPage(1)
                  }}
                >
                  {t(label)}
                </Button>
              ))}
              <label className='text-muted-foreground flex min-h-11 items-center gap-2 px-2 text-sm'>
                <input
                  type='checkbox'
                  checked={unread}
                  onChange={(event) => {
                    setUnread(event.target.checked)
                    setPage(1)
                  }}
                />
                {t('Unread only')}
              </label>
            </div>
            <StoreError error={list.error} retry={() => void list.refetch()} />
            {list.isPending ? (
              <StoreLoading />
            ) : list.data?.items.length === 0 ? (
              <p className='text-muted-foreground px-3 py-10 text-sm'>
                {t(
                  'No conversations yet. Use Message seller on a product or order.'
                )}
              </p>
            ) : (
              <div className='space-y-1'>
                {list.data?.items.map((conversation) => (
                  <button
                    type='button'
                    key={conversation.id}
                    onClick={() => selectConversation(conversation.id)}
                    aria-pressed={selected === conversation.id}
                    className={cn(
                      'hover:bg-muted/60 focus-visible:outline-ring flex w-full min-w-0 gap-3 rounded-2xl px-4 py-4 text-start transition-colors focus-visible:outline-2 motion-reduce:transition-none',
                      selected === conversation.id && 'bg-muted'
                    )}
                  >
                    <span className='min-w-0 flex-1 space-y-1'>
                      <span className='block truncate text-sm font-medium'>
                        {role === 'buyer'
                          ? conversation.seller_name
                          : conversation.buyer_name}
                      </span>
                      <span className='text-muted-foreground block truncate text-sm'>
                        {conversation.subject}
                      </span>
                      <span className='text-muted-foreground block text-xs'>
                        {storeDate(conversation.updated_at, i18n.language)}
                      </span>
                    </span>
                    {conversation.unread_count > 0 && (
                      <span
                        aria-label={`${t('Unread messages')}: ${conversation.unread_count}`}
                        className='bg-primary text-primary-foreground flex size-6 shrink-0 items-center justify-center rounded-full text-xs tabular-nums'
                      >
                        {conversation.unread_count > 99
                          ? '99+'
                          : conversation.unread_count}
                      </span>
                    )}
                  </button>
                ))}
              </div>
            )}
            <div className='flex justify-between gap-2'>
              <Button
                variant='ghost'
                className='min-h-11'
                disabled={page === 1 || list.isFetching}
                onClick={() => setPage((value) => value - 1)}
              >
                {t('Previous page')}
              </Button>
              <Button
                variant='ghost'
                className='min-h-11'
                disabled={!list.data?.has_more || list.isFetching}
                onClick={() => setPage((value) => value + 1)}
              >
                {t('Next page')}
              </Button>
            </div>
          </aside>
          {selected ? (
            <StoreSupportChat
              key={`${user.id}-${selected}`}
              userId={user.id}
              id={selected}
              draft={drafts[selected] || { text: '' }}
              onDraft={(id, update) =>
                setDrafts((current) => ({
                  ...current,
                  [id]:
                    typeof update === 'function'
                      ? update(current[id] || { text: '' })
                      : update,
                }))
              }
              onBack={() => setSelected('')}
            />
          ) : (
            <section className='bg-muted/20 text-muted-foreground hidden min-h-96 items-center justify-center rounded-3xl p-10 text-center text-sm md:flex'>
              {t('Select a conversation to read and reply.')}
            </section>
          )}
        </div>
      )}
    </div>
  )
}
