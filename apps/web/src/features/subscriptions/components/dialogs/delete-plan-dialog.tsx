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
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'

import { deletePlan } from '../../api.js'
import { useSubscriptions } from '../subscriptions-provider.js'
import { getDeletePlanErrorMessage } from './delete-plan-error.js'

export function DeletePlanDialog() {
  const { t } = useTranslation()
  const { open, setOpen, currentRow, triggerRefresh } = useSubscriptions()
  const [loading, setLoading] = useState(false)

  if (open !== 'delete-plan' || !currentRow) return null

  const planLabel = currentRow.plan.title || `#${currentRow.plan.id}`
  const handleConfirm = async () => {
    setLoading(true)
    try {
      const response = await deletePlan(currentRow.plan.id)
      if (!response.success) {
        toast.error(t(response.message || 'Operation failed'))
        return
      }
      toast.success(t('Subscription plan deleted'))
      triggerRefresh()
      setOpen(null)
    } catch (error) {
      toast.error(t(getDeletePlanErrorMessage(error, t('Operation failed'))))
    } finally {
      setLoading(false)
    }
  }

  return (
    <ConfirmDialog
      open
      onOpenChange={(nextOpen) => !nextOpen && setOpen(null)}
      title={t('Delete subscription plan')}
      desc={t(
        'Delete subscription plan "{{name}}"? Only unused plans can be deleted. This action cannot be undone.',
        { name: planLabel }
      )}
      handleConfirm={handleConfirm}
      isLoading={loading}
      confirmText={t('Delete')}
      destructive
    />
  )
}
