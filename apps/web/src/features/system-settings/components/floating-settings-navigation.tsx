/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { Link, useLocation, useNavigate } from '@tanstack/react-router'
import {
  ArrowLeft,
  ChevronDown,
  ChevronUp,
  GripVertical,
  Search,
} from 'lucide-react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { useSidebar } from '@/components/ui/sidebar'

import { mountSettingsScrubber } from '../utils/settings-scrubber'
import {
  buildSettingsSearchIndex,
  searchSettings,
} from '../utils/settings-search-index'

import './floating-settings-navigation.css'

/** Shares the settings route registry, sidebar trigger and route-change guards. */
export function FloatingSettingsNavigation() {
  const { t } = useTranslation()
  const { open, setOpen, openMobile, setOpenMobile, isMobile } = useSidebar()
  const { pathname, searchStr } = useLocation()
  const navigate = useNavigate()
  const [query, setQuery] = useState('')
  const [preview, setPreview] = useState<number | null>(null)
  const handle = useRef<HTMLButtonElement>(null)
  const sidebar = useRef({ open, setOpen, setOpenMobile })
  sidebar.current = { open, setOpen, setOpenMobile }
  const index = useMemo(() => buildSettingsSearchIndex(t), [t])
  const entries = useMemo(
    () => index.flatMap((group) => group.entries),
    [index]
  )
  const currentIndex = Math.max(
    0,
    entries.findIndex((entry) => {
      const [path, query] = entry.url.split('?')
      const actual = new URLSearchParams(searchStr)
      return (
        path === pathname &&
        [...new URLSearchParams(query)].every(
          ([key, value]) => actual.get(key) === value
        )
      )
    })
  )
  const current = entries[currentIndex]
  const shown = isMobile ? openMobile : open
  const setShown = useCallback(
    (value: boolean) => {
      if (isMobile) setOpenMobile(value)
      else setOpen(value)
    },
    [isMobile, setOpen, setOpenMobile]
  )
  const select = useCallback(
    (position: number) => {
      const entry = entries[position]
      if (!entry) return
      setShown(false)
      const [to, query] = entry.url.split('?')
      void navigate({
        to,
        search: query
          ? Object.fromEntries(new URLSearchParams(query))
          : undefined,
      })
    },
    [entries, navigate, setShown]
  )

  useEffect(() => {
    const button = handle.current
    if (!button) return
    return mountSettingsScrubber(
      button,
      currentIndex,
      entries.length,
      setPreview,
      select,
      () => setShown(true)
    )
  }, [currentIndex, entries.length, select, setShown])

  // A settings directory must never appear automatically over the form.
  useEffect(() => {
    const previous = sidebar.current.open
    sidebar.current.setOpen(false)
    sidebar.current.setOpenMobile(false)
    return () => {
      sidebar.current.setOpen(previous)
      sidebar.current.setOpenMobile(false)
    }
  }, [])

  const results = searchSettings(index, query)
  return (
    <>
      <aside className='settings-float-rail' aria-label={t('System Settings')}>
        <Link
          to='/dashboard/$section'
          params={{ section: 'overview' }}
          className='settings-rail-back'
          aria-label={t('Back to Dashboard')}
        >
          <ArrowLeft aria-hidden='true' className='size-4' />
        </Link>
        <div className='settings-float-handle'>
          <Button
            variant='ghost'
            size='icon'
            className='size-11'
            disabled={currentIndex === 0}
            aria-label={t('Previous settings section')}
            onClick={() => select(currentIndex - 1)}
          >
            <ChevronUp aria-hidden='true' />
          </Button>
          <button
            ref={handle}
            type='button'
            className='settings-scrub-handle'
            aria-label={t(
              'Open settings directory; hold and drag to switch sections'
            )}
            aria-haspopup='dialog'
            aria-expanded={shown}
            title={t(
              'Hold and drag up or down. Release to open the selected section.'
            )}
          >
            <GripVertical aria-hidden='true' className='size-5' />
            <span>{t('Directory')}</span>
          </button>
          <Button
            variant='ghost'
            size='icon'
            className='size-11'
            disabled={currentIndex === entries.length - 1}
            aria-label={t('Next settings section')}
            onClick={() => select(currentIndex + 1)}
          >
            <ChevronDown aria-hidden='true' />
          </Button>
        </div>
        <span className='settings-rail-position' aria-hidden='true'>
          {currentIndex + 1}/{entries.length}
        </span>
        <output
          className='settings-scrub-preview'
          hidden={preview === null}
          aria-live='polite'
        >
          {preview !== null ? (
            <>
              <small>{entries[preview]?.group}</small>
              <strong>{entries[preview]?.section}</strong>
              <span>{t('Release to open')}</span>
            </>
          ) : null}
        </output>
      </aside>
      <Sheet open={shown} onOpenChange={setShown}>
        <SheetContent
          side={isMobile ? 'bottom' : 'left'}
          className='settings-floating-directory'
          finalFocus={handle}
        >
          <SheetHeader className='px-5 pt-5 pb-0'>
            <SheetTitle>{t('System Settings')}</SheetTitle>
            <SheetDescription>
              {current?.group} · {current?.section}
            </SheetDescription>
          </SheetHeader>
          <div className='relative mx-5'>
            <Search
              aria-hidden='true'
              className='text-muted-foreground pointer-events-none absolute top-3.5 left-3 size-4'
            />
            <Input
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder={t('Search settings')}
              aria-label={t('Search settings')}
              className='h-11 rounded-xl pl-9'
            />
          </div>
          <nav
            className='settings-directory-list'
            aria-label={t('Go to a settings section')}
          >
            {results.length === 0 ? (
              <p className='text-muted-foreground px-3 py-6'>
                {t('No settings found')}
              </p>
            ) : null}
            {results.map((group) => (
              <details
                key={`${group.group}:${query}`}
                open={
                  query.trim()
                    ? true
                    : group.entries.some((entry) => entry.url === current?.url)
                }
              >
                <summary>
                  <span>{group.group}</span>
                  <small>{group.entries.length}</small>
                  <ChevronDown aria-hidden='true' className='size-4' />
                </summary>
                {group.entries.map((entry) => (
                  <button
                    key={entry.url}
                    type='button'
                    aria-current={
                      entry.url === current?.url ? 'page' : undefined
                    }
                    onClick={() =>
                      select(
                        entries.findIndex((item) => item.url === entry.url)
                      )
                    }
                  >
                    {entry.section}
                  </button>
                ))}
              </details>
            ))}
          </nav>
          <p className='text-muted-foreground px-5 pb-4 text-xs'>
            {t('{{count}} sections', { count: entries.length })} ·{' '}
            {t('Changes are saved only with the Save button.')}
          </p>
        </SheetContent>
      </Sheet>
    </>
  )
}
