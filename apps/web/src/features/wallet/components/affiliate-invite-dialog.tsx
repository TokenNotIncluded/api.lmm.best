/*
Copyright (C) 2026 LIghtJUNction

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
*/
import {
  CheckIcon,
  CopyIcon,
  GiftIcon,
  LoaderCircleIcon,
  MailIcon,
  SparklesIcon,
  TriangleAlertIcon,
} from 'lucide-react'
import { type FormEvent, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'
import { useSystemConfig } from '@/hooks/use-system-config'

import { isApiSuccess, sendAffiliateInvitation } from '../api'

interface AffiliateInviteDialogProps {
  affiliateLink: string
}

function isEmailAddress(value: string): boolean {
  return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(value)
}

export function AffiliateInviteDialog({
  affiliateLink,
}: AffiliateInviteDialogProps) {
  const { t } = useTranslation()
  const { systemName } = useSystemConfig()
  const { copiedText, copyToClipboard } = useCopyToClipboard()
  const [open, setOpen] = useState(false)
  const [email, setEmail] = useState('')
  const [sending, setSending] = useState(false)
  const [failure, setFailure] = useState<string | null>(null)

  const normalizedEmail = email.trim()
  const canSend =
    Boolean(affiliateLink) && isEmailAddress(normalizedEmail) && !sending
  const copied = copiedText === affiliateLink

  const handleOpenChange = (nextOpen: boolean) => {
    setOpen(nextOpen)
    setFailure(null)
  }

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!canSend) return

    setSending(true)
    setFailure(null)
    try {
      const response = await sendAffiliateInvitation({
        email: normalizedEmail,
      })
      if (!isApiSuccess(response)) {
        throw new Error(response.message || 'affiliate invitation failed')
      }

      toast.success(
        t('Invitation sent to {{email}}', { email: normalizedEmail })
      )
      setEmail('')
      setOpen(false)
    } catch {
      const message = t(
        'The invitation email could not be sent. Try again later or copy your invitation link.'
      )
      setFailure(message)
      toast.error(message)
    } finally {
      setSending(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogTrigger
        disabled={!affiliateLink}
        render={<Button type='button' className='h-9 shrink-0' />}
      >
        <MailIcon data-icon='inline-start' />
        {t('Invite friends')}
      </DialogTrigger>

      <DialogContent className='overflow-hidden p-0 sm:max-w-lg'>
        <div className='bg-primary/[0.07] relative flex h-36 items-center justify-center overflow-hidden border-b sm:h-40'>
          <div
            aria-hidden='true'
            className='border-primary/10 absolute size-52 rounded-full border'
          />
          <div
            aria-hidden='true'
            className='border-primary/10 absolute size-32 rounded-full border'
          />
          <div className='bg-foreground text-background ring-background relative flex size-20 items-center justify-center rounded-[1.35rem] shadow-sm ring-4'>
            <SparklesIcon className='size-9' strokeWidth={1.7} />
          </div>
        </div>

        <form
          className='space-y-4 px-4 pb-4 sm:px-6 sm:pb-6'
          onSubmit={handleSubmit}
        >
          <div className='border-success/25 bg-success/8 flex items-center gap-2.5 border px-3 py-2.5'>
            <GiftIcon className='text-success size-4 shrink-0' />
            <p className='text-sm font-medium'>
              {t('Invite friends and earn account credit when they join.')}
            </p>
          </div>

          <DialogHeader className='gap-2 text-left'>
            <DialogTitle className='text-xl leading-tight font-semibold tracking-tight'>
              {t('Invite friends to {{systemName}}', { systemName })}
            </DialogTitle>
            <DialogDescription className='leading-relaxed'>
              {t(
                "Enter a friend's email and we'll send your personal invitation link through the configured mail server."
              )}
            </DialogDescription>
          </DialogHeader>

          <div className='space-y-2'>
            <Label htmlFor='affiliate-invite-email'>
              {t("Friend's email")}
            </Label>
            <Input
              id='affiliate-invite-email'
              type='email'
              inputMode='email'
              autoComplete='email'
              placeholder={t('name@example.com')}
              value={email}
              disabled={sending}
              required
              maxLength={254}
              aria-invalid={Boolean(failure)}
              aria-describedby={failure ? 'affiliate-invite-error' : undefined}
              onChange={(event) => {
                setEmail(event.target.value)
                if (failure) setFailure(null)
              }}
            />
          </div>

          {failure ? (
            <Alert id='affiliate-invite-error' variant='destructive'>
              <TriangleAlertIcon />
              <AlertTitle>{t('Invitation not sent')}</AlertTitle>
              <AlertDescription>{failure}</AlertDescription>
            </Alert>
          ) : null}

          <div className='grid gap-2 pt-1 sm:grid-cols-2'>
            <Button
              type='button'
              variant='outline'
              className='h-10'
              disabled={!affiliateLink || sending}
              onClick={() => void copyToClipboard(affiliateLink)}
            >
              {copied ? (
                <CheckIcon data-icon='inline-start' className='text-success' />
              ) : (
                <CopyIcon data-icon='inline-start' />
              )}
              {copied ? t('Copied') : t('Copy invitation link')}
            </Button>
            <Button type='submit' className='h-10' disabled={!canSend}>
              {sending ? (
                <LoaderCircleIcon
                  data-icon='inline-start'
                  className='animate-spin motion-reduce:animate-none'
                />
              ) : (
                <MailIcon data-icon='inline-start' />
              )}
              {sending ? t('Sending invitation...') : t('Send invitation')}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  )
}
