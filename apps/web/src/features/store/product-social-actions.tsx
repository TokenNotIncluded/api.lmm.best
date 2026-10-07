/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import {
  Bookmark01Icon,
  Share01Icon,
  ThumbsUpIcon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { copyToClipboard } from '@/lib/copy-to-clipboard'

import { catalogueApi } from './catalogue-api'
import { useStoreCatalogueSupport } from './catalogue-support'
import type { StoreCatalogueProduct } from './catalogue-types'
import { shareStoreProduct } from './product-share'
import { storeProductSocialProjection } from './product-social-state'
import { StoreError } from './shared'
import {
  consumeStoreSocialIntent,
  rememberStoreSocialIntent,
  storeSocialSignInUrl,
  type StoreSocialAction,
} from './social-intent'
import { currentStoreViewer, useStoreViewer } from './store-viewer'

export function StoreProductSocialActions({
  product,
}: {
  product: StoreCatalogueProduct
}) {
  const viewer = useStoreViewer()
  return (
    <ProductSocialActions
      key={`${viewer}:${product.id}`}
      product={product}
      viewer={viewer}
    />
  )
}

function ProductSocialActions({
  product,
  viewer,
}: {
  product: StoreCatalogueProduct
  viewer: string
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const support = useStoreCatalogueSupport()
  const account = /^account:\d+$/.test(viewer)
  const guest = viewer === 'anonymous'
  const [copied, setCopied] = useState(false)
  const assertViewer = () => {
    if (currentStoreViewer() !== viewer) {
      throw new Error(
        'Your account changed. Refresh this page before continuing.'
      )
    }
  }
  const favoriteQuery = useQuery({
    queryKey: ['store', 'favorites', viewer],
    queryFn: ({ signal }) => catalogueApi.allFavorites(signal),
    enabled: account && support.collectionsSupported,
    retry: false,
  })
  const favorite =
    favoriteQuery.data?.some((item) => item.product_id === product.id) ?? false
  const likesSupported = support.data?.store_likes_supported === true
  const likeKey = ['store', 'likes', viewer, product.id]
  const projection = storeProductSocialProjection(product, viewer)
  const likesQuery = useQuery({
    queryKey: likeKey,
    queryFn: ({ signal }) => catalogueApi.likes(product.id, signal),
    enabled: false,
    initialData: projection?.data,
    initialDataUpdatedAt: projection?.requestedAt,
    refetchOnWindowFocus: false,
    retry: false,
  })
  useEffect(() => {
    if (!projection || currentStoreViewer() !== viewer) return
    const key = ['store', 'likes', viewer, product.id]
    const knownAt = client.getQueryState(key)?.dataUpdatedAt ?? 0
    // A list request started before a confirmed mutation must not overwrite it
    // merely because its older response arrived after the write completed.
    if (projection.requestedAt >= knownAt) {
      client.setQueryData(key, projection.data, {
        updatedAt: projection.requestedAt,
      })
    }
  }, [client, product.id, projection, viewer])
  const liked = likesQuery.data?.liked === true
  const favoriteMutation = useMutation({
    mutationFn: async (save: boolean) => {
      assertViewer()
      if (save) await catalogueApi.addFavorite(product.id)
      else await catalogueApi.removeFavorite(product.id)
      assertViewer()
    },
    onSuccess: () => {
      if (currentStoreViewer() === viewer) {
        void client.invalidateQueries({
          queryKey: ['store', 'favorites', viewer],
        })
      }
    },
  })
  const likeMutation = useMutation({
    mutationFn: async (save: boolean) => {
      assertViewer()
      const data = save
        ? await catalogueApi.like(product.id)
        : await catalogueApi.unlike(product.id)
      assertViewer()
      return data
    },
    onSuccess: (data) => {
      if (currentStoreViewer() === viewer) {
        client.setQueryData(likeKey, data)
      }
    },
  })
  const shareMutation = useMutation({
    mutationFn: () =>
      shareStoreProduct(product, window.location.origin, {
        share:
          typeof navigator.share === 'function'
            ? navigator.share.bind(navigator)
            : undefined,
        copy: copyToClipboard,
      }),
    onSuccess: (result) => setCopied(result === 'copied'),
  })
  const saveFavorite = favoriteMutation.mutate
  const saveLike = likeMutation.mutate
  useEffect(() => {
    if (!account || currentStoreViewer() !== viewer) return
    if (
      support.collectionsSupported &&
      favoriteQuery.isSuccess &&
      consumeStoreSocialIntent(product.id, 'favorite') &&
      !favorite
    ) {
      saveFavorite(true)
    }
    if (
      likesSupported &&
      likesQuery.isSuccess &&
      likesQuery.data.supported &&
      consumeStoreSocialIntent(product.id, 'like') &&
      !liked
    ) {
      saveLike(true)
    }
  }, [
    account,
    viewer,
    product.id,
    support.collectionsSupported,
    favoriteQuery.isSuccess,
    favorite,
    likesSupported,
    likesQuery.isSuccess,
    likesQuery.data?.supported,
    liked,
    saveFavorite,
    saveLike,
  ])
  const signIn = (action: StoreSocialAction) =>
    rememberStoreSocialIntent(product.id, action)
  return (
    <div className='space-y-2'>
      <div className='flex flex-wrap items-center gap-2'>
        <Button
          type='button'
          size='sm'
          variant='ghost'
          disabled={shareMutation.isPending}
          onClick={() => {
            setCopied(false)
            shareMutation.mutate()
          }}
        >
          <HugeiconsIcon icon={Share01Icon} data-icon='inline-start' />
          {t('Share link')}
        </Button>
        {support.collectionsSupported &&
          (guest ? (
            <Button
              size='sm'
              variant='ghost'
              render={
                <a
                  href={storeSocialSignInUrl(product.id)}
                  onClick={() => signIn('favorite')}
                />
              }
            >
              <HugeiconsIcon icon={Bookmark01Icon} data-icon='inline-start' />
              {t('Sign in to save favorites')}
            </Button>
          ) : (
            <Button
              type='button'
              size='sm'
              variant='ghost'
              aria-pressed={favorite}
              disabled={
                !account ||
                favoriteQuery.isPending ||
                !!favoriteQuery.error ||
                favoriteMutation.isPending
              }
              onClick={() => favoriteMutation.mutate(!favorite)}
            >
              <HugeiconsIcon icon={Bookmark01Icon} data-icon='inline-start' />
              {t(favorite ? 'Remove from favorites' : 'Save to favorites')}
            </Button>
          ))}
        {likesSupported &&
          likesQuery.data?.supported &&
          (guest ? (
            <Button
              size='sm'
              variant='ghost'
              render={
                <a
                  href={storeSocialSignInUrl(product.id)}
                  onClick={() => signIn('like')}
                />
              }
            >
              <HugeiconsIcon icon={ThumbsUpIcon} data-icon='inline-start' />
              {t('Sign in to like this product')}
              <span className='text-muted-foreground tabular-nums'>
                {likesQuery.data.count}
              </span>
            </Button>
          ) : (
            <Button
              type='button'
              size='sm'
              variant='ghost'
              aria-pressed={liked}
              aria-label={t(
                liked ? 'Unlike this product' : 'Like this product'
              )}
              disabled={
                !account ||
                likesQuery.isPending ||
                !!likesQuery.error ||
                likeMutation.isPending
              }
              onClick={() => likeMutation.mutate(!liked)}
            >
              <HugeiconsIcon icon={ThumbsUpIcon} data-icon='inline-start' />
              {t(liked ? 'Unlike this product' : 'Like this product')}
              <span className='text-muted-foreground tabular-nums'>
                {likesQuery.data.count}
              </span>
            </Button>
          ))}
      </div>
      {copied && (
        <p role='status' className='text-muted-foreground text-xs'>
          {t('Link copied')}
        </p>
      )}
      <StoreError
        error={
          shareMutation.error ||
          favoriteMutation.error ||
          favoriteQuery.error ||
          likeMutation.error ||
          likesQuery.error
        }
        retry={
          favoriteQuery.error
            ? () => void favoriteQuery.refetch()
            : likesQuery.error
              ? () => void likesQuery.refetch()
              : undefined
        }
      />
    </div>
  )
}
