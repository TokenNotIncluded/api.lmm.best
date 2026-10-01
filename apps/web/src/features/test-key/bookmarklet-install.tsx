/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import {
  Bookmark,
  ExternalLink,
  KeyRound,
  MousePointer2,
  Pause,
  Play,
} from 'lucide-react'
import { useEffect, useId, useRef, useState, type DragEvent } from 'react'

import { Button } from '@/components/ui/button'

import {
  buildTestKeyBookmarklet,
  openTestKeyWindow,
  TEST_KEY_PATH,
} from './bookmarklet'
import { useTestKeyCopy } from './copy'

import './bookmarklet.css'

function BookmarkletDemo() {
  const q = useTestKeyCopy()
  const [paused, setPaused] = useState(false)
  return (
    <figure className='min-w-0' aria-label={q('demo')}>
      <div
        className='lmm-bookmark-demo'
        data-paused={paused}
        aria-hidden='true'
      >
        <div className='lmm-demo-address'>
          <span>○ ○ ○</span>
          <span>api.lmm.best</span>
        </div>
        <div className='lmm-demo-bar'>
          <span className='lmm-demo-saved'>
            <Bookmark size={12} />
            {q('bookmark')}
          </span>
        </div>
        <div className='lmm-demo-page'>
          <p className='text-muted-foreground text-xs'>API Key</p>
          <div className='lmm-demo-input'>
            <span className='lmm-demo-pasted'>sk-demo-••••••••</span>
          </div>
          <div className='lmm-demo-drag'>
            <Bookmark size={14} />
            {q('bookmark')}
            <MousePointer2 className='lmm-demo-cursor' size={22} />
          </div>
        </div>
        <div className='lmm-demo-popup'>
          <p className='mb-3 flex items-center gap-2 text-sm font-medium'>
            <KeyRound size={16} />
            {q('title')}
          </p>
          <div className='lmm-demo-input font-mono text-xs'>
            sk-demo-••••••••
          </div>
          <div className='bg-primary text-primary-foreground mt-3 rounded-md px-3 py-2 text-xs'>
            {q('create')}
          </div>
        </div>
      </div>
      <figcaption className='text-muted-foreground mt-2 flex items-center justify-between gap-2 text-xs'>
        <span>{q('demo')}</span>
        <Button
          type='button'
          size='icon'
          variant='ghost'
          className='shrink-0'
          aria-label={q(paused ? 'play' : 'pause')}
          aria-pressed={paused}
          onClick={() => setPaused(!paused)}
        >
          {paused ? <Play aria-hidden='true' /> : <Pause aria-hidden='true' />}
        </Button>
      </figcaption>
    </figure>
  )
}

export function BookmarkletInstall({ compact = false }: { compact?: boolean }) {
  const q = useTestKeyCopy()
  const titleId = useId()
  const anchor = useRef<HTMLAnchorElement>(null)
  useEffect(() => {
    // React intentionally rejects javascript: URLs in JSX. This is a fixed,
    // locally generated bookmark, never a URL supplied by the current page.
    anchor.current?.setAttribute(
      'href',
      buildTestKeyBookmarklet(window.location.origin)
    )
  }, [])
  const drag = (event: DragEvent<HTMLAnchorElement>) => {
    const bookmarklet = buildTestKeyBookmarklet(window.location.origin)
    event.dataTransfer.effectAllowed = 'copyLink'
    event.dataTransfer.setData('text/uri-list', bookmarklet)
    event.dataTransfer.setData('text/plain', bookmarklet)
    event.dataTransfer.setData(
      'text/x-moz-url',
      `${bookmarklet}\n${q('bookmark')}`
    )
  }
  const tutorial = (
    <div className='mt-6 grid gap-6 lg:grid-cols-2 lg:items-center'>
      <ol className='space-y-5'>
        {(['One', 'Two', 'Three'] as const).map((step, index) => (
          <li key={step} className='flex items-start gap-3'>
            <span
              className='text-muted-foreground mt-0.5 font-mono text-xs'
              aria-hidden='true'
            >
              0{index + 1}
            </span>
            <div>
              <h3 className='text-sm font-medium'>{q(`step${step}`)}</h3>
              <p className='text-muted-foreground mt-1 text-sm leading-6'>
                {q(
                  index === 0
                    ? 'shortcut'
                    : index === 1
                      ? 'dragHelp'
                      : 'createHelp'
                )}
              </p>
            </div>
          </li>
        ))}
      </ol>
      <BookmarkletDemo />
    </div>
  )
  return (
    <section
      className='bg-muted/20 min-w-0 rounded-xl border p-5 sm:p-6'
      aria-labelledby={titleId}
    >
      <div className='flex flex-wrap items-start justify-between gap-5'>
        <div className='max-w-xl'>
          <h2 id={titleId} className='text-lg font-semibold tracking-tight'>
            {q('installTitle')}
          </h2>
          <p className='text-muted-foreground mt-2 text-sm leading-6'>
            {q('installBody')}
          </p>
        </div>
        <div className='flex flex-wrap items-center gap-3'>
          <a
            ref={anchor}
            href={TEST_KEY_PATH}
            draggable
            onDragStart={drag}
            title={q('drag')}
            className='bg-primary text-primary-foreground focus-visible:ring-ring inline-flex min-h-11 cursor-grab items-center gap-2 rounded-lg px-4 py-2 text-sm font-medium outline-none focus-visible:ring-2 active:cursor-grabbing'
            onClick={(event) => {
              event.preventDefault()
              openTestKeyWindow()
            }}
          >
            <Bookmark size={16} aria-hidden='true' />
            {q('bookmark')}
          </a>
          <a
            href={TEST_KEY_PATH}
            target='_blank'
            rel='noopener noreferrer'
            className='text-muted-foreground inline-flex min-h-11 items-center gap-1 text-sm underline underline-offset-4'
          >
            {q('open')}
            <ExternalLink size={13} aria-hidden='true' />
          </a>
        </div>
      </div>
      {compact ? (
        <details className='mt-3'>
          <summary className='min-h-11 cursor-pointer py-3 text-sm'>
            {q('tutorial')}
          </summary>
          {tutorial}
        </details>
      ) : (
        tutorial
      )}
      <p className='text-muted-foreground mt-4 text-xs leading-5'>
        {q('compatibility')}
      </p>
      {!compact && (
        <p className='text-muted-foreground mt-1 text-xs leading-5'>
          {q('privacy')}
        </p>
      )}
    </section>
  )
}
