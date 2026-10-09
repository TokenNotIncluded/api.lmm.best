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
import { SidebarTrigger } from '@/components/ui/sidebar'
import { cn } from '@/lib/utils'

type HeaderProps = React.HTMLAttributes<HTMLElement>

type HeaderLayoutProps = HeaderProps & {
  showSidebarTrigger?: boolean
}

export function Header({
  className,
  children,
  showSidebarTrigger = true,
  ...props
}: HeaderLayoutProps) {
  return (
    <header
      className={cn(
        'sticky top-0 z-40 h-[var(--app-header-height,3rem)] w-full shrink-0 bg-transparent',
        className
      )}
      {...props}
    >
      <div className='flex h-full min-w-0 items-center gap-1.5 px-2 md:gap-2 md:px-3'>
        {showSidebarTrigger ? (
          <SidebarTrigger variant='ghost' className='size-11 md:size-8' />
        ) : null}
        {children}
      </div>
    </header>
  )
}
