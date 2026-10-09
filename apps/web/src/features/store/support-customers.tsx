/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { useAuthStore } from '@/stores/auth-store'

import { StoreAmount, StoreError, StoreLoading } from './shared'
import { supportApi, type StoreCustomer } from './support-api'
import { storeDate } from './utils'

export function StoreSupportCustomers({ userId, onConversation }: { userId: number; onConversation: (id: string) => void }) {
  const { t } = useTranslation()
  const [input, setInput] = useState('')
  const [search, setSearch] = useState('')
  const [page, setPage] = useState(1)
  const query = useQuery({
    queryKey: ['store-support', userId, 'customers', search, page],
    queryFn: ({ signal }) => supportApi.customers(search, page, signal),
    gcTime: 0,
    retry: false,
  })
  return (
    <section className='space-y-5' aria-label={t('Customer management')}>
      <p className='text-muted-foreground max-w-3xl text-sm'>{t('Only your buyers and inquiries appear here. Paid totals are historical gross amounts before refunds, not current revenue.')}</p>
      <form className='flex max-w-xl gap-2' onSubmit={(event) => { event.preventDefault(); setSearch(input.trim()); setPage(1) }}>
        <Input aria-label={t('Search customers')} placeholder={t('Search customers')} maxLength={100} value={input} onChange={(event) => setInput(event.target.value)} className='min-h-11 rounded-full' />
        <Button type='submit' variant='secondary' className='min-h-11 rounded-full'>{t('Search')}</Button>
      </form>
      <StoreError error={query.error} retry={() => void query.refetch()} />
      {query.isPending ? <StoreLoading /> : query.data?.items.length === 0 ? (
        <p className='text-muted-foreground py-12 text-center text-sm'>{t('No customers yet. Customers appear after an order or a product inquiry.')}</p>
      ) : (
        <div className='grid items-start gap-5 lg:grid-cols-2'>
          {query.data?.items.map((customer) => <CustomerCard key={`${userId}-${customer.buyer_id}-${customer.revision}`} userId={userId} customer={customer} onConversation={onConversation} onRefresh={() => void query.refetch()} />)}
        </div>
      )}
      <div className='flex justify-end gap-2'>
        <Button variant='ghost' className='min-h-11' disabled={page === 1 || query.isFetching} onClick={() => setPage((value) => value - 1)}>{t('Previous page')}</Button>
        <Button variant='ghost' className='min-h-11' disabled={!query.data?.has_more || query.isFetching} onClick={() => setPage((value) => value + 1)}>{t('Next page')}</Button>
      </div>
    </section>
  )
}

function CustomerCard({ userId, customer, onConversation, onRefresh }: { userId: number; customer: StoreCustomer; onConversation: (id: string) => void; onRefresh: () => void }) {
  const { t, i18n } = useTranslation()
  const client = useQueryClient()
  const [note, setNote] = useState(customer.note)
  const [tags, setTags] = useState(customer.tags.join(', '))
  const [busy, setBusy] = useState(false)
  const pending = useRef(false)
  const [error, setError] = useState<unknown>(null)
  const [saved, setSaved] = useState(false)
  async function action(operation: () => Promise<void>) {
    if (pending.current || useAuthStore.getState().auth.user?.id !== userId) return
    pending.current = true
    setBusy(true)
    setError(null)
    try { await operation() }
    catch (issue) { setError(issue) }
    finally { pending.current = false; setBusy(false) }
  }
  return (
    <article className='bg-muted/30 min-w-0 space-y-5 rounded-3xl p-5 sm:p-6'>
      <header className='flex min-w-0 flex-wrap items-start justify-between gap-3'>
        <div className='min-w-0'>
          <h2 className='truncate font-semibold'>{customer.display_name}</h2>
          <p className='text-muted-foreground text-xs'>ID {customer.buyer_id}</p>
        </div>
        <Button className='min-h-11 rounded-full' variant='secondary' disabled={busy || (!customer.conversation_id && !customer.last_order_id)} onClick={() => void action(async () => {
          const id = customer.conversation_id || (await supportApi.open({ order_id: customer.last_order_id })).id
          if (useAuthStore.getState().auth.user?.id === userId) onConversation(id)
        })}>{t('Message buyer')}</Button>
      </header>
      <dl className='grid grid-cols-2 gap-4'>
        <div><dt className='text-muted-foreground text-xs'>{t('Orders')}</dt><dd className='mt-1 text-xl font-medium tabular-nums'>{customer.order_count}</dd></div>
        <div><dt className='text-muted-foreground text-xs'>{t('Historical paid total')}</dt><dd className='mt-1 text-xl font-medium'><StoreAmount quota={customer.paid_quota} /></dd></div>
      </dl>
      {customer.last_order_id && <a className='text-muted-foreground inline-flex min-h-11 flex-wrap items-center gap-2 text-xs underline underline-offset-4' href={`/store/orders?order=${encodeURIComponent(customer.last_order_id)}`}>{t('Latest order')} · {storeDate(customer.last_order_at, i18n.language)}</a>}
      {customer.tags.length > 0 && <div className='flex flex-wrap gap-2'>{customer.tags.map((tag) => <span className='bg-background rounded-full px-3 py-1 text-xs [overflow-wrap:anywhere]' key={tag}>{tag}</span>)}</div>}
      <details className='space-y-3'>
        <summary className='cursor-pointer py-3 text-sm font-medium'>{t('Private notes and tags')}</summary>
        <p className='text-muted-foreground text-xs'>{t('Only you can read these notes. They are not shared with the buyer or the assistant.')}</p>
        <form className='space-y-3' onSubmit={(event) => { event.preventDefault(); void action(async () => {
          await supportApi.saveCustomer(customer.buyer_id, { note, tags: tags.split(/[,，]/).map((tag) => tag.trim()).filter(Boolean), revision: customer.revision })
          setSaved(true)
          await client.invalidateQueries({ queryKey: ['store-support', userId, 'customers'] })
        }) }}>
          <label className='block space-y-2 text-sm'><span>{t('Private customer note')}</span><Textarea value={note} maxLength={4000} rows={4} disabled={busy} onChange={(event) => { setNote(event.target.value); setSaved(false) }} /></label>
          <label className='block space-y-2 text-sm'><span>{t('Tags, separated by commas')}</span><Input value={tags} maxLength={330} disabled={busy} onChange={(event) => { setTags(event.target.value); setSaved(false) }} /></label>
          <p className='text-muted-foreground text-xs'>{t('Up to ten tags, with 32 characters per tag.')}</p>
          <Button className='min-h-11 rounded-full' type='submit' disabled={busy}>{t('Save customer details')}</Button>
          {saved && <p role='status' className='text-sm'>{t('Saved')}</p>}
        </form>
      </details>
      <StoreError error={error} retry={onRefresh} />
    </article>
  )
}
