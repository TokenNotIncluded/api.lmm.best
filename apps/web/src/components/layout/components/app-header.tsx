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
import { AiChat02Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfigDrawer } from '@/components/config-drawer'
import { LanguageSwitcher } from '@/components/language-switcher'
import { NotificationPopover } from '@/components/notification-popover'
import { ProfileDropdown } from '@/components/profile-dropdown'
import { Button } from '@/components/ui/button'
import { requestAssistantOpen } from '@/features/assistant/assistant-events'
import {
  isAssistantRailOpen,
  onAssistantRailChange,
  toggleAssistantRail,
} from '@/features/assistant/assistant-rail'
import { useNotifications } from '@/hooks/use-notifications'
import { useStatus } from '@/hooks/use-status'
import { useTopNavLinks } from '@/hooks/use-top-nav-links'
import { cn } from '@/lib/utils'

import { defaultTopNavLinks } from '../config/top-nav.config'
import type { TopNavLink } from '../types'
import { Header } from './header'
import { SystemBrand } from './system-brand'
import { TopNav } from './top-nav'

type AppHeaderProps = {
  navLinks?: TopNavLink[]
  showTopNav?: boolean
  leftContent?: React.ReactNode
  rightContent?: React.ReactNode
  showNotifications?: boolean
  showConfigDrawer?: boolean
  showSidebarTrigger?: boolean
  showProfileDropdown?: boolean
  /** The console puts the brand and low-frequency preferences in its sidebar. */
  showBrand?: boolean
  showLanguageSwitcher?: boolean
  showAssistant?: boolean
}

export function AppHeader({
  navLinks = defaultTopNavLinks,
  showTopNav = true,
  leftContent,
  rightContent,
  showNotifications = true,
  showConfigDrawer = true,
  showSidebarTrigger = true,
  showProfileDropdown = true,
  showBrand = true,
  showLanguageSwitcher = true,
  showAssistant = true,
}: AppHeaderProps) {
  const dynamicLinks = useTopNavLinks()
  const links = dynamicLinks.length > 0 ? dynamicLinks : navLinks
  const { t } = useTranslation()
  const { status } = useStatus()
  const notifications = useNotifications()
  const assistantEnabled = status?.assistant?.enabled !== false
  const [railOpen, setRailOpenState] = useState(false)

  useEffect(
    () => onAssistantRailChange(() => setRailOpenState(isAssistantRailOpen())),
    []
  )

  const handleAssistantClick = () => {
    if (window.matchMedia('(min-width: 1280px)').matches) {
      toggleAssistantRail()
    } else {
      requestAssistantOpen()
    }
  }

  return (
    <Header showSidebarTrigger={showSidebarTrigger}>
      {showBrand && <SystemBrand variant='inline' />}
      {leftContent ? (
        <div className='ms-2 flex min-w-0 items-center'>{leftContent}</div>
      ) : null}
      {showTopNav && (
        <div className='mx-auto hidden min-w-0 flex-1 justify-center px-4 lg:flex'>
          <TopNav
            links={links}
            className='max-h-11 min-w-0 [scrollbar-width:none] overflow-x-auto p-1 whitespace-nowrap [&::-webkit-scrollbar]:hidden'
            aria-label={t('Header navigation')}
          />
        </div>
      )}
      {rightContent ?? (
        <div className='ms-auto flex shrink-0 items-center gap-1 sm:gap-2'>
          {showTopNav && (
            <div className='lg:hidden'>
              <TopNav links={links} aria-label={t('Header navigation')} />
            </div>
          )}
          {showAssistant && assistantEnabled && (
            <Button
              variant='ghost'
              size='icon'
              className={cn(
                'relative hidden size-8 sm:inline-flex',
                railOpen && 'bg-accent text-accent-foreground'
              )}
              aria-label={t('Open AI assistant')}
              title={t('Open AI assistant')}
              aria-pressed={railOpen}
              onClick={handleAssistantClick}
            >
              <HugeiconsIcon
                icon={AiChat02Icon}
                strokeWidth={2}
                className='size-4'
                aria-hidden='true'
              />
            </Button>
          )}
          {showNotifications && (
            <NotificationPopover
              open={notifications.popoverOpen}
              onOpenChange={notifications.setPopoverOpen}
              unreadCount={notifications.unreadCount}
              activeTab={notifications.activeTab}
              onTabChange={notifications.setActiveTab}
              notice={notifications.notice}
              announcements={notifications.announcements}
              ratioFeed={notifications.ratioFeed}
              bountyTips={notifications.bountyTips}
              thankingTipId={notifications.thankingTipId}
              onThankTip={notifications.thankTip}
              loading={notifications.loading}
            />
          )}
          {showLanguageSwitcher && <LanguageSwitcher />}
          {showConfigDrawer && <ConfigDrawer />}
          {showProfileDropdown && <ProfileDropdown />}
        </div>
      )}
    </Header>
  )
}
