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
import { useTranslation } from 'react-i18next'

import { cn } from '@/lib/utils'

type AccessRestrictionNoticeProps = {
  className?: string
}

const PROJECT_LINKS = {
  project: 'https://github.com/LIghtJUNction/api.lmm.best',
  issues: 'https://github.com/LIghtJUNction/api.lmm.best/issues',
  releases: 'https://github.com/LIghtJUNction/api.lmm.best/releases',
} as const

export function AccessRestrictionNotice(props: AccessRestrictionNoticeProps) {
  const { t } = useTranslation()

  return (
    <aside
      role='note'
      className={cn(
        'bg-muted/40 text-muted-foreground px-4 py-2 text-center text-[11px] leading-4 font-medium',
        props.className
      )}
    >
      <span className='font-semibold'>
        {t(
          'Service access notice: This notice refers only to ISO 3166-1 alpha-2 CN (Mainland China). It does not state service availability for any other location.'
        )}
      </span>
      <span aria-hidden='true' className='mx-1.5'>
        ·
      </span>
      <a
        href={PROJECT_LINKS.project}
        target='_blank'
        rel='noopener noreferrer'
        className='hover:text-foreground underline-offset-2 transition-colors hover:underline'
      >
        {t('GitHub project')}
      </a>
      <span aria-hidden='true' className='mx-1.5'>
        ·
      </span>
      <a
        href={PROJECT_LINKS.issues}
        target='_blank'
        rel='noopener noreferrer'
        className='hover:text-foreground underline-offset-2 transition-colors hover:underline'
      >
        {t('Report an issue')}
      </a>
      <span aria-hidden='true' className='mx-1.5'>
        ·
      </span>
      <a
        href={PROJECT_LINKS.releases}
        target='_blank'
        rel='noopener noreferrer'
        className='hover:text-foreground underline-offset-2 transition-colors hover:underline'
      >
        {t('Changelog')}
      </a>
    </aside>
  )
}
