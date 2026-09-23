/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import {
  useInfiniteQuery,
  useMutation,
  useQueryClient,
} from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import {
  hideDirectoryAd,
  listDirectoryAds,
  type DirectoryAd,
} from '@/features/ai-directory/ads-api'
import { refreshCurrentAccount } from '@/features/onboarding/use-auth-user-refresh'

import { SettingsSection } from '../components/settings-section'

export function AIDirectoryAdsSection() {
  const { t } = useTranslation()
  const cache = useQueryClient()
  const [selected, setSelected] = useState<DirectoryAd | null>(null)
  const adsQuery = useInfiniteQuery({
    queryKey: ['ai-directory-ads'],
    queryFn: ({ pageParam }) => listDirectoryAds(pageParam),
    initialPageParam: 0,
    getNextPageParam: (page) => (page.has_more ? page.next_offset : undefined),
    staleTime: 30_000,
    retry: false,
  })
  const ads = adsQuery.data?.pages.flatMap((page) => page.items) ?? []
  const hide = useMutation({
    mutationFn: hideDirectoryAd,
    onSuccess: () => {
      setSelected(null)
      toast.success(t('Advertisement hidden and payment refunded.'))
      void Promise.allSettled([
        cache.invalidateQueries({ queryKey: ['ai-directory-ads'] }),
        cache.invalidateQueries({ queryKey: ['ai-directory-my-ads'] }),
        refreshCurrentAccount(),
      ])
    },
    onError: () =>
      toast.error(t('Unable to hide the advertisement. Try again.')),
  })

  return (
    <SettingsSection title={t('Advertisement moderation')}>
      <p className='text-muted-foreground max-w-2xl text-sm'>
        {t(
          'Review active paid placements. Hiding an ad removes it immediately and refunds its full wallet charge.'
        )}
      </p>
      {adsQuery.isPending && (
        <p className='text-muted-foreground text-sm'>
          {t('Loading advertisements...')}
        </p>
      )}
      {adsQuery.isError && (
        <div className='flex items-center gap-3 text-sm'>
          <p>{t('Unable to load advertisements.')}</p>
          <Button
            variant='outline'
            size='sm'
            onClick={() => void adsQuery.refetch()}
          >
            {t('Try again')}
          </Button>
        </div>
      )}
      {adsQuery.isSuccess && ads.length === 0 && (
        <p className='text-muted-foreground rounded-xl border border-dashed p-6 text-sm'>
          {t('No active advertisements.')}
        </p>
      )}
      <div className='divide-border overflow-hidden rounded-xl border'>
        {ads.map((ad) => (
          <div
            key={ad.id}
            className='flex flex-wrap items-center justify-between gap-3 px-4 py-3'
          >
            <div className='min-w-0'>
              <p className='truncate font-semibold'>{ad.name}</p>
              <p className='text-muted-foreground truncate text-xs'>{ad.url}</p>
              <p className='text-muted-foreground mt-1 text-xs'>
                {t('Bid: {{amount}} USD equivalent', {
                  amount: (ad.bid_cents / 100).toFixed(2),
                })}
                {' · '}
                {t('Ends {{date}}', {
                  date: new Date(ad.expires_at * 1000).toLocaleDateString(),
                })}
              </p>
            </div>
            <Button
              type='button'
              variant='destructive'
              size='sm'
              onClick={() => setSelected(ad)}
            >
              {t('Hide and refund')}
            </Button>
          </div>
        ))}
      </div>
      {adsQuery.hasNextPage && (
        <Button
          variant='outline'
          disabled={adsQuery.isFetchingNextPage}
          onClick={() => void adsQuery.fetchNextPage()}
        >
          {t('Load more advertisements')}
        </Button>
      )}
      <ConfirmDialog
        open={selected !== null}
        onOpenChange={(open) => {
          if (!open && !hide.isPending) setSelected(null)
        }}
        title={t('Hide this advertisement?')}
        desc={t(
          'The ad will disappear immediately and {{quota}} wallet units will be refunded to its owner.',
          { quota: selected?.charged_quota.toLocaleString() ?? '0' }
        )}
        confirmText={t('Hide and refund')}
        destructive
        isLoading={hide.isPending}
        handleConfirm={() => {
          if (selected) hide.mutate(selected.id)
        }}
      />
    </SettingsSection>
  )
}
