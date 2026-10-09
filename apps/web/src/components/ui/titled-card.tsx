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
import type { ReactNode } from 'react'

import { cn } from '@/lib/utils'

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from './card'
import { IconBadge, type IconBadgeTone } from './icon-badge'

type TitledCardProps = {
  title: ReactNode
  description?: ReactNode
  icon?: ReactNode
  action?: ReactNode
  actionPlacement?: 'beside' | 'below'
  children?: ReactNode
  disableHoverEffect?: boolean
  appearance?: 'paper' | 'outlined'
  className?: string
  headerClassName?: string
  contentClassName?: string
  iconClassName?: string
  iconTone?: IconBadgeTone
  titleClassName?: string
  descriptionClassName?: string
}

export function TitledCard({
  title,
  description,
  icon,
  action,
  actionPlacement = 'beside',
  children,
  disableHoverEffect,
  appearance = 'paper',
  className,
  headerClassName,
  contentClassName,
  iconClassName,
  iconTone,
  titleClassName,
  descriptionClassName,
}: TitledCardProps) {
  return (
    <Card
      variant={appearance}
      data-card-hover={
        disableHoverEffect || appearance === 'paper' ? 'false' : undefined
      }
      className={cn('gap-0 overflow-hidden py-0', className)}
    >
      <CardHeader
        className={cn(
          'p-4 !pb-0 sm:p-6 sm:!pb-0',
          appearance === 'outlined' && 'border-b !pb-4 sm:!pb-5',
          headerClassName
        )}
      >
        <div
          className={cn(
            'flex min-w-0 flex-col gap-3',
            actionPlacement === 'beside' &&
              'sm:flex-row sm:items-start sm:justify-between'
          )}
        >
          <div className='flex min-w-0 items-center gap-3'>
            {icon != null && (
              <IconBadge size='title' tone={iconTone} className={iconClassName}>
                {icon}
              </IconBadge>
            )}
            <div
              className={cn('min-w-0', actionPlacement === 'below' && 'flex-1')}
            >
              <CardTitle
                className={cn(
                  'text-base font-semibold tracking-tight sm:text-lg',
                  titleClassName
                )}
              >
                {title}
              </CardTitle>
              {description != null && (
                <CardDescription
                  className={cn(
                    'mt-1 text-xs leading-relaxed sm:text-sm',
                    descriptionClassName
                  )}
                >
                  {description}
                </CardDescription>
              )}
            </div>
          </div>
          {action != null && (
            <div
              className={cn(
                'w-full shrink-0',
                actionPlacement === 'beside' && 'sm:w-auto'
              )}
            >
              {action}
            </div>
          )}
        </div>
      </CardHeader>
      <CardContent className={cn('p-4 pt-3 sm:p-6 sm:pt-4', contentClassName)}>
        {children}
      </CardContent>
    </Card>
  )
}
