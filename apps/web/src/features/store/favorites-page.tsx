/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

import { catalogueApi } from './catalogue-api'
import { useStoreCatalogueSupport } from './catalogue-support'
import { StoreCatalogueTags } from './catalogue-tags'
import type { StoreFavoriteItem } from './catalogue-types'
import { StoreCollectionActions } from './collection-actions'
import { StoreMerchantIdentity } from './merchant-identity'
import { StoreError, StoreLoading } from './shared'
import { currentStoreViewer, useStoreViewer } from './store-viewer'
import { StoreProductPrice } from './variant-summary'

export function StoreFavoritesPage() {
  const viewer = useStoreViewer()
  return <FavoritesPage key={viewer} viewer={viewer} />
}

function FavoritesPage({ viewer }: { viewer: string }) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const support = useStoreCatalogueSupport()
  const query = useQuery({
    queryKey: ['store', 'favorites', viewer],
    queryFn: ({ signal }) => catalogueApi.allFavorites(signal),
    enabled: viewer !== 'anonymous' && support.collectionsSupported,
    retry: false,
  })
  const clear = useMutation({
    mutationFn: () => {
      if (currentStoreViewer() !== viewer) {
        throw new Error(
          'Your account changed. Refresh this page before continuing.'
        )
      }
      return catalogueApi.clearFavorites()
    },
    onSuccess: () => {
      void client.invalidateQueries({
        queryKey: ['store', 'favorites', viewer],
      })
    },
  })
  return (
    <div className='space-y-5'>
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <h1 className='text-xl font-bold'>{t('Favorite products')}</h1>
        <Button
          variant='outline'
          disabled={
            clear.isPending ||
            !query.data?.length ||
            !support.collectionsSupported
          }
          onClick={() => clear.mutate()}
        >
          {t('Clear favorites')}
        </Button>
      </div>
      {viewer === 'anonymous' ? (
        <div className='space-y-3'>
          <p className='text-muted-foreground'>
            {t('Sign in to save and view your favorite products.')}
          </p>
          <Button render={<a href='/sign-in?redirect=%2Fstore%2Ffavorites' />}>
            {t('Sign in')}
          </Button>
        </div>
      ) : (
        <>
          <StoreError
            error={support.error}
            retry={() => void support.refetch()}
          />
          <StoreError
            error={query.error || clear.error}
            retry={query.error ? () => void query.refetch() : undefined}
          />
          {support.isPending ? (
            <StoreLoading />
          ) : !support.collectionsSupported ? (
            <p role='status' className='text-muted-foreground'>
              {t(
                'Cart and favorites are not supported by this shop server yet.'
              )}
            </p>
          ) : query.isPending ? (
            <StoreLoading />
          ) : (
            query.data &&
            (query.data.length === 0 ? (
              <p className='text-muted-foreground py-6'>
                {t('You have no favorite products yet.')}
              </p>
            ) : (
              <div className='grid gap-4 sm:grid-cols-2 lg:grid-cols-3'>
                {query.data.map((item) => (
                  <FavoriteItem
                    key={item.product_id}
                    item={item}
                    viewer={viewer}
                  />
                ))}
              </div>
            ))
          )}
        </>
      )}
      <Button variant='ghost' render={<a href='/store' />}>
        {t('Continue browsing')}
      </Button>
    </div>
  )
}

function FavoriteItem({
  item,
  viewer,
}: {
  item: StoreFavoriteItem
  viewer: string
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const product = item.product
  const remove = useMutation({
    mutationFn: () => {
      if (currentStoreViewer() !== viewer) {
        throw new Error(
          'Your account changed. Refresh this page before continuing.'
        )
      }
      return catalogueApi.removeFavorite(item.product_id)
    },
    onSuccess: () => {
      void client.invalidateQueries({
        queryKey: ['store', 'favorites', viewer],
      })
    },
  })
  return (
    <article className='bg-card space-y-3 rounded-lg border p-4'>
      <h2 className='font-semibold'>
        {product?.status === 'paused' ? (
          product.title
        ) : product ? (
          <a
            className='hover:underline'
            href={`/store/products/${encodeURIComponent(product.id)}`}
          >
            {product.title}
          </a>
        ) : (
          t('Unavailable product')
        )}
      </h2>
      {product?.status === 'paused' && (
        <p className='text-muted-foreground text-sm'>{t(product.status)}</p>
      )}
      {product ? (
        <>
          <StoreCatalogueTags product={product} />
          <StoreProductPrice product={product} />
          <StoreMerchantIdentity
            seller={product.seller}
            sellerId={product.seller_id}
          />
          <StoreCollectionActions product={product} />
        </>
      ) : (
        <>
          <p className='text-muted-foreground text-sm'>
            {t('This product is no longer visible to this account.')}
          </p>
          <Button
            variant='outline'
            size='sm'
            disabled={remove.isPending}
            onClick={() => remove.mutate()}
          >
            {t('Remove from favorites')}
          </Button>
          <StoreError error={remove.error} />
        </>
      )}
    </article>
  )
}
