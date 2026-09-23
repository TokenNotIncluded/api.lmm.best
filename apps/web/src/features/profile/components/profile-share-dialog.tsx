/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import {
  Copy01Icon,
  Download01Icon,
  Link01Icon,
  NewTwitterIcon,
  Share01Icon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button, buttonVariants } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { toIntlLocale } from '@/i18n/languages'
import { getGravatarImage } from '@/lib/avatar-cache'
import { formatCompactNumber, formatNumber } from '@/lib/format'
import { getRoleLabel } from '@/lib/roles'

import { getDisplayName } from '../lib'
import type { ProfileDailyUsage, ProfileUsageSummary } from '../lib/activity'
import {
  drawProfileShareCard,
  PROFILE_SHARE_CARD_HEIGHT,
  PROFILE_SHARE_CARD_WIDTH,
  PROFILE_SHARE_URL,
  profileSharePostText,
  profileShareXIntent,
} from '../lib/share-card'
import type { UserProfile } from '../types'

interface ProfileShareDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  profile: UserProfile
  days: ProfileDailyUsage[]
  summary: ProfileUsageSummary
  activityAvailable: boolean
}

function canvasToBlob(canvas: HTMLCanvasElement): Promise<Blob> {
  return new Promise((resolve, reject) => {
    canvas.toBlob((blob) => {
      if (blob) resolve(blob)
      else reject(new Error('PNG export failed'))
    }, 'image/png')
  })
}

