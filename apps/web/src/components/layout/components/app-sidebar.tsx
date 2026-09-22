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
import { AnimatePresence, motion, useReducedMotion } from 'motion/react'
import { useTranslation } from 'react-i18next'

import {
  Sidebar,
  SidebarContent,
  SidebarHeader,
  SidebarRail,
} from '@/components/ui/sidebar'
import { useLayout } from '@/context/layout-provider'
import { useSidebarDensity } from '@/hooks/use-sidebar-config'
import { useSidebarView } from '@/hooks/use-sidebar-view'
import { MOTION_TRANSITION, MOTION_VARIANTS } from '@/lib/motion'

import { organizeConsoleNavigation } from '../lib/console-navigation'
import {
  ConsoleNavSection,
  ConsoleSearch,
  ConsoleSidebarFooter,
} from './console-navigation'
import { NavGroup } from './nav-group'
import { SidebarViewHeader } from './sidebar-view-header'
import { SystemBrand } from './system-brand'

export function AppSidebar() {
  const { t } = useTranslation()
  const { collapsible, variant } = useLayout()
  const density = useSidebarDensity()
  const { key, view, navGroups } = useSidebarView()
  const shouldReduce = useReducedMotion()
  const groups = view
    ? navGroups
    : organizeConsoleNavigation(navGroups, t('Console'))

  return (
    <Sidebar
      collapsible={collapsible}
      variant={variant}
      data-sidebar-density={density}
      className='top-0 h-dvh'
    >
      <SidebarHeader className='gap-3 px-3 pt-3 pb-2 group-data-[collapsible=icon]:px-1'>
        <SystemBrand variant='navigation' />
        <ConsoleSearch />
      </SidebarHeader>
      {view && <SidebarViewHeader view={view} />}
      <SidebarContent className='gap-1 py-2 group-data-[collapsible=icon]:overflow-auto'>
        <nav aria-label={t('Sidebar')}>
          <AnimatePresence mode='wait' initial={false}>
            <motion.div
              key={key}
              initial={
                shouldReduce ? false : MOTION_VARIANTS.sidebarSlide.initial
              }
              animate={MOTION_VARIANTS.sidebarSlide.animate}
              exit={
                shouldReduce ? undefined : MOTION_VARIANTS.sidebarSlide.exit
              }
              transition={MOTION_TRANSITION.fast}
              className='flex flex-col gap-1'
            >
              {groups.map((group) =>
                view ||
                group.id === 'console-primary' ||
                group.id === 'onboarding' ? (
                  <NavGroup key={group.id || group.title} {...group} />
                ) : (
                  <ConsoleNavSection
                    key={group.id || group.title}
                    group={group}
                  />
                )
              )}
            </motion.div>
          </AnimatePresence>
        </nav>
      </SidebarContent>
      <ConsoleSidebarFooter groups={navGroups} />
      <SidebarRail />
    </Sidebar>
  )
}
