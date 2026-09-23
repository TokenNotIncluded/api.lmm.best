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

import {
  buildSettingsSearchIndex,
  countSettingsEntries,
  searchSettings,
} from '../utils/settings-search-index'

function useSettingsNavigation() {
  const { t } = useTranslation()
  const pathname = useLocation({ select: (location) => location.pathname })
  const search = useLocation({ select: (location) => location.searchStr })
  const index = useMemo(() => buildSettingsSearchIndex(t), [t])
  const current = index.find((group) =>
    group.entries.some((entry) => entry.url === `${pathname}${search}`)
  )
  return { index, current, pathname, search }
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
          <span>{current.section}</span>
        </>
      )}
    </div>
  )
}

export function SettingsSearch() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { index, pathname, search } = useSettingsNavigation()
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const results = useMemo(() => searchSettings(index, query), [index, query])
  const activeUrl = `${pathname}${search}`
  const total = countSettingsEntries(index)

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
        onOpenChange={(next) => {
          setOpen(next)
          if (!next) setQuery('')
        }}
        className='top-[12dvh] sm:top-[15dvh]'
        title={t('Search settings')}
        description={t('Go to a settings section')}
      >
        {/*
          Filtering is owned by the index (titles, admin section, alternate
          terms and URL slug) so `shouldFilter` stays off and the ranking the
          user sees is the one `searchSettings` computed.
        */}
        <Command shouldFilter={false}>
          <CommandInput
            value={query}
            onValueChange={setQuery}
            placeholder={t('Search settings')}
          />
          <CommandList className='max-h-[min(65dvh,28rem)]'>
            <CommandEmpty>{t('No settings found')}</CommandEmpty>
            {results.map((group) => (
              <CommandGroup key={group.section} heading={group.group}>
                {group.entries.map((entry) => (
                  <CommandItem
                    key={entry.url}
                    value={`${group.group} ${entry.section} ${entry.url}`}
                    onSelect={() => {
                      setOpen(false)
                      setQuery('')
                      void navigate({
                        to: entry.url.split('?')[0],
                        search: entry.url.includes('?')
                          ? Object.fromEntries(
                              new URLSearchParams(entry.url.split('?')[1])
                            )
                          : undefined,
                      })
                    }}
                    className='gap-3 py-3'
                    aria-current={activeUrl === entry.url ? 'page' : undefined}
                  >
                    <span className='flex-1'>{entry.section}</span>
                    <span className='text-muted-foreground text-xs'>
                      {group.group}
                    </span>
                    <ArrowUpRight
                      className='text-muted-foreground size-3.5'
                      aria-hidden='true'
                    />
                  </CommandItem>
                ))}
              </CommandGroup>
            ))}
          </CommandList>
          <div className='text-muted-foreground border-border/70 border-t px-3 py-2 text-xs'>
            {t('{{count}} sections', {
              count: query.trim() ? countSettingsEntries(results) : total,
            })}
          </div>
        </Command>
      </CommandDialog>
    </>
  )
}
