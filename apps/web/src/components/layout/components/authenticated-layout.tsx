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

import '@/styles/console-pages.css'

import { AccessRestrictionNotice } from '@/components/access-restriction-notice'
import { CommandMenu } from '@/components/command-menu'
import { FrontendUpdateNotice } from '@/components/frontend-update-notice'
import { AnimatedOutlet } from '@/components/page-transition'
import { SkipToMain } from '@/components/skip-to-main'
import { SidebarInset, SidebarProvider } from '@/components/ui/sidebar'
import { LayoutProvider } from '@/context/layout-provider'
import { ModelPlazaProvider } from '@/context/model-plaza-provider'
import { SearchProvider } from '@/context/search-provider'
import { AssistantLauncher } from '@/features/assistant/assistant-launcher'
import { MandatoryAnnouncements } from '@/features/onboarding/mandatory-announcements'
import { ModelPlazaPanel } from '@/features/pricing/components/model-plaza-panel'
import { ReleaseNoteDialog } from '@/features/release-notes/release-note-dialog'
import { isConsoleActivated } from '@/lib/console-activation'
import { getCookie } from '@/lib/cookies'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import { AppHeader } from './app-header'
import { AppSidebar } from './app-sidebar'
import { ConsoleLocation } from './console-navigation'
import { QuickSwitchProvider } from './quick-switch-provider'
import { ShellBridgeRegistrar } from './shell-bridge-registrar'
import { ShortcutCheatsheetDialog } from './shortcut-cheatsheet-dialog'

type AuthenticatedLayoutProps = {
  children?: React.ReactNode
}

export function AuthenticatedLayout(props: AuthenticatedLayoutProps) {
  const defaultOpen = getCookie('sidebar_state') !== 'false'
  const user = useAuthStore((state) => state.auth.user)
  const consoleActivated = isConsoleActivated(user)
  const pathname = useRouterState({
    select: (state) => state.location.pathname,
  })
  const assistantPage = pathname === '/getting-started'
  const focusedOnboarding = assistantPage && !consoleActivated

  return (
    <MandatoryAnnouncements>
      <LayoutProvider>
        <ModelPlazaProvider>
          <SearchProvider>
            <QuickSwitchProvider>
              <SidebarProvider
                defaultOpen={defaultOpen}
                className='console-editorial h-dvh min-h-0 flex-col overflow-hidden'
              >
                <SkipToMain />
                <div className='flex min-h-0 w-full min-w-0 flex-1 basis-0 flex-col flex-nowrap md:flex-row'>
                  {focusedOnboarding ? null : <AppSidebar />}
                  <SidebarInset className='min-h-0 min-w-0 flex-1 overflow-hidden'>
                    <AppHeader
                      showTopNav={false}
                      showSidebarTrigger={!focusedOnboarding}
                      showBrand={focusedOnboarding}
                      showLanguageSwitcher={focusedOnboarding}
                      showConfigDrawer={focusedOnboarding}
                      showAssistant={!focusedOnboarding}
                      leftContent={
                        focusedOnboarding ? undefined : <ConsoleLocation />
                      }
                    />
                    <FrontendUpdateNotice />
                    <div className='flex min-h-0 min-w-0 flex-1'>
                      <div
                        className={cn(
                          '@container/content flex flex-col',
                          'min-h-0 min-w-0 flex-1 basis-0 overflow-hidden',
                          assistantPage
                            ? 'pb-0'
                            : 'pb-[calc(4.5rem+env(safe-area-inset-bottom))] md:pb-16 xl:pb-0'
                        )}
                      >
                        {props.children ?? <AnimatedOutlet />}
                      </div>
                      {!focusedOnboarding && (
                        <AssistantLauncher hideMobileLauncher={assistantPage} />
                      )}
                    </div>
                  </SidebarInset>
                </div>
                <AccessRestrictionNotice className='shrink-0' />
                <ReleaseNoteDialog />
                <CommandMenu />
                <ShortcutCheatsheetDialog />
                <ShellBridgeRegistrar />
                <ModelPlazaPanel />
              </SidebarProvider>
            </QuickSwitchProvider>
          </SearchProvider>
        </ModelPlazaProvider>
      </LayoutProvider>
    </MandatoryAnnouncements>
  )
}
