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
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'

import { moderateReferralUser, type ReferralModerationPayload } from '../api'
import { createReferralSubmission } from '../lib/referral-submission'
import type { User } from '../types'

export function ReferralModerationDialog({
  user,
  restore,
  onClose,
  onSuccess,
}: {
  user: User
  restore: boolean
  onClose: () => void
  onSuccess: () => void
}) {
  const { t } = useTranslation()
  const [evidence, setEvidence] = useState('')
  const [bulk, setBulk] = useState(false)
  const [penalize, setPenalize] = useState(false)
  const [pending, setPending] = useState(false)
  const busy = useRef(false)
  // Both transport and API errors may arrive after the transaction committed.
  const submission = useRef(
    createReferralSubmission<ReferralModerationPayload>()
  )
  const [locked, setLocked] = useState(false)
  const submit = async () => {
    if (busy.current || !evidence.trim()) return
    busy.current = true
    setPending(true)
    try {
      const payload = submission.current(() => ({
        id: user.id,
        action: restore ? 'restore_referral' : 'ban_abuse',
        reason: restore ? 'mistaken_ban' : bulk ? 'bulk_registration' : 'abuse',
        evidence: evidence.trim(),
        penalize_inviter: !restore && penalize,
        request_id: crypto.randomUUID(),
      }))
      setLocked(true)
      const response = await moderateReferralUser(payload)
      if (!response.success) {
        throw new Error(response.message || t('Referral moderation failed'))
      }
      toast.success(t('Referral moderation saved'))
      onSuccess()
      onClose()
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t('Referral moderation failed')
      )
    } finally {
      busy.current = false
      setPending(false)
    }
  }
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !busy.current) onClose()
      }}
      title={restore ? t('Overturn abuse ban') : t('Ban for abuse')}
      description={
        restore
          ? t(
              'Restore the account and reverse this ban’s reward deductions. A refunded top-up remains ineligible for a reward.'
            )
          : t(
              'Disable this account and revoke only its first-top-up referral reward. An additional penalty requires explicit confirmation of inviter involvement.'
            )
      }
      footer={
        <>
          <Button variant='outline' disabled={pending} onClick={onClose}>
            {t('Cancel')}
          </Button>
          <Button
            variant={restore ? 'default' : 'destructive'}
            disabled={pending || !evidence.trim()}
            onClick={() => void submit()}
          >
            {pending ? t('Saving...') : t('Confirm')}
          </Button>
        </>
      }
    >
      <div className='space-y-4'>
        <p className='text-sm'>
          {user.username} · #{user.id}
        </p>
        <div className='space-y-2'>
          <Label htmlFor={`referral-evidence-${user.id}`}>
            {t('Moderation evidence or appeal reason')}
          </Label>
          <Textarea
            id={`referral-evidence-${user.id}`}
            value={evidence}
            onChange={(event) => setEvidence(event.target.value)}
            maxLength={1000}
            disabled={locked}
            required
          />
        </div>
        {!restore && (
          <>
            <Label className='flex items-start gap-2'>
              <Checkbox
                checked={bulk}
                onCheckedChange={(value) => setBulk(value === true)}
                disabled={locked}
              />
              {t('Bulk registration')}
            </Label>
            <Label className='flex items-start gap-2'>
              <Checkbox
                checked={penalize}
                onCheckedChange={(value) => setPenalize(value === true)}
                disabled={locked}
              />
              {t(
                'I confirm inviter involvement and authorize the configured extra penalty'
              )}
            </Label>
          </>
        )}
        {locked && !pending && (
          <p role='status' className='text-muted-foreground text-xs'>
            {t(
              'The request is unchanged for a safe retry. Check the account and reward history before starting a new operation.'
            )}
          </p>
        )}
      </div>
    </Dialog>
  )
}
