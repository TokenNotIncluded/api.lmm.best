/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
export const STORE_ACCESS_COPY = {
  title: 'Product access',
  visibility: 'Who can see this product',
  public: 'Everyone',
  publicHelp: 'Anyone can browse this product.',
  registered: 'Signed-in users',
  registeredHelp: 'Customers must sign in to view and buy this product.',
  private: 'Only me (test mode)',
  privateHelp:
    'Only you can view and buy this product. You can also buy your own products in other access modes.',
  login: 'Require sign-in to buy',
  loginHelp:
    'Turn this off to allow guest purchases and eligible free offers. Guests cannot use account balance.',
  conflictTitle: 'Allow guests to collect their orders?',
  conflictHelp:
    'Guest purchases require collection with a pickup link. Continuing turns off account-only collection for this product.',
  conflictConfirm: 'Allow guest collection',
  accountCollectionTitle: 'Require sign-in for purchase and collection?',
  accountCollectionHelp:
    'Account-only collection requires customers to sign in before ordering. Continuing turns on required sign-in for this product.',
  accountCollectionConfirm: 'Require sign-in',
  pickupConflict:
    'Guest purchases require account-only collection to be turned off.',
  purchaseTerms: 'Purchase terms',
  readTerms: 'Read purchase terms',
  termsTitle: 'Seller purchase terms',
  termsHelp:
    'These terms apply to all your products. Buyers must agree to the current version before ordering.',
  termsContent: 'Purchase terms',
  termsPlaceholder: 'Write your delivery, support and refund terms here.',
  termsRequired:
    'Add purchase terms before publishing or selling your products.',
  termsTooLong: 'Purchase terms are too long. Shorten them before saving.',
  termsInvalid: 'Purchase terms contain an invalid character.',
  termsReload: 'Reload current terms',
  termsVersion: 'Terms version: {{version}}',
  termsSaved: 'Seller purchase terms saved.',
  termsChanged:
    'The seller updated their terms. Read and agree to the new version before ordering.',
  termsAccept: 'I have read and agree to this seller’s purchase terms.',
  emailVerify: 'Verify pickup email',
  emailSend: 'Send verification code',
  emailCode: 'Email verification code',
  emailConfirm: 'Verify email',
  emailVerified: 'Pickup email verified.',
  emailSent: 'Verification code sent to {{email}}.',
  emailResend: 'Send again in {{seconds}} seconds',
  orderUnknown:
    'The order result is still unknown. Check its status before placing another order.',
  orderCheck: 'Check order status',
  orderRetry: 'Retry this order request',
  orderAbsent:
    'No order was found yet. You can retry the same request; its request number will stay the same.',
  orderReceiptFailed:
    'Order recovery information could not be saved. Keep the order number shown here.',
  orderClearCompleted: 'Clear completed order reminders',
  buyAgain: 'Buy again',
  guestHistory: 'Orders from this browser',
  guestEmpty: 'No guest orders are saved in this browser.',
  guestSessionFailed: 'Guest checkout could not be started. Please try again.',
  guestHistoryHelp:
    'Use this browser to manage guest orders, or keep your pickup link to collect after payment.',
} as const
