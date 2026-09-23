/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
/*
Copyright (C) 2026 LIghtJUNction
*/
import { ArrowLeft01Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { CopyButton } from '@/components/copy-button'
import { SectionPageLayout } from '@/components/layout'
import { Button, buttonVariants } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'

import {
  disableProfileShare,
  enableProfileShare,
  getProfileShareState,
} from './api'
import { PROFILE_SHARE_URL } from './lib/share-card'

type BadgeTheme = 'paper' | 'dark' | 'transparent'
type BadgeLayout = 'profile' | 'badge'
type BadgePeriod = '7d' | '30d' | '365d' | 'all'
type BadgeAnimation = 'wave' | 'pulse' | 'none'
type BadgeFont = 'sans' | 'mono' | 'serif'
type BadgeFormat = 'compact' | 'full'

interface BadgeColors {
  bg: string
  fg: string
  accent: string
  muted: string
  border: string
}

interface BadgeOptions {
  layout: BadgeLayout
  theme: BadgeTheme
  period: BadgePeriod
  animation: BadgeAnimation
  font: BadgeFont
  format: BadgeFormat
  width: number
  height: number
  radius: number
  requests: boolean
  title: string
  label: string
  footer: string
  colors: BadgeColors
}

const BADGE_PALETTES: Record<BadgeTheme, BadgeColors> = {
  paper: {
    bg: '#faf9f5',
    fg: '#17231f',
    accent: '#d97757',
    muted: '#6c746d',
    border: '#d9ded5',
  },
  dark: {
    bg: '#202020',
    fg: '#f3f3f3',
    accent: '#dedede',
    muted: '#aaa9a8',
    border: '#363636',
  },
  transparent: {
    bg: '#faf9f5',
    fg: '#17231f',
    accent: '#d97757',
    muted: '#6c746d',
    border: '#d9ded5',
  },
}

const INITIAL_OPTIONS: BadgeOptions = {
  layout: 'profile',
  theme: 'dark',
  period: '365d',
  animation: 'wave',
  font: 'sans',
  format: 'compact',
  width: 1200,
  height: 865,
  radius: 0,
  requests: true,
  title: '',
  label: '',
  footer: '',
  colors: BADGE_PALETTES.dark,
}

function buildBadgeURL(
  baseURL: string,
  options: BadgeOptions,
  language: string
): string {
  const url = new URL(baseURL)
  for (const [key, value] of Object.entries({
    layout: options.layout,
    theme: options.theme,
    period: options.period,
    animation: options.animation,
    font: options.font,
    format: options.format,
    width: options.width,
    height: options.height,
    radius: options.radius,
    requests: options.requests ? '1' : '0',
    lang: language,
  })) {
    url.searchParams.set(key, String(value))
  }
  for (const [key, value] of Object.entries(options.colors)) {
    if (key !== 'bg' || options.theme !== 'transparent') {
      url.searchParams.set(key, value)
    }
  }
  for (const key of ['title', 'label', 'footer'] as const) {
    if (options[key].trim()) {
      url.searchParams.set(key, options[key].trim())
    }
  }
  return url.toString()
}

function clampNumber(
  value: string,
  fallback: number,
  min: number,
  max: number
) {
  const number = Number(value)
  return Number.isFinite(number)
    ? Math.min(max, Math.max(min, Math.round(number)))
    : fallback
}

export function ProfileSharePage() {
  const { t, i18n } = useTranslation()
  const queryClient = useQueryClient()
  const [options, setOptions] = useState<BadgeOptions>(INITIAL_OPTIONS)
  const [previewURL, setPreviewURL] = useState('')
  const [previewFailed, setPreviewFailed] = useState(false)
  const shareQuery = useQuery({
    queryKey: ['profile-share'],
    queryFn: getProfileShareState,
    retry: 1,
  })
  const enableMutation = useMutation({
    mutationFn: enableProfileShare,
    onSuccess: (data) => {
      queryClient.setQueryData(['profile-share'], data)
      toast.success(t('Public SVG badge enabled'))
    },
    onError: () => toast.error(t('Could not enable the public badge')),
  })
  const disableMutation = useMutation({
    mutationFn: disableProfileShare,
    onSuccess: (data) => {
      queryClient.setQueryData(['profile-share'], data)
      toast.success(t('Public SVG badge disabled'))
    },
    onError: () => toast.error(t('Could not disable the public badge')),
  })

  const resolvedLanguage = i18n.resolvedLanguage || i18n.language
  const badgeLanguage = ['en', 'zh', 'zh-TW', 'fr', 'ja', 'ru', 'vi'].includes(
    resolvedLanguage
  )
    ? resolvedLanguage
    : 'en'
  const badgeURL = useMemo(
    () =>
      shareQuery.data?.url
        ? buildBadgeURL(shareQuery.data.url, options, badgeLanguage)
        : '',
    [badgeLanguage, options, shareQuery.data?.url]
  )

  useEffect(() => {
    if (!badgeURL) {
      setPreviewURL('')
      return
    }
    const timeout = window.setTimeout(() => {
      setPreviewFailed(false)
      setPreviewURL(badgeURL)
    }, 400)
    return () => window.clearTimeout(timeout)
  }, [badgeURL])

  const readmeCode = badgeURL
    ? `[![LMM Best token usage](${badgeURL})](${PROFILE_SHARE_URL})`
    : ''
  const htmlCode = badgeURL
    ? `<a href="${PROFILE_SHARE_URL}" target="_blank" rel="noopener noreferrer"><img src="${badgeURL.replaceAll('&', '&amp;')}" alt="LMM Best token usage" width="${options.width}" height="${options.height}"></a>`
    : ''

  const updateOption = <K extends keyof BadgeOptions>(
    key: K,
    value: BadgeOptions[K]
  ) => setOptions((previous) => ({ ...previous, [key]: value }))
  const updateColor = (key: keyof BadgeColors, value: string) =>
    setOptions((previous) => ({
      ...previous,
      colors: { ...previous.colors, [key]: value },
    }))

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Public SVG badge')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='mx-auto max-w-6xl space-y-6 pb-10'>
          <div className='space-y-2'>
            <Link
              to='/profile'
              className={buttonVariants({ variant: 'link', size: 'sm' })}
            >
              <HugeiconsIcon icon={ArrowLeft01Icon} strokeWidth={2} />
              {t('Profile')}
            </Link>
            <h1 className='text-foreground text-2xl font-semibold tracking-tight sm:text-3xl'>
              {t('Show your token usage anywhere')}
            </h1>
            <p className='text-muted-foreground max-w-2xl text-sm leading-relaxed'>
              {t(
                'Create an animated SVG from your real usage, then copy a clickable snippet for GitHub or your website.'
              )}
            </p>
          </div>

          {shareQuery.isPending ? (
            <Skeleton className='h-28 w-full rounded-2xl' />
          ) : shareQuery.isError ? (
            <div className='border-destructive/30 rounded-xl border p-5'>
              <p className='text-destructive text-sm'>
                {t('Could not load badge settings.')}
              </p>
              <Button
                variant='outline'
                className='mt-3'
                onClick={() => void shareQuery.refetch()}
              >
                {t('Retry')}
              </Button>
            </div>
          ) : (
            <div className='border-border/70 bg-card/40 flex flex-col gap-4 rounded-2xl border p-4 sm:flex-row sm:items-center sm:justify-between sm:p-5'>
              <div className='space-y-1'>
                <h2 className='font-medium'>
                  {shareQuery.data?.enabled
                    ? t('Your public badge is on')
                    : t('Your public badge is off')}
                </h2>
                <p className='text-muted-foreground text-sm leading-relaxed'>
                  {shareQuery.data?.enabled
                    ? t(
                        'Anyone with the image URL can see the usage figures you choose to show.'
                      )
                    : t(
                        'Turn it on to create a public image URL. You can turn it off at any time.'
                      )}
                </p>
              </div>
              {shareQuery.data?.enabled ? (
                <Button
                  variant='outline'
                  disabled={disableMutation.isPending}
                  onClick={() => void disableMutation.mutate()}
                >
                  {t('Turn off public badge')}
                </Button>
              ) : (
                <Button
                  disabled={enableMutation.isPending}
                  onClick={() => void enableMutation.mutate()}
                >
                  {t('Turn on public badge')}
                </Button>
              )}
            </div>
          )}

          <div className='grid items-start gap-6 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.12fr)]'>
            <section className='border-border/70 space-y-6 rounded-2xl border p-4 sm:p-6'>
              <div>
                <h2 className='text-lg font-medium'>
                  {t('Customize appearance')}
                </h2>
                <p className='text-muted-foreground mt-1 text-sm'>
                  {t('Every option becomes part of your SVG URL.')}
                </p>
              </div>

              <div className='grid gap-4 sm:grid-cols-2'>
                <div className='space-y-2'>
                  <Label htmlFor='badge-layout'>{t('Layout')}</Label>
                  <NativeSelect
                    id='badge-layout'
                    className='w-full'
                    value={options.layout}
                    onChange={(event) => {
                      const layout = event.target.value as BadgeLayout
                      setOptions((previous) => ({
                        ...previous,
                        layout,
                        period: layout === 'profile' ? '365d' : '30d',
                        width: layout === 'profile' ? 1200 : 800,
                        height: layout === 'profile' ? 865 : 240,
                        radius: layout === 'profile' ? 0 : 24,
                      }))
                    }}
                  >
                    <NativeSelectOption value='profile'>
                      {t('Profile overview')}
                    </NativeSelectOption>
                    <NativeSelectOption value='badge'>
                      {t('Compact badge')}
                    </NativeSelectOption>
                  </NativeSelect>
                </div>
                <div className='space-y-2'>
                  <Label htmlFor='badge-theme'>{t('Theme')}</Label>
                  <NativeSelect
                    id='badge-theme'
                    className='w-full'
                    value={options.theme}
                    onChange={(event) => {
                      const theme = event.target.value as BadgeTheme
                      setOptions((previous) => ({
                        ...previous,
                        theme,
                        colors: BADGE_PALETTES[theme],
                      }))
                    }}
                  >
                    <NativeSelectOption value='paper'>
                      {t('Paper')}
                    </NativeSelectOption>
                    <NativeSelectOption value='dark'>
                      {t('Dark')}
                    </NativeSelectOption>
                    <NativeSelectOption value='transparent'>
                      {t('Transparent')}
                    </NativeSelectOption>
                  </NativeSelect>
                </div>
                {options.layout === 'badge' ? (
                  <div className='space-y-2'>
                    <Label htmlFor='badge-period'>{t('Usage period')}</Label>
                    <NativeSelect
                      id='badge-period'
                      className='w-full'
                      value={options.period}
                      onChange={(event) =>
                        updateOption(
                          'period',
                          event.target.value as BadgePeriod
                        )
                      }
                    >
                      <NativeSelectOption value='7d'>
                        {t('Last 7 days')}
                      </NativeSelectOption>
                      <NativeSelectOption value='30d'>
                        {t('Last 30 days')}
                      </NativeSelectOption>
                      <NativeSelectOption value='365d'>
                        {t('Last 365 days')}
                      </NativeSelectOption>
                      <NativeSelectOption value='all'>
                        {t('All time')}
                      </NativeSelectOption>
                    </NativeSelect>
                  </div>
                ) : null}
                <div className='space-y-2'>
                  <Label htmlFor='badge-animation'>{t('Animation')}</Label>
                  <NativeSelect
                    id='badge-animation'
                    className='w-full'
                    value={options.animation}
                    onChange={(event) =>
                      updateOption(
                        'animation',
                        event.target.value as BadgeAnimation
                      )
                    }
                  >
                    <NativeSelectOption value='wave'>
                      {t('Wave')}
                    </NativeSelectOption>
                    <NativeSelectOption value='pulse'>
                      {t('Pulse')}
                    </NativeSelectOption>
                    <NativeSelectOption value='none'>
                      {t('No animation')}
                    </NativeSelectOption>
                  </NativeSelect>
                </div>
                <div className='space-y-2'>
                  <Label htmlFor='badge-font'>{t('Font')}</Label>
                  <NativeSelect
                    id='badge-font'
                    className='w-full'
                    value={options.font}
                    onChange={(event) =>
                      updateOption('font', event.target.value as BadgeFont)
                    }
                  >
                    <NativeSelectOption value='sans'>
                      {t('Sans serif')}
                    </NativeSelectOption>
                    <NativeSelectOption value='mono'>
                      {t('Monospace')}
                    </NativeSelectOption>
                    <NativeSelectOption value='serif'>
                      {t('Serif')}
                    </NativeSelectOption>
                  </NativeSelect>
                </div>
                <div className='space-y-2'>
                  <Label htmlFor='badge-format'>{t('Number format')}</Label>
                  <NativeSelect
                    id='badge-format'
                    className='w-full'
                    value={options.format}
                    onChange={(event) =>
                      updateOption('format', event.target.value as BadgeFormat)
                    }
                  >
                    <NativeSelectOption value='compact'>
                      {t('Compact')}
                    </NativeSelectOption>
                    <NativeSelectOption value='full'>
                      {t('Full number')}
                    </NativeSelectOption>
                  </NativeSelect>
                </div>
                <div className='flex items-end justify-between gap-4 pb-2'>
                  <Label htmlFor='badge-requests'>
                    {t('Show API requests')}
                  </Label>
                  <Switch
                    id='badge-requests'
                    checked={options.requests}
                    onCheckedChange={(checked) =>
                      updateOption('requests', checked)
                    }
                  />
                </div>
              </div>

              <div className='border-border/70 grid gap-4 border-t pt-5 sm:grid-cols-3'>
                {(
                  [
                    ['width', t('Width'), 480, 1600],
                    ['height', t('Height'), 200, 1200],
                    ['radius', t('Corner radius'), 0, 48],
                  ] as const
                ).map(([key, label, min, max]) => (
                  <div key={key} className='space-y-2'>
                    <Label htmlFor={`badge-${key}`}>{label}</Label>
                    <Input
                      key={`${options.layout}-${key}`}
                      id={`badge-${key}`}
                      type='number'
                      min={min}
                      max={max}
                      defaultValue={options[key]}
                      onBlur={(event) => {
                        const value = clampNumber(
                          event.target.value,
                          options[key],
                          min,
                          max
                        )
                        event.target.value = String(value)
                        updateOption(key, value)
                      }}
                      onKeyDown={(event) => {
                        if (event.key === 'Enter') event.currentTarget.blur()
                      }}
                    />
                  </div>
                ))}
              </div>

              <div className='border-border/70 space-y-4 border-t pt-5'>
                <h3 className='font-medium'>{t('Colors')}</h3>
                <div className='grid gap-3 sm:grid-cols-2'>
                  {(
                    [
                      ['bg', t('Background')],
                      ['fg', t('Text color')],
                      ['accent', t('Accent')],
                      ['muted', t('Secondary text')],
                      ['border', t('Border')],
                    ] as const
                  )
                    .filter(
                      ([key]) => key !== 'bg' || options.theme !== 'transparent'
                    )
                    .map(([key, label]) => (
                      <div key={key} className='flex items-center gap-3'>
                        <Input
                          id={`badge-color-${key}`}
                          type='color'
                          value={options.colors[key]}
                          onChange={(event) =>
                            updateColor(key, event.target.value)
                          }
                          className='h-10 w-14 cursor-pointer p-1'
                        />
                        <Label
                          htmlFor={`badge-color-${key}`}
                          className='min-w-0'
                        >
                          {label}
                          <span className='text-muted-foreground ml-2 font-mono text-xs'>
                            {options.colors[key]}
                          </span>
                        </Label>
                      </div>
                    ))}
                </div>
              </div>

              <div className='border-border/70 space-y-4 border-t pt-5'>
                <h3 className='font-medium'>{t('Custom text')}</h3>
                {(
                  [
                    ['title', t('Title'), 60],
                    ['label', t('Token label'), 32],
                    [
                      'footer',
                      options.layout === 'profile'
                        ? t('Footer text')
                        : t('Period caption'),
                      48,
                    ],
                  ] as const
                ).map(([key, label, maxLength]) => (
                  <div key={key} className='space-y-2'>
                    <Label htmlFor={`badge-${key}`}>{label}</Label>
                    <Input
                      id={`badge-${key}`}
                      value={options[key]}
                      maxLength={maxLength}
                      placeholder={t('Leave blank for the default')}
                      onChange={(event) =>
                        updateOption(key, event.target.value)
                      }
                    />
                  </div>
                ))}
              </div>
            </section>

            <section className='space-y-5 lg:sticky lg:top-4'>
              <div className='border-border/70 space-y-4 rounded-2xl border p-4 sm:p-6'>
                <h2 className='text-lg font-medium'>{t('Live SVG preview')}</h2>
                {previewURL && shareQuery.data?.enabled ? (
                  previewFailed ? (
                    <p role='alert' className='text-destructive text-sm'>
                      {t('Preview failed. Check the settings and try again.')}
                    </p>
                  ) : (
                    <>
                      <img
                        src={previewURL}
                        alt={t('Preview of your token usage badge')}
                        width={options.width}
                        height={options.height}
                        onError={() => setPreviewFailed(true)}
                        className='h-auto w-full'
                      />
                      <a
                        className={buttonVariants({
                          variant: 'outline',
                          size: 'sm',
                          className: 'w-full sm:w-auto',
                        })}
                        href={badgeURL}
                        target='_blank'
                        rel='noopener noreferrer'
                      >
                        {t('Open image')}
                      </a>
                    </>
                  )
                ) : (
                  <div className='bg-muted/50 text-muted-foreground flex min-h-48 items-center justify-center rounded-xl px-6 text-center text-sm'>
                    {t('Turn on the public badge to preview your real usage.')}
                  </div>
                )}
                <p className='text-muted-foreground text-xs leading-relaxed'>
                  {t('Usage updates periodically. GitHub may cache the image.')}
                </p>
              </div>

              {badgeURL ? (
                <div className='border-border/70 space-y-5 rounded-2xl border p-4 sm:p-6'>
                  <div>
                    <h2 className='text-lg font-medium'>
                      {t('Copy embed code')}
                    </h2>
                    <p className='text-muted-foreground mt-1 text-sm'>
                      {t(
                        'Paste the whole snippet so clicking the image opens LMM Best.'
                      )}
                    </p>
                  </div>
                  {(
                    [
                      ['GitHub README', readmeCode, t('Copy README code')],
                      ['Website HTML', htmlCode, t('Copy HTML code')],
                      ['SVG URL', badgeURL, t('Copy SVG URL')],
                    ] as const
                  ).map(([heading, value, copyLabel]) => (
                    <div key={heading} className='space-y-2'>
                      <div className='flex items-center justify-between gap-2'>
                        <h3 className='text-sm font-medium'>{heading}</h3>
                        <CopyButton
                          value={value}
                          variant='outline'
                          size='sm'
                          aria-label={copyLabel}
                        >
                          {copyLabel}
                        </CopyButton>
                      </div>
                      <textarea
                        readOnly
                        value={value}
                        aria-label={heading}
                        className='border-border bg-muted/40 focus-visible:ring-ring/30 min-h-20 w-full resize-y rounded-xl border p-3 font-mono text-xs break-all focus-visible:ring-3 focus-visible:outline-none'
                      />
                    </div>
                  ))}
                </div>
              ) : null}
            </section>
          </div>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
