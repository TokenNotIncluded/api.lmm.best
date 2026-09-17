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
import { Search, X } from 'lucide-react'
import { useId, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Input } from '@/components/ui/input'

import { getWorkspaceCopy } from '../lib/workspace-copy'
import { filterWorkspaceNavigation } from '../lib/workspace-navigation'
import type { NavGroup as NavGroupData } from '../types'
import { NavGroup } from './nav-group'

export function SidebarNavigation({ groups }: { groups: NavGroupData[] }) {
  const { i18n } = useTranslation()
  const copy = getWorkspaceCopy(i18n.resolvedLanguage || i18n.language)
  const [query, setQuery] = useState('')
  const input = useRef<HTMLInputElement>(null)
  const id = useId()
  const filtered = useMemo(
    () => filterWorkspaceNavigation(groups, query),
    [groups, query]
  )
  const clear = () => {
    setQuery('')
    input.current?.focus()
  }

  return (
    <>
      <div className='workspace-sidebar-filter'>
        <label htmlFor={id} className='workspace-sidebar-label'>
          {copy.navigation}
        </label>
        <div className='workspace-search-field'>
          <Search size={16} aria-hidden='true' />
          <Input
            ref={input}
            id={id}
            type='search'
            value={query}
            placeholder={copy.searchPlaceholder}
            maxLength={120}
            autoComplete='off'
            spellCheck={false}
            onChange={(event) => setQuery(event.target.value)}
            onKeyDown={(event) => {
              if (
                event.key === 'Escape' &&
                query &&
                !event.nativeEvent.isComposing
              ) {
                event.preventDefault()
                event.stopPropagation()
                clear()
              }
            }}
          />
          {query && (
            <button type='button' aria-label={copy.clear} onClick={clear}>
              <X size={16} aria-hidden='true' />
            </button>
          )}
        </div>
      </div>
      <nav aria-label={copy.navigation} className='workspace-navigation'>
        {filtered.map((group) => (
          <NavGroup key={group.id || group.title} {...group} />
        ))}
        {filtered.length === 0 && query.trim() && (
          <div role='status' className='workspace-navigation-empty'>
            <p>{copy.noResults}</p>
            <button type='button' onClick={clear}>
              {copy.clear}
            </button>
          </div>
        )}
      </nav>
    </>
  )
}
