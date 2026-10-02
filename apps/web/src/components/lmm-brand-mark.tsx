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

export const LMM_BRAND_NAME = 'LMM Forge'

type LmmBrandMarkProps = SVGProps<SVGSVGElement> & {
  title?: string
}

/** Original cut symbol; keep the full viewBox and its open counterforms. */
export function LmmBrandMark({
  className,
  title,
  ...props
}: LmmBrandMarkProps) {
  return (
    <svg
      viewBox='0 0 128 128'
      xmlns='http://www.w3.org/2000/svg'
      role={title ? 'img' : undefined}
      aria-label={title}
      aria-hidden={title ? undefined : true}
      focusable='false'
      className={cn('shrink-0', className)}
      {...props}
    >
      <path
        d='M6 10H24V94H32V20H44L52 40L60 20H72V94H82V20H94L102 40L110 20H122V100L110 112H6Z M44 94V52L52 70L60 52V94Z M94 94V52L102 70L110 52V94Z'
        fill='currentColor'
        fillRule='evenodd'
      />
    </svg>
  )
}
