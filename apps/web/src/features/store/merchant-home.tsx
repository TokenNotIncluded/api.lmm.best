/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Markdown } from '@/components/ui/markdown'
import { Textarea } from '@/components/ui/textarea'
import { useAuthStore } from '@/stores/auth-store'

import { storeApi } from './api'
import { StoreMerchantIdentity } from './merchant-identity'
import {
  normalizeStoreImageSource,
  safeStoreMediaUrl,
  storeImageEditorText,
} from './product-media'
import { StoreError, StoreLoading } from './shared'
import { useStoreViewer } from './store-viewer'
import type { StoreMerchantHome } from './types'

const MERCHANT_HOME_COPY = {
  settings: 'Shop profile',
  biography: 'Shop introduction',
  announcement: 'Merchant announcement',
  header: 'Shop header image',
  headerHint:
    'HTTPS image URL or safe SVG text; supported SVG animations are preserved.',
  avatar:
    'Your shop uses the same Gravatar as your platform account, based on your account email. Without an image, your name initial is shown.',
  save: 'Save shop profile',
  visit: 'View my shop',
  storeAnnouncement: 'Store announcement',
  saveAnnouncement: 'Save store announcement',
} as const

export function StoreAnnouncement({ supported }: { supported: boolean }) {
  const { t } = useTranslation()
  const query = useQuery({
    queryKey: ['store', 'announcement'],
    queryFn: ({ signal }) => storeApi.announcement(signal),
    enabled: supported,
    retry: false,
  })
  if (!supported) return null
  return (
    <>
      <StoreError error={query.error} retry={() => void query.refetch()} />
      {query.data?.content && (
        <section
          className='bg-muted/50 space-y-2 rounded-xl border px-5 py-4'
          aria-label={t(MERCHANT_HOME_COPY.storeAnnouncement)}
        >
          <h2 className='text-sm font-semibold'>
            {t(MERCHANT_HOME_COPY.storeAnnouncement)}
          </h2>
          <Markdown className='min-w-0'>{query.data.content}</Markdown>
        </section>
      )}
    </>
  )
}

export function StoreMerchantHomeHeader({
  sellerId,
  supported,
}: {
  sellerId: number
  supported: boolean
}) {
  const { t } = useTranslation()
  const viewer = useStoreViewer()
  const user = useAuthStore((state) => state.auth.user)
  const query = useQuery({
    queryKey: ['store', 'merchant-home', viewer, sellerId],
    queryFn: ({ signal }) => storeApi.merchantHome(sellerId, signal),
    enabled: supported,
    retry: false,
  })
  if (!supported) return null
  const home = query.data
  const header = safeStoreMediaUrl(home?.header_image)
  return (
    <>
      <StoreError error={query.error} retry={() => void query.refetch()} />
      {query.isPending ? (
        <StoreLoading />
      ) : (
        home && (
          <section className='overflow-hidden rounded-2xl border'>
            {header && (
              <img
                src={header}
                alt=''
                referrerPolicy='no-referrer'
                className='max-h-72 w-full object-cover'
              />
            )}
            <div className='space-y-4 p-5 sm:p-7'>
              <h1 className='console-page-title text-2xl font-bold break-words'>
                {t('Shop by {{name}}', {
                  name: home.seller.display_name || home.seller.username,
                })}
              </h1>
              <StoreMerchantIdentity seller={home.seller} />
              {user?.id === home.seller.id && (
                <a
                  href='/store/settings'
                  className='text-sm underline underline-offset-4'
                >
                  {t(MERCHANT_HOME_COPY.settings)}
                </a>
              )}
              {home.biography && (
                <Markdown className='min-w-0'>{home.biography}</Markdown>
              )}
              {home.announcement && (
                <section
                  className='bg-muted/50 space-y-2 rounded-xl p-4'
                  aria-label={t(MERCHANT_HOME_COPY.announcement)}
                >
                  <h2 className='text-sm font-semibold'>
                    {t(MERCHANT_HOME_COPY.announcement)}
                  </h2>
                  <Markdown className='min-w-0'>{home.announcement}</Markdown>
                </section>
              )}
            </div>
          </section>
        )
      )}
    </>
  )
}

export function StoreMerchantHomeSettings({
  supported,
}: {
  supported: boolean
}) {
  const user = useAuthStore((state) => state.auth.user)
  const query = useQuery({
    queryKey: ['store', 'my-home', user?.id],
    queryFn: storeApi.myHome,
    enabled: supported && !!user,
    retry: false,
  })
  if (!supported || !user) return null
  return (
    <>
      <StoreError error={query.error} retry={() => void query.refetch()} />
      {query.isPending ? (
        <StoreLoading />
      ) : (
        query.data && (
          <MerchantHomeForm
            key={`${user.id}:${query.data.version}`}
            home={query.data}
          />
        )
      )}
    </>
  )
}

