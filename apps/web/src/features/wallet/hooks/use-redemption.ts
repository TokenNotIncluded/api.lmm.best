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
import i18next from 'i18next'
import { useState, useCallback } from 'react'
import { toast } from 'sonner'

import { api, getSelf } from '@/lib/api'
import { formatQuota } from '@/lib/format'

type RedemptionReward = {
  reward_type: 'quota' | 'reset_voucher'
  quota?: number
  reset_voucher_id?: number
  reset_plan_id?: number
  reset_voucher_expires_at?: number
}

type RedemptionResponse = {
  success: boolean
  message?: string
  data?: RedemptionReward
}

export function useRedemption() {
  const [redeeming, setRedeeming] = useState(false)

  const redeemCode = useCallback(async (code: string): Promise<boolean> => {
    if (!code || code.trim() === '') {
      toast.error(i18next.t('Please enter a redemption code'))
      return false
    }

    try {
      setRedeeming(true)
      const result = await api.post<RedemptionResponse>(
        '/api/user/redemption',
        {
          key: code.trim(),
        }
      )
      const response = result.data

      if (response.success && response.data) {
        if (response.data.reward_type === 'reset_voucher') {
          toast.success(
            i18next.t(
              'Redemption successful! A banked reset voucher was added.'
            )
          )
        } else {
          toast.success(
            i18next.t('Redemption successful! Added: {{quota}}', {
              quota: formatQuota(response.data.quota ?? 0),
            })
          )
        }
        await getSelf()
        return true
      }

      toast.error(response.message || i18next.t('Redemption failed'))
      return false
    } catch {
      toast.error(i18next.t('Redemption failed'))
      return false
    } finally {
      setRedeeming(false)
    }
  }, [])

  return {
    redeeming,
    redeemCode,
  }
}
