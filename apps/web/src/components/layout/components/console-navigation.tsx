/*
Copyright (C) 2026 LIghtJUNction

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

*/
import { Link, useLocation } from '@tanstack/react-router'
import { ChevronRight, Ellipsis, Search } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfigDrawer } from '@/components/config-drawer'
import { LanguageSwitcher } from '@/components/language-switcher'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  SidebarFooter,
  SidebarGroup,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  useSidebar,
} from '@/components/ui/sidebar'
import { useSearch } from '@/context/search-provider'
import { useSidebarView } from '@/hooks/use-sidebar-view'
import { useTopNavLinks } from '@/hooks/use-top-nav-links'

import { defaultTopNavLinks } from '../config/top-nav.config'
import {
  getConsolePageTitle,
  getConsoleSiteLinks,
} from '../lib/console-navigation'
import { checkIsActive } from '../lib/url-utils'
import type { NavGroup as NavGroupProps } from '../types'
import { NavGroup } from './nav-group'

export function ConsoleLocation() {
  const { t } = useTranslation()
  const href = useLocation({ select: (location) => location.href })
  const { navGroups } = useSidebarView()
  return (
    <span
      className='min-w-0 truncate text-sm font-medium'
      data-testid='console-location'
    >
      {getConsolePageTitle(navGroups, href) ?? t('Console')}
    </span>
  )
}

export function ConsoleSearch() {
  const { t } = useTranslation()
  const { setOpen } = useSearch()
  const { setOpenMobile } = useSidebar()
  return (
    <SidebarMenu>
      <SidebarMenuItem>
        <SidebarMenuButton
          tooltip={t('Search')}
          className='text-muted-foreground rounded-lg border border-sidebar-border/70'
          onClick={() => {
            setOpenMobile(false)
            setOpen(true)
          }}
        >
          <Search aria-hidden='true' />
          <span>{t('Search')}</span>
        </SidebarMenuButton>
      </SidebarMenuItem>
    </SidebarMenu>
  )
}

export function ConsoleNavSection({ group }: { group: NavGroupProps }) {
  const { state, isMobile } = useSidebar()
  const href = useLocation({ select: (location) => location.href })
  const pathname = href.split(/[?#]/)[0]
  const active =
    group.items.some((item) => checkIsActive(href, item)) ||
    Boolean(getConsolePageTitle([group], href))
  const [choice, setChoice] = useState<{
    pathname: string
    open: boolean
  } | null>(null)
  const open = choice?.pathname === pathname ? choice.open : active
  const Icon = group.items.find((item) => item.icon)?.icon
  const hasUnread = group.items.some((item) => Boolean(item.badge))

  // The optional icon rail keeps the existing tooltips and nested dropdowns.
  if (state === 'collapsed' && !isMobile) return <NavGroup {...group} />

  return (
    <Collapsible
      open={open}
      onOpenChange={(nextOpen) => setChoice({ pathname, open: nextOpen })}
      className='group/section px-2 py-0.5'
      data-testid={`console-section-${group.id}`}
    >
      <SidebarMenu>
        <SidebarMenuItem>
          <CollapsibleTrigger
            render={<SidebarMenuButton isActive={active} />}
            className='group/section-trigger h-11 md:h-9'
          >
            {Icon && <Icon aria-hidden='true' />}
            <span className='min-w-0 flex-1 truncate'>{group.title}</span>
            {hasUnread && (
              <span
                className='size-1.5 shrink-0 rounded-full bg-primary'
                aria-hidden='true'
              />
            )}
            <ChevronRight
              aria-hidden='true'
              className='size-3.5 shrink-0 transition-transform duration-150 group-data-[panel-open]/section-trigger:rotate-90 motion-reduce:transition-none'
            />
          </CollapsibleTrigger>
          <CollapsibleContent className='CollapsibleContent [&_[data-slot=sidebar-group-label]]:hidden'>
            <NavGroup {...group} />
          </CollapsibleContent>
        </SidebarMenuItem>
      </SidebarMenu>
    </Collapsible>
  )
}

export function ConsoleSidebarFooter({ groups }: { groups: NavGroupProps[] }) {
  const { t } = useTranslation()
  const { setOpenMobile } = useSidebar()
  const dynamicLinks = useTopNavLinks()
  const links = groups.some((group) => group.id === 'onboarding')
    ? []
    : getConsoleSiteLinks(
        dynamicLinks.length ? dynamicLinks : defaultTopNavLinks,
        groups
      )

  return (
    <SidebarFooter className='gap-2 border-t border-sidebar-border/60 p-2 pb-[max(0.5rem,env(safe-area-inset-bottom))]'>
      {links.length > 0 && (
        <SidebarGroup className='p-0'>
          <SidebarMenu>
            <SidebarMenuItem>
              <DropdownMenu modal={false}>
                <DropdownMenuTrigger
                  render={<SidebarMenuButton tooltip={t('More')} />}
                >
                  <Ellipsis aria-hidden='true' />
                  <span>{t('More')}</span>
                </DropdownMenuTrigger>
                <DropdownMenuContent
                  side='top'
                  align='start'
                  className='min-w-48'
                >
                  {links.map((link) => (
                    <DropdownMenuItem
                      key={`${link.external}:${link.href}`}
                      disabled={link.disabled}
                      onClick={() => setOpenMobile(false)}
                      render={
                        link.external ? (
                          <a
                            href={link.href}
                            target='_blank'
                            rel='noopener noreferrer'
                          />
                        ) : (
                          <Link to={link.href} disabled={link.disabled} />
                        )
                      }
                    >
                      {link.title}
                    </DropdownMenuItem>
                  ))}
                </DropdownMenuContent>
              </DropdownMenu>
            </SidebarMenuItem>
          </SidebarMenu>
        </SidebarGroup>
      )}
      <div className='flex items-center justify-between gap-2 px-1 group-data-[collapsible=icon]:hidden'>
        <span className='text-muted-foreground text-xs'>{t('Settings')}</span>
        <div className='flex items-center gap-1'>
          <LanguageSwitcher />
          <ConfigDrawer />
        </div>
      </div>
    </SidebarFooter>
  )
}
