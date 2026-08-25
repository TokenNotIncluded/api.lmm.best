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
import { ChevronDown, Flame, RefreshCw, Star } from 'lucide-react'
import { useMemo, useState, type ReactNode } from 'react'

import { Button } from '@/components/ui/button'
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import { cn } from '@/lib/utils'

export interface SmsCatalogOption {
  value: string
  label: string
  description?: string
  searchText: string
  indexKey: string
  popularity: number
  favorite?: boolean
  leading: ReactNode
}

interface SmsCatalogPickerProps {
  id: string
  value: string
  options: SmsCatalogOption[]
  placeholder: string
  searchPlaceholder: string
  noResultsText: string
  allText: string
  popularText: string
  favoritesText: string
  retryText: string
  errorText: string
  disabled?: boolean
  isError?: boolean
  onRetry?: () => void
  onValueChange: (value: string) => void
}

type QuickFilter = 'all' | 'popular' | 'favorites' | string

const popularLimit = 12

function normalizeSearch(value: string) {
  return value.trim().toLocaleLowerCase()
}

function catalogVariant(active: boolean) {
  if (active) return 'secondary' as const
  return 'ghost' as const
}

function getVisibleOptions(
  options: SmsCatalogOption[],
  quickFilter: QuickFilter,
  searchValue: string
) {
  const search = normalizeSearch(searchValue)
  if (search) {
    return options.filter((option) =>
      normalizeSearch(option.searchText).includes(search)
    )
  }
  if (quickFilter === 'popular') {
    return options
      .filter((option) => option.popularity > 0)
      .slice(0, popularLimit)
  }
  if (quickFilter === 'favorites') {
    return options.filter((option) => option.favorite)
  }
  if (quickFilter !== 'all') {
    return options.filter((option) => option.indexKey === quickFilter)
  }
  return options
}

function SmsCatalogTrigger({
  selected,
  placeholder,
}: {
  selected?: SmsCatalogOption
  placeholder: string
}) {
  return (
    <>
      <span className='flex min-w-0 flex-1 items-center gap-3'>
        {selected?.leading && (
          <span className='shrink-0'>{selected.leading}</span>
        )}
        <span className='min-w-0 flex-1'>
          <span
            className={cn(
              'block truncate font-medium',
              !selected && 'text-muted-foreground font-normal'
            )}
          >
            {selected?.label ?? placeholder}
          </span>
          {selected?.description && (
            <span className='text-muted-foreground block truncate text-xs'>
              {selected.description}
            </span>
          )}
        </span>
      </span>
      <ChevronDown aria-hidden='true' className='size-4 shrink-0 opacity-50' />
    </>
  )
}

function SmsQuickFilterBar({
  searchValue,
  quickFilter,
  hasPopularity,
  hasFavorites,
  letters,
  allText,
  popularText,
  favoritesText,
  onFilter,
}: {
  searchValue: string
  quickFilter: QuickFilter
  hasPopularity: boolean
  hasFavorites: boolean
  letters: string[]
  allText: string
  popularText: string
  favoritesText: string
  onFilter: (filter: QuickFilter) => void
}) {
  if (searchValue) return null
  const presets: Array<{
    value: QuickFilter
    label: string
    icon?: ReactNode
  }> = [{ value: 'all', label: allText }]
  if (hasPopularity) {
    presets.push({
      value: 'popular',
      label: popularText,
      icon: <Flame data-icon='inline-start' />,
    })
  }
  if (hasFavorites) {
    presets.push({
      value: 'favorites',
      label: favoritesText,
      icon: <Star data-icon='inline-start' />,
    })
  }
  return (
    <div
      className='no-scrollbar flex gap-1 overflow-x-auto border-b px-2 py-2'
      aria-label={allText}
    >
      {presets.map((preset) => (
        <Button
          key={preset.value}
          type='button'
          size='sm'
          variant={catalogVariant(quickFilter === preset.value)}
          className='h-7 shrink-0 px-2 text-xs'
          onClick={() => onFilter(preset.value)}
        >
          {preset.icon}
          {preset.label}
        </Button>
      ))}
      {letters.map((letter) => (
        <Button
          key={letter}
          type='button'
          size='icon-sm'
          variant={catalogVariant(quickFilter === letter)}
          className='size-7 shrink-0 text-xs'
          aria-label={letter}
          onClick={() => onFilter(letter)}
        >
          {letter}
        </Button>
      ))}
    </div>
  )
}

