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
import type { SVGProps } from 'react'

import { cn } from '@/lib/utils'

export const LMM_BRAND_NAME = 'LMM Best'

type LmmBrandMarkProps = SVGProps<SVGSVGElement> & {
  title?: string
}

/**
 * LMM monogram: the L is a corner bracket that holds two Ms sharing one
 * middle leg — many models behind one endpoint.
 */
export function LmmBrandMark({
  className,
  title,
  ...props
}: LmmBrandMarkProps) {
  return (
    <svg
      viewBox='0 0 56 56'
      xmlns='http://www.w3.org/2000/svg'
      role={title ? 'img' : undefined}
      aria-label={title}
      aria-hidden={title ? undefined : true}
      focusable='false'
      className={cn('shrink-0', className)}
      {...props}
    >
      <path
        d='M18 39V17l7 11 7-11 7 11 7-11v22M32 17v22'
        fill='none'
        stroke='var(--forge-brand-mark-ink, currentColor)'
        strokeWidth='4'
        strokeLinejoin='miter'
        strokeMiterlimit='10'
      />
      <path
        d='M9 8v39h39'
        fill='none'
        stroke='var(--forge-brand-mark-accent, currentColor)'
        strokeWidth='4'
        strokeLinecap='square'
      />
    </svg>
  )
}
