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
// Luma visual recipes adapted from shadcn/ui (MIT); see LUMA-LICENSE.txt.
import * as React from 'react'

import { cn } from '@/lib/utils'

function Card({
  className,
  size = 'default',
  variant = 'outlined',
  ...props
}: React.ComponentProps<'div'> & {
  size?: 'default' | 'sm'
  variant?: 'outlined' | 'paper'
}) {
  return (
    <div
      data-slot='card'
      data-size={size}
      data-variant={variant}
      className={cn(
        'group/card border-border/70 flex flex-col border transition-[color,background-color,border-color,box-shadow,opacity,transform] has-data-[slot=card-footer]:pb-0 data-[size=sm]:has-data-[slot=card-footer]:pb-0 bg-card text-card-foreground ring-foreground/5 dark:ring-foreground/10 gap-(--card-spacing) overflow-hidden rounded-4xl py-(--card-spacing) text-sm shadow-md ring-1 [--card-spacing:--spacing(6)] has-[>img:first-child]:pt-0 data-[size=sm]:[--card-spacing:--spacing(4)] *:[img:first-child]:rounded-t-4xl *:[img:last-child]:rounded-b-4xl motion-reduce:transition-none',
        variant === 'paper' && 'border-0 ring-0 shadow-none',
        className
      )}
      {...props}
    />
  )
}

function CardHeader({ className, ...props }: React.ComponentProps<'div'>) {
  return (
    <div
      data-slot='card-header'
      className={cn(
        'group/card-header @container/card-header grid auto-rows-min items-start has-data-[slot=card-action]:grid-cols-[1fr_auto] has-data-[slot=card-description]:grid-rows-[auto_auto] gap-1.5 rounded-t-4xl px-(--card-spacing) [.border-b]:pb-(--card-spacing)',
        className
      )}
      {...props}
    />
  )
}

function CardTitle({ className, ...props }: React.ComponentProps<'div'>) {
  return (
    <div
      data-slot='card-title'
      className={cn(
        'tracking-tight group-data-[size=sm]/card:text-sm text-base font-medium',
        className
      )}
      {...props}
    />
  )
}

function CardDescription({ className, ...props }: React.ComponentProps<'div'>) {
  return (
    <div
      data-slot='card-description'
      className={cn('text-muted-foreground text-sm', className)}
      {...props}
    />
  )
}

function CardAction({ className, ...props }: React.ComponentProps<'div'>) {
  return (
    <div
      data-slot='card-action'
      className={cn(
        'col-start-2 row-span-2 row-start-1 self-start justify-self-end',
        className
      )}
      {...props}
    />
  )
}

function CardContent({ className, ...props }: React.ComponentProps<'div'>) {
  return (
    <div
      data-slot='card-content'
      className={cn('px-(--card-spacing)', className)}
      {...props}
    />
  )
}

function CardFooter({ className, ...props }: React.ComponentProps<'div'>) {
  return (
    <div
      data-slot='card-footer'
      className={cn(
        'bg-muted/50 flex items-center border-t p-4 group-data-[size=sm]/card:p-3 rounded-b-4xl px-(--card-spacing) [.border-t]:pt-(--card-spacing)',
        className
      )}
      {...props}
    />
  )
}

export {
  Card,
  CardHeader,
  CardFooter,
  CardTitle,
  CardAction,
  CardDescription,
  CardContent,
}
