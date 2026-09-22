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
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { I18nextProvider } from 'react-i18next'

import { DirectionProvider } from '@/context/direction-provider'
import { FontProvider } from '@/context/font-provider'
import { ThemeProvider } from '@/context/theme-provider'
import appI18n from '@/i18n/config'

import '@/styles/index.css'

import { UIFoundationPreview } from './ui-foundation-preview'
import { UIFoundationSheetPreview } from './ui-foundation-sheet-preview'

// This module is only imported from debug-main. There is no production route.
const element = document.getElementById('root')
if (!element) throw new Error('Missing preview root')
const sheetReview =
  new URLSearchParams(window.location.search).get('sheet_review') === '1'
createRoot(element).render(
  <StrictMode>
    <I18nextProvider i18n={appI18n}>
      <ThemeProvider defaultTheme='light'>
        <FontProvider>
          <DirectionProvider>
            {sheetReview ? <UIFoundationSheetPreview /> : <UIFoundationPreview />}
          </DirectionProvider>
        </FontProvider>
      </ThemeProvider>
    </I18nextProvider>
  </StrictMode>
)
