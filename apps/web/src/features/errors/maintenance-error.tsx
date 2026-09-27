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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

import { ErrorPageFrame } from './error-page-frame'
import { SignalTuner } from './signal-tuner'

export function MaintenanceError() {
  const { t } = useTranslation()
  const [checked, setChecked] = useState(false)

  // There is no server action available during maintenance, so the primary
  // button reloads the page - the one thing a visitor can actually try.
  const retry = () => {
    setChecked(true)
    window.location.reload()
  }

  return (
    <ErrorPageFrame
      status='503'
      title={t('Down for a short repair.')}
      description={t('The site is offline while it is being upgraded.')}
      note={
        checked
          ? t('Still offline. Reload again in a minute.')
          : t('Everything else keeps working: prices and docs stay public.')
      }
      actions={
        <>
          <Button
            size='lg'
            className='error-editorial-action error-editorial-action-primary'
            onClick={retry}
          >
            {t('Retry')}
          </Button>
          <Button
            variant='outline'
            className='error-editorial-action error-editorial-action-secondary'
            render={
              <a
                href='https://github.com/TokenNotIncluded/api.lmm.best'
                target='_blank'
                rel='noopener noreferrer'
              />
            }
          >
            {t('Learn more')}
          </Button>
        </>
      }
      play={<SignalTuner />}
    />
  )
}
