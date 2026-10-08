/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

import { catalogueApi } from './catalogue-api'
import { useStoreCatalogueSupport } from './catalogue-support'
import type { StoreCatalogueProduct } from './catalogue-types'
import { guestCartUpsert, readGuestStoreCart } from './collection-storage'
import { storeCartCapacity } from './collection-utils'
import { StoreProductSocialActions } from './product-social-actions'
import { StoreError } from './shared'
import { currentStoreViewer, useStoreViewer } from './store-viewer'
import {
  enabledStoreVariants,
  initialStoreVariant,
  selectedStoreVariant,
} from './variant-utils'

export function StoreCollectionActions(props: {
  product: StoreCatalogueProduct
  variantId?: string
  quantity?: number
  cartAction?: boolean
  socialActions?: boolean
}) {
  const viewer = useStoreViewer()
  return <CollectionActions key={viewer} {...props} viewer={viewer} />
}

function CollectionActions({
  product,
  variantId: selectedId,
  quantity = 1,
  viewer,
  cartAction = true,
  socialActions = true,
}: {
  product: StoreCatalogueProduct
  variantId?: string
  quantity?: number
  viewer: string
  cartAction?: boolean
  socialActions?: boolean
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const support = useStoreCatalogueSupport()
  const [localVariantId, setLocalVariantId] = useState(() =>
    initialStoreVariant(product)
  )
  const [added, setAdded] = useState(false)
  const variantId = selectedId ?? localVariantId
  const variants = enabledStoreVariants(product)
  const account = viewer !== 'anonymous'
  const add = useMutation({
    mutationFn: async () => {
      const assertViewer = () => {
        if (currentStoreViewer() !== viewer) {
          throw new Error(
            'Your account changed. Refresh this page before continuing.'
          )
        }
      }
      assertViewer()
      const fresh = await catalogueApi.product(product.id)
      assertViewer()
      if (
        !selectedStoreVariant(fresh, variantId) ||
        !Number.isSafeInteger(quantity) ||
        quantity < 1
      ) {
        throw new Error('Choose a variant before adding to the cart.')
      }
      if (!account && fresh.visibility !== 'public') {
        throw new Error('Sign in to view this product.')
      }
      const existing = account
        ? (await catalogueApi.allCart()).find(
            (item) =>
              item.product_id === product.id && item.variant_id === variantId
          )
        : readGuestStoreCart().find(
            (item) =>
              item.product_id === product.id && item.variant_id === variantId
          )
      assertViewer()
      const total = (existing?.quantity ?? 0) + quantity
      if (
        !Number.isSafeInteger(total) ||
        total > storeCartCapacity(fresh, variantId)
      ) {
        throw new Error(
          'This quantity is no longer available. Refresh the product and check its purchase limits.'
        )
      }
      const ref = {
        product_id: product.id,
        variant_id: variantId,
        quantity: total,
      }
      if (account) await catalogueApi.putCart(ref)
      else guestCartUpsert(ref)
    },
    onSuccess: () => {
      setAdded(true)
      void client.invalidateQueries({ queryKey: ['store', 'cart', viewer] })
    },
  })
  if (!support.collectionsSupported) {
    return socialActions ? (
      <StoreProductSocialActions product={product} />
    ) : null
  }
  return (
    <div className='space-y-2'>
      {cartAction && selectedId === undefined && variants.length > 1 && (
        <select
          aria-label={t('Cart variant')}
          className='bg-background h-11 w-full rounded-md border px-3 text-sm'
          value={variantId}
          onChange={(event) => {
            setLocalVariantId(event.target.value)
            setAdded(false)
          }}
        >
          <option value=''>{t('Choose a variant')}</option>
          {variants.map((variant) => (
            <option key={variant.id} value={variant.id}>
              {variant.name}
            </option>
          ))}
        </select>
      )}
      <div className='flex flex-wrap gap-2'>
        {cartAction && (
          <Button
            type='button'
            size='sm'
            variant='outline'
            disabled={
              add.isPending ||
              !selectedStoreVariant(product, variantId) ||
              !Number.isSafeInteger(quantity) ||
              quantity < 1 ||
              quantity > storeCartCapacity(product, variantId)
            }
            onClick={() => {
              setAdded(false)
              add.mutate()
            }}
          >
            {t(add.isPending ? 'Adding...' : 'Add to cart')}
          </Button>
        )}
        <Button size='sm' variant='ghost' render={<a href='/store/cart' />}>
          {t('View cart')}
        </Button>
      </div>
      {socialActions && <StoreProductSocialActions product={product} />}
      {added && (
        <p role='status' className='text-muted-foreground text-xs'>
          {t(
            'Added to cart. Price and availability are checked again before checkout.'
          )}
        </p>
      )}
      <StoreError error={add.error} />
    </div>
  )
}
