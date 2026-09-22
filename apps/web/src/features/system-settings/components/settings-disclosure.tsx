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
import { ChevronDown } from 'lucide-react'
import { useEffect, useRef, useState, type ReactNode } from 'react'

import { cn } from '@/lib/utils'

export function SettingsDisclosure({
  title,
  description,
  children,
  defaultOpen = false,
  className,
}: {
  title: ReactNode
  description?: ReactNode
  children: ReactNode
  defaultOpen?: boolean
  className?: string
}) {
  const ref = useRef<HTMLDetailsElement>(null)
  const [open, setOpen] = useState(defaultOpen)
  useEffect(() => {
    const element = ref.current
    if (!element || typeof MutationObserver === 'undefined') return
    const revealInvalidFields = () => {
      if (element.querySelector('[aria-invalid="true"]')) {
        element.open = true
        setOpen(true)
      }
    }
    revealInvalidFields()
    const observer = new MutationObserver(revealInvalidFields)
    observer.observe(element, {
      subtree: true,
      attributes: true,
      attributeFilter: ['aria-invalid'],
      childList: true,
    })
    return () => observer.disconnect()
  }, [])
  return (
    <details
      ref={ref}
      open={open}
      onToggle={(event) => setOpen(event.currentTarget.open)}
      onInvalidCapture={() => {
        if (ref.current) ref.current.open = true
        setOpen(true)
      }}
      className={cn('settings-disclosure group/disclosure', className)}
    >
      <summary className='settings-disclosure-summary'>
        <span className='min-w-0 flex-1'>
          <span className='block text-sm font-semibold break-words'>
            {title}
          </span>
          {description && (
            <span className='text-muted-foreground mt-1 block text-xs leading-relaxed'>
              {description}
            </span>
          )}
        </span>
        <ChevronDown
          className='text-muted-foreground size-4 shrink-0 transition-transform duration-200 group-open/disclosure:rotate-180 motion-reduce:transition-none'
          aria-hidden='true'
        />
      </summary>
      <div className='settings-disclosure-body'>{children}</div>
    </details>
  )
}
