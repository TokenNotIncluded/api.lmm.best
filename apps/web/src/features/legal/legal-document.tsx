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
import { FileWarning } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { RichContent } from '@/components/rich-content'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { ForgePublicShell } from '@/features/forge/forge-public-shell'
import { isHttpUrl, isLikelyHtml } from '@/lib/content-format'

import { splitLegalSections } from './legal-reader'
import type { LegalDocumentResponse } from './types'

import './legal-reading.css'

type LegalDocumentProps = {
  title: string
  queryKey: string
  fetchDocument: (language: string) => Promise<LegalDocumentResponse>
  emptyMessage: string
}

/** How far the reader has scrolled through the document body, in percent. */
function useReadingProgress(
  targetRef: React.RefObject<HTMLElement | null>,
  enabled: boolean
) {
  const [progress, setProgress] = useState(0)

  useEffect(() => {
    const node = targetRef.current
    if (!node || !enabled) return
    let frame = 0
    const update = () => {
      frame = 0
      const { top, height } = node.getBoundingClientRect()
      const viewport = window.innerHeight
      const total = height - viewport
      const ratio = total <= 0 ? 1 : -top / total
      setProgress(Math.min(1, Math.max(0, ratio)))
    }
    const schedule = () => {
      if (frame) return
      frame = window.requestAnimationFrame(update)
    }
    update()
    window.addEventListener('scroll', schedule, { passive: true })
    window.addEventListener('resize', schedule)
    return () => {
      if (frame) window.cancelAnimationFrame(frame)
      window.removeEventListener('scroll', schedule)
      window.removeEventListener('resize', schedule)
    }
  }, [targetRef, enabled])

  return progress
}

export function LegalDocument({
  title,
  queryKey,
  fetchDocument,
  emptyMessage,
}: LegalDocumentProps) {
  const { t, i18n } = useTranslation()
  const language = i18n.language
  const { data, isLoading, isError, refetch } = useQuery({
    queryKey: [queryKey, language],
    queryFn: () => fetchDocument(language),
    staleTime: 10 * 60 * 1000,
  })

  const rawContent = data?.data?.trim() ?? ''
  const hasContent = rawContent.length > 0
  const isUrl = hasContent && isHttpUrl(rawContent)
  const contentIsHtml = hasContent && isLikelyHtml(rawContent)
  const success = data?.success ?? false
  const mode = contentIsHtml ? ('html' as const) : ('markdown' as const)
  const articleRef = useRef<HTMLElement>(null)

  const { sections, headings } = useMemo(
    () =>
      hasContent && !isUrl
        ? splitLegalSections(rawContent, mode)
        : { sections: [], headings: [] },
    [hasContent, isUrl, rawContent, mode]
  )

  const progress = useReadingProgress(
    articleRef,
    success && hasContent && !isUrl && !contentIsHtml
  )
  // HTML documents keep their original structure and isolated rendering.
  const showReader =
    success && hasContent && !isUrl && !contentIsHtml && headings.length >= 3

  if (isLoading) {
    return (
      <ForgePublicShell>
        <div className='mx-auto flex max-w-4xl flex-col gap-4 py-12'>
          <Skeleton className='h-8 w-[45%]' />
          <Skeleton className='h-4 w-full' />
          <Skeleton className='h-4 w-[90%]' />
          <Skeleton className='h-4 w-[80%]' />
        </div>
      </ForgePublicShell>
    )
  }

  if (!success || !hasContent) {
    return (
      <ForgePublicShell>
        <div className='mx-auto max-w-2xl px-4 py-12'>
          <Card className='border-dashed'>
            <CardHeader className='flex flex-row items-center gap-4'>
              <div className='bg-muted rounded-lg p-2'>
                <FileWarning className='text-muted-foreground h-5 w-5' />
              </div>
              <div className='space-y-1'>
                <CardTitle className='text-lg font-semibold'>{title}</CardTitle>
                <p className='text-muted-foreground text-sm'>
                  {isError
                    ? t('Request failed')
                    : data?.message || emptyMessage}
                </p>
              </div>
            </CardHeader>
            <CardContent>
              <Button onClick={() => void refetch()}>{t('Retry')}</Button>
            </CardContent>
          </Card>
        </div>
      </ForgePublicShell>
    )
  }

  if (isUrl) {
    return (
      <ForgePublicShell>
        <div className='mx-auto max-w-2xl py-12'>
          <Card>
            <CardHeader>
              <CardTitle>{title}</CardTitle>
            </CardHeader>
            <CardContent className='space-y-4'>
              <p className='text-muted-foreground text-sm'>
                {t(
                  'The administrator configured an external link for this document.'
                )}
              </p>
              <Button
                render={
                  <a
                    href={rawContent}
                    target='_blank'
                    rel='noopener noreferrer'
                  />
                }
              >
                {t('View document')}
              </Button>
            </CardContent>
          </Card>
        </div>
      </ForgePublicShell>
    )
  }

  if (contentIsHtml) {
    return (
      <ForgePublicShell>
        <RichContent mode='html' htmlVariant='isolated' content={rawContent} />
      </ForgePublicShell>
    )
  }

  return (
    <ForgePublicShell>
      {/* Reading progress: a quiet rule, not a decoration over the numbers. */}
      <div className='legal-progress' aria-hidden='true'>
        <span style={{ transform: `scaleX(${progress})` }} />
      </div>
      <div className='legal-layout'>
        <article
          ref={articleRef}
          className='legal-article'
          aria-labelledby='legal-title'
        >
          <h1 id='legal-title' className='legal-title'>
            {title}
          </h1>
          <p className='legal-meta'>
            {t('{{count}} sections', { count: headings.length })}
          </p>
          {showReader ? (
            sections.map((section, index) => (
              <section
                key={section.heading?.id ?? `preamble-${index}`}
                id={section.heading?.id}
                aria-labelledby={
                  section.heading ? `${section.heading.id}-heading` : undefined
                }
                className='legal-section'
              >
                {section.heading && (
                  <h2
                    id={`${section.heading.id}-heading`}
                    className={
                      section.heading.level === 1
                        ? 'legal-h1'
                        : section.heading.level === 2
                          ? 'legal-h2'
                          : 'legal-h3'
                    }
                  >
                    {section.heading.text}
                  </h2>
                )}
                <RichContent
                  mode={mode}
                  content={section.body}
                  className='prose-neutral dark:prose-invert max-w-none'
                />
              </section>
            ))
          ) : (
            <RichContent
              mode={mode}
              content={rawContent}
              className='prose-neutral dark:prose-invert max-w-none'
            />
          )}
        </article>

        {showReader && (
          <nav
            className='legal-toc'
            aria-label={t('On this page')}
            data-legal-toc
          >
            <p className='legal-toc-title'>{t('On this page')}</p>
            <ol>
              {headings.map((heading) => (
                <li key={heading.id} data-level={heading.level}>
                  <a href={`#${heading.id}`}>{heading.text}</a>
                </li>
              ))}
            </ol>
          </nav>
        )}
      </div>
    </ForgePublicShell>
  )
}
