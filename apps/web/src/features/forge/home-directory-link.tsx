/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { Link, useNavigate } from '@tanstack/react-router'
import { ArrowUpRight, Globe2 } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { directoryGestureHints } from './home-directory-copy'
import { attachDirectoryGesture } from './home-directory-gesture'

import './home-directory-link.css'

export function HomeDirectoryLink() {
  const { t, i18n } = useTranslation()
  const hints = directoryGestureHints(i18n.resolvedLanguage ?? i18n.language)
  const navigate = useNavigate()
  const entryRef = useRef<HTMLElement>(null)
  const [progress, setProgress] = useState(0)
  const percent = Math.round(progress * 100)

  useEffect(() => {
    const entry = entryRef.current
    if (!entry) return
    return attachDirectoryGesture(entry, {
      onProgress: setProgress,
      onNavigate: () => navigate({ to: '/ai-directory' }),
    })
  }, [navigate])

  return (
    <aside
      ref={entryRef}
      className='home-directory-link'
      aria-label={t('AI directory')}
      data-active={progress > 0 || undefined}
    >
      <div className='home-directory-entry'>
        <Link
          to='/ai-directory'
          className='home-directory-target focus-visible:ring-2 focus-visible:outline-none'
          aria-describedby='home-directory-hint'
        >
          <Globe2 className='home-directory-icon' aria-hidden='true' />
          <div className='min-w-0 flex-1'>
            <strong>{t('AI directory')}</strong>
            <span id='home-directory-hint' className='home-directory-hint'>
              <span className='home-directory-wheel-hint'>{hints.wheel}</span>
              <span className='home-directory-touch-hint'>
                {hints.touch}
              </span>
            </span>
          </div>
          <span className='home-directory-percent' aria-hidden='true'>
            {percent.toString().padStart(2, '0')}%
          </span>
          <ArrowUpRight className='home-directory-arrow' aria-hidden='true' />
        </Link>
        <div
          className='home-directory-track'
          role='progressbar'
          aria-label={t('AI directory')}
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={percent}
        >
          <span style={{ transform: `scaleX(${progress})` }} />
        </div>
      </div>
    </aside>
  )
}
