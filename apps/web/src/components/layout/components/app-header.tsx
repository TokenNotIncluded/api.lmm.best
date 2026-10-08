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
import { BubbleChatSparkIcon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { Link, type LinkProps } from '@tanstack/react-router'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfigDrawer } from '@/components/config-drawer'
import { LanguageSwitcher } from '@/components/language-switcher'
import { NotificationPopover } from '@/components/notification-popover'
import { ProfileDropdown } from '@/components/profile-dropdown'
import { ForgeShaderSurface } from '@/components/shaders/forge-shader-surface'
import { Button } from '@/components/ui/button'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
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
import { useAuthStore } from '@/stores/auth-store'

import { defaultTopNavLinks } from '../config/top-nav.config'
import type { TopNavLink } from '../types'
import { AccountBalanceBadge } from './account-balance-badge'
import { Header } from './header'
import { HeaderTools, type HeaderTool } from './header-tools'
import { StoreIcon } from './store-icon'
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
  /** Persistent account balance and wallet entry. */
  showBalanceBadge?: boolean
  showAssistant?: boolean
  /** The console exposes its assistant here instead of a mobile floating pill. */
  showMobileAssistant?: boolean
  /** Public community shop entry; available before console activation. */
  showStore?: boolean
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
  showBalanceBadge = true,
  showAssistant = true,
  showMobileAssistant = false,
  showStore = true,
}: AppHeaderProps) {
  const dynamicLinks = useTopNavLinks()
  const links = dynamicLinks.length > 0 ? dynamicLinks : navLinks
  const { t } = useTranslation()
  const { status } = useStatus()
  const notifications = useNotifications()
  const user = useAuthStore((state) => state.auth.user)
  const assistantEnabled = status?.assistant?.enabled !== false
  const mobileAssistantAvailable = showMobileAssistant && user !== null
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

  const tools: HeaderTool[] = []
  if (showStore) {
    tools.push({
      id: 'store',
      label: t('Shop'),
      content: (
        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                variant='ghost'
                size='icon'
                className='size-11 rounded-lg md:size-8'
                render={<Link to={'/store' as LinkProps['to']} />}
                aria-label={t('Open shop')}
                title={t('Open shop')}
                data-testid='header-store-link'
              />
            }
          >
            <StoreIcon aria-hidden='true' />
          </TooltipTrigger>
          <TooltipContent side='bottom'>{t('Shop')}</TooltipContent>
        </Tooltip>
      ),
    })
  }
  if (showNotifications) {
    tools.push({
      id: 'notifications',
      label: t('Notifications'),
      content: (
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
      ),
    })
  }
  if (showLanguageSwitcher) {
    tools.push({
      id: 'language',
      label: t('Language'),
      content: <LanguageSwitcher />,
    })
  }
  if (showConfigDrawer) {
    tools.push({
      id: 'appearance',
      label: t('Appearance'),
      content: <ConfigDrawer />,
    })
  }
  if (showProfileDropdown) {
    tools.push({
      id: 'profile',
      label: t('Profile'),
      content: <ProfileDropdown />,
    })
  }

  return (
    <Header showSidebarTrigger={showSidebarTrigger}>
      {showBrand && <SystemBrand variant='inline' />}
      {leftContent ? (
        <div className='ms-1 flex min-w-0 flex-1 items-center md:ms-2 md:flex-initial'>
          {leftContent}
        </div>
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
        <div className='ms-auto flex shrink-0 items-center gap-1 md:gap-2'>
          {showTopNav && (
            <div className='lg:hidden'>
              <TopNav links={links} aria-label={t('Header navigation')} />
            </div>
          )}
          {showAssistant && (assistantEnabled || mobileAssistantAvailable) && (
            <Tooltip>
              <TooltipTrigger
                render={
                  <Button
                    variant='ghost'
                    size='icon'
                    className={cn(
                      'relative isolate size-11 overflow-hidden rounded-lg md:size-8',
                      !showMobileAssistant && 'hidden md:inline-flex',
                      !assistantEnabled && 'md:hidden',
                      railOpen && 'bg-accent text-accent-foreground'
                    )}
                    aria-label={t('Open AI assistant')}
                    title={t('Open AI assistant')}
                    aria-pressed={railOpen}
                    data-testid='header-assistant-launcher'
                    onClick={handleAssistantClick}
                  />
                }
              >
                <ForgeShaderSurface
                  variant='assistant'
                  interaction='intent'
                  className='absolute inset-0 opacity-70'
                />
                <HugeiconsIcon
                  icon={BubbleChatSparkIcon}
                  strokeWidth={1.8}
                  className='relative z-10'
                  aria-hidden='true'
                />
              </TooltipTrigger>
              <TooltipContent side='bottom'>
                {t('Open AI assistant')}
              </TooltipContent>
            </Tooltip>
          )}
          {showBalanceBadge && <AccountBalanceBadge compactMobile />}
          <HeaderTools
            items={tools}
            onDismiss={() => notifications.setPopoverOpen(false)}
            unreadCount={showNotifications ? notifications.unreadCount : 0}
          />
        </div>
      )}
    </Header>
  )
}
