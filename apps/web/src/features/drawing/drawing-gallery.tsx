/*
Copyright (C) 2026 LIghtJUNction
*/
import {
  Copy01Icon,
  Download01Icon,
  Image01Icon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { buttonVariants } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { toIntlLocale } from '@/i18n/languages'
import { copyToClipboard } from '@/lib/copy-to-clipboard'
import { cn } from '@/lib/utils'

import type { DrawingPreview } from './drawing-history'

/**
 * One gallery tile. The frame reserves its box before the bytes arrive, so the
 * grid never reflows while images load, and a skeleton covers the gap.
 */
function DrawingTile({ image }: { image: DrawingPreview }) {
  const { t, i18n } = useTranslation()
  const [loaded, setLoaded] = useState(false)
  const date = new Date(image.createdAt)
  const isValidDate = !Number.isNaN(date.getTime())
  const caption = image.revisedPrompt || image.prompt
  // Sampling options are technical tokens, not prose: they read the same in
  // every language, so they are shown raw. Older records simply have none.
  const params = [
    image.model,
    image.size,
    image.quality,
    image.count ? `${image.count}×` : undefined,
  ].filter((value): value is string => Boolean(value?.trim()))

  const copyPrompt = async () => {
    const copied = await copyToClipboard(caption)
    if (copied) toast.success(t('Prompt copied'))
    else toast.error(t('Unable to copy the prompt'))
  }

  return (
    <figure
      className='group relative min-w-0 overflow-hidden rounded-lg border border-white/10 bg-black/30'
      data-loaded={loaded ? 'true' : 'false'}
    >
      <div className='relative aspect-square w-full overflow-hidden bg-black/40 sm:aspect-[4/3]'>
        {loaded ? null : (
          <Skeleton className='absolute inset-0 rounded-none bg-white/5' />
        )}
        <img
          src={image.src}
          alt={caption}
          loading='lazy'
          referrerPolicy='no-referrer'
          decoding='async'
          onLoad={() => setLoaded(true)}
          onError={() => setLoaded(true)}
          className={cn(
            'size-full object-contain transition-[opacity,transform] duration-300 motion-reduce:transition-none motion-reduce:transform-none',
            'group-hover:scale-[1.03] group-focus-within:scale-[1.03]',
            loaded ? 'opacity-100' : 'opacity-0'
          )}
        />

        {/* The overlay repeats the prompt in full. The caption clamps to two
            lines, so this is the only place the whole text is readable, and it
            costs no layout because the tile already reserves its box. */}
        <div
          className={cn(
            'pointer-events-none absolute inset-0 flex flex-col justify-end gap-2 bg-gradient-to-t from-black/90 via-black/60 to-transparent p-3',
            'opacity-0 transition-opacity duration-300 motion-reduce:transition-none',
            'group-hover:opacity-100 group-focus-within:opacity-100'
          )}
        >
          {/* Decorative: the caption below already carries this text, so the
              reveal is aria-hidden rather than announced a second time. */}
          <p
            aria-hidden='true'
            className='max-h-[9rem] overflow-y-auto text-xs leading-5 break-words text-white/90'
          >
            {caption}
          </p>
          {params.length > 0 ? (
            <ul className='flex flex-wrap items-center gap-1'>
              {params.map((param) => (
                <li
                  key={param}
                  className='rounded-full border border-white/20 bg-white/10 px-2 py-0.5 font-mono text-[10px] leading-4 text-white/80'
                >
                  {param}
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      </div>

      <figcaption className='grid gap-2 border-t border-white/10 p-2 text-xs leading-5 text-white/70'>
        <p className='line-clamp-2 break-words'>{caption}</p>
        <div className='flex flex-wrap items-center justify-between gap-2'>
          <time dateTime={isValidDate ? date.toISOString() : undefined}>
            {isValidDate
              ? date.toLocaleString(
                  toIntlLocale(i18n.resolvedLanguage || i18n.language) ?? 'en'
                )
              : ''}
          </time>
          <div className='flex items-center gap-1.5'>
            <button
              type='button'
              onClick={() => void copyPrompt()}
              aria-label={t('Copy prompt')}
              title={t('Copy prompt')}
              className={cn(
                buttonVariants({ variant: 'ghost', size: 'xs' }),
                'text-white/60 hover:bg-white/10 hover:text-white'
              )}
            >
              <HugeiconsIcon
                icon={Copy01Icon}
                data-icon='inline-start'
                strokeWidth={2}
                aria-hidden='true'
              />
              {t('Copy')}
            </button>
            <a
              href={image.src}
              download={
                image.blob
                  ? `drawing-${image.id}.${image.blob.type === 'image/jpeg' ? 'jpg' : image.blob.type.split('/')[1] || 'png'}`
                  : undefined
              }
              target={image.blob ? undefined : '_blank'}
              rel='noopener noreferrer'
              referrerPolicy='no-referrer'
              className={cn(
                buttonVariants({ variant: 'outline', size: 'xs' }),
                'border-white/15 bg-white/5 text-white hover:bg-white/15 hover:text-white'
              )}
            >
              <HugeiconsIcon
                icon={image.blob ? Download01Icon : Image01Icon}
                data-icon='inline-start'
                strokeWidth={2}
                aria-hidden='true'
              />
              {image.blob ? t('Download image') : t('Open image')}
            </a>
          </div>
        </div>
        {!image.saved ? (
          <span className='text-white/45'>
            {t('Not saved in this browser')}
          </span>
        ) : null}
      </figcaption>
    </figure>
  )
}

export function DrawingGallery({ images }: { images: DrawingPreview[] }) {
  return (
    <div className='grid max-h-[min(58vh,42rem)] w-full max-w-4xl grid-cols-1 gap-3 overflow-y-auto sm:grid-cols-2 sm:gap-4'>
      {images.map((image) => (
        <DrawingTile key={image.id} image={image} />
      ))}
    </div>
  )
}
