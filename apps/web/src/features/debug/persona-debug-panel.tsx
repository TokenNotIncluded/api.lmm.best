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
import { Bug01Icon, RefreshIcon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Popover,
  PopoverContent,
  PopoverDescription,
  PopoverHeader,
  PopoverTitle,
  PopoverTrigger,
} from '@/components/ui/popover'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { getAuthenticatedLandingRoute } from '@/lib/console-activation'
import { useAuthStore } from '@/stores/auth-store'

import {
  DEBUG_PERSONA_IDS,
  getActiveDebugPersona,
  resetPersonaDebugRuntime,
  setActiveDebugPersona,
  subscribeDebugPersona,
  type DebugPersonaId,
} from './persona-runtime'

const PERSONA_LABELS: Record<DebugPersonaId, string> = {
  l0: 'L0 newcomer',
  b: 'B · Guided buyer',
  e: 'E · Normal user',
  f: 'F · Enterprise operator',
  l1: 'L1 developer',
  admin: 'Administrator',
}

export function PersonaDebugPanel() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [persona, setPersona] = useState(getActiveDebugPersona)

  useEffect(() => subscribeDebugPersona(setPersona), [])

  const activate = (nextPersona: DebugPersonaId) => {
    if (nextPersona === persona) return
    setActiveDebugPersona(nextPersona)
    queryClient.clear()
    setPersona(nextPersona)
    const user = useAuthStore.getState().auth.user
    void navigate({ to: getAuthenticatedLandingRoute(user), replace: true })
  }

  const reset = () => {
    resetPersonaDebugRuntime()
    queryClient.clear()
    setPersona('l0')
    void navigate({ to: '/wallet', replace: true })
  }

  return (
    <Popover>
      <PopoverTrigger
        render={
          <Button
            type='button'
            variant='outline'
            size='sm'
            className='fixed bottom-[max(4.5rem,env(safe-area-inset-bottom))] left-3 shadow-md md:bottom-3'
            aria-label={t('Open persona debug panel')}
            data-testid='persona-debug-trigger'
          />
        }
      >
        <HugeiconsIcon
          icon={Bug01Icon}
          strokeWidth={2}
          data-icon='inline-start'
          aria-hidden='true'
        />
        <span className='hidden sm:inline'>{t('Persona lab')}</span>
        <Badge variant='secondary'>{persona.toUpperCase()}</Badge>
      </PopoverTrigger>
      <PopoverContent
        align='end'
        side='top'
        className='w-[min(23rem,calc(100vw-1.5rem))] gap-3'
        data-testid='persona-debug-panel'
      >
        <PopoverHeader>
          <PopoverTitle>{t('Persona lab')}</PopoverTitle>
          <PopoverDescription>
            {t(
              'Local fixture data only. Unmocked API requests are blocked and no credentials are sent.'
            )}
          </PopoverDescription>
        </PopoverHeader>
        <Alert>
          <HugeiconsIcon icon={Bug01Icon} strokeWidth={2} aria-hidden='true' />
          <AlertTitle>{t('Debug mode is active')}</AlertTitle>
          <AlertDescription>
            {t('This identity exists only in the local development server.')}
          </AlertDescription>
        </Alert>
        <ToggleGroup
          value={[persona]}
          onValueChange={(values) => {
            const nextPersona = values.find((value) => value !== persona)
            if (DEBUG_PERSONA_IDS.includes(nextPersona as DebugPersonaId)) {
              activate(nextPersona as DebugPersonaId)
            }
          }}
          variant='outline'
          spacing={2}
          aria-label={t('Test persona')}
          className='grid w-full grid-cols-1 gap-2 sm:grid-cols-3'
        >
          {DEBUG_PERSONA_IDS.map((id) => (
            <ToggleGroupItem
              key={id}
              value={id}
              className='h-auto min-h-12 w-full flex-col gap-0.5 px-2 py-2'
              data-testid={`persona-debug-option-${id}`}
            >
              <span className='font-medium'>{id.toUpperCase()}</span>
              <span className='text-muted-foreground text-xs font-normal'>
                {t(PERSONA_LABELS[id])}
              </span>
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
        <Button type='button' variant='outline' onClick={reset}>
          <HugeiconsIcon
            icon={RefreshIcon}
            strokeWidth={2}
            data-icon='inline-start'
            aria-hidden='true'
          />
          {t('Reset fixture data')}
        </Button>
      </PopoverContent>
    </Popover>
  )
}
