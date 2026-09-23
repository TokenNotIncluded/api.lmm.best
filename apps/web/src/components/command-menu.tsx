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
import { useNavigate } from '@tanstack/react-router'
import {
  ArrowRight,
  ChevronRight,
  Clock,
  CoinsIcon,
  KeyRound,
  Keyboard,
  Laptop,
  Moon,
  Plus,
  Sun,
} from 'lucide-react'
import React from 'react'
import { useTranslation } from 'react-i18next'

import {
  Command,
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from '@/components/ui/command'
import { useModelPlaza } from '@/context/model-plaza-provider'
import { useSearch } from '@/context/search-provider'
import { useTheme } from '@/context/theme-provider'
import { useSidebarView } from '@/hooks/use-sidebar-view'
import appI18n from '@/i18n/config'
import {
  INTERFACE_LANGUAGE_OPTIONS,
  normalizeInterfaceLanguage,
} from '@/i18n/languages'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { useQuickSwitch } from './layout/components/quick-switch-provider'
import { onPaletteOpenRequest } from './layout/lib/quick-switch-events'
import { resolveRecentRouteLabel } from './layout/lib/recent-routes'
import { ScrollArea } from './ui/scroll-area'

export function CommandMenu() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { setTheme } = useTheme()
  const { open, setOpen } = useSearch()
  const { openPanel } = useModelPlaza()
  const quickSwitch = useQuickSwitch()
  const signedInUser = useAuthStore((state) => state.auth.user)
  // Search must respect the same role and module filters as the visible sidebar.
  const { navGroups } = useSidebarView()
  const currentLanguage = normalizeInterfaceLanguage(appI18n.language)

  const runCommand = React.useCallback(
    (command: () => unknown) => {
      setOpen(false)
      command()
    },
    [setOpen]
  )

  // Other shell surfaces (sidebar, shortcut sheet) ask for the palette by event
  // so they never need to import the search context.
  React.useEffect(() => onPaletteOpenRequest(() => setOpen(true)), [setOpen])

  const recentEntries = quickSwitch.recentRoutes
    .map((pathname) => ({
      pathname,
      label: resolveRecentRouteLabel(navGroups, pathname),
    }))
    .filter((entry): entry is { pathname: string; label: string } =>
      Boolean(entry.label)
    )
    .slice(0, 5)

  // Mirrors the header language switcher: switch immediately, persist best-effort.
  const changeLanguage = async (code: string) => {
    await appI18n.changeLanguage(code)
    if (signedInUser) {
      try {
        await api.put('/api/user/self', { language: code })
      } catch {
        // Best-effort persistence; don't block the UI on failure
      }
    }
  }

  return (
    <CommandDialog modal open={open} onOpenChange={setOpen}>
      <Command>
        <CommandInput placeholder={t('Type a command or search...')} />
        <CommandList>
          <ScrollArea className='h-72 pe-1'>
            <CommandEmpty>{t('No results found.')}</CommandEmpty>
            <CommandGroup heading={t('Quick actions')}>
              <CommandItem
                value={`${t('Top up')} wallet`}
                onSelect={() => runCommand(() => navigate({ to: '/wallet' }))}
              >
                <CoinsIcon />
                <span>{t('Top up')}</span>
              </CommandItem>
              <CommandItem
                value={`${t('Create API key')} keys`}
                onSelect={() => runCommand(() => navigate({ to: '/keys' }))}
              >
                <Plus />
                <span>{t('Create API key')}</span>
              </CommandItem>
              <CommandItem
                value={`${t('API keys')} keys`}
                onSelect={() => runCommand(() => navigate({ to: '/keys' }))}
              >
                <KeyRound />
                <span>{t('API keys')}</span>
              </CommandItem>
              <CommandItem
                value={`${t('Keyboard shortcuts')} shortcuts`}
                onSelect={() => runCommand(() => quickSwitch.openShortcuts())}
              >
                <Keyboard />
                <span>{t('Keyboard shortcuts')}</span>
              </CommandItem>
            </CommandGroup>
            {recentEntries.length > 0 && (
              <>
                <CommandSeparator />
                <CommandGroup heading={t('Recently visited')}>
                  {recentEntries.map((entry) => (
                    <CommandItem
                      key={`recent-${entry.pathname}`}
                      value={`recent ${entry.label} ${entry.pathname}`}
                      onSelect={() =>
                        runCommand(() => navigate({ to: entry.pathname }))
                      }
                    >
                      <Clock />
                      <span>{entry.label}</span>
                    </CommandItem>
                  ))}
                </CommandGroup>
              </>
            )}
            {navGroups.map((group) => (
              <CommandGroup key={group.id || group.title} heading={group.title}>
                {group.items.map((navItem, i) => {
                  if (navItem.url) {
                    return (
                      <CommandItem
                        key={`${navItem.url}-${i}`}
                        value={navItem.title}
                        disabled={navItem.disabled}
                        onSelect={() => {
                          runCommand(() =>
                            navItem.interaction === 'model-panel'
                              ? openPanel()
                              : navigate({ to: navItem.url })
                          )
                        }}
                      >
                        <div className='flex size-4 items-center justify-center'>
                          <ArrowRight className='text-muted-foreground/80 size-2' />
                        </div>
                        {navItem.title}
                      </CommandItem>
                    )
                  }

                  return navItem.items?.map((subItem, i) => (
                    <CommandItem
                      key={`${navItem.title}-${subItem.url}-${i}`}
                      value={`${navItem.title}-${subItem.url}`}
                      disabled={navItem.disabled || subItem.disabled}
                      onSelect={() => {
                        runCommand(() =>
                          subItem.interaction === 'model-panel'
                            ? openPanel()
                            : navigate({ to: subItem.url })
                        )
                      }}
                    >
                      <div className='flex size-4 items-center justify-center'>
                        <ArrowRight className='text-muted-foreground/80 size-2' />
                      </div>
                      {navItem.title} <ChevronRight /> {subItem.title}
                    </CommandItem>
                  ))
                })}
              </CommandGroup>
            ))}
            <CommandSeparator />
            <CommandGroup heading={t('Language')}>
              {INTERFACE_LANGUAGE_OPTIONS.map((language) => (
                <CommandItem
                  key={`language-${language.code}`}
                  value={`language ${language.label}`}
                  onSelect={() =>
                    runCommand(() => {
                      void changeLanguage(language.code)
                    })
                  }
                  aria-current={
                    currentLanguage === language.code ? 'true' : undefined
                  }
                >
                  <span className='w-4 text-center text-xs'>
                    {currentLanguage === language.code ? '✓' : ''}
                  </span>
                  <span>{language.label}</span>
                </CommandItem>
              ))}
            </CommandGroup>
            <CommandSeparator />
            <CommandGroup heading={t('Theme')}>
              <CommandItem onSelect={() => runCommand(() => setTheme('light'))}>
                <Sun /> <span>{t('Light')}</span>
              </CommandItem>
              <CommandItem onSelect={() => runCommand(() => setTheme('dark'))}>
                <Moon className='scale-90' />
                <span>{t('Dark')}</span>
              </CommandItem>
              <CommandItem
                onSelect={() => runCommand(() => setTheme('system'))}
              >
                <Laptop />
                <span>{t('System')}</span>
              </CommandItem>
            </CommandGroup>
          </ScrollArea>
        </CommandList>
      </Command>
    </CommandDialog>
  )
}