function MerchantHomeForm({ home }: { home: StoreMerchantHome }) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [biography, setBiography] = useState(home.biography)
  const [announcement, setAnnouncement] = useState(home.announcement)
  const [headerImage, setHeaderImage] = useState(
    storeImageEditorText(home.header_image)
  )
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const header = normalizeStoreImageSource(headerImage)
  async function save(event: React.FormEvent) {
    event.preventDefault()
    setBusy(true)
    setError(null)
    try {
      if (headerImage.trim() && (!header || /^http:/i.test(header))) {
        throw new Error(MERCHANT_HOME_COPY.headerHint)
      }
      await storeApi.saveHome({
        biography,
        announcement,
        header_image: header ?? '',
        expected_version: home.version,
      })
      await Promise.all([
        client.invalidateQueries({
          queryKey: ['store', 'my-home', home.seller.id],
        }),
        client.invalidateQueries({
          queryKey: ['store', 'merchant-home'],
        }),
      ])
    } catch (issue) {
      setError(issue)
    } finally {
      setBusy(false)
    }
  }
  return (
    <section className='space-y-4 rounded-xl border p-5'>
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <h2 className='font-semibold'>{t(MERCHANT_HOME_COPY.settings)}</h2>
        <a
          href={`/store?seller_id=${home.seller.id}`}
          className='text-sm underline underline-offset-4'
        >
          {t(MERCHANT_HOME_COPY.visit)}
        </a>
      </div>
      <StoreMerchantIdentity seller={home.seller} />
      <p className='text-muted-foreground text-xs'>
        {t(MERCHANT_HOME_COPY.avatar)}
      </p>
      <form onSubmit={(event) => void save(event)} className='space-y-4'>
        <StoreError error={error} />
        <div className='space-y-2'>
          <Label htmlFor='merchant-biography'>
            {t(MERCHANT_HOME_COPY.biography)}
          </Label>
          <Textarea
            id='merchant-biography'
            value={biography}
            onChange={(event) => setBiography(event.target.value)}
            rows={3}
            maxLength={4096}
          />
        </div>
        <div className='space-y-2'>
          <Label htmlFor='merchant-announcement'>
            {t(MERCHANT_HOME_COPY.announcement)}
          </Label>
          <Textarea
            id='merchant-announcement'
            value={announcement}
            onChange={(event) => setAnnouncement(event.target.value)}
            rows={4}
            maxLength={16384}
          />
        </div>
        <div className='space-y-2'>
          <Label htmlFor='merchant-header'>
            {t(MERCHANT_HOME_COPY.header)}
          </Label>
          <Textarea
            id='merchant-header'
            value={headerImage}
            onChange={(event) => setHeaderImage(event.target.value)}
            rows={4}
            placeholder={t(MERCHANT_HOME_COPY.headerHint)}
          />
          <p className='text-muted-foreground text-xs'>
            {t(MERCHANT_HOME_COPY.headerHint)}
          </p>
          {header && (
            <img
              src={header}
              alt=''
              referrerPolicy='no-referrer'
              className='max-h-48 w-full rounded-lg object-cover'
            />
          )}
        </div>
        <Button type='submit' disabled={busy}>
          {t(busy ? 'Saving...' : MERCHANT_HOME_COPY.save)}
        </Button>
      </form>
    </section>
  )
}

export function StoreAnnouncementSettings({
  supported,
}: {
  supported: boolean
}) {
  const user = useAuthStore((state) => state.auth.user)
  const query = useQuery({
    queryKey: ['store', 'announcement'],
    queryFn: () => storeApi.announcement(),
    enabled: supported && (user?.role ?? 0) >= 10,
    retry: false,
  })
  if (!supported || (user?.role ?? 0) < 10) return null
  return (
    <>
      <StoreError error={query.error} retry={() => void query.refetch()} />
      {query.data && (
        <AnnouncementForm
          key={query.data.content}
          initial={query.data.content}
        />
      )}
    </>
  )
}

function AnnouncementForm({ initial }: { initial: string }) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [content, setContent] = useState(initial)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  return (
    <section className='space-y-3 rounded-xl border p-5'>
      <form
        className='space-y-3'
        onSubmit={(event) => {
          event.preventDefault()
          setBusy(true)
          setError(null)
          void storeApi
            .saveAnnouncement(content)
            .then(() =>
              client.invalidateQueries({ queryKey: ['store', 'announcement'] })
            )
            .catch(setError)
            .finally(() => setBusy(false))
        }}
      >
        <Label htmlFor='store-announcement'>
          {t(MERCHANT_HOME_COPY.storeAnnouncement)}
        </Label>
        <Textarea
          id='store-announcement'
          rows={5}
          maxLength={16384}
          value={content}
          onChange={(event) => setContent(event.target.value)}
        />
        <StoreError error={error} />
        <Button disabled={busy}>
          {t(busy ? 'Saving...' : MERCHANT_HOME_COPY.saveAnnouncement)}
        </Button>
      </form>
    </section>
  )
}