export function ProfileShareDialog({
  open,
  onOpenChange,
  profile,
  days,
  summary,
  activityAvailable,
}: ProfileShareDialogProps) {
  const { t, i18n } = useTranslation()
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const [imageBlob, setImageBlob] = useState<Blob | null>(null)
  const [imageURL, setImageURL] = useState<string | null>(null)
  const [renderError, setRenderError] = useState(false)
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language) ?? 'en'
  const postMessage = t('My AI activity on LMM Best')
  const canShareImage =
    typeof navigator !== 'undefined' &&
    typeof navigator.share === 'function' &&
    typeof navigator.canShare === 'function'

  useEffect(() => {
    if (!imageBlob) {
      setImageURL(null)
      return
    }
    const url = URL.createObjectURL(imageBlob)
    setImageURL(url)
    return () => URL.revokeObjectURL(url)
  }, [imageBlob])

  useEffect(() => {
    if (!open) return
    let active = true
    setImageBlob(null)
    setRenderError(false)

    const content = {
      displayName: getDisplayName(profile),
      username: profile.username,
      roleLabel: getRoleLabel(profile.role),
      totalTokens: activityAvailable
        ? formatCompactNumber(summary.totalTokens, locale)
        : '—',
      peakDailyTokens: activityAvailable
        ? formatCompactNumber(summary.peakDailyTokens, locale)
        : '—',
      requestCount: formatCompactNumber(profile.request_count, locale),
      currentStreak: activityAvailable
        ? `${formatNumber(summary.currentStreak, locale)}${summary.currentStreakCapped ? '+' : ''} ${t('days')}`
        : '—',
      longestStreak: activityAvailable
        ? `${formatNumber(summary.longestStreak, locale)} ${t('days')}`
        : '—',
      days,
      activityAvailable,
      locale,
      labels: {
        totalTokens: t('Tokens in the past year'),
        activity: t('Token activity'),
        activityUnavailable: t('Activity unavailable'),
        peakDailyTokens: t('Peak daily tokens'),
        requestCount: t('API Requests'),
        currentStreak: t('Current streak'),
        longestStreak: t('Longest streak this year'),
      },
    }

    const render = async (avatar?: ImageBitmap) => {
      const canvas = canvasRef.current
      if (!active || !canvas) return
      drawProfileShareCard(canvas, content, avatar)
      const blob = await canvasToBlob(canvas)
      if (active) setImageBlob(blob)
    }

    const prepare = async () => {
      try {
        await document.fonts?.ready
        await render()
      } catch {
        if (active) setRenderError(true)
        return
      }
      // Only CORS-readable image bytes are drawn, so PNG export stays safe.
      try {
        const image = await getGravatarImage(profile.email, 256)
        if (!active || !(image instanceof Blob)) return
        const avatar = await createImageBitmap(image)
        try {
          await render(avatar)
        } finally {
          avatar.close()
        }
      } catch {
        // The initial-based card remains ready when an avatar cannot be read.
      }
    }

    void prepare()
    return () => {
      active = false
    }
  }, [activityAvailable, days, locale, open, profile, summary, t])

  const downloadImage = () => {
    if (!imageBlob) return
    const objectUrl = URL.createObjectURL(imageBlob)
    const link = document.createElement('a')
    link.href = objectUrl
    link.download = 'lmm-best-share.png'
    document.body.append(link)
    link.click()
    link.remove()
    window.setTimeout(() => URL.revokeObjectURL(objectUrl), 1000)
  }

  const copyImage = async () => {
    if (!imageBlob) return
    if (!navigator.clipboard?.write || typeof ClipboardItem === 'undefined') {
      toast.error(
        t('Image copying is unavailable. Download the image instead.')
      )
      return
    }
    try {
      await navigator.clipboard.write([
        new ClipboardItem({ 'image/png': imageBlob }),
      ])
      toast.success(t('Image copied'))
    } catch {
      toast.error(t('Could not copy the image. Download it instead.'))
    }
  }

  const copyText = async (text: string, success: string) => {
    try {
      await navigator.clipboard.writeText(text)
      toast.success(success)
    } catch {
      toast.error(t('Could not copy. Please try again.'))
    }
  }

  const shareImage = async () => {
    if (!imageBlob || !canShareImage) return
    const file = new File([imageBlob], 'lmm-best-share.png', {
      type: 'image/png',
    })
    if (!navigator.canShare({ files: [file] })) {
      toast.error(
        t('Image sharing is unavailable. Download the image instead.')
      )
      return
    }
    try {
      await navigator.share({
        files: [file],
        title: t('My year with LMM Best'),
        text: profileSharePostText(postMessage),
      })
    } catch (error) {
      if (error instanceof DOMException && error.name === 'AbortError') return
      toast.error(t('Could not share the image. Download it instead.'))
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='sm:max-w-3xl'>
        <DialogHeader>
          <DialogTitle>{t('Your share card')}</DialogTitle>
          <DialogDescription>
            {t(
              'The image shows your name and usage activity. Review it before sharing.'
            )}
          </DialogDescription>
        </DialogHeader>

        <div className='space-y-2'>
          <div className='border-border/60 bg-muted/30 overflow-hidden rounded-xl border'>
            <a
              href={imageURL ?? undefined}
              target='_blank'
              rel='noopener noreferrer'
              aria-label={imageURL ? t('Open image') : undefined}
              className={imageURL ? 'block cursor-zoom-in' : 'block'}
            >
              <canvas
                ref={canvasRef}
                width={PROFILE_SHARE_CARD_WIDTH}
                height={PROFILE_SHARE_CARD_HEIGHT}
                role='img'
                aria-label={t('Preview of your share card')}
                className='block h-auto w-full'
              />
            </a>
          </div>
          {imageURL ? (
            <a
              href={imageURL}
              target='_blank'
              rel='noopener noreferrer'
              className='text-muted-foreground hover:text-foreground inline-block text-xs underline underline-offset-4'
            >
              {t('Open image')}
            </a>
          ) : null}
        </div>
        {renderError ? (
          <p role='alert' className='text-destructive text-sm'>
            {t('Could not create the image. Close and try again.')}
          </p>
        ) : null}

        <div className='grid gap-2 sm:grid-cols-2'>
          <Button onClick={downloadImage} disabled={!imageBlob}>
            <HugeiconsIcon icon={Download01Icon} strokeWidth={2} />
            {t('Download image')}
          </Button>
          <Button
            variant='outline'
            onClick={() => void copyImage()}
            disabled={!imageBlob}
          >
            <HugeiconsIcon icon={Copy01Icon} strokeWidth={2} />
            {t('Copy image')}
          </Button>
        </div>

        <div className='border-border/60 flex flex-wrap items-center gap-2 border-t pt-4'>
          {canShareImage ? (
            <Button
              variant='secondary'
              size='sm'
              onClick={() => void shareImage()}
              disabled={!imageBlob}
            >
              <HugeiconsIcon icon={Share01Icon} strokeWidth={2} />
              {t('Share via device')}
            </Button>
          ) : null}
          <a
            className={buttonVariants({ variant: 'secondary', size: 'sm' })}
            href={profileShareXIntent(postMessage)}
            target='_blank'
            rel='noopener noreferrer'
          >
            <HugeiconsIcon icon={NewTwitterIcon} strokeWidth={2} />
            {t('Post on X')}
          </a>
          <Button
            variant='ghost'
            size='sm'
            onClick={() =>
              void copyText(
                profileSharePostText(postMessage),
                t('Post text copied')
              )
            }
          >
            <HugeiconsIcon icon={Copy01Icon} strokeWidth={2} />
            {t('Copy post text')}
          </Button>
          <Button
            variant='ghost'
            size='sm'
            onClick={() => void copyText(PROFILE_SHARE_URL, t('Link copied'))}
          >
            <HugeiconsIcon icon={Link01Icon} strokeWidth={2} />
            {t('Copy link')}
          </Button>
        </div>
        <p className='text-muted-foreground text-xs leading-relaxed'>
          {t(
            'X opens with text and a link. To include the card, paste or attach the image.'
          )}
        </p>
      </DialogContent>
    </Dialog>
  )
}
