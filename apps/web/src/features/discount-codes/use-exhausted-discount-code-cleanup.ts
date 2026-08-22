/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { useMutation } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { deleteExhaustedDiscountCodes } from './api.js'

export function useExhaustedDiscountCodeCleanup(onCleaned: () => void) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const mutation = useMutation({
    mutationFn: deleteExhaustedDiscountCodes,
    onSuccess: (result) => {
      if (!result.success) {
        toast.error(
          result.message || t('Unable to clear exhausted discount codes')
        )
        return
      }
      const count = result.data?.count ?? 0
      setOpen(false)
      onCleaned()
      if (count === 0) {
        toast.info(t('No exhausted discount codes to delete'))
      } else {
        toast.success(
          t('Deleted {{count}} exhausted discount codes', { count })
        )
      }
    },
    onError: () => toast.error(t('Unable to clear exhausted discount codes')),
  })
  return {
    open,
    setOpen,
    pending: mutation.isPending,
    confirm: () => mutation.mutate(),
  }
}