function SmsCatalogList({
  options,
  selectedValue,
  popularValues,
  noResultsText,
  onSelect,
}: {
  options: SmsCatalogOption[]
  selectedValue: string
  popularValues: Set<string>
  noResultsText: string
  onSelect: (value: string) => void
}) {
  return (
    <CommandList className='max-h-[min(360px,50vh)]'>
      <CommandEmpty>{noResultsText}</CommandEmpty>
      <CommandGroup>
        {options.map((option) => (
          <CommandItem
            key={option.value}
            value={option.value}
            data-checked={option.value === selectedValue}
            onSelect={() => onSelect(option.value)}
            className='items-center gap-3 rounded-lg px-3 py-2.5 [content-visibility:auto]'
          >
            <span className='shrink-0'>{option.leading}</span>
            <span className='min-w-0 flex-1'>
              <span className='block truncate font-medium'>{option.label}</span>
              {option.description && (
                <span className='text-muted-foreground block truncate text-xs'>
                  {option.description}
                </span>
              )}
            </span>
            {popularValues.has(option.value) && (
              <Flame
                aria-hidden='true'
                className='text-muted-foreground size-3.5'
              />
            )}
            {option.favorite && (
              <Star
                aria-hidden='true'
                className='text-muted-foreground size-3.5 fill-current'
              />
            )}
          </CommandItem>
        ))}
      </CommandGroup>
    </CommandList>
  )
}

function SmsCatalogError({
  errorText,
  retryText,
  onRetry,
}: Pick<SmsCatalogPickerProps, 'errorText' | 'retryText' | 'onRetry'>) {
  return (
    <div className='space-y-3 p-4 text-center'>
      <p className='text-muted-foreground text-sm'>{errorText}</p>
      <Button type='button' variant='outline' size='sm' onClick={onRetry}>
        <RefreshCw data-icon='inline-start' />
        {retryText}
      </Button>
    </div>
  )
}

export function SmsCatalogPicker(props: SmsCatalogPickerProps) {
  const [open, setOpen] = useState(false)
  const [searchValue, setSearchValue] = useState('')
  const [quickFilter, setQuickFilter] = useState<QuickFilter>('all')
  const selected = props.options.find((option) => option.value === props.value)
  const popularValues = useMemo(
    () =>
      new Set(
        props.options
          .filter((option) => option.popularity > 0)
          .slice(0, popularLimit)
          .map((option) => option.value)
      ),
    [props.options]
  )
  const letters = useMemo(
    () =>
      [...new Set(props.options.map((option) => option.indexKey))]
        .filter((letter) => letter !== '#')
        .sort((left, right) => left.localeCompare(right)),
    [props.options]
  )
  const visibleOptions = useMemo(
    () => getVisibleOptions(props.options, quickFilter, searchValue),
    [props.options, quickFilter, searchValue]
  )

  const handleSelect = (selectedValue: string) => {
    props.onValueChange(selectedValue)
    setOpen(false)
    setSearchValue('')
    setQuickFilter('all')
  }

  let content = (
    <Command shouldFilter={false}>
      <CommandInput
        placeholder={props.searchPlaceholder}
        value={searchValue}
        onValueChange={setSearchValue}
      />
      <SmsQuickFilterBar
        searchValue={searchValue}
        quickFilter={quickFilter}
        hasPopularity={popularValues.size > 0}
        hasFavorites={props.options.some((option) => option.favorite)}
        letters={letters}
        allText={props.allText}
        popularText={props.popularText}
        favoritesText={props.favoritesText}
        onFilter={setQuickFilter}
      />
      <SmsCatalogList
        options={visibleOptions}
        selectedValue={props.value}
        popularValues={popularValues}
        noResultsText={props.noResultsText}
        onSelect={handleSelect}
      />
    </Command>
  )
  if (props.isError) {
    content = (
      <SmsCatalogError
        errorText={props.errorText}
        retryText={props.retryText}
        onRetry={props.onRetry}
      />
    )
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        render={
          <Button
            id={props.id}
            type='button'
            variant='outline'
            role='combobox'
            aria-expanded={open}
            disabled={props.disabled}
            className='border-input bg-background hover:bg-muted/55 hover:text-foreground data-popup-open:border-ring data-popup-open:ring-ring/20 h-auto min-h-12 w-full justify-between gap-3 rounded-lg px-3 py-2 text-start font-normal shadow-none data-popup-open:ring-[3px]'
          />
        }
      >
        <SmsCatalogTrigger
          selected={selected}
          placeholder={props.placeholder}
        />
      </PopoverTrigger>
      <PopoverContent
        align='start'
        className='data-closed:zoom-out-100 data-open:zoom-in-100 data-[side=bottom]:slide-in-from-top-0 w-[var(--anchor-width)] max-w-[calc(100vw-2rem)] overflow-hidden rounded-xl p-0 shadow-lg data-closed:duration-75 data-open:duration-100'
        onWheel={(event) => event.stopPropagation()}
        onTouchMove={(event) => event.stopPropagation()}
      >
        {content}
      </PopoverContent>
    </Popover>
  )
}
