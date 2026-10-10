/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
export const STORE_PUBLISHING_COPY = {
  setup: 'Configure seller terms before publishing or relisting this product.',
  setupHelp:
    'Save your delivery and after-sales terms here. Buyers accept them separately when ordering.',
  continue: 'Continue publishing',
  offShelf: 'Temporarily unlist',
  offShelfHelp:
    'Temporarily unlisting hides the product and keeps its approval. Existing orders remain available.',
  withdraw: 'Withdraw from review',
  withdrawTitle: 'Withdraw this product from review?',
  withdrawHelp:
    'This clears the approval. Edit and submit the product again before publishing. Existing orders remain available.',
  variantsTitle: 'Specifications and prices',
  variantsHelp:
    'Give each specification its own name, price and delivery content. All specifications are saved with this draft.',
  invalidVariants: 'Enter unique variant names and valid prices.',
  variantsLimit: 'A product can have at most 200 specifications.',
} as const
