/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { Download01Icon, NewTwitterIcon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

import {
  downloadStoreProductShareImage,
  prepareStoreProductShareImage,
  storeProductShareImageUrl,
} from './product-share-image'
import {
  storeProductXShare,
  type PublicStoreShareProduct,
} from './product-x-share'

export function StoreProductXShareActions({
  product,
}: {
  product: PublicStoreShareProduct
}) {
  const { t } = useTranslation()
  const [state, setState] = useState<
    'idle' | 'loading' | 'downloaded' | 'failed'
  >('idle')
  const request = useRef<AbortController | null>(null)
  const imageSource = product.image_urls?.[0]
  useEffect(
    () => () => {
      request.current?.abort()
      request.current = null
    },
    [product.id, imageSource]
  )
  const share =
    typeof window === 'undefined'
      ? undefined
      : storeProductXShare(product, window.location.origin)
  if (!share) return null
  const image = storeProductShareImageUrl(product)
  async function downloadImage() {
    request.current?.abort()
    const controller = new AbortController()
    request.current = controller
    const timeout = window.setTimeout(() => controller.abort(), 30000)
    setState('loading')
    try {
      const { blob, filename } = await prepareStoreProductShareImage(
        product,
        controller.signal
      )
      if (controller.signal.aborted) return
      downloadStoreProductShareImage(blob, filename)
      setState('downloaded')
    } catch {
      if (request.current === controller) setState('failed')
    } finally {
      window.clearTimeout(timeout)
      controller.abort()
    }
  }
  return (
    <div className='flex flex-wrap items-center gap-2'>
      <Button
        size='sm'
        variant='ghost'
        render={
          <a href={share.intentUrl} target='_blank' rel='noopener noreferrer' />
        }
      >
        <HugeiconsIcon icon={NewTwitterIcon} data-icon='inline-start' />
        {t('Share on X')}
      </Button>
      {image && (
        <Button
          type='button'
          size='sm'
          variant='ghost'
          disabled={state === 'loading'}
          onClick={() => void downloadImage()}
        >
          <HugeiconsIcon icon={Download01Icon} data-icon='inline-start' />
          {t(
            state === 'loading' ? 'Preparing image' : 'Download product image'
          )}
        </Button>
      )}
      {state === 'downloaded' && (
        <p role='status' className='text-muted-foreground basis-full text-xs'>
          {t('Add the downloaded image in X before posting.')}
        </p>
      )}
      {state === 'failed' && image && (
        <p role='status' className='text-muted-foreground basis-full text-xs'>
          {t('Image download unavailable. Open the image to save it manually.')}{' '}
          <a
            href={image}
            target='_blank'
            rel='noopener noreferrer'
            className='underline'
          >
            {t('Open image')}
          </a>
        </p>
      )}
    </div>
  )
}
