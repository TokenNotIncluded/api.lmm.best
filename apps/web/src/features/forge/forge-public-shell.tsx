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
import { useEffect } from 'react'

import { BrandLogo } from '@/components/brand-logo'
import { PublicLayout } from '@/components/layout'
import { LMM_BRAND_NAME } from '@/components/lmm-brand-mark'
import { useSystemConfig } from '@/hooks/use-system-config'
import { useTopNavLinks } from '@/hooks/use-top-nav-links'
import { DEFAULT_SYSTEM_NAME } from '@/lib/constants'

import { HomeDirectoryLink } from './home-directory-link'

import './forge-public-shell.css'

type ForgePublicShellProps = {
  children: React.ReactNode
}

export function ForgePublicShell(props: ForgePublicShellProps) {
  const { systemName, logo } = useSystemConfig()
  const siteName =
    systemName === DEFAULT_SYSTEM_NAME ? LMM_BRAND_NAME : systemName
  const isHome = useRouterState({
    select: (state) => state.location.pathname === '/',
  })
  const securityLink = useTopNavLinks().find(
    (link) => link.href === '/security'
  )

  useEffect(() => {
    const previousTitle = document.title
    document.title = siteName
    return () => {
      document.title = previousTitle
    }
  }, [siteName])

  return (
    <PublicLayout
      showMainContainer={false}
      siteName={siteName}
      logo={<BrandLogo src={logo} className='size-7 object-contain' />}
      navLinks={[
        { title: 'Home', href: '/' },
        { title: 'AI directory', href: '/ai-directory' },
        { title: 'Models and pricing', href: '/pricing' },
        { title: 'Developers', href: '/developers' },
        { title: 'Guide', href: '/guide' },
        { title: 'Scripts', href: '/scripts' },
        { title: 'WebMCP', href: '/webmcp' },
        { title: 'Signal path', href: '/games/signal' },
        { title: 'Challenges', href: '/challenges' },
        ...(securityLink ? [securityLink] : []),
      ]}
      showNotifications={false}
      headerProps={{
        useDynamicNavLinks: false,
        className: 'forge-public-header',
      }}
    >
      <div className='forge-surface min-h-svh'>
        {isHome && <HomeDirectoryLink />}
        {props.children}
      </div>
    </PublicLayout>
  )
}
