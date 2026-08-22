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
import { HugeiconsIcon } from '@hugeicons/react'
import type { TFunction } from 'i18next'

import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'

import { getHeroSmsStatusPresentation } from './status-meta'

export function HeroSmsStatusBadge({
  status,
  className,
  t,
}: {
  status: string | null | undefined
  className?: string
  t: TFunction
}) {
  const presentation = getHeroSmsStatusPresentation(status, t)

  return (
    <Badge
      variant={presentation.tone}
      className={cn('gap-1.5', className)}
      aria-label={presentation.label}
    >
      <HugeiconsIcon icon={presentation.icon} strokeWidth={2} aria-hidden='true' />
      <span>{presentation.label}</span>
    </Badge>
  )
}
