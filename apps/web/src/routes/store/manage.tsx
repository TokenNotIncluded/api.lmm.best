/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { createFileRoute } from '@tanstack/react-router'

import { StoreExtoreImport } from '@/features/store/extore-import-dialog'
import { StoreSellerPage } from '@/features/store/seller-page'

function StoreManagePage() {
  return (
    <>
      <StoreExtoreImport />
      <StoreSellerPage />
    </>
  )
}

export const Route = createFileRoute('/store/manage')({
  component: StoreManagePage,
})
