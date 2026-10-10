/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from '@/components/ui/dialog'

import { storeAccessApi } from './access-api'
import { STORE_ACCESS_COPY as accessCopy } from './access-copy'
import { StoreMerchantTermsForm } from './merchant-terms'
import { STORE_PUBLISHING_COPY as copy } from './publishing-copy'
import { StoreError, StoreLoading } from './shared'

export function StoreSellerTermsDialog({
  sellerId,
  onClose,
  onContinue,
}: {
  sellerId: number
  onClose: () => void
  onContinue?: () => void
}) {
  const { t } = useTranslation()
  const query = useQuery({
    queryKey: ['store', 'my-terms', sellerId],
    queryFn: storeAccessApi.myTerms,
    retry: false,
    staleTime: 0,
  })
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose()
      }}
    >
      <DialogContent className='max-h-[85dvh] overflow-y-auto sm:max-w-2xl'>
        <DialogTitle>{t(accessCopy.termsTitle)}</DialogTitle>
        <DialogDescription>
          {t(onContinue ? copy.setup : copy.setupHelp)}
        </DialogDescription>
        {query.isPending && <StoreLoading />}
        <StoreError error={query.error} retry={() => void query.refetch()} />
        {query.data && (
          <StoreMerchantTermsForm
            key={query.data.version}
            terms={query.data}
            onReload={async () => {
              await query.refetch()
            }}
            onSaved={async () => {
              await query.refetch()
            }}
          />
        )}
        {onContinue && (
          <Button
            type='button'
            disabled={
              !query.data?.configured || query.isFetching || !!query.error
            }
            onClick={onContinue}
          >
            {t(copy.continue)}
          </Button>
        )}
        <p className='text-muted-foreground text-xs leading-5'>
          {t(copy.setupHelp)}
        </p>
      </DialogContent>
    </Dialog>
  )
}
