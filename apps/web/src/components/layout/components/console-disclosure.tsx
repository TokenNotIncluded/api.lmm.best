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
import { useLocation } from '@tanstack/react-router'
import { ChevronRight } from 'lucide-react'
import { useState, type ReactNode } from 'react'

export function ConsoleDisclosure({
  id,
  title,
  children,
}: {
  id: string
  title: ReactNode
  children: ReactNode
}) {
  const hash = useLocation({ select: (location) => location.hash })
  const [choice, setChoice] = useState<{ hash: string; open: boolean } | null>(
    null
  )
  const open =
    choice?.hash === hash ? choice.open : hash.replace(/^#/, '') === id
  return (
    <details
      id={id}
      className='console-disclosure scroll-mt-4'
      open={open}
      onToggle={(event) => {
        const nextOpen = event.currentTarget.open
        if (nextOpen !== open) setChoice({ hash, open: nextOpen })
      }}
    >
      <summary>
        <span>{title}</span>
        <ChevronRight aria-hidden='true' />
      </summary>
      <div className='console-disclosure-body'>{children}</div>
    </details>
  )
}
