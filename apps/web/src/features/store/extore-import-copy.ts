/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
export const EXTORE_COPY = {
  title: 'Import from Extore',
  help: 'Choose a product and variant, review the details, then fill a new product draft.',
  baseUrl: 'Extore base URL',
  clientId: 'Extore client ID',
  connect: 'Authorize product access',
  registration: 'Connection setup',
  registrationHelp:
    'In Extore, register this shop in Commerce connections with the callback below. Paste its public client ID here. No client secret is needed.',
  callback: 'Registered callback URL',
  unsupported:
    'Update the Go backend and configure its public HTTPS server address to enable Extore import.',
  readOnly:
    'Read-only access. No cards are issued and no products are published.',
  empty:
    'No products were authorized. Connect again and select products in Extore.',
  reconnect: 'Connect again',
  selectProduct: 'Extore product',
  selectVariant: 'Extore variant',
  reference: 'Reference price',
  unknownPrice: 'Not provided',
  import: 'Fill product draft',
  fields: 'Source details and fields not copied',
  sourceHelp:
    'This imports one variant as a new product draft. Attributes, localized source text, input forms, outputs, tutorials and revision rules are shown below but are not copied or synchronized. Redemption stays in Extore.',
  draftHelp:
    'Enter the actual selling price and review the draft. Reference prices are not converted. No sales inventory is imported; add card inventory separately.',
  imageWarning:
    'Some source images are not valid HTTPS URLs and will not be copied.',
  privacyWarning:
    'This draft stays private. Review its visibility before publishing.',
  privateUnsupported:
    'Update the backend to support private product visibility before importing this product.',
  noVariants: 'No enabled variants are available.',
  source: 'Extore source',
} as const
