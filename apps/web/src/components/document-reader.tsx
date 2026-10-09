/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { ChevronDown } from 'lucide-react'
import { useEffect, useMemo, useRef } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Markdown } from '@/components/ui/markdown'
import { splitLegalSections } from '@/features/legal/legal-reader'
import { cn } from '@/lib/utils'

import './document-reader.css'

/** Full text stays mounted. Section links and printing expand the source text. */
export function DocumentReader({
  content,
  className,
  breaks,
}: {
  content: string
  className?: string
  breaks?: boolean
}) {
  const { t } = useTranslation()
  const root = useRef<HTMLDivElement>(null)
  const { sections, headings } = useMemo(
    () => splitLegalSections(content, 'markdown'),
    [content]
  )
  const all = () => [
    ...(root.current?.querySelectorAll<HTMLDetailsElement>(
      '[data-reading-section]'
    ) ?? []),
  ]
  const expand = (open: boolean) => {
    all().forEach((section) => {
      section.open = open
    })
  }
  useEffect(() => {
    const node = root.current
    if (!node) return
    const hash = () => {
      let id: string
      try {
        id = decodeURIComponent(window.location.hash.slice(1))
      } catch {
        return
      }
      const target = [
        ...node.querySelectorAll<HTMLDetailsElement>('[data-reading-section]'),
      ].find((section) => section.id === id)
      if (target) {
        target.open = true
        target.scrollIntoView({ block: 'start' })
      }
    }
    let printState: boolean[] = []
    const beforePrint = () => {
      const entries = [
        ...node.querySelectorAll<HTMLDetailsElement>('[data-reading-section]'),
      ]
      printState = entries.map((section) => section.open)
      entries.forEach((section) => {
        section.open = true
      })
    }
    const afterPrint = () => {
      node
        .querySelectorAll<HTMLDetailsElement>('[data-reading-section]')
        .forEach((section, index) => {
          section.open = printState[index] ?? section.open
        })
    }
    // Opening before native anchor navigation also handles clicking the same hash twice.
    const follow = (event: MouseEvent) => {
      const link = (event.target as Element | null)?.closest?.('a[href^="#"]')
      const id = link?.getAttribute('href')?.slice(1)
      if (!id) return
      const target = [
        ...node.querySelectorAll<HTMLDetailsElement>('[data-reading-section]'),
      ].find(
        (section) => encodeURIComponent(section.id) === id || section.id === id
      )
      if (target) target.open = true
    }
    hash()
    window.addEventListener('hashchange', hash)
    document.addEventListener('click', follow)
    window.addEventListener('beforeprint', beforePrint)
    window.addEventListener('afterprint', afterPrint)
    return () => {
      window.removeEventListener('hashchange', hash)
      document.removeEventListener('click', follow)
      window.removeEventListener('beforeprint', beforePrint)
      window.removeEventListener('afterprint', afterPrint)
    }
  }, [content])
  if (headings.length < 3) {
    return (
      <Markdown breaks={breaks} className={className}>
        {content}
      </Markdown>
    )
  }
  return (
    <div ref={root} className={cn('document-reader', className)}>
      <div className='document-reader-tools'>
        <span>
          {t('On this page')} · {headings.length}
        </span>
        <Button variant='ghost' size='sm' onClick={() => expand(true)}>
          {t('Expand all')}
        </Button>
        <Button variant='ghost' size='sm' onClick={() => expand(false)}>
          {t('Collapse all')}
        </Button>
      </div>
      {sections.map((section, index) =>
        section.heading ? (
          <details
            key={section.heading.id}
            id={section.heading.id}
            data-reading-section
            open={index === 0 || index === 1}
          >
            <summary>
              <span className='document-section-number' aria-hidden='true'>
                {String(headings.indexOf(section.heading) + 1).padStart(2, '0')}
              </span>
              <h2>{section.heading.text}</h2>
              <ChevronDown aria-hidden='true' className='size-4 shrink-0' />
            </summary>
            <div className='document-section-body'>
              <Markdown breaks={breaks}>{section.body}</Markdown>
            </div>
          </details>
        ) : (
          <Markdown key={`preamble-${index}`} breaks={breaks}>
            {section.body}
          </Markdown>
        )
      )}
    </div>
  )
}
