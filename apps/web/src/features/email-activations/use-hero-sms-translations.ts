/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { useTranslation } from 'react-i18next'

import { registerHeroSmsTranslations } from './i18n.js'
import { registerTemporaryActivationTranslations } from './temporary-i18n.js'

export function useHeroSmsTranslations() {
  const { i18n } = useTranslation()
  registerHeroSmsTranslations(i18n)
  registerTemporaryActivationTranslations(i18n)
}
