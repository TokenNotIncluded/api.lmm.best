/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { AlertCircle, ArrowUpRight, Rss, Search, Settings2 } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout/components/section-page-layout'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import {
  getRSSFeeds,
  safeRSSFeedUrl,
  type RSSFeedResult,
  type RSSItem,
} from './api'
import { registerRSSTranslations } from './i18n'

registerRSSTranslations()

type ReaderItem = RSSItem & {
  feedID: string
  feedName: string
}

function publishedLabel(value?: string) {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(date)
}

function flattenFeeds(feeds: RSSFeedResult[]): ReaderItem[] {
  return feeds
    .flatMap((feed) =>
      feed.items.map((item) => ({
        ...item,
        feedID: feed.id,
        feedName: feed.name,
      }))
    )
    .sort((left, right) => {
      const leftTime = left.published_at ? Date.parse(left.published_at) : 0
      const rightTime = right.published_at ? Date.parse(right.published_at) : 0
      return rightTime - leftTime
    })
}

export function RSSReader() {
  const { t } = useTranslation()
  const isOwner = useAuthStore(
    (state) => (state.auth.user?.role ?? 0) >= ROLE.SUPER_ADMIN
  )
  const [search, setSearch] = useState('')
  const [feedFilter, setFeedFilter] = useState('all')
  const query = useQuery({
    queryKey: ['rss'],
    queryFn: getRSSFeeds,
    staleTime: 3 * 60_000,
  })
  const feeds = query.data ?? []
  const allItems = useMemo(() => flattenFeeds(feeds), [feeds])
  const unavailable = useMemo(
    () => feeds.filter((feed) => Boolean(feed.error)),
    [feeds]
  )

  useEffect(() => {
    if (
      feedFilter !== 'all' &&
      !feeds.some((feed) => feed.id === feedFilter)
    ) {
      setFeedFilter('all')
    }
  }, [feedFilter, feeds])

  const visibleItems = useMemo(() => {
    const term = search.trim().toLocaleLowerCase()
    return allItems.filter((item) => {
      if (feedFilter !== 'all' && item.feedID !== feedFilter) return false
      if (!term) return true
      return [item.title, item.summary, item.feedName]
        .join(' ')
        .toLocaleLowerCase()
        .includes(term)
    })
  }, [allItems, feedFilter, search])

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>RSS</SectionPageLayout.Title>
      {isOwner && (
        <SectionPageLayout.Actions>
          <Button
            variant='outline'
            size='sm'
            aria-label={t('Manage feeds')}
            title={t('Manage feeds')}
            render={
              <Link
                to='/system-settings/site/$section'
                params={{ section: 'rss' }}
              />
            }
          >
            <Settings2 data-icon='inline-start' />
            <span className='hidden sm:inline'>{t('Manage feeds')}</span>
          </Button>
        </SectionPageLayout.Actions>
      )}
      <SectionPageLayout.Content>
        <div className='mx-auto flex w-full max-w-5xl flex-col gap-5'>
          <div className='flex flex-col gap-4 border-b pb-5 sm:flex-row sm:items-end sm:justify-between'>
            <div>
              <div className='flex items-center gap-2'>
                <Rss className='text-muted-foreground size-5' aria-hidden='true' />
                <p className='text-sm font-medium'>RSS / Atom</p>
              </div>
              <p className='text-muted-foreground mt-1 text-sm'>
                {t('{{feeds}} feeds · {{articles}} articles', {
                  feeds: feeds.length,
                  articles: allItems.length,
                })}
              </p>
            </div>
            <label className='relative w-full sm:max-w-sm'>
              <Search
                className='text-muted-foreground pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2'
                aria-hidden='true'
              />
              <span className='sr-only'>{t('Search articles')}</span>
              <Input
                type='search'
                className='pl-9'
                value={search}
                onChange={(event) => setSearch(event.target.value)}
                placeholder={t('Search articles')}
              />
            </label>
          </div>

          {feeds.length > 0 && (
            <div
              className='flex max-w-full gap-1 overflow-x-auto pb-1'
              role='group'
              aria-label={t('All feeds')}
            >
              <Button
                type='button'
                size='sm'
                variant={feedFilter === 'all' ? 'secondary' : 'ghost'}
                onClick={() => setFeedFilter('all')}
              >
                {t('All feeds')}
              </Button>
              {feeds.map((feed) => (
                <Button
                  key={feed.id}
                  type='button'
                  size='sm'
                  variant={feedFilter === feed.id ? 'secondary' : 'ghost'}
                  onClick={() => setFeedFilter(feed.id)}
                >
                  {feed.name}
                  <span className='text-muted-foreground ml-1 tabular-nums'>
                    {feed.items.length}
                  </span>
                </Button>
              ))}
            </div>
          )}

          {unavailable.length > 0 && (
            <div className='border-border/70 bg-muted/20 flex items-start gap-3 rounded-xl border px-4 py-3 text-sm'>
              <AlertCircle className='text-muted-foreground mt-0.5 size-4 shrink-0' />
              <div className='min-w-0'>
                <p>{t('Some feeds could not be refreshed.')}</p>
                <p className='text-muted-foreground mt-0.5 truncate text-xs'>
                  {unavailable.map((feed) => feed.name).join(' · ')}
                </p>
              </div>
            </div>
          )}

          {query.isPending && (
            <p className='text-muted-foreground py-14 text-center text-sm'>
              {t('Loading articles...')}
            </p>
          )}

          {query.isError && (
            <ErrorState
              title={t('Unable to load RSS feeds')}
              description={t('Try loading the feeds again.')}
              onRetry={() => void query.refetch()}
            />
          )}

          {query.isSuccess && feeds.length === 0 && (
            <div className='rounded-xl border border-dashed px-6 py-14 text-center'>
              <Rss className='text-muted-foreground mx-auto size-6' />
              <p className='mt-3 text-sm font-medium'>
                {t('No RSS feeds are configured yet.')}
              </p>
              {isOwner && (
                <Button
                  className='mt-4'
                  variant='outline'
                  size='sm'
                  render={
                    <Link
                      to='/system-settings/site/$section'
                      params={{ section: 'rss' }}
                    />
                  }
                >
                  {t('Manage feeds')}
                </Button>
              )}
            </div>
          )}

          {query.isSuccess && feeds.length > 0 && visibleItems.length === 0 && (
            <div className='py-14 text-center'>
              <p className='text-muted-foreground text-sm'>
                {t('No articles match your filters.')}
              </p>
              {(search || feedFilter !== 'all') && (
                <Button
                  className='mt-2'
                  variant='link'
                  onClick={() => {
                    setSearch('')
                    setFeedFilter('all')
                  }}
                >
                  {t('Clear filters')}
                </Button>
              )}
            </div>
          )}

          {query.isSuccess && visibleItems.length > 0 && (
            <div className='divide-border divide-y border-y'>
              {visibleItems.map((item, index) => {
                const href = safeRSSFeedUrl(item.url)
                const published = publishedLabel(item.published_at)
                return (
                  <article
                    key={`${item.feedID}:${item.id}:${index}`}
                    className='group py-5 sm:py-6'
                  >
                    <div className='text-muted-foreground flex flex-wrap items-center gap-x-2 gap-y-1 text-xs'>
                      <span className='font-medium'>{item.feedName}</span>
                      {published && (
                        <>
                          <span aria-hidden='true'>·</span>
                          <time dateTime={item.published_at}>{published}</time>
                        </>
                      )}
                    </div>
                    <h2 className='mt-2 text-base leading-snug font-semibold tracking-tight sm:text-lg'>
                      {href ? (
                        <a
                          href={href}
                          target='_blank'
                          rel='noopener noreferrer'
                          className='hover:text-primary inline-flex items-start gap-1.5'
                        >
                          <span>{item.title}</span>
                          <ArrowUpRight
                            className='mt-0.5 size-4 shrink-0 opacity-0 transition-opacity group-hover:opacity-100'
                            aria-hidden='true'
                          />
                        </a>
                      ) : (
                        item.title
                      )}
                    </h2>
                    {item.summary && (
                      <p className='text-muted-foreground mt-2 line-clamp-3 max-w-4xl text-sm leading-relaxed'>
                        {item.summary}
                      </p>
                    )}
                  </article>
                )
              })}
            </div>
          )}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
