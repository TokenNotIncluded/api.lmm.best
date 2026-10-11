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

type LmmBrandWordmarkProps = SVGProps<SVGSVGElement> & { title?: string }

/** Geometric LMM API lettering; no external font request. */
export function LmmBrandWordmark({
  className,
  title,
  ...props
}: LmmBrandWordmarkProps) {
  return (
    <svg
      viewBox='0 0 643 100'
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
      <path d='M0 0H18V82H76V100H0Z' />
      <path d='M90 100V0H110L148 44L186 0H206V100H186V30L148 74L110 30V100Z' />
      <path d='M224 100V0H244L282 44L320 0H340V100H320V30L282 74L244 30V100Z' />
      <path d='M390 100L432 0H455L497 100H475L467 80H420L412 100Z M428 61H459L443 22Z' />
      <path d='M510 100V0H564C590 0 605 13 605 36C605 59 590 72 564 72H530V100Z M530 18V54H562C576 54 585 49 585 36C585 24 576 18 562 18Z' />
      <path d='M623 0H643V100H623Z' />
    </svg>
  )
}
