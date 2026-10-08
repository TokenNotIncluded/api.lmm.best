/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
export const STORE_FIXED_CONTENT_COPY = {
  template: 'Fixed content',
  content: 'Delivery content',
  unlimited: 'Unlimited supply',
  help: 'Every purchase receives the same private content. No inventory import is needed. Sales and purchase limits still apply.',
  privateHelp:
    'Only the seller and eligible paid buyers can read this content. Existing orders keep the content saved at purchase.',
  invalid: 'Enter delivery content within 128 KiB.',
  noImport:
    'This variant uses fixed content and does not accept inventory imports. Edit its delivery content in the product or variant editor.',
  draftHelp:
    'Save a draft first. Submit it after adding delivery content and configuring payment methods.',
} as const
