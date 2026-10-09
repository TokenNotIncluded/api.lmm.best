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
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { ArrowRight, CodeXml } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { RichContent } from '@/components/rich-content'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { useStatus } from '@/hooks/use-status'
import { useSystemConfig } from '@/hooks/use-system-config'
import { getBuildVersion } from '@/lib/build-metadata'
import { isHttpUrl, isLikelyHtml } from '@/lib/content-format'

import { ForgePublicShell } from '../forge/forge-public-shell'
import { getAboutContent } from './api'

const ABOUT_FACTS = [
  ['AGPL-3.0', 'Open-source code'],
  ['1', 'One gateway for supported models'],
  ['Community', 'Tools, store, and open-source challenges'],
] as const

function EmptyAboutState() {
  const { t } = useTranslation()
  const { systemName } = useSystemConfig()
  const { status, capabilitiesReady } = useStatus()
  const apiVersion = capabilitiesReady
    ? status?.version?.trim() || status?.data?.version?.trim()
    : undefined

  return (
    <main className='mx-auto max-w-5xl px-5 pt-32 pb-24 md:px-10 md:pt-40'>
      <p className='mb-5 flex items-center gap-2 text-xs font-bold uppercase'>
        <span className='bg-foreground size-2 rounded-full' />
        {t('About')} · {systemName}
      </p>
      <h1 className='max-w-3xl font-serif text-5xl leading-[1.02] font-normal md:text-7xl'>
        {t('Open-source work, made accountable.')}
      </h1>
      <p className='text-muted-foreground mt-6 max-w-2xl text-base leading-7 md:text-lg'>
        {t(
          'Access supported AI models through one API, manage keys and usage, and explore tools, the store, and open-source challenges.'
        )}
      </p>

      <dl className='mt-12 grid gap-8 py-8 sm:grid-cols-3'>
        {ABOUT_FACTS.map(([value, label]) => (
          <div key={label}>
            <dt className='text-3xl font-semibold tracking-tight'>
              {t(value)}
            </dt>
            <dd className='text-muted-foreground mt-2 text-sm leading-6'>
              {t(label)}
            </dd>
          </div>
        ))}
      </dl>

      <div className='mt-10 flex flex-wrap items-center gap-x-6 gap-y-4'>
        <Button
          size='lg'
          render={
            <Link to='/sign-in' search={{ redirect: '/getting-started' }} />
          }
        >
          {t('Sign in to get started')}
          <ArrowRight data-icon='inline-end' />
        </Button>
        <Link
          to='/guide'
          className='text-sm font-medium underline underline-offset-4'
        >
          {t('Read the guide')}
        </Link>
        <Link
          to='/challenges'
          className='text-sm font-medium underline underline-offset-4'
        >
          {t('Browse challenges')}
        </Link>
      </div>

      <div className='border-foreground mt-14 flex items-start gap-3 border-t-2 pt-5 text-sm leading-6'>
        <CodeXml className='mt-0.5 size-5 shrink-0' aria-hidden='true' />
        <div className='text-muted-foreground flex flex-wrap items-center gap-x-6 gap-y-2'>
          <a
            href='https://github.com/TokenNotIncluded/api.lmm.best/blob/main/LICENSE'
            target='_blank'
            rel='noopener noreferrer'
            className='border-foreground text-foreground border-b hover:opacity-70'
          >
            {t('AGPL v3.0 License')}
          </a>
          <p>
            {t('API')} {apiVersion ?? t('Unknown version')} · {t('Web')}{' '}
            {getBuildVersion()}
          </p>
        </div>
      </div>
    </main>
  )
}

export function About() {
  const { t } = useTranslation()
  const { data, isLoading, isError, refetch } = useQuery({
    queryKey: ['about-content'],
    queryFn: getAboutContent,
  })

  const rawContent = data?.data?.trim() ?? ''
  const hasContent = rawContent.length > 0
  const isUrl = hasContent && isHttpUrl(rawContent)
  const contentIsHtml = hasContent && isLikelyHtml(rawContent)

  if (isLoading) {
    return (
      <ForgePublicShell>
        <main className='mx-auto flex max-w-4xl flex-col gap-4 px-5 pt-32 pb-24 md:px-10'>
          <Skeleton className='h-8 w-[45%]' />
          <Skeleton className='h-4 w-full' />
          <Skeleton className='h-4 w-[90%]' />
          <Skeleton className='h-4 w-[80%]' />
        </main>
      </ForgePublicShell>
    )
  }

  if (isError || data?.success === false) {
    return (
      <ForgePublicShell>
        <main className='mx-auto max-w-4xl px-5 pt-32 pb-24 md:px-10'>
          <ErrorState
            title={t('Failed to load')}
            onRetry={() => void refetch()}
          />
        </main>
      </ForgePublicShell>
    )
  }

  if (!hasContent) {
    return (
      <ForgePublicShell>
        <EmptyAboutState />
      </ForgePublicShell>
    )
  }

  if (isUrl) {
    return (
      <ForgePublicShell>
        <iframe
          src={rawContent}
          className='h-[calc(100svh-4rem)] w-full border-0 pt-16'
          title={t('About')}
          sandbox='allow-forms allow-popups allow-popups-to-escape-sandbox allow-scripts'
        />
      </ForgePublicShell>
    )
  }

  if (contentIsHtml) {
    return (
      <ForgePublicShell>
        <RichContent
          mode='html'
          htmlVariant='isolated'
          content={rawContent}
          className='forge-rich-content prose-neutral dark:prose-invert max-w-none'
        />
      </ForgePublicShell>
    )
  }

  return (
    <ForgePublicShell>
      <main className='mx-auto max-w-6xl px-5 pt-32 pb-24 md:px-10'>
        <RichContent
          mode='markdown'
          collapsible
          content={rawContent}
          className='prose-neutral dark:prose-invert max-w-none'
        />
      </main>
    </ForgePublicShell>
  )
}
