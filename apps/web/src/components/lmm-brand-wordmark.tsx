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

type LmmBrandWordmarkProps = SVGProps<SVGSVGElement> & {
  title?: string
}

/** Original lettering from the shared master geometry, with no font request. */
export function LmmBrandWordmark({
  className,
  title,
  ...props
}: LmmBrandWordmarkProps) {
  return (
    <svg
      viewBox='0 -2 718 104'
      xmlns='http://www.w3.org/2000/svg'
      role={title ? 'img' : undefined}
      aria-label={title}
      aria-hidden={title ? undefined : true}
      focusable='false'
      fill='currentColor'
      fillRule='evenodd'
      className={cn('shrink-0', className)}
      {...props}
    >
      <g>
        <path d='M0 0H20V80H62V88L50 100H0Z' />
        <path
          transform='translate(70 0)'
          d='M0 100V0H22L45 34L68 0H90V100H70V34L45 67L20 34V100Z'
        />
        <path
          transform='translate(172 0)'
          d='M0 100V0H22L45 34L68 0H90V100H70V34L45 67L20 34V100Z'
        />
      </g>
      <g transform='translate(302 0)'>
        <path d='M0 0H64V18H20V42H55V60H20V100H0Z' />
        <path
          transform='translate(76 0)'
          d='M14 -1H64L78 13V87L64 101H14L0 87V13Z M22 18L20 20V80L22 82H56L58 80V20L56 18Z'
        />
        <path
          transform='translate(168 0)'
          d='M0 0H55L74 19V42L59 57H45L78 100H54L20 57V100H0Z M20 18V39H47L54 32V25L47 18Z'
        />
        <path
          transform='translate(258 0)'
          d='M14 -1H64L78 13V29H58V20L56 18H22L20 20V80L22 82H56L58 80V64H43V46H78V87L64 101H14L0 87V13Z'
        />
        <path
          transform='translate(350 0)'
          d='M0 0H66V18H20V41H57V59H20V82H66V88L54 100H0Z'
        />
      </g>
    </svg>
  )
}
