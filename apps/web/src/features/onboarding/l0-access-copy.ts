/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/

import appI18n from '@/i18n/config'
import { normalizeInterfaceLanguage } from '@/i18n/languages'

/** Resolve the welcome copy through the same seven locales as the console. */
export function getL0AccessCopy(language: string) {
  const t = appI18n.getFixedT(normalizeInterfaceLanguage(language))
  return {
    greeting: t('What will you make?'),
    prompt: t('Ask a question. Start an idea.'),
    explore: t('Explore'),
    access: t('Unlock'),
    navigation: t('Workspace views'),
    unlockTitle: t('Apply for access'),
    modelsNote: t('Find a model for your next request.'),
    toolsNote: t('Bring LMM into the tools you use.'),
    challengesNote: t('Build something together.'),
    browseModels: t('Browse models'),
    browseTools: t('Explore tools'),
    browseChallenges: t('View challenges'),
    previous: t('Previous destination'),
    next: t('Next destination'),
    carousel: t('carousel'),
    support: t('Contact support'),
    plans: t('Plans & top-ups'),
    privacyNote: t('Keep passwords and API keys out of this conversation.'),
    connectPi: t('Connect Pi'),
    toggleMotion: t('Pause or resume animation'),
    wallet: t('Top up'),
    walletNote: t('Open the wallet to add credit'),
    apply: t('Apply for access'),
    check: t('Already paid'),
    remaining: t('Eligible credit needed'),
    eligibility: t(
      'LinuxDO Credit does not count. External paid top-ups count; the checkout amount is final.'
    ),
    review: t('Apply for access'),
    reviewNote: t('Automatic paid activation is unavailable for this account.'),
    unknown: t('Refresh access conditions before paying for an upgrade.'),
    sync: t('No more top-ups needed'),
    syncNote: t('Credit requirement met. Refresh to confirm access.'),
    checking: t('Checking access…'),
    waiting: t('Waiting for confirmation. Do not pay again.'),
    error: t('Refresh failed. Retrying; do not pay again.'),
    timeout: t('Checks stopped. Contact support before paying again.'),
    help: t('Help'),
    models: t('Models'),
    tools: t('Tools'),
    challenges: t('Open source'),
    conditions: t('Access details'),
    conversation: t('Conversation'),
    newChat: t('New conversation'),
    responding: t('Responding…'),
    chatError: t('The response was interrupted. Try again or contact support.'),
    stopped: t('Stopped'),
    stop: t('Stop response'),
    latest: t('Latest message'),
    copyAnswer: t('Copy response'),
    copied: t('Copied'),
    copyFailed: t('Copy failed'),
    retry: t('Retry'),
    unavailable: t('Assistant unavailable'),
  }
}
