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
import { LmmBrandWordmark } from '@/components/lmm-brand-wordmark'
import {
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
} from '@/components/ui/sidebar'
import { useStatus } from '@/hooks/use-status'
import { useSystemConfig } from '@/hooks/use-system-config'
import { getBuildVersion } from '@/lib/build-metadata'
import {
  DEFAULT_SYSTEM_NAME,
  isDefaultLogo,
  resolveSystemName,
} from '@/lib/constants'
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
  const name = resolveSystemName(systemName || props.defaultName)
  const usesDefaultBrand = name === DEFAULT_SYSTEM_NAME && isDefaultLogo(logo)
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
            <span
              className={cn(
                'flex size-5 shrink-0 items-center justify-center',
                usesDefaultBrand &&
                  'md:hidden group-data-[collapsible=icon]:flex'
              )}
            >
              <BrandLogo src={logo} className='size-full! object-contain' />
            </span>
            <span
              className={cn(
                'truncate group-data-[collapsible=icon]:hidden',
                usesDefaultBrand && 'md:hidden'
              )}
            >
              {name}
            </span>
            {usesDefaultBrand && (
              <LmmBrandWordmark
                title={name}
                className='hidden h-[21px]! w-auto! group-data-[collapsible=icon]:hidden md:block'
              />
            )}
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
        <div
          className={cn(
            'flex size-5 items-center justify-center',
            usesDefaultBrand && 'sm:hidden'
          )}
        >
          <BrandLogo src={logo} className='size-full object-contain' />
        </div>
        {usesDefaultBrand ? (
          <LmmBrandWordmark
            title={name}
            className='hidden h-[21px] w-auto sm:block'
          />
        ) : (
          <span className='hidden max-w-[12rem] truncate sm:inline'>
            {name}
          </span>
        )}
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
          <div
            className={cn(
              'flex aspect-square size-8 items-center justify-center',
              usesDefaultBrand && 'md:hidden group-data-[collapsible=icon]:flex'
            )}
          >
            <BrandLogo src={logo} className='size-full! object-contain' />
          </div>
          <div className='grid flex-1 text-start text-sm leading-tight group-data-[collapsible=icon]:hidden'>
            {usesDefaultBrand ? (
              <>
                <LmmBrandWordmark
                  title={name}
                  className='hidden h-[21px]! w-auto! md:block'
                />
                <span className='truncate font-semibold md:hidden'>{name}</span>
              </>
            ) : (
              <span className='truncate font-semibold'>{name}</span>
            )}
            <span className='truncate text-xs'>{version}</span>
          </div>
        </SidebarMenuButton>
      </SidebarMenuItem>
    </SidebarMenu>
  )
}
