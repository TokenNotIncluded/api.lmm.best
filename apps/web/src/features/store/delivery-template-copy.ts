/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
export const STORE_DELIVERY_TEMPLATE_COPY = {
  redemption: 'Redemption codes',
  license: 'License keys',
  download: 'Download links',
  account: 'Account details',
  cardHelp:
    'For activation keys. Import one key per line; buyers copy each key exactly as supplied.',
  textHelp:
    'For short text items. Import one item per line; buyers receive the original text.',
  customHelp:
    'For multi-line notes or instructions. Add each complete item separately; line breaks stay within that item.',
  redemptionHelp:
    'For codes redeemed on another site. Add a code, an optional redemption URL, and instructions for each item.',
  licenseHelp:
    'For software licenses. Add the license key, product name, and activation instructions for each item.',
  downloadHelp:
    'For downloadable content. Add a download URL, an optional access code, and instructions for each item.',
  accountHelp:
    'For account delivery. Add the username, password, optional login URL, and instructions together as one item.',
  add: 'Add one item',
  remove: 'Remove this item',
  contents: 'Complete item contents',
  redeemCode: 'Redemption code',
  redeemUrl: 'Redemption URL',
  licenseKey: 'License key',
  downloadUrl: 'Download URL',
  accessCode: 'Access code',
  loginUrl: 'Login URL',
  instructions: 'Instructions',
  showPassword: 'Show delivered password',
  hidePassword: 'Hide delivered password',
  openDownload: 'Open download link',
  openRedemption: 'Open redemption site',
  openLogin: 'Open login page',
  missing: 'Complete the required fields before adding this item.',
} as const
