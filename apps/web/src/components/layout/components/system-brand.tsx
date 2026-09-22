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
import { Link } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import { BrandLogo } from '@/components/brand-logo'
import {
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
} from '@/components/ui/sidebar'
import { useStatus } from '@/hooks/use-status'
import { useSystemConfig } from '@/hooks/use-system-config'
import { getBuildVersion } from '@/lib/build-metadata'
import { cn } from '@/lib/utils'

type SystemBrandProps = {
  defaultName?: string
  defaultVersion?: string
  variant?: 'sidebar' | 'inline' | 'navigation'
}

export function SystemBrand(props: SystemBrandProps) {
  const { t } = useTranslation()
  const { status } = useStatus()
  const { logo, systemName } = useSystemConfig()
  const variant = props.variant ?? 'sidebar'
  const name = systemName || props.defaultName || 'LMM Best'
  const apiVersion =
    status?.version || props.defaultVersion || t('Unknown version')
  const webVersion = getBuildVersion()
  const version = `${t('API')} ${apiVersion} · ${t('Web')} ${webVersion}`

  if (variant === 'navigation') {
    return (
      <SidebarMenu>
        <SidebarMenuItem>
          <SidebarMenuButton
            className='h-9 font-semibold'
            tooltip={t('Go to home')}
            render={<Link to='/' aria-label={t('Go to home')} />}
          >
            <span className='flex size-5 shrink-0 items-center justify-center'>
              <BrandLogo src={logo} className='size-full object-contain' />
            </span>
            <span className='truncate group-data-[collapsible=icon]:hidden'>
              {name}
            </span>
          </SidebarMenuButton>
        </SidebarMenuItem>
      </SidebarMenu>
    )
  }

  if (variant === 'inline') {
    return (
      <Link
        to='/'
        aria-label={t('Go to home')}
        className={cn(
          'text-foreground inline-flex h-11 min-w-11 shrink-0 items-center justify-center sm:h-8 sm:min-w-0 gap-1.5 rounded-md px-1.5 text-sm font-medium transition-colors outline-none select-none',
          'hover:bg-accent focus-visible:ring-ring/40 focus-visible:ring-2'
        )}
      >
        <div className='flex size-5 items-center justify-center'>
          <BrandLogo src={logo} className='size-full object-contain' />
        </div>
        <span className='hidden max-w-[12rem] truncate sm:inline'>{name}</span>
      </Link>
    )
  }

  return (
    <SidebarMenu>
      <SidebarMenuItem>
        <SidebarMenuButton
          size='lg'
          className='hover:text-sidebar-foreground active:text-sidebar-foreground cursor-default hover:bg-transparent active:bg-transparent'
          render={<div />}
        >
          <div className='flex aspect-square size-8 items-center justify-center'>
            <BrandLogo src={logo} className='size-full object-contain' />
          </div>
          <div className='grid flex-1 text-start text-sm leading-tight group-data-[collapsible=icon]:hidden'>
            <span className='truncate font-semibold'>{name}</span>
            <span className='truncate text-xs'>{version}</span>
          </div>
        </SidebarMenuButton>
      </SidebarMenuItem>
    </SidebarMenu>
  )
}
