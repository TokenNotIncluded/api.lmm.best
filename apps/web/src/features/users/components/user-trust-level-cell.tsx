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
import { ArrowDown01Icon, ArrowUp01Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Spinner } from '@/components/ui/spinner'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'

import { setUserTrustLevel } from '../api'
import { USER_ROLE, isUserDeleted } from '../constants'
import type { User } from '../types'
import { useUsers } from './users-provider'

const MIN_USER_TRUST_LEVEL = 0
const MAX_USER_TRUST_LEVEL = 4

export function UserTrustLevelCell({ user }: { user: User }) {
  const { t } = useTranslation()
  const { triggerRefresh } = useUsers()
  const updatingRef = useRef(false)
  const [pendingLevel, setPendingLevel] = useState<number | null>(null)
  const info = user.trust_level_info
  let level = info?.level ?? MIN_USER_TRUST_LEVEL
  if (user.role >= USER_ROLE.ROOT) level = 6
  else if (user.role >= USER_ROLE.ADMIN) level = 5

  let badgeVariant: 'info' | 'success' | 'neutral' = 'neutral'
  if (level >= 5) badgeVariant = 'info'
  else if (level >= 3) badgeVariant = 'success'

  const overridden = info?.overridden === true
  const isAdjustable =
    user.role < USER_ROLE.ADMIN &&
    !isUserDeleted(user) &&
    level >= MIN_USER_TRUST_LEVEL &&
    level <= MAX_USER_TRUST_LEVEL
  const isUpdating = pendingLevel !== null
  const canDecrease = isAdjustable && level > MIN_USER_TRUST_LEVEL
  const canIncrease = isAdjustable && level < MAX_USER_TRUST_LEVEL

  const updateLevel = async (nextLevel: number) => {
    if (
      updatingRef.current ||
      !isAdjustable ||
      Math.abs(nextLevel - level) !== 1 ||
      nextLevel < MIN_USER_TRUST_LEVEL ||
      nextLevel > MAX_USER_TRUST_LEVEL
    ) {
      return
    }

    updatingRef.current = true
    setPendingLevel(nextLevel)
    try {
      const result = await setUserTrustLevel({
        id: user.id,
        action: 'set_trust_level',
        value: nextLevel,
      })
      if (!result.success) {
        toast.error(result.message || t('Failed to update trust level'))
        return
      }
      toast.success(t('Trust level updated successfully'))
      triggerRefresh()
    } catch {
      toast.error(t('Failed to update trust level'))
    } finally {
      updatingRef.current = false
      setPendingLevel(null)
    }
  }

  return (
    <div className='flex items-center gap-0.5'>
      <Tooltip>
        <TooltipTrigger
          render={
            <Button
              variant='ghost'
              size='icon-xs'
              disabled={!canDecrease || isUpdating}
              onClick={() => updateLevel(level - 1)}
              aria-label={t('Decrease trust level')}
            />
          }
        >
          {pendingLevel === level - 1 ? (
            <Spinner />
          ) : (
            <HugeiconsIcon
              icon={ArrowDown01Icon}
              strokeWidth={2}
              aria-hidden='true'
            />
          )}
        </TooltipTrigger>
        <TooltipContent>{t('Decrease trust level')}</TooltipContent>
      </Tooltip>

      <Tooltip>
        <TooltipTrigger
          render={
            <StatusBadge
              label={`L${level}`}
              variant={badgeVariant}
              copyable={false}
              className='cursor-help'
            />
          }
        />
        <TooltipContent>
          <p className='text-xs'>
            {user.role >= USER_ROLE.ADMIN
              ? t('Role-assigned access')
              : overridden
                ? t('Administrator override')
                : t('Automatic recharge level')}
            {info?.discount_percent
              ? ` · ${info.discount_percent}% ${t('discount')}`
              : ''}
          </p>
        </TooltipContent>
      </Tooltip>

      <Tooltip>
        <TooltipTrigger
          render={
            <Button
              variant='ghost'
              size='icon-xs'
              disabled={!canIncrease || isUpdating}
              onClick={() => updateLevel(level + 1)}
              aria-label={t('Increase trust level')}
            />
          }
        >
          {pendingLevel === level + 1 ? (
            <Spinner />
          ) : (
            <HugeiconsIcon
              icon={ArrowUp01Icon}
              strokeWidth={2}
              aria-hidden='true'
            />
          )}
        </TooltipTrigger>
        <TooltipContent>{t('Increase trust level')}</TooltipContent>
      </Tooltip>
    </div>
  )
}
