/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { ArrowRight01Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
} from '@/components/ui/card'
import { cn } from '@/lib/utils'

import { StoreCatalogueTags } from './catalogue-tags'
import type { StoreCatalogueProduct } from './catalogue-types'
import { StoreCollectionActions } from './collection-actions'
import { StoreMerchantIdentity } from './merchant-identity'
import { StoreProductCardMedia } from './product-card-media'
import { StoreBadges } from './shared'
import {
  useStoreProductImpression,
  type StoreTrafficPage,
} from './traffic-page'
import { StoreProductPrice } from './variant-summary'

export function StoreCatalogueProductCard({
  product,
  featured,
  list,
  trafficPage,
}: {
  product: StoreCatalogueProduct
  featured: boolean
  list: boolean
  trafficPage?: StoreTrafficPage
}) {
  const { t } = useTranslation()
  const impressionRef = useStoreProductImpression(trafficPage, product)
  const href = `/store/products/${product.id}`
  const sold =
    typeof product.net_paid_quantity === 'number' &&
    Number.isSafeInteger(product.net_paid_quantity) &&
    product.net_paid_quantity >= 0
      ? t('Sold: {{count}}', { count: product.net_paid_quantity })
      : undefined

  // List view keeps its existing density and actions. Large cards apply only
  // to the first three products in the current server-ordered result.
  if (list) {
    return (
      <article
        ref={impressionRef}
        data-store-product-id={product.id}
        className='min-w-0 py-6 sm:grid sm:grid-cols-[minmax(0,1fr)_16rem] sm:gap-8'
      >
        <a
          href={href}
          className='focus-visible:outline-ring block min-w-0 focus-visible:outline-2 sm:flex'
        >
          <StoreProductCardMedia
            images={product.image_urls || []}
            title={product.title}
            list
          />
          <div className='flex min-w-0 flex-1 flex-col gap-3 px-1 py-4 sm:px-5'>
            <StoreBadges product={product} />
            <StoreCatalogueTags product={product} />
            <h2 className='line-clamp-2 font-semibold break-words'>
              {product.title}
            </h2>
            <p className='text-muted-foreground line-clamp-2 text-sm break-words'>
              {product.description}
            </p>
            <div className='flex flex-wrap items-center justify-between gap-2 text-sm'>
              <strong className='min-w-0 break-words'>
                <StoreProductPrice product={product} />
              </strong>
              {sold && <span className='text-muted-foreground'>{sold}</span>}
            </div>
          </div>
        </a>
        <div className='flex min-w-0 flex-col gap-4 px-1 py-3 sm:px-4'>
          <StoreMerchantIdentity
            seller={product.seller}
            sellerId={product.seller_id}
          />
          <StoreCollectionActions product={product} />
        </div>
      </article>
    )
  }

  return (
    <Card
      ref={impressionRef}
      variant='paper'
      role='article'
      data-store-product-id={product.id}
      data-store-card-priority={featured ? 'lead' : 'standard'}
      className={cn(
        'col-span-12 min-w-0 gap-0 rounded-none bg-transparent py-0 shadow-none ring-0 sm:col-span-6',
        featured ? 'lg:col-span-4' : 'lg:col-span-3'
      )}
    >
      <CardHeader className={cn('gap-3 px-0 pt-2 pb-6', featured && 'sm:pb-7')}>
        <h2
          className={cn(
            'line-clamp-2 text-lg leading-6 font-semibold tracking-tight break-words',
            featured && 'sm:text-xl sm:leading-7'
          )}
        >
          <a
            href={href}
            className='focus-visible:outline-ring rounded-sm underline-offset-4 hover:underline focus-visible:outline-2'
          >
            {product.title}
          </a>
        </h2>
        <CardDescription className='line-clamp-2 leading-6 break-words'>
          {product.description}
        </CardDescription>
        <StoreBadges product={product} />
      </CardHeader>
      <StoreProductCardMedia
        images={product.image_urls || []}
        title={product.title}
        list={false}
        featured={featured}
        href={href}
      />
      <CardContent
        className={cn(
          'flex min-w-0 flex-1 flex-col gap-4 px-0 py-5',
          featured && 'sm:py-6'
        )}
      >
        <StoreCatalogueTags product={product} />
        <div className='mt-auto flex flex-wrap items-end justify-between gap-3'>
          <strong
            className={cn(
              'min-w-0 text-lg leading-7 font-semibold break-words',
              featured && 'sm:text-2xl sm:leading-8'
            )}
          >
            <StoreProductPrice product={product} />
          </strong>
          <Button
            variant='ghost'
            className='h-11 max-w-full px-2'
            render={<a href={href} />}
          >
            {t('View details')}
            <HugeiconsIcon
              icon={ArrowRight01Icon}
              data-icon='inline-end'
              className='size-4'
            />
          </Button>
        </div>
        {sold && <p className='text-muted-foreground text-xs'>{sold}</p>}
      </CardContent>
      <CardFooter
        className={cn(
          'flex min-w-0 flex-col items-stretch gap-4 rounded-none border-0 bg-transparent px-0 pt-0 pb-4'
        )}
      >
        <StoreMerchantIdentity
          seller={product.seller}
          sellerId={product.seller_id}
        />
        <details className='group text-sm'>
          <summary className='text-muted-foreground hover:text-foreground cursor-pointer py-2'>
            {t('Cart and favorites')}
          </summary>
          <div className='pt-2'>
            <StoreCollectionActions product={product} />
          </div>
        </details>
      </CardFooter>
    </Card>
  )
}
