/*
Copyright (C) 2026 LIghtJUNction
*/
import { toast } from 'sonner'

import type { UpdateOptionResponse } from '../types'

export function showOptionUpdateToast(
  response: UpdateOptionResponse,
  successMessage: string
) {
  if (response.warnings?.length) {
    toast.warning(response.warnings.join('\n'))
  } else {
    toast.success(successMessage)
  }
}
