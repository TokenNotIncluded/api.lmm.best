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
import { useQuery } from '@tanstack/react-query'
import { isAxiosError } from 'axios'
import { type ReactNode, useLayoutEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { RichContent } from '@/components/rich-content'
import { Button } from '@/components/ui/button'
import { Progress } from '@/components/ui/progress'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

export type MandatoryAnnouncement = {
  id: number
  content: string
  extra?: string
  publishDate: string
  revision: string
  read_at: number
}

async function loadAnnouncements() {
  try {
    const response = await api.get('/api/user/self/announcements', {
      skipErrorHandler: true,
      skipBusinessError: true,
    })
    if (!response.data.success || !Array.isArray(response.data.data)) {
      throw new Error('Unable to load announcements')
    }
    return {
      supported: true,
      items: response.data.data as MandatoryAnnouncement[],
    }
  } catch (error) {
    // Older backends have no announcement acknowledgement API. A missing route
    // must not lock every authenticated page behind an impossible retry.
    // Real authorization/server/transport failures must still require a retry.
    if (isAxiosError(error) && error.response?.status === 404) {
      return { supported: false, items: [] as MandatoryAnnouncement[] }
    }
    throw error
  }
}

export function MandatoryAnnouncements({ children }: { children: ReactNode }) {
  const { t } = useTranslation()
  const userID = useAuthStore((state) => state.auth.user?.id)
  const query = useQuery({
    queryKey: ['mandatory-announcements', userID],
    queryFn: loadAnnouncements,
    enabled: !!userID,
    retry: false,
    staleTime: (query) =>
      query.state.data?.supported === false ? 5 * 60_000 : 0,
    refetchOnMount: true,
    refetchOnWindowFocus: false,
    refetchInterval: (query) =>
      query.state.data?.supported === false ? false : 60_000,
  })
  if (!userID) return children
  const items = query.data?.items ?? []
  const nextIndex = items.findIndex((item) => !item.read_at)
  const next = nextIndex === -1 ? undefined : items[nextIndex]
  if (query.isError || query.isPending) {
    return (
      <main
        className='mx-auto flex min-h-dvh max-w-xl flex-col justify-center gap-4 p-6'
        aria-live='polite'
      >
        <h1 className='text-xl font-semibold'>{t('Announcements')}</h1>
        <p>{t(query.isError ? 'Unable to load announcements' : 'Loading')}</p>
        {query.isError && (
          <div className='flex flex-wrap gap-2'>
            <Button
              disabled={query.isFetching}
              onClick={() => void query.refetch()}
            >
              {t('Retry')}
            </Button>
            <Button variant='outline' render={<a href='/' />}>
              {t('Back to home')}
            </Button>
          </div>
        )}
      </main>
    )
  }
  if (!next) return children
  return (
    <AnnouncementReader
      key={`${userID}:${next.revision}`}
      item={next}
      // The position is the announcement's own place in the published order.
      // Counting acknowledgements would mislabel a notice inserted earlier.
      completed={nextIndex}
      total={items.length}
      onContinue={async () => {
        try {
          const response = await api.post(
            '/api/user/self/announcements/read',
            { id: next.id, revision: next.revision },
            { skipErrorHandler: true, skipBusinessError: true }
          )
          if (!response.data.success) {
            throw new Error('Unable to confirm reading')
          }
          await query.refetch({ throwOnError: true })
        } catch (error) {
          // A conflict can mean the publication changed while it was being read.
          await query.refetch()
          throw error
        }
      }}
    />
  )
}

export function AnnouncementReader({
  item,
  completed,
  total,
  onContinue,
}: {
  item: MandatoryAnnouncement
  completed: number
  total: number
  onContinue: () => Promise<void>
}) {
  const { t } = useTranslation()
  const viewport = useRef<HTMLDivElement>(null)
  const content = useRef<HTMLDivElement>(null)
  const [progress, setProgress] = useState(0)
  const [saving, setSaving] = useState(false)
  const [failed, setFailed] = useState(false)
  const measure = () => {
    const node = viewport.current
    if (!node) return
    const distance = node.scrollHeight - node.clientHeight
    setProgress(
      distance <= 1
        ? 100
        : Math.min(100, Math.floor(((node.scrollTop + 1) / distance) * 100))
    )
  }
  useLayoutEffect(() => {
    measure()
    const observer = new ResizeObserver(measure)
    if (viewport.current) observer.observe(viewport.current)
    if (content.current) observer.observe(content.current)
    return () => observer.disconnect()
  }, [])
  const confirm = async () => {
    if (progress < 100 || saving) return
    setSaving(true)
    setFailed(false)
    try {
      await onContinue()
    } catch {
      setFailed(true)
    } finally {
      setSaving(false)
    }
  }
  return (
    <main className='mx-auto flex h-dvh max-w-3xl flex-col gap-4 p-4 sm:p-8'>
      <header className='shrink-0 space-y-2'>
        <h1 className='text-xl font-semibold'>{t('Required announcement')}</h1>
        <p>
          {t('Announcement {{current}} of {{total}}', {
            current: completed + 1,
            total,
          })}
        </p>
        <p className='text-muted-foreground text-sm'>
          {t('Read to the bottom, then confirm to continue.')}
        </p>
      </header>
      <div
        ref={viewport}
        onScroll={measure}
        tabIndex={0}
        role='region'
        aria-label={t('Announcement content')}
        className='focus-visible:outline-ring min-h-0 flex-1 overflow-y-auto overscroll-contain border p-4 focus-visible:outline-2'
      >
        <div ref={content} className='space-y-4 break-words'>
          <RichContent breaks content={item.content} />
          {item.extra && <RichContent breaks content={item.extra} />}
        </div>
      </div>
      <footer className='shrink-0 space-y-3 pb-[env(safe-area-inset-bottom)]'>
        <Progress value={progress} aria-label={t('Reading progress')} />
        <p className='text-muted-foreground text-sm'>{progress}%</p>
        {failed && (
          <p role='alert'>
            {t('Unable to confirm reading. Please try again.')}
          </p>
        )}
        <Button
          className='min-h-11 w-full'
          disabled={progress < 100 || saving}
          onClick={() => void confirm()}
        >
          {t(saving ? 'Saving' : 'I have read and continue')}
        </Button>
      </footer>
    </main>
  )
}
