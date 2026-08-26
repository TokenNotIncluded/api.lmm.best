/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { MessageSquareWarning } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'

import type { HeroSmsSmsComplaintReason } from './sms-api.js'

// typos:ignore DISMATCH -- HeroSMS's official complaint enum uses this spelling.
const SMS_COMPLAINT_REASONS: Array<{
  value: HeroSmsSmsComplaintReason
  label: string
}> = [
  { value: 'SMS_NOT_RECEIVED', label: 'SMS not received' },
  { value: 'NUMBER_BLOCKED', label: 'Number is blocked' },
  { value: 'NUMBER_ALREADY_IN_USE', label: 'Number is already in use' },
  { value: 'SMS_CODE_DISMATCH', label: 'SMS code does not match' },
  { value: 'CODE_SENT_TO_APP', label: 'Code was sent to the app' },
  { value: 'INCOMING_CALL_NUMBER', label: 'Incoming call showed a number' },
  { value: 'INCOMING_CALL_VOICE', label: 'Incoming call used a voice code' },
]

export function SmsComplaintDialog({
  available,
  showAvailabilityHint,
  operationPending,
  complaintPending,
  onSubmit,
}: {
  available: boolean
  showAvailabilityHint: boolean
  operationPending: boolean
  complaintPending: boolean
  onSubmit: (reason: HeroSmsSmsComplaintReason) => void
}) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [reason, setReason] =
    useState<HeroSmsSmsComplaintReason>('SMS_NOT_RECEIVED')
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <Button
        type='button'
        variant='outline'
        size='sm'
        disabled={!available || operationPending || complaintPending}
        title={
          showAvailabilityHint && !available && !complaintPending
            ? t('Complaints become available two minutes after purchase')
            : undefined
        }
        onClick={() => setOpen(true)}
      >
        <MessageSquareWarning data-icon='inline-start' />
        {t('I did not receive the SMS code')}
      </Button>
      <DialogContent className='sm:max-w-lg'>
        <DialogHeader>
          <DialogTitle>{t('I did not receive the SMS code')}</DialogTitle>
          <DialogDescription>
            {t(
              'Choose the reason sent to HeroSMS. Your platform balance is refunded only after HeroSMS confirms the upstream cancellation.'
            )}
          </DialogDescription>
        </DialogHeader>
        <RadioGroup
          value={reason}
          onValueChange={(value) =>
            setReason(value as HeroSmsSmsComplaintReason)
          }
          className='max-h-72 gap-2 overflow-y-auto pr-1'
          aria-label={t('Complaint reason')}
        >
          {SMS_COMPLAINT_REASONS.map((item) => (
            <Label
              key={item.value}
              htmlFor={`hero-sms-complaint-${item.value}`}
              className='has-[[data-state=checked]]:border-primary has-[[data-state=checked]]:bg-primary/5 flex cursor-pointer items-center gap-3 rounded-lg border p-3'
            >
              <RadioGroupItem
                id={`hero-sms-complaint-${item.value}`}
                value={item.value}
              />
              <span>{t(item.label)}</span>
            </Label>
          ))}
        </RadioGroup>
        <DialogFooter>
          <Button
            type='button'
            variant='outline'
            onClick={() => setOpen(false)}
            disabled={operationPending}
          >
            {t('Back')}
          </Button>
          <Button
            type='button'
            onClick={() => {
              onSubmit(reason)
              setOpen(false)
            }}
            disabled={operationPending}
          >
            {operationPending ? t('Submitting') : t('Submit complaint')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
