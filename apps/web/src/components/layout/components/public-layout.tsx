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
import { useRouterState } from '@tanstack/react-router'

import { AccessRestrictionNotice } from '@/components/access-restriction-notice'
import { cn } from '@/lib/utils'

import type { TopNavLink } from '../types'
import {
  MobileScrollChrome,
  MobileScrollChromeProvider,
} from './mobile-scroll-chrome'
import { PublicHeader, type PublicHeaderProps } from './public-header'

type PublicLayoutProps = {
  children: React.ReactNode
  showMainContainer?: boolean
  mainClassName?: string
  className?: string
  navContent?: React.ReactNode
  headerProps?: Omit<PublicHeaderProps, 'navContent'>
  navLinks?: TopNavLink[]
  showThemeSwitch?: boolean
  showAuthButtons?: boolean
  showNotifications?: boolean
  logo?: React.ReactNode
  siteName?: string
}

export function PublicLayout(props: PublicLayoutProps) {
  const pathname = useRouterState({
    select: (state) => state.location.pathname,
  })
  return (
    <MobileScrollChromeProvider resetKey={pathname} documentScroll>
      <div
        className={cn(
          'bg-background text-foreground relative min-h-svh overflow-x-clip pt-[env(safe-area-inset-top)]',
          props.className
        )}
      >
        <MobileScrollChrome overlay>
          <PublicHeader
            navContent={props.navContent}
            navLinks={props.navLinks}
            showThemeSwitch={props.showThemeSwitch}
            showAuthButtons={props.showAuthButtons}
            showNotifications={props.showNotifications}
            logo={props.logo}
            siteName={props.siteName}
            {...props.headerProps}
          />
        </MobileScrollChrome>

        {props.showMainContainer !== false ? (
          <main
            className={cn(
              'container mx-auto px-5 pt-24 pb-12 sm:px-8 sm:pt-28 sm:pb-16 lg:px-12',
              props.mainClassName
            )}
          >
            {props.children}
          </main>
        ) : (
          props.children
        )}

        <AccessRestrictionNotice />
      </div>
    </MobileScrollChromeProvider>
  )
}
