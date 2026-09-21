/*
Copyright (C) 2026 LIghtJUNction
*/
import { useTranslation } from 'react-i18next'

import { buttonVariants } from '@/components/ui/button'
import { toIntlLocale } from '@/i18n/languages'

import type { DrawingPreview } from './drawing-history'

export function DrawingGallery({ images }: { images: DrawingPreview[] }) {
  const { t, i18n } = useTranslation()
  return (
    <div className='grid max-h-[min(58vh,42rem)] w-full max-w-4xl gap-4 overflow-y-auto sm:grid-cols-2'>
      {images.map((image) => (
        <figure
          className='group relative min-w-0 overflow-hidden rounded-lg border border-white/10 bg-black/30 p-2'
          key={image.id}
        >
          <img
            src={image.src}
            alt={image.revisedPrompt || image.prompt}
            className='h-auto max-h-[38rem] w-full rounded-lg object-contain'
            loading='lazy'
            referrerPolicy='no-referrer'
          />
          <figcaption className='mt-2 grid gap-2 text-xs leading-5 text-white/70'>
            <p className='line-clamp-2 break-words'>
              {image.revisedPrompt || image.prompt}
            </p>
            <div className='flex flex-wrap items-center justify-between gap-2'>
              {(() => {
                const date = new Date(image.createdAt)
                const isValid = !Number.isNaN(date.getTime())
                return (
                  <time dateTime={isValid ? date.toISOString() : undefined}>
                    {isValid
                      ? date.toLocaleString(
                          toIntlLocale(
                            i18n.resolvedLanguage || i18n.language
                          ) ?? 'en'
                        )
                      : ''}
                  </time>
                )
              })()}
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
                className={buttonVariants({ variant: 'outline', size: 'xs' })}
              >
                {image.blob ? t('Download image') : t('Open image')}
              </a>
            </div>
            {!image.saved ? (
              <span>{t('Not saved in this browser')}</span>
            ) : null}
          </figcaption>
        </figure>
      ))}
    </div>
  )
}
