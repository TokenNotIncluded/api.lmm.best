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
/*
Copyright (C) 2026 LIghtJUNction
*/
import { useQueryClient } from '@tanstack/react-query'
import { isAxiosError } from 'axios'
import { Loader2, Trash2 } from 'lucide-react'
import { useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import {
  SecureVerificationDialog,
  useSecureVerification,
  type VerificationMethod,
} from '@/features/auth/secure-verification'

import {
  deleteAssistantReviewRuns,
  previewAssistantReviewRunCleanup,
  type AssistantReviewRunCleanupData,
  type AssistantReviewRunCleanupResponse,
} from './security-audit-api'

const REVIEW_HISTORY_KEEP = 30

function isStalePreviewError(error: unknown): boolean {
  if (!isAxiosError(error)) return false
  const code = (error.response?.data as { code?: unknown } | undefined)?.code
  return error.response?.status === 409 || code === 'STALE_PREVIEW'
}

export function AssistantReviewCleanup({
  disabled = false,
  onCleaned,
}: {
  disabled?: boolean
  onCleaned: () => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [previewing, setPreviewing] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [preview, setPreview] = useState<AssistantReviewRunCleanupData>()

  const handleCleanupSuccess = useCallback(
    (result: unknown) => {
      const response = result as AssistantReviewRunCleanupResponse
      if (!response.success || !response.data) {
        toast.error(t('Failed to clean up automatic review history'))
        return
      }
      setPreview(undefined)
      setConfirmOpen(false)
      onCleaned()
      toast.success(t('Automatic review history cleanup completed'))
      void Promise.all([
        queryClient.invalidateQueries({
          queryKey: ['admin-assistant-review-history'],
        }),
        queryClient.invalidateQueries({
          queryKey: ['admin-assistant-review-task'],
        }),
      ])
    },
    [onCleaned, queryClient, t]
  )

  const {
    open: verificationOpen,
    setOpen: setVerificationOpen,
    methods,
    state,
    startVerification,
    executeVerification,
    cancel,
    setCode,
    switchMethod,
    sendEmailCode,
    emailCodeSending,
    emailCodeSent,
  } = useSecureVerification({ onSuccess: handleCleanupSuccess })

  const loadPreview = async () => {
    if (previewing) return
    setPreviewing(true)
    try {
      const response =
        await previewAssistantReviewRunCleanup(REVIEW_HISTORY_KEEP)
      if (!response.success || !response.data) {
        throw new Error(response.message || 'cleanup preview failed')
      }
      if (response.data.eligible_count === 0) {
        toast.info(
          t('No completed automatic review runs are eligible for cleanup.')
        )
        return
      }
      setPreview(response.data)
      setConfirmOpen(true)
    } catch {
      toast.error(t('Failed to clean up automatic review history'))
    } finally {
      setPreviewing(false)
    }
  }

  const requestCleanup = async (proofToken?: string) => {
    if (!preview) {
      throw new Error(t('Failed to clean up automatic review history'))
    }
    setDeleting(true)
    try {
      return await deleteAssistantReviewRuns(
        REVIEW_HISTORY_KEEP,
        preview.eligible_count,
        proofToken
      )
    } catch (error) {
      if (isStalePreviewError(error)) {
        setVerificationOpen(false)
        setPreview(undefined)
        setPreviewing(true)
        try {
          const response =
            await previewAssistantReviewRunCleanup(REVIEW_HISTORY_KEEP)
          if (response.success && response.data) {
            if (response.data.eligible_count === 0) {
              toast.info(
                t(
                  'No completed automatic review runs are eligible for cleanup.'
                )
              )
            } else {
              setPreview(response.data)
              setConfirmOpen(true)
            }
          }
        } catch {
          // The stale-preview message still requires a fresh confirmation.
        } finally {
          setPreviewing(false)
        }
        throw new Error(
          t(
            'Automatic review history changed. Review the refreshed preview and confirm again.'
          )
        )
      }
      throw new Error(t('Failed to clean up automatic review history'), {
        cause: error,
      })
    } finally {
      setDeleting(false)
    }
  }

  const confirmCleanup = async () => {
    if (!preview || deleting) return
    setConfirmOpen(false)
    await startVerification(requestCleanup, {
      scope: 'security.review_runs.delete',
      title: t('Security verification'),
      description: t('Clean up review history'),
    })
  }

  const verify = async (method: VerificationMethod, code?: string) => {
    try {
      await executeVerification(method, code)
    } catch {
      // The verification hook and request wrapper already surface failures.
    }
  }

  return (
    <>
      <Button
        type='button'
        variant='ghost'
        size='sm'
        className='text-destructive hover:text-destructive'
        disabled={disabled || previewing || deleting}
        onClick={loadPreview}
      >
        {previewing ? <Loader2 className='animate-spin' /> : <Trash2 />}
        {t('Clean up review history')}
      </Button>

      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={t('Clean up automatic review history?')}
        desc={
          <div className='space-y-2'>
            <p>
              {t(
                'This will permanently delete {{count}} completed or failed automatic review runs while keeping the latest {{keep}}. Active runs and security audit evidence will not be deleted.',
                {
                  count: preview?.eligible_count ?? 0,
                  keep: preview?.keep ?? REVIEW_HISTORY_KEEP,
                }
              )}
            </p>
            <p>{t('This action cannot be undone.')}</p>
          </div>
        }
        confirmText={t('Confirm Cleanup')}
        destructive
        isLoading={deleting}
        disabled={!preview}
        handleConfirm={confirmCleanup}
      />

      <SecureVerificationDialog
        open={verificationOpen}
        onOpenChange={setVerificationOpen}
        methods={methods}
        state={state}
        onVerify={verify}
        onCancel={cancel}
        onCodeChange={setCode}
        onMethodChange={switchMethod}
        onSendEmailCode={sendEmailCode}
        emailCodeSending={emailCodeSending}
        emailCodeSent={emailCodeSent}
      />
    </>
  )
}
