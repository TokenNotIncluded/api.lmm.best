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

/** Shared cut symbol; keep its full viewBox and the open 16-unit gaps. */
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
        d='M8 16H24V96H120V104L112 112H8Z M40 80V16H50L56 30L62 16H72V80H62V42L56 56L50 42V80Z M88 80V16H98L104 30L110 16H120V80H110V42L104 56L98 42V80Z'
        fill='currentColor'
        fillRule='evenodd'
      />
    </svg>
  )
}
