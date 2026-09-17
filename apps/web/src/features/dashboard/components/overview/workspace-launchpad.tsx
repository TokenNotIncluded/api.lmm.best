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
import { ArrowUpRight, Search } from 'lucide-react'

import type { WorkspaceCopy } from '@/components/layout/lib/workspace-copy'
import type { NavLink } from '@/components/layout/types'

function shortcutDescription(url: NavLink['url'], copy: WorkspaceCopy): string {
  switch (url) {
    case '/keys':
      return copy.keys
    case '/pricing':
      return copy.pricing
    case '/wallet':
      return copy.wallet
    case '/usage-logs/common':
      return copy.usage
    default:
      return ''
  }
}

type WorkspaceLaunchpadProps = {
  name?: string
  copy: WorkspaceCopy
  shortcuts: NavLink[]
  onSearch: () => void
  onModelPanel: (trigger: HTMLElement) => void
}

/** Presentation only; destinations have passed the existing sidebar policy. */
export function WorkspaceLaunchpad({
  name,
  copy,
  shortcuts,
  onSearch,
  onModelPanel,
}: WorkspaceLaunchpadProps) {
  return (
    <section className='workspace-launchpad' aria-label={copy.workspace}>
      <div className='workspace-welcome'>
        <div className='workspace-welcome-copy'>
          <p className='workspace-eyebrow'>{copy.workspace}</p>
          <h2>
            {name || copy.workspace}
            <span aria-hidden='true'>↗</span>
          </h2>
          <p className='workspace-description'>{copy.description}</p>
        </div>
        <button
          type='button'
          className='workspace-command'
          onClick={onSearch}
        >
          <Search size={17} aria-hidden='true' />
          <span>{copy.allTools}</span>
          <ArrowUpRight size={17} aria-hidden='true' />
        </button>
      </div>
      {shortcuts.length > 0 ? (
        <nav className='workspace-shortcuts' aria-label={copy.shortcuts}>
          {shortcuts.map((item, index) => {
            const contents = (
              <>
                <span className='workspace-shortcut-top'>
                  <span className='workspace-shortcut-icon'>
                    {item.icon && <item.icon size={21} aria-hidden='true' />}
                  </span>
                  <span
                    className='workspace-shortcut-index'
                    aria-hidden='true'
                  >
                    0{index + 1}
                  </span>
                </span>
                <span className='workspace-shortcut-title'>
                  {item.title}
                  <ArrowUpRight size={18} aria-hidden='true' />
                </span>
                <span className='workspace-shortcut-description'>
                  {shortcutDescription(item.url, copy)}
                </span>
              </>
            )
            // In-context model selection stays a button, not a pretend link.
            return item.interaction === 'model-panel' ? (
              <button
                type='button'
                key={item.url}
                className='workspace-shortcut'
                onClick={(event) => onModelPanel(event.currentTarget)}
              >
                {contents}
              </button>
            ) : (
              <Link
                key={item.url}
                to={item.url}
                className='workspace-shortcut'
              >
                {contents}
              </Link>
            )
          })}
        </nav>
      ) : (
        <p className='workspace-empty-hint'>{copy.empty}</p>
      )}
    </section>
  )
}
