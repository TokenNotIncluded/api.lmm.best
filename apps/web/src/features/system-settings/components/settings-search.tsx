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
/*
Copyright (C) 2026 LIghtJUNction
*/
import { useNavigate, useLocation } from '@tanstack/react-router'
import { ArrowUpRight, ChevronRight, Search } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { SYSTEM_SETTINGS_VIEW } from '@/components/layout/config/system-settings.config'
import { Button } from '@/components/ui/button'
import {
  Command,
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command'

function useSettingsNavigation() {
  const { t } = useTranslation()
  const pathname = useLocation({ select: (location) => location.pathname })
  const groups = useMemo(
    () =>
      SYSTEM_SETTINGS_VIEW.getNavGroups(t).flatMap((group) =>
        group.items.flatMap((item) =>
          'items' in item && item.items
            ? [{ title: item.title, icon: item.icon, items: item.items }]
            : []
        )
      ),
    [t]
  )
  const current = groups.find((group) =>
    group.items.some((item) => item.url === pathname)
  )
  return { groups, current, pathname }
}

export function SettingsBreadcrumb() {
  const { t } = useTranslation()
  const { current } = useSettingsNavigation()
  return (
    <div className='settings-breadcrumb text-muted-foreground flex items-center gap-2 text-xs'>
      <span>{t('System Settings')}</span>
      {current && (
        <>
          <ChevronRight className='size-3' aria-hidden='true' />
          <span>{current.title}</span>
        </>
      )}
    </div>
  )
}

export function SettingsSearch() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { groups, pathname } = useSettingsNavigation()
  const [open, setOpen] = useState(false)
  return (
    <>
      <Button
        type='button'
        variant='outline'
        onClick={() => setOpen(true)}
        className='settings-search-trigger gap-2'
      >
        <Search className='size-4' aria-hidden='true' />
        <span>{t('Search settings')}</span>
      </Button>
      <CommandDialog
        open={open}
        onOpenChange={setOpen}
        className='top-[12dvh] sm:top-[15dvh]'
        title={t('Search settings')}
        description={t('Go to a settings section')}
      >
        <Command>
          <CommandInput placeholder={t('Search settings')} />
          <CommandList className='max-h-[min(65dvh,28rem)]'>
            <CommandEmpty>{t('No settings found')}</CommandEmpty>
            {groups.map((group) => (
              <CommandGroup key={group.title} heading={group.title}>
                {group.items.map((item) => (
                  <CommandItem
                    key={item.url}
                    value={`${group.title} ${item.title} ${item.url}`}
                    onSelect={() => {
                      setOpen(false)
                      void navigate({ to: item.url })
                    }}
                    className='gap-3 py-3'
                    aria-current={pathname === item.url ? 'page' : undefined}
                  >
                    {group.icon && (
                      <group.icon
                        className='text-muted-foreground size-4'
                        aria-hidden='true'
                      />
                    )}
                    <span className='flex-1'>{item.title}</span>
                    <ArrowUpRight
                      className='text-muted-foreground size-3.5'
                      aria-hidden='true'
                    />
                  </CommandItem>
                ))}
              </CommandGroup>
            ))}
          </CommandList>
        </Command>
      </CommandDialog>
    </>
  )
}
