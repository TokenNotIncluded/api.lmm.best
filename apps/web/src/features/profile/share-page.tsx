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
import { useReducedMotion } from 'motion/react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { CopyButton } from '@/components/copy-button'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { Button, buttonVariants } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'

import { disableProfileShare, enableProfileShare, getProfileShareState } from './api'
import { ModelUsageReport } from './components/model-usage-report'
import { useProfile } from './hooks'
import {
  BADGE_PALETTES, BADGE_STYLE_PRESETS, INITIAL_OPTIONS,
  buildBadgeURL, buildBadgeEmbedCode, canShareBadge, changeBadgeLayout,
  clampNumber, isPresetActive,
  type BadgeOptions, type BadgeColors, type BadgeLayout, type BadgeTheme,
  type BadgePeriod, type BadgeAnimation, type BadgeFont, type BadgeFormat,
} from './lib/share-badge'
import type { ModelUsageRangeKey } from './lib/model-usage'
import { registerProfileShareTranslations } from './share-i18n'

export function ProfileSharePage() {
  const { t, i18n } = useTranslation()
  registerProfileShareTranslations(i18n)
  const reduceMotion = useReducedMotion()
  const queryClient = useQueryClient()
  const { profile } = useProfile()
  const [options, setOptions] = useState<BadgeOptions>(INITIAL_OPTIONS)
  const [showStatistics, setShowStatistics] = useState(false)
  const [reportRange, setReportRange] = useState<ModelUsageRangeKey>('30d')
  const [previewURL, setPreviewURL] = useState('')
  const [failedURL, setFailedURL] = useState('')
  const [previewAttempt, setPreviewAttempt] = useState(0)
  const shareQuery = useQuery({ queryKey: ['profile-share'], queryFn: getProfileShareState, retry: 1 })
  const enableMutation = useMutation({
    mutationFn: (modelUsage?: boolean) => enableProfileShare(modelUsage),
    onSuccess: (data, modelUsage) => {
      queryClient.setQueryData(['profile-share'], data)
      toast.success(t(modelUsage === false ? 'Public SVG badge disabled' : 'Public SVG badge enabled'))
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
  const publicEnabled = canShareBadge(shareQuery.data, options.layout)
  const badgeURL = useMemo(
    () => publicEnabled && shareQuery.data?.url
      ? buildBadgeURL(shareQuery.data.url, reduceMotion ? { ...options, animation: 'none' } : options, i18n.resolvedLanguage || i18n.language)
      : '',
    [publicEnabled, options, reduceMotion, shareQuery.data?.url, i18n.resolvedLanguage, i18n.language]
  )
  useEffect(() => {
    if (!badgeURL) { setPreviewURL(''); return }
    const timeout = window.setTimeout(() => { setFailedURL(''); setPreviewURL(badgeURL) }, 400)
    return () => window.clearTimeout(timeout)
  }, [badgeURL])

  const { markdown: readmeCode, html: htmlCode } = buildBadgeEmbedCode(badgeURL, options)
  const changeLayout = (layout: BadgeLayout) => setOptions((previous) => ({
    ...changeBadgeLayout(previous, layout),
    ...(layout === 'models' ? { period: reportRange } : {}),
  }))
  const changeReportRange = (period: ModelUsageRangeKey) => {
    setReportRange(period)
    setOptions((previous) => previous.layout === 'models' ? { ...previous, period } : previous)
  }
  const updateOption = <K extends keyof BadgeOptions>(key: K, value: BadgeOptions[K]) => setOptions((previous) => ({ ...previous, [key]: value }))
  const updateColor = (key: keyof BadgeColors, value: string) => setOptions((previous) => ({ ...previous, colors: { ...previous.colors, [key]: value } }))

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Public SVG badge')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='mx-auto max-w-6xl space-y-6 pb-10'>
          <div className='space-y-2'>
            <Link to='/profile' className={buttonVariants({ variant: 'link', size: 'sm' })}>
              <HugeiconsIcon icon={ArrowLeft01Icon} strokeWidth={2} />{t('Profile')}
            </Link>
            <h1 className='text-foreground text-lg font-semibold tracking-tight'>{t('Show your token usage anywhere')}</h1>
            <p className='text-muted-foreground max-w-2xl text-sm leading-relaxed'>
              {t('Create an animated SVG from your real usage, then copy a clickable snippet for GitHub or your website.')}
            </p>
          </div>
          {shareQuery.isPending ? <Skeleton className='h-28 w-full rounded-2xl' /> : shareQuery.isError ? (
            <ErrorState title={t('Could not load badge settings.')} description={t('Check your connection and try again.')} onRetry={() => void shareQuery.refetch()} />
          ) : (
            <div className='border-border/70 bg-card/40 flex flex-col gap-4 rounded-2xl border p-4 sm:flex-row sm:items-center sm:justify-between sm:p-5'>
              <div className='space-y-1'>
                <h2 className='font-medium'>{publicEnabled ? t('Your public badge is on') : t('Your public badge is off')}</h2>
                <p className='text-muted-foreground max-w-2xl text-sm leading-relaxed'>
                  {options.layout === 'models' ? t('Model names, tokens, requests and platform spend become public. Appearance options do not restrict access.') : t('Turn it on to create a public image URL. You can turn it off at any time.')}
                </p>
              </div>
              {options.layout === 'models' ? (
                <Button variant={publicEnabled ? 'outline' : 'default'} disabled={enableMutation.isPending || disableMutation.isPending} onClick={() => void enableMutation.mutate(!publicEnabled)}>
                  {publicEnabled ? t('Turn off model sharing') : t('Turn on model sharing')}
                </Button>
              ) : shareQuery.data?.enabled ? (
                <Button variant='outline' disabled={disableMutation.isPending || enableMutation.isPending} onClick={() => void disableMutation.mutate()}>{t('Turn off public badge')}</Button>
              ) : (
                <Button disabled={enableMutation.isPending || disableMutation.isPending} onClick={() => void enableMutation.mutate(undefined)}>{t('Turn on public badge')}</Button>
              )}
            </div>
          )}
          <div className='grid items-start gap-6 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.12fr)]'>
            <section className='border-border/70 space-y-6 rounded-2xl border p-4 sm:p-6'>
              <div>
                <h2 className='text-lg font-medium'>{t('Customize appearance')}</h2>
                <p className='text-muted-foreground mt-1 text-sm'>{t('Every option becomes part of your SVG URL.')}</p>
              </div>
              <div className='border-border/70 space-y-3 border-b pb-5'>
                <Label>{t('Theme')}</Label>
                <div className='grid grid-cols-2 gap-2 sm:grid-cols-4'>
                  {BADGE_STYLE_PRESETS.map((preset) => {
                    const active = isPresetActive(options, preset)
                    return (
                      <button key={preset.name} type='button' aria-pressed={active}
                        onClick={() => setOptions((previous) => ({ ...previous, theme: preset.theme, animation: preset.animation, font: preset.font, radius: preset.radius, colors: { ...preset.colors } }))}
                        className={`border-border/70 hover:border-foreground/30 focus-visible:ring-ring/30 flex min-h-16 items-center gap-3 rounded-xl border px-3 py-2 text-left transition-colors focus-visible:ring-3 focus-visible:outline-none ${active ? 'border-foreground/40 bg-muted/60' : 'bg-card/40'}`}>
                        <span aria-hidden='true' className='relative size-8 shrink-0 overflow-hidden rounded-lg border' style={{ backgroundColor: preset.theme === 'transparent' ? 'transparent' : preset.colors.bg, borderColor: preset.colors.border }}>
                          <span className='absolute inset-x-1 bottom-1 h-1.5 rounded-full' style={{ backgroundColor: preset.colors.accent }} />
                        </span>
                        <span className='min-w-0 text-xs font-medium'>{preset.name}</span>
                      </button>
                    )
                  })}
                </div>
              </div>
              <div className='grid gap-4 sm:grid-cols-2'>
                <div className='space-y-2'>
                  <Label htmlFor='badge-layout'>{t('Layout')}</Label>
                  <NativeSelect id='badge-layout' className='w-full' value={options.layout} onChange={(event) => changeLayout(event.target.value as BadgeLayout)}>
                    <NativeSelectOption value='models'>{t('Model by model')}</NativeSelectOption>
                    <NativeSelectOption value='profile'>{t('Profile overview')}</NativeSelectOption>
                    <NativeSelectOption value='badge'>{t('Compact badge')}</NativeSelectOption>
                  </NativeSelect>
                </div>
                <div className='space-y-2'>
                  <Label htmlFor='badge-theme'>{t('Theme')}</Label>
                  <NativeSelect id='badge-theme' className='w-full' value={options.theme} onChange={(event) => {
                    const theme = event.target.value as BadgeTheme
                    setOptions((previous) => ({ ...previous, theme, colors: BADGE_PALETTES[theme] }))
                  }}>
                    <NativeSelectOption value='paper'>{t('Paper')}</NativeSelectOption>
                    <NativeSelectOption value='dark'>{t('Dark')}</NativeSelectOption>
                    <NativeSelectOption value='transparent'>{t('Transparent')}</NativeSelectOption>
                  </NativeSelect>
                </div>
                {options.layout !== 'profile' ? (
                  <div className='space-y-2'>
                    <Label htmlFor='badge-period'>{t('Usage period')}</Label>
                    <NativeSelect id='badge-period' className='w-full' value={options.period} onChange={(event) => options.layout === 'models' ? changeReportRange(event.target.value as ModelUsageRangeKey) : updateOption('period', event.target.value as BadgePeriod)}>
                      <NativeSelectOption value='7d'>{t('Last 7 days')}</NativeSelectOption>
                      <NativeSelectOption value='30d'>{t('Last 30 days')}</NativeSelectOption>
                      <NativeSelectOption value='365d'>{t('Last 365 days')}</NativeSelectOption>
                      {options.layout === 'badge' ? <NativeSelectOption value='all'>{t('All time')}</NativeSelectOption> : null}
                    </NativeSelect>
                  </div>
                ) : null}
                {options.layout === 'models' ? (
                  <div className='space-y-2'>
                    <Label htmlFor='badge-top'>{t('Models to display')}</Label>
                    <NativeSelect id='badge-top' className='w-full' value={options.top} onChange={(event) => updateOption('top', Number(event.target.value))}>
                      {[3, 6, 9, 12].map((count) => <NativeSelectOption key={count} value={count}>{count}</NativeSelectOption>)}
                    </NativeSelect>
                    <p className='text-muted-foreground text-xs'>{t('Remaining models are grouped together.')}</p>
                  </div>
                ) : null}
                <div className='space-y-2'>
                  <Label htmlFor='badge-animation'>{t('Animation')}</Label>
                  <NativeSelect id='badge-animation' className='w-full' value={options.animation} onChange={(event) => updateOption('animation', event.target.value as BadgeAnimation)}>
                    <NativeSelectOption value='wave'>{t('Wave')}</NativeSelectOption>
                    <NativeSelectOption value='pulse'>{t('Pulse')}</NativeSelectOption>
                    <NativeSelectOption value='none'>{t('No animation')}</NativeSelectOption>
                  </NativeSelect>
                </div>
                <div className='space-y-2'>
                  <Label htmlFor='badge-font'>{t('Font')}</Label>
                  <NativeSelect id='badge-font' className='w-full' value={options.font} onChange={(event) => updateOption('font', event.target.value as BadgeFont)}>
                    <NativeSelectOption value='sans'>{t('Sans serif')}</NativeSelectOption>
                    <NativeSelectOption value='mono'>{t('Monospace')}</NativeSelectOption>
                    <NativeSelectOption value='serif'>{t('Serif')}</NativeSelectOption>
                  </NativeSelect>
                </div>
                <div className='space-y-2'>
                  <Label htmlFor='badge-format'>{t('Number format')}</Label>
                  <NativeSelect id='badge-format' className='w-full' value={options.format} onChange={(event) => updateOption('format', event.target.value as BadgeFormat)}>
                    <NativeSelectOption value='compact'>{t('Compact')}</NativeSelectOption>
                    <NativeSelectOption value='full'>{t('Full number')}</NativeSelectOption>
                  </NativeSelect>
                </div>
                <div className='flex items-end justify-between gap-4 pb-2'>
                  <Label htmlFor='badge-requests'>{t('Show API requests')}</Label>
                  <Switch id='badge-requests' checked={options.requests} onCheckedChange={(checked) => updateOption('requests', checked)} />
                </div>
              </div>
              <div className='border-border/70 grid gap-4 border-t pt-5 sm:grid-cols-3'>
                {([['width', t('Width'), 480, 1600], ['height', t('Height'), 200, 1200], ['radius', t('Corner radius'), 0, 48]] as const).map(([key, label, min, max]) => (
                  <div key={key} className='space-y-2'>
                    <Label htmlFor={`badge-${key}`}>{label}</Label>
                    <Input key={`${options.layout}-${key}-${options[key]}`} id={`badge-${key}`} type='number' min={min} max={max} defaultValue={options[key]}
                      onBlur={(event) => { const value = clampNumber(event.target.value, options[key], min, max); event.target.value = String(value); updateOption(key, value) }}
                      onKeyDown={(event) => { if (event.key === 'Enter') event.currentTarget.blur() }} />
                  </div>
                ))}
              </div>
              <div className='border-border/70 space-y-4 border-t pt-5'>
                <h3 className='font-medium'>{t('Colors')}</h3>
                <div className='grid gap-3 sm:grid-cols-2'>
                  {([['bg', t('Background')], ['fg', t('Text color')], ['accent', t('Accent')], ['muted', t('Secondary text')], ['border', t('Border')]] as const)
                    .filter(([key]) => key !== 'bg' || options.theme !== 'transparent')
                    .map(([key, label]) => (
                      <div key={key} className='flex items-center gap-3'>
                        <Input id={`badge-color-${key}`} type='color' value={options.colors[key]} onChange={(event) => updateColor(key, event.target.value)} className='h-10 w-14 cursor-pointer p-1' />
                        <Label htmlFor={`badge-color-${key}`} className='min-w-0'>{label}<span className='text-muted-foreground ml-2 font-mono text-xs'>{options.colors[key]}</span></Label>
                      </div>
                    ))}
                </div>
              </div>
              <div className='border-border/70 space-y-4 border-t pt-5'>
                <h3 className='font-medium'>{t('Custom text')}</h3>
                {([['title', t('Title'), 60], ['label', t('Token label'), 32], ['footer', options.layout === 'profile' ? t('Footer text') : t('Period caption'), 48]] as const).map(([key, label, maxLength]) => (
                  <div key={key} className='space-y-2'>
                    <Label htmlFor={`badge-${key}`}>{label}</Label>
                    <Input id={`badge-${key}`} value={options[key]} maxLength={maxLength} placeholder={t('Leave blank for the default')} onChange={(event) => updateOption(key, event.target.value)} />
                  </div>
                ))}
              </div>
            </section>
            <section className='space-y-5 lg:sticky lg:top-4'>
              <div className='border-border/70 space-y-4 rounded-2xl border p-4 sm:p-6'>
                <h2 className='text-lg font-medium'>{t('Live SVG preview')}</h2>
                {badgeURL ? previewURL !== badgeURL ? <Skeleton className='h-80 w-full rounded-xl' /> : failedURL === previewURL ? (
                  <ErrorState title={t('Preview failed. Check the settings and try again.')} onRetry={() => { setFailedURL(''); setPreviewAttempt((value) => value + 1) }} />
                ) : (
                  <>
                    <img key={`${previewURL}-${previewAttempt}`} src={previewURL} alt={t('Preview of your token usage badge')} width={options.width} height={options.height} onError={() => setFailedURL(previewURL)} className='h-auto w-full' />
                    <a className={buttonVariants({ variant: 'outline', size: 'sm', className: 'w-full sm:w-auto' })} href={badgeURL} target='_blank' rel='noopener noreferrer'>{t('Open image')}</a>
                  </>
                ) : (
                  <div className='bg-muted/50 text-muted-foreground flex min-h-48 items-center justify-center rounded-xl px-6 text-center text-sm'>
                    {options.layout === 'models' ? t('Turn on model sharing to preview your real usage.') : t('Turn on the public badge to preview your real usage.')}
                  </div>
                )}
                <p className='text-muted-foreground text-xs leading-relaxed'>{t('Usage updates periodically. GitHub may cache the image.')}</p>
              </div>
              {badgeURL ? (
                <div className='border-border/70 space-y-5 rounded-2xl border p-4 sm:p-6'>
                  <div>
                    <h2 className='text-lg font-medium'>{t('Copy embed code')}</h2>
                    <p className='text-muted-foreground mt-1 text-sm'>{t('Paste the whole snippet so clicking the image opens LMM Best.')}</p>
                  </div>
                  {([['GitHub README', readmeCode, t('Copy README code')], ['Website HTML', htmlCode, t('Copy HTML code')], ['SVG URL', badgeURL, t('Copy SVG URL')]] as const).map(([heading, value, copyLabel]) => (
                    <div key={heading} className='space-y-2'>
                      <div className='flex items-center justify-between gap-2'>
                        <h3 className='text-sm font-medium'>{heading}</h3>
                        <CopyButton value={value} variant='outline' size='sm' aria-label={copyLabel}>{copyLabel}</CopyButton>
                      </div>
                      <textarea readOnly value={value} aria-label={heading} className='border-border bg-muted/40 focus-visible:ring-ring/30 min-h-20 w-full resize-y rounded-xl border p-3 font-mono text-xs break-all focus-visible:ring-3 focus-visible:outline-none' />
                    </div>
                  ))}
                </div>
              ) : null}
            </section>
          </div>
          <details className='border-border/70 rounded-2xl border p-4 sm:p-6' onToggle={(event) => setShowStatistics(event.currentTarget.open)}>
            <summary className='cursor-pointer text-sm font-medium'>{t('View full model statistics')}</summary>
            {showStatistics ? (
              <div className='pt-5'><ModelUsageReport accountCreatedTime={profile?.created_time} rangeKey={reportRange} onRangeKeyChange={changeReportRange} /></div>
            ) : null}
          </details>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
