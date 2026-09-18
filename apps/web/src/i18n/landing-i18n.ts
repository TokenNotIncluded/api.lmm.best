/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import i18next from 'i18next'
import { initReactI18next } from 'react-i18next'

import enLocale from './locales/en.json'

// A standalone i18next instance scoped to the public landing page ('/').
// Unlike the shared `i18n` instance in `./config.ts`, this one never
// registers LanguageDetector and never touches localStorage, so mounting it
// cannot overwrite a signed-in user's saved language preference. It is
// preloaded with English only and always resolves to English, regardless of
// browser locale or a previously saved preference.
const landingI18n = i18next.createInstance()

void landingI18n.use(initReactI18next).init({
  lng: 'en',
  fallbackLng: 'en',
  supportedLngs: ['en'],
  resources: {
    en: enLocale,
  },
  nsSeparator: false,
  interpolation: {
    escapeValue: false,
  },
})

export default landingI18n
