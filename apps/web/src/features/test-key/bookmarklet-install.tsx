/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import {
  Download,
  ExternalLink,
  KeyRound,
  MousePointer2,
  Pause,
  Play,
} from 'lucide-react'
import { useCallback, useId, useState, type DragEvent } from 'react'

import { Button } from '@/components/ui/button'

import {
  buildTestKeyBookmarklet,
  openTestKeyWindow,
  TEST_KEY_PATH,
} from './bookmarklet'
import {
  TEST_KEY_BOOKMARK_ICON,
  testKeyBookmarkDownloadUrl,
} from './bookmarklet-file'
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
            <img
              src={TEST_KEY_BOOKMARK_ICON}
              alt=''
              width={12}
              height={12}
              draggable={false}
            />
            {q('bookmark')}
          </span>
        </div>
        <div className='lmm-demo-page'>
          <p className='text-muted-foreground text-xs'>API Key</p>
          <div className='lmm-demo-input'>
            <span className='lmm-demo-pasted'>sk-demo-••••••••</span>
          </div>
          <div className='lmm-demo-drag'>
            <img
              src={TEST_KEY_BOOKMARK_ICON}
              alt=''
              width={14}
              height={14}
              draggable={false}
            />
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
  const bookmarkName = q('bookmark')
  // Set the real script when the node attaches, not after the first paint.
  // Never expose /test-key as a temporary, draggable ordinary bookmark.
  // Keep javascript: out of JSX because React rejects that URL scheme.
  const anchor = useCallback((element: HTMLAnchorElement | null) => {
    element?.setAttribute(
      'href',
      buildTestKeyBookmarklet(window.location.origin)
    )
  }, [])
  const download = useCallback(
    (element: HTMLAnchorElement | null) => {
      element?.setAttribute(
        'href',
        testKeyBookmarkDownloadUrl(window.location.origin, bookmarkName)
      )
    },
    [bookmarkName]
  )
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
      <div className='text-muted-foreground space-y-2 text-xs leading-5 lg:col-span-2'>
        <p>{q('iconImportHelp')}</p>
        <p>{q('installationCheck')}</p>
      </div>
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
            draggable
            onDragStart={drag}
            title={q('drag')}
            className='bg-primary text-primary-foreground focus-visible:ring-ring inline-flex min-h-11 cursor-grab items-center gap-2 rounded-lg px-4 py-2 text-sm font-medium outline-none focus-visible:ring-2 active:cursor-grabbing'
            onClick={(event) => {
              event.preventDefault()
              openTestKeyWindow()
            }}
          >
            <img
              src={TEST_KEY_BOOKMARK_ICON}
              alt=''
              width={20}
              height={20}
              draggable={false}
              className='shrink-0 rounded-sm'
            />
            {q('bookmark')}
          </a>
          <Button type='button' variant='outline' onClick={openTestKeyWindow}>
            <KeyRound aria-hidden='true' />
            {q('testPopup')}
          </Button>
          <a
            ref={download}
            download='lmm-test-key-bookmark.html'
            className='text-muted-foreground inline-flex min-h-11 items-center gap-1 text-sm underline underline-offset-4'
          >
            <Download size={14} aria-hidden='true' />
            {q('downloadBookmark')}
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
