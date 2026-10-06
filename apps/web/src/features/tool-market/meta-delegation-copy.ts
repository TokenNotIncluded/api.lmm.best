/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
export const metaDelegationCopy = {
  title: 'AI tool management',
  help: 'Let AI find, authorize, load and unload tools for this connection.',
  budget: 'Paid tool budget (Credits)',
  free: 'Zero allows only free tools. This budget includes credits already spent or reserved by this client.',
  permissions:
    'Allow tool calls and loading when creating this connection to use AI tool management.',
  budgets: 'The account and tool budgets still apply.',
  invalid: 'Enter a whole number of credits from 0 to {{max}}.',
  failed: 'Could not save AI tool management settings.',
  saved: 'AI tool management saved.',
  tighter:
    'This connection has a stricter client budget. The saved limit is {{limit}} Credits.',
} as const
