/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { Link, useLocation, type LinkProps } from '@tanstack/react-router'
import { ChartNoAxesCombined, KeyRound, Menu, Wallet } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { useSidebar } from '@/components/ui/sidebar'
import { useSidebarView } from '@/hooks/use-sidebar-view'

import type { NavGroup } from '../types'

const destinations = [
  { url: '/dashboard/overview', icon: ChartNoAxesCombined },
  { url: '/keys', icon: KeyRound },
  { url: '/wallet', icon: Wallet },
] as const

function mobileDestinations(groups: NavGroup[]) {
  const entries = groups.flatMap((group) =>
    group.items.flatMap((item) => item.items ?? (item.url ? [item] : []))
  )
  return destinations.flatMap((destination) => {
    const entry = entries.find(
      (item) => item.url === destination.url && !item.disabled
    )
    return entry ? [{ ...destination, title: entry.title }] : []
  })
}

/** The rail occupies layout space; it never covers forms, tables or save actions. */
export function ConsoleMobileDock() {
  const { t } = useTranslation()
  const { isMobile, setOpenMobile } = useSidebar()
  const { navGroups, view } = useSidebarView()
  const pathname = useLocation({ select: (location) => location.pathname })
  const [editing, setEditing] = useState(false)
  useEffect(() => {
    if (!isMobile) return
    const update = () =>
      setEditing(
        Boolean(
          document.activeElement?.matches(
            'input, textarea, [contenteditable="true"]'
          )
        )
      )
    const blur = () => queueMicrotask(update)
    document.addEventListener('focusin', update)
    document.addEventListener('focusout', blur)
    return () => {
      document.removeEventListener('focusin', update)
      document.removeEventListener('focusout', blur)
    }
  }, [isMobile])
  if (!isMobile || view || editing) return null
  return (
    <nav className='console-mobile-dock' aria-label={t('Quick navigation')}>
      {mobileDestinations(navGroups).map(({ url, title, icon: Icon }) => (
        <Link
          key={url}
          to={url as LinkProps['to']}
          aria-current={pathname === url ? 'page' : undefined}
        >
          <Icon aria-hidden='true' className='size-5' />
          <span>{title}</span>
        </Link>
      ))}
      <button
        type='button'
        onClick={() => setOpenMobile(true)}
        aria-haspopup='dialog'
      >
        <Menu aria-hidden='true' className='size-5' />
        <span>{t('More')}</span>
      </button>
    </nav>
  )
}
