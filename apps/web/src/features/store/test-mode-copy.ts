/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
export const STORE_TEST_MODE_COPY = {
  label: 'Product test mode',
  help: 'Only you can view and buy this product while test mode is on. Purchases use real payments and inventory.',
  changed:
    'Changing test mode returns the product to draft. Turn it off before submitting for review or listing publicly.',
  preview: 'Private product preview',
  previewHelp:
    'This preview is only visible to the product owner. Purchases use real payments and inventory.',
} as const
