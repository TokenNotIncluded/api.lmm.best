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

/** A quiet angular monogram for the LMM Best identity. */
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
        d='M10 39V16l18 18 18-18v23'
        fill='none'
        stroke='var(--forge-brand-mark-ink)'
        strokeWidth='3.5'
        strokeLinecap='square'
        strokeLinejoin='round'
      />
      <path
        d='M12 45h32'
        fill='none'
        stroke='var(--forge-brand-mark-accent)'
        strokeWidth='2.5'
        strokeLinecap='square'
      />
    </svg>
  )
}
