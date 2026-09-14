/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { parseHeroSmsError } from './api.js'

type Translate = (key: string) => string

export function describeSmsAccessError(
  error: unknown,
  t: Translate
): { title: string; description: string } {
  const parsed = parseHeroSmsError(error)
  const code = parsed.code?.toUpperCase() ?? ''
  const message = parsed.message.toLowerCase()
  if (
    code === 'TEMPORARY_SMS_MINIMUM_BALANCE' ||
    code === 'INSUFFICIENT_BALANCE' ||
    code === 'INSUFFICIENT_QUOTA' ||
    /(?:minimum|insufficient).*(?:balance|quota)|(?:balance|quota).*(?:minimum|insufficient)/.test(
      message
    )
  ) {
    return {
      title: t('Insufficient quota'),
      description: t(
        'Temporary SMS purchases require a balance of at least USD 10'
      ),
    }
  }
  if (
    parsed.status === 403 ||
    /(?:unlock|locked|trust level|access level|permission|forbidden|not allowed)/.test(
      message
    ) ||
    /(?:UNLOCK|LEVEL|ACCESS|PERMISSION|FORBIDDEN)/.test(code)
  ) {
    return {
      title: t('Purchasing unavailable'),
      description: t(
        'A funded, active account gradually unlocks more tools and better rates. Your current level is shown in the wallet.'
      ),
    }
  }
  if (code === 'NOT_CONFIGURED') {
    return {
      title: t('Purchasing unavailable'),
      description: t('HeroSMS purchasing is disabled'),
    }
  }
  return {
    title: t('Request failed'),
    description:
      parsed.message && parsed.message !== 'HeroSMS request failed'
        ? parsed.message
        : t('Unable to load phone services'),
  }
}
